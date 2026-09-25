// 工具注册表与各工具的覆盖测试。
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestCtx 构造一个默认 ExecContext。
func newTestCtx() *ExecContext {
	return &ExecContext{
		Signal:   context.Background(),
		AgentCWD: "/root/work/lidsh",
	}
}

// TestRegistrySchemasWhitelist 验证 Schemas 白名单只含 name/description/parameters，
// 不含 timeoutMs/presentCall 等内部字段。
func TestRegistrySchemasWhitelist(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:        "x",
		Description: "desc",
		InputSchema: map[string]any{"type": "object"},
		TimeoutMs:   9999,
		IsConcurrencySafe: func(map[string]any) bool {
			return true
		},
		PresentCall: func(map[string]any) *CallView { return &CallView{Card: "generic"} },
	})

	schemas := r.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	s := schemas[0]
	if s.Name != "x" || s.Description != "desc" {
		t.Errorf("whitelist fields wrong: %+v", s)
	}
	if _, ok := s.InputSchema["type"]; !ok {
		t.Errorf("inputSchema should be carried: %+v", s.InputSchema)
	}
}

// TestBashEcho：bash "echo hello" 返回含 hello。
func TestBashEcho(t *testing.T) {
	r := NewRegistry()
	RegisterBash(r)
	res, err := r.Execute(ToolCall{Name: "bash", Arguments: `{"command":"echo hello"}`}, newTestCtx())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("expected content to contain hello, got %q", res.Content)
	}
}

// TestBashExitCode：bash 命令 exit 3 返回含 "3"。
func TestBashExitCode(t *testing.T) {
	r := NewRegistry()
	RegisterBash(r)
	res, err := r.Execute(ToolCall{Name: "bash", Arguments: `{"command":"exit 3"}`}, newTestCtx())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("bash exit code should not be isError: %+v", res)
	}
	if !strings.Contains(res.Content, "3") {
		t.Errorf("expected content to contain exit code 3, got %q", res.Content)
	}
}

// TestBashEmptyCommand：空命令 isError。
func TestBashEmptyCommand(t *testing.T) {
	r := NewRegistry()
	RegisterBash(r)
	res, err := r.Execute(ToolCall{Name: "bash", Arguments: `{"command":""}`}, newTestCtx())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected isError for empty command, got %+v", res)
	}
}

// TestReadWriteRoundtrip：write 再 read 回读一致。
func TestReadWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	path := filepath.Join(dir, "a.txt")
	content := "line1\nline2\nline3\n"

	ec := &ExecContext{Signal: context.Background(), CWD: dir, AgentCWD: dir}

	wargs := marshalArgs(t, map[string]any{"path": path, "content": content})
	wres, err := r.Execute(ToolCall{Name: "write", Arguments: wargs}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if wres.IsError {
		t.Fatalf("write failed: %+v", wres)
	}

	rargs := marshalArgs(t, map[string]any{"path": path})
	rres, err := r.Execute(ToolCall{Name: "read", Arguments: rargs}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if rres.IsError {
		t.Fatalf("read failed: %+v", rres)
	}
	if !strings.Contains(rres.Content, "line2") {
		t.Errorf("expected read to contain content, got %q", rres.Content)
	}
}

// marshalArgs 把参数 map 序列化成合法 JSON 字符串。
func marshalArgs(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestEditNoMatch：edit 找不到 old_string → isError。
func TestEditNoMatch(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	ec := &ExecContext{Signal: context.Background(), CWD: dir, AgentCWD: dir}
	res, err := r.Execute(ToolCall{Name: "edit", Arguments: `{"path":"` + path + `","old_string":"zzz","new_string":"x"}`}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected isError for no-match edit, got %+v", res)
	}
}

// TestEditMultipleNoReplaceAll：old_string 出现多次且 replace_all=false → isError。
func TestEditMultipleNoReplaceAll(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("aa aa"), 0o644); err != nil {
		t.Fatal(err)
	}
	ec := &ExecContext{Signal: context.Background(), CWD: dir, AgentCWD: dir}
	res, err := r.Execute(ToolCall{Name: "edit", Arguments: `{"path":"` + path + `","old_string":"aa","new_string":"b"}`}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected isError for multiple-match without replace_all, got %+v", res)
	}
}

// TestEditReplaceAll：replace_all=true 替换全部。
func TestEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("aa aa"), 0o644); err != nil {
		t.Fatal(err)
	}
	ec := &ExecContext{Signal: context.Background(), CWD: dir, AgentCWD: dir}
	res, err := r.Execute(ToolCall{Name: "edit", Arguments: `{"path":"` + path + `","old_string":"aa","new_string":"b","replace_all":true}`}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "b b" {
		t.Errorf("expected file to become 'b b', got %q", string(data))
	}
}

// TestGrepFinds：grep 找到匹配行。
func TestGrepFinds(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ec := &ExecContext{Signal: context.Background(), CWD: dir, AgentCWD: dir}
	res, err := r.Execute(ToolCall{Name: "grep", Arguments: `{"pattern":"bet"}`}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res)
	}
	if !strings.Contains(res.Content, "beta") || !strings.Contains(res.Content, ":2:") {
		t.Errorf("expected grep to find beta on line 2, got %q", res.Content)
	}
}

// TestGlob：在 temp 目录建文件 glob 到。
func TestGlob(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	ec := &ExecContext{Signal: context.Background(), CWD: dir, AgentCWD: dir}

	res, err := r.Execute(ToolCall{Name: "glob", Arguments: `{"pattern":"*.go"}`}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Errorf("expected glob to find a.go, got %q", res.Content)
	}

	// ** 递归。
	res2, err := r.Execute(ToolCall{Name: "glob", Arguments: `{"pattern":"**/*.go"}`}, ec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Content, "sub/b.go") {
		t.Errorf("expected ** glob to find sub/b.go, got %q", res2.Content)
	}
}

// TestExecuteUnknownTool 验证未知工具返回 isError。
func TestExecuteUnknownTool(t *testing.T) {
	r := NewRegistry()
	res, err := r.Execute(ToolCall{Name: "nope", Arguments: `{}`}, newTestCtx())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected isError for unknown tool, got %+v", res)
	}
}
