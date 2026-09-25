// bash 工具，复刻 dsh-tool-bash。在 /bin/bash 下执行命令并合并返回输出。
//
// 语义要点（dsh-tool-bash/lib/index.js:273-275）：
//   - timeoutMs 是工具参数自管：拿到就当作用来替换 Signal 的 deadline，
//     默认 120000ms（120s）。
//   - 标准输出 + 标准错误合并返回，模型可见。
//   - non-zero exit 返回 exit code 文本但工具本身不设 isError；
//     只有命令超时或 Signal 取消才算失败（isError）。
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// defaultBashTimeoutMs 是 bash 工具默认超时（dsh-tool-bash 默认 120000ms）。
const defaultBashTimeoutMs = 120000

// bashInputSchema 是 bash 工具的 JSON Schema（能力开关 run_in_background /
// sandbox_permissions 只在 GUI 传输层出现，本包不暴露）。
var bashInputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"command":     map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
		"timeoutMs":   map[string]any{"type": "number"},
		"workdir":     map[string]any{"type": "string"},
	},
	"required": []any{"command"},
}

// RegisterBash 注册 bash 工具并返回注销函数。
func RegisterBash(r *Registry) func() {
	return r.Register(Definition{
		Name:        "bash",
		Description: "Execute a shell command and return its output.",
		InputSchema: bashInputSchema,
		Execute:     execBash,
		PresentCall: func(args map[string]any) *CallView {
			cmd := asString(anyLookup(args, "command"))
			prev := cmd
			if len(prev) > 80 {
				prev = prev[:80] + "…"
			}
			return &CallView{Card: "terminal", Title: prev}
		},
	})
}

// execBash 执行 bash 命令。
func execBash(args map[string]any, ec *ExecContext) (*Result, error) {
	command := asString(anyLookup(args, "command"))
	if strings.TrimSpace(command) == "" {
		return &Result{Content: "Error: command is empty", IsError: true}, nil
	}

	// timeoutMs 是工具参数自管：用它替换 Signal 的 deadline。
	timeout := time.Duration(anyNumber(args, "timeoutMs", defaultBashTimeoutMs)) * time.Millisecond
	ctx, cancel := context.WithTimeout(ec.Signal, timeout)
	defer cancel()

	// workdir 参数覆盖 agent cwd。
	workdir := ec.CWD
	if wd := asString(anyLookup(args, "workdir")); wd != "" {
		workdir = wd
	}
	if workdir == "" {
		workdir = ec.AgentCWD
	}

	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
	if workdir != "" {
		cmd.Dir = workdir
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	err := cmd.Run()
	output := out.String() + errBuf.String()

	exitCode := 0
	if err != nil {
		if isTimeout(ctx) {
			return &Result{
				Content: fmt.Sprintf("command timed out after %dms\n%s", int(timeout.Milliseconds()), strings.TrimSpace(output)),
				IsError: true,
			}, nil
		}
		if ctx.Err() == context.Canceled {
			return &Result{
				Content: "command cancelled",
				IsError: true,
			}, nil
		}
		// non-zero exit：返回 exit code 文本但工具本身成功（不设 isError）。
		exitCode = exitCodeOf(err)
	}

	content := strings.TrimSpace(output)
	if exitCode != 0 {
		content = fmt.Sprintf("exit code: %d\n%s", exitCode, content)
	}
	if content == "" {
		content = "(no output)"
	}
	return &Result{Content: content, IsError: false}, nil
}

// exitCodeOf 提取进程退出码；拿不到时返回 -1。
func exitCodeOf(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// isTimeout 判断超时是否由我们的 timeout 触发（而非外部取消）。
func isTimeout(ctx context.Context) bool {
	return ctx.Err() == context.DeadlineExceeded
}

// anyLookup 从参数 map 取 key 的值，缺省返回 nil。
func anyLookup(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

// asString 把 any 转字符串，nil/非 string 返回空串。
func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	}
	return ""
}

// anyNumber 从参数取数值；缺省/非数值返回 def。
func anyNumber(m map[string]any, key string, def int) int {
	v := anyLookup(m, key)
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return def
}
