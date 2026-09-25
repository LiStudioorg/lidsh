// fs 工具族，复刻 dsh-tool-fs（read/write/edit/glob）与 dsh-tool-fs-search（grep）
// 以及 dsh-tool-str-replace-editor 的字符串替换语义。
//
// 约定：
//   - read：文本文件按行编号输出（`行号: 内容`），支持 offset/limit 分页，行号从 1 起。
//   - write：写文本文件（0644）。
//   - edit：字符串替换，old_string 必须恰好出现（replace_all=false 时），
//     找不到 → isError "no match"。
//   - glob：支持 ** 递归展开（Go 的 filepath.Glob 不支持 **，这里自行展开）。
//   - grep：正则递归搜索文件内容，返回 `匹配文件:行号:行内容`，限 250 行提示截断。
package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"lidsh/internal/sandbox"
)

const grepMaxMatches = 250

// fsWriteDenied 复刻 SandboxedFileSystem.checkedTarget（bash-sandbox.md §5.3）
// 与 fs 工具错误映射（§4）：danger-full-access 直放；read-only 拒；workspace-write
// 写前即时重新 canonicalize 目标后逐根 containment。
// 返回空串=允许；非空=拒绝结果文本（marker + 升级 hint，isError）。
func fsWriteDenied(sc *SandboxContext, path string) string {
	if sc == nil || sc.Policy.Mode == sandbox.ModeDangerFullAccess {
		return ""
	}
	if sc.Policy.Mode == sandbox.ModeReadOnly {
		return sandbox.DenialMarker(sandbox.ModeReadOnly) + "\n" +
			sandbox.EscalationHintMarker("operation")
	}
	// workspace-write：canonicalize-then-contain（§5.3）。
	target := sandbox.CanonicalPath(path)
	for _, root := range sc.Policy.WritableRoots() {
		if sandbox.IsPathUnder(root, target) {
			return ""
		}
	}
	return sandbox.DenialMarker(sandbox.ModeWorkspaceWrite) + "\n" +
		sandbox.EscalationHintMarker("operation")
}

// RegisterFSTools 注册全部 fs 工具族并返回批量注销函数。
func RegisterFSTools(r *Registry) func() {
	unregs := []func(){
		RegisterFSRead(r),
		RegisterFSWrite(r),
		RegisterFSEdit(r),
		RegisterFSGlob(r),
		RegisterFSGrep(r),
	}
	return func() {
		for _, u := range unregs {
			u()
		}
	}
}

// ---- read ----

var readSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"path":   map[string]any{"type": "string"},
		"offset": map[string]any{"type": "number"},
		"limit":  map[string]any{"type": "number"},
	},
	"required": []any{"path"},
}

// RegisterFSRead 注册 read 工具。
func RegisterFSRead(r *Registry) func() {
	return r.Register(Definition{
		Name:        "read",
		Description: "Read a text file, optionally with line offset/limit paging.",
		InputSchema: readSchema,
		Execute:     execFSRead,
		PresentCall: func(args map[string]any) *CallView {
			return &CallView{Card: "read", Title: asString(anyLookup(args, "path")), Kind: "read", Location: asString(anyLookup(args, "path"))}
		},
	})
}

func execFSRead(args map[string]any, ec *ExecContext) (*Result, error) {
	path := resolvePath(ec, asString(anyLookup(args, "path")))
	data, err := os.ReadFile(path)
	if err != nil {
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}
	offset := anyNumber(args, "offset", 1)
	limit := anyNumber(args, "limit", 0)
	if offset < 1 {
		offset = 1
	}

	lines := strings.Split(string(data), "\n")
	// 去掉末尾空行（来自换行符/空文件）。
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var b strings.Builder
	total := len(lines)
	end := len(lines)
	if offset-1 <= len(lines) && limit > 0 {
		end = min(offset-1+limit, len(lines))
	}
	for i := offset - 1; i < end && i < total; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i+1, lines[i])
	}
	// 截断提示。
	if end < total {
		fmt.Fprintf(&b, "... (%d more lines)\n", total-end)
	}
	return &Result{Content: strings.TrimSuffix(b.String(), "\n"), IsError: false}, nil
}

// ---- write ----

var writeSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"path":    map[string]any{"type": "string"},
		"content": map[string]any{"type": "string"},
	},
	"required": []any{"path", "content"},
}

// RegisterFSWrite 注册 write 工具。
func RegisterFSWrite(r *Registry) func() {
	return r.Register(Definition{
		Name:        "write",
		Description: "Write text content to a file (0644).",
		InputSchema: writeSchema,
		Execute:     execFSWrite,
		PresentCall: func(args map[string]any) *CallView {
			return &CallView{Card: "diff", Title: asString(anyLookup(args, "path")), Kind: "edit", Location: asString(anyLookup(args, "path"))}
		},
	})
}

func execFSWrite(args map[string]any, ec *ExecContext) (*Result, error) {
	path := resolvePath(ec, asString(anyLookup(args, "path")))
	content := asString(anyLookup(args, "content"))
	// M2：写 fence（§5.3 checkedTarget；拒绝文本=marker+hint，§4）。
	if d := fsWriteDenied(ec.Sandbox, path); d != "" {
		return &Result{Content: d, IsError: true}, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}
	return &Result{
		Content: fmt.Sprintf("Written %d bytes to %s", len(content), path),
		IsError: false,
	}, nil
}

// ---- edit ----

var editSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"path":        map[string]any{"type": "string"},
		"old_string":  map[string]any{"type": "string"},
		"new_string":  map[string]any{"type": "string"},
		"replace_all": map[string]any{"type": "boolean"},
	},
	"required": []any{"path", "old_string", "new_string"},
}

// RegisterFSEdit 注册 edit 工具。
func RegisterFSEdit(r *Registry) func() {
	return r.Register(Definition{
		Name:        "edit",
		Description: "Replace an exact string in a file; old_string must match (replace_all=false) or it is an error.",
		InputSchema: editSchema,
		Execute:     execFSEdit,
		PresentCall: func(args map[string]any) *CallView {
			return &CallView{Card: "diff", Title: asString(anyLookup(args, "path")), Kind: "edit", Location: asString(anyLookup(args, "path"))}
		},
	})
}

func execFSEdit(args map[string]any, ec *ExecContext) (*Result, error) {
	path := resolvePath(ec, asString(anyLookup(args, "path")))
	oldStr := asString(anyLookup(args, "old_string"))
	newStr := asString(anyLookup(args, "new_string"))
	if oldStr == "" {
		return &Result{Content: "Error: old_string must not be empty", IsError: true}, nil
	}
	replaceAll, _ := anyBool(args, "replace_all")

	data, err := os.ReadFile(path)
	if err != nil {
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}
	text := string(data)

	var replacement string
	var count int
	if replaceAll {
		replacement = strings.ReplaceAll(text, oldStr, newStr)
		count = strings.Count(text, oldStr)
	} else {
		idx := strings.Index(text, oldStr)
		if idx < 0 {
			return &Result{Content: "Error: no match for old_string", IsError: true}, nil
		}
		// 必须恰好出现一次。
		count = strings.Count(text, oldStr)
		if count != 1 {
			return &Result{Content: fmt.Sprintf("Error: old_string appears %d times (expected exactly 1); use replace_all", count), IsError: true}, nil
		}
		replacement = text[:idx] + newStr + text[idx+len(oldStr):]
	}

	if count == 0 {
		return &Result{Content: "Error: no match for old_string", IsError: true}, nil
	}
	// M2：写 fence（§5.3 checkedTarget；拒绝文本=marker+hint，§4）。
	if d := fsWriteDenied(ec.Sandbox, path); d != "" {
		return &Result{Content: d, IsError: true}, nil
	}
	if err := os.WriteFile(path, []byte(replacement), 0o644); err != nil {
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}

	return &Result{
		Content: fmt.Sprintf("Replaced %d occurrence(s) of %q in %s", count, oldStr, path),
		IsError: false,
	}, nil
}

// ---- glob ----

var globSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
	},
	"required": []any{"pattern"},
}

// RegisterFSGlob 注册 glob 工具。
func RegisterFSGlob(r *Registry) func() {
	return r.Register(Definition{
		Name:        "glob",
		Description: "Find files matching a glob pattern (supports ** recursive wildcard).",
		InputSchema: globSchema,
		Execute:     execFSGlob,
	})
}

func execFSGlob(args map[string]any, ec *ExecContext) (*Result, error) {
	pattern := asString(anyLookup(args, "pattern"))
	if pattern == "" {
		return &Result{Content: "Error: pattern is empty", IsError: true}, nil
	}
	base := resolvePath(ec, asString(anyLookup(args, "path")))
	if base == "" {
		base = ec.CWD
	}
	if base == "" {
		base = ec.AgentCWD
	}

	matches, err := glow(base, pattern)
	if err != nil {
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}
	sort.Strings(matches)
	return &Result{Content: strings.Join(matches, "\n"), IsError: false}, nil
}

// glow 以 base 为根展开 pattern，支持 **（Go filepath.Glob 不支持，自行展开）。
// 返回值是相对 base 的路径。
func glow(base, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	pattern = strings.TrimPrefix(pattern, "./")
	// 绝对模式也相对 base 解析。
	if filepath.IsAbs(pattern) {
		pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
	}
	segs := strings.Split(pattern, "/")

	var results []string
	var walk func(dir string, segs []string)
	walk = func(dir string, segs []string) {
		if len(segs) == 0 {
			return
		}
		seg := segs[0]
		rest := segs[1:]
		if seg == "**" {
			// ** 匹配零层或多层。零层：跳过本段继续。
			walk(dir, rest)
			// 多层：对每个直接子目录递归展开剩余（含 ** 自身）。
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				child := filepath.Join(dir, e.Name())
				if e.IsDir() {
					walk(child, segs) // 继续匹配 ** 的下一层
				} else {
					// 文件：只能匹配到 rest（若有），否则跳过。
					if len(rest) == 0 {
						results = append(results, child)
					}
				}
			}
			return
		}
		// 普通段（可带通配符）。
		full := filepath.Join(dir, seg)
		matches, err := filepath.Glob(full)
		if err != nil {
			return
		}
		for _, m := range matches {
			fi, err := os.Stat(m)
			if err != nil {
				continue
			}
			if fi.IsDir() {
				if len(rest) == 0 {
					results = append(results, m) // 目录也作为匹配结果
				} else {
					walk(m, rest)
				}
			} else {
				if len(rest) == 0 {
					results = append(results, m)
				}
				// 文件不继续往下展开
			}
		}
	}
	walk(base, segs)

	var out []string
	seen := map[string]bool{}
	for _, m := range results {
		rel, err := filepath.Rel(base, m)
		if err != nil {
			rel = m
		}
		rel = filepath.ToSlash(rel)
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out, nil
}

// ---- grep ----

var grepSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
		"include": map[string]any{"type": "string"},
	},
	"required": []any{"pattern"},
}

// RegisterFSGrep 注册 grep 工具。
func RegisterFSGrep(r *Registry) func() {
	return r.Register(Definition{
		Name:        "grep",
		Description: "Search file contents with a regular expression, returning matched lines.",
		InputSchema: grepSchema,
		Execute:     execFSGrep,
	})
}

func execFSGrep(args map[string]any, ec *ExecContext) (*Result, error) {
	pattern := asString(anyLookup(args, "pattern"))
	if pattern == "" {
		return &Result{Content: "Error: pattern is empty", IsError: true}, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return &Result{Content: "Error: invalid regex: " + err.Error(), IsError: true}, nil
	}
	base := resolvePath(ec, asString(anyLookup(args, "path")))
	if base == "" {
		base = ec.CWD
	}
	if base == "" {
		base = ec.AgentCWD
	}
	include := asString(anyLookup(args, "include"))
	var includeRe *regexp.Regexp
	if include != "" {
		if includeRe, err = regexp.Compile(include); err != nil {
			return &Result{Content: "Error: invalid include regex: " + err.Error(), IsError: true}, nil
		}
	}

	var b strings.Builder
	count := 0
	truncated := false
	filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// 跳过不可读目录/文件。
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// 跳过 .git 等元数据目录。
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= grepMaxMatches {
			truncated = true
			return filepath.SkipAll
		}
		if includeRe != nil && !includeRe.MatchString(p) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		lines := bytes.Split(data, []byte("\n"))
		for i, line := range lines {
			if count >= grepMaxMatches {
				truncated = true
				return filepath.SkipAll
			}
			if re.Match(line) {
				fmt.Fprintf(&b, "%s:%d:%s\n", p, i+1, line)
				count++
			}
		}
		return nil
	})

	if truncated {
		fmt.Fprintf(&b, "... (truncated at %d matches)\n", grepMaxMatches)
	}
	if count == 0 {
		return &Result{Content: "no matches", IsError: false}, nil
	}
	return &Result{Content: strings.TrimSuffix(b.String(), "\n"), IsError: false}, nil
}

// ---- helpers ----

// resolvePath 把工具给的 path 相对 ec.CWD/AgentCWD 解析为绝对路径。
func resolvePath(ec *ExecContext, p string) string {
	if p == "" {
		return p
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	base := ec.CWD
	if base == "" {
		base = ec.AgentCWD
	}
	return filepath.Join(base, p)
}

// anyBool 从参数取布尔，缺省返回 def。
func anyBool(m map[string]any, key string) (bool, bool) {
	v := anyLookup(m, key)
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		if b == "true" {
			return true, true
		}
		if b == "false" {
			return false, true
		}
	}
	return false, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
