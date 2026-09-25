package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lidsh/internal/sandbox"
)

// TestMain 拦截 Wrap 的自再执行（测试二进制当 launcher；与 cmd/lidsh 同路径）。
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		if err := sandbox.HandleSandboxExec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeApprover 按固定 outcome 应答审批。
type fakeApprover struct{ outcome sandbox.Outcome }

func (f fakeApprover) Request(ctx context.Context, req sandbox.Request) sandbox.Outcome {
	return f.outcome
}

// sandboxedCtx 构造一个挂载沙箱的执行上下文。
func sandboxedCtx(mode sandbox.Mode, root string, ap sandbox.Approver) *ExecContext {
	runner := ""
	if r, err := sandbox.DetectRunner(); err == nil {
		runner = r
	}
	return &ExecContext{
		Signal:   context.Background(),
		AgentCWD: root,
		CWD:      root,
		Sandbox: &SandboxContext{
			Policy:    sandbox.Policy{Mode: mode, WorkspaceRoot: root},
			Runner:    runner,
			Approver:  ap,
			Approval:  sandbox.ApprovalAsk,
			SessionID: "ses_test",
			CallID:    "call_test",
		},
	}
}

// ---------- schema advertise（§1.2/§6.1） ----------

func TestBashSchemaEscalationAdvertise(t *testing.T) {
	r1 := NewRegistry()
	RegisterBash(r1)
	b1 := r1.Get("bash")
	if b1 == nil {
		t.Fatal("bash not registered")
	}
	p1 := b1.InputSchema["properties"].(map[string]any)
	if _, ok := p1["sandbox_permissions"]; ok {
		t.Error("plain bash must not advertise sandbox_permissions")
	}

	r2 := NewRegistry()
	RegisterBashSandbox(r2)
	b2 := r2.Get("bash")
	if b2 == nil {
		t.Fatal("sandboxed bash not registered")
	}
	p2 := b2.InputSchema["properties"].(map[string]any)
	sp, ok := p2["sandbox_permissions"].(map[string]any)
	if !ok {
		t.Fatal("sandboxed bash must advertise sandbox_permissions")
	}
	enum, _ := sp["enum"].([]any)
	if len(enum) != 2 || enum[0] != "workspace-write" || enum[1] != "danger-full-access" {
		t.Errorf("escalation targets enum = %v", enum)
	}
	if _, ok := p2["justification"]; !ok {
		t.Error("sandboxed bash must advertise justification")
	}
}

// ---------- 无沙箱组合守卫（§6.1） ----------

func TestBashCompositionGuard(t *testing.T) {
	r := NewRegistry()
	RegisterBash(r)
	res, err := r.Execute(ToolCall{Name: "bash",
		Arguments: `{"command":"echo hi","sandbox_permissions":"danger-full-access","justification":"x"}`},
		newTestCtx())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not available in this composition") {
		t.Fatalf("expected composition guard error, got %+v", res)
	}
}

// ---------- 拒绝 → marker + 升级 hint（§1.3/§4，真实 Landlock e2e） ----------

func TestBashSandboxDeniedReadOnly(t *testing.T) {
	if !sandbox.ProbeLandlock().Available {
		t.Skip("kernel Landlock unavailable")
	}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim.txt") // 不在授权集内
	r := NewRegistry()
	RegisterBashSandbox(r)
	res, err := r.Execute(ToolCall{Name: "bash",
		Arguments: fmt.Sprintf(`{"command":"echo x > %s","description":"probe write"}`, target)},
		sandboxedCtx(sandbox.ModeReadOnly, root, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("denial is a marker result, not isError: %+v", res)
	}
	if !strings.Contains(res.Content, "[sandbox: file access denied under read-only mode]") {
		t.Errorf("missing denial marker: %q", res.Content)
	}
	if !strings.Contains(res.Content, "[sandbox: escalation available — retry this exact command once with sandbox_permissions") {
		t.Errorf("missing escalation hint: %q", res.Content)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("write must not have landed under read-only mode")
	}
}

// ---------- 提权闭环：获批 mode 仅 stamp 本次调用（§6.3/§6.6） ----------

func TestBashEscalationApprovedOneShot(t *testing.T) {
	if !sandbox.ProbeLandlock().Available {
		t.Skip("kernel Landlock unavailable")
	}
	root := t.TempDir()
	inside := filepath.Join(root, "ok.txt")
	r := NewRegistry()
	RegisterBashSandbox(r)
	args, _ := json.Marshal(map[string]any{
		"command":     fmt.Sprintf("echo approved > %s", inside),
		"description": "escalated write",
		"timeoutMs":   10000,
		// 第一次被 read-only 拒绝后的合法重试：请求 workspace-write + 理由。
		"sandbox_permissions": "workspace-write",
		"justification":       "need to write the requested file inside the workspace",
	})
	res, err := r.Execute(ToolCall{Name: "bash", Arguments: string(args)},
		sandboxedCtx(sandbox.ModeReadOnly, root, fakeApprover{sandbox.OutcomeAllowedOnce}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("approved escalation should run: %q", res.Content)
	}
	if b, err := os.ReadFile(inside); err != nil || !strings.Contains(string(b), "approved") {
		t.Fatalf("escalated write did not land: %v %q", err, b)
	}
}

func TestBashEscalationRejected(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	RegisterBashSandbox(r)
	args, _ := json.Marshal(map[string]any{
		"command":             "echo x",
		"description":         "probe",
		"sandbox_permissions": "danger-full-access",
		"justification":       "want root",
	})
	res, err := r.Execute(ToolCall{Name: "bash", Arguments: string(args)},
		sandboxedCtx(sandbox.ModeReadOnly, root, fakeApprover{sandbox.OutcomeRejected}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, `the user rejected escalating this bash to "danger-full-access"`) {
		t.Fatalf("expected rejection error, got %+v", res)
	}
}

func TestBashEscalationNonWiderThrows(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	RegisterBashSandbox(r)
	args, _ := json.Marshal(map[string]any{
		"command":             "echo x",
		"description":         "probe",
		"sandbox_permissions": "workspace-write",
		"justification":       "same mode",
	})
	res, err := r.Execute(ToolCall{Name: "bash", Arguments: string(args)},
		sandboxedCtx(sandbox.ModeWorkspaceWrite, root, fakeApprover{sandbox.OutcomeAllowedOnce}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not strictly wider") {
		t.Fatalf("expected non-wider error, got %+v", res)
	}
}

func TestBashEscalationPairValidation(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	RegisterBashSandbox(r)
	// 只有 permissions 没有 justification → 配对校验失败（§6.2）。
	res, err := r.Execute(ToolCall{Name: "bash",
		Arguments: `{"command":"echo x","description":"p","sandbox_permissions":"danger-full-access"}`},
		sandboxedCtx(sandbox.ModeReadOnly, root, fakeApprover{sandbox.OutcomeAllowedOnce}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "sandbox_permissions requires a justification") {
		t.Fatalf("expected pair validation error, got %+v", res)
	}
}

// ---------- fs 写 fence（§5.3/§4） ----------

func TestFSWriteFenceReadOnly(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "f.txt")
	r := NewRegistry()
	RegisterFSTools(r)
	res, err := r.Execute(ToolCall{Name: "write",
		Arguments: fmt.Sprintf(`{"path":%q,"content":"x"}`, target)},
		sandboxedCtx(sandbox.ModeReadOnly, root, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("read-only write must be denied: %+v", res)
	}
	if !strings.Contains(res.Content, "[sandbox: file access denied under read-only mode]") ||
		!strings.Contains(res.Content, "retry this exact operation once with sandbox_permissions") {
		t.Fatalf("fence denial text wrong: %q", res.Content)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("write must not have landed")
	}
}

func TestFSWriteFenceWorkspaceWrite(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	RegisterFSTools(r)
	// 授权集内（workspace root 下）→ 放行。
	inside := filepath.Join(root, "ok.txt")
	res, err := r.Execute(ToolCall{Name: "write",
		Arguments: fmt.Sprintf(`{"path":%q,"content":"x"}`, inside)},
		sandboxedCtx(sandbox.ModeWorkspaceWrite, root, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("inside-root write should pass: %+v", res)
	}
	// 授权集外（/etc）→ fence 拒绝（写前拦截，不触碰磁盘）。
	res, err = r.Execute(ToolCall{Name: "write",
		Arguments: `{"path":"/etc/lidsh-fence-probe","content":"x"}`},
		sandboxedCtx(sandbox.ModeWorkspaceWrite, root, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "file access denied under workspace-write mode") {
		t.Fatalf("outside-root write must be denied: %+v", res)
	}
}

func TestFSEditFenceReadOnly(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "seed.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	RegisterFSTools(r)
	res, err := r.Execute(ToolCall{Name: "edit",
		Arguments: fmt.Sprintf(`{"path":%q,"old_string":"seed","new_string":"new"}`, target)},
		sandboxedCtx(sandbox.ModeReadOnly, root, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "file access denied under read-only mode") {
		t.Fatalf("read-only edit must be denied: %+v", res)
	}
}

func TestFSWriteNoSandboxPass(t *testing.T) {
	// 未挂载沙箱（ec.Sandbox=nil）→ fence 直放（M1a 行为不变）。
	root := t.TempDir()
	target := filepath.Join(root, "free.txt")
	r := NewRegistry()
	RegisterFSTools(r)
	res, err := r.Execute(ToolCall{Name: "write",
		Arguments: fmt.Sprintf(`{"path":%q,"content":"x"}`, target)}, newTestCtx())
	if err != nil || res.IsError {
		t.Fatalf("unsandboxed write should pass: %v %+v", err, res)
	}
}
