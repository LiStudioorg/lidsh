package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain 拦截 Wrap 的自再执行：测试二进制当 launcher（与生产 cmd/lidsh 同路径）。
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		if err := HandleSandboxExec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// ---------- 升级阶梯（§2.3） ----------

func TestIsStrictlyWider(t *testing.T) {
	cases := []struct {
		from, to Mode
		want     bool
	}{
		{ModeReadOnly, ModeWorkspaceWrite, true},
		{ModeReadOnly, ModeDangerFullAccess, true},
		{ModeWorkspaceWrite, ModeDangerFullAccess, true},
		{ModeWorkspaceWrite, ModeReadOnly, false},
		{ModeDangerFullAccess, ModeReadOnly, false},
		{ModeDangerFullAccess, ModeWorkspaceWrite, false},
		{ModeReadOnly, ModeReadOnly, false},
	}
	for _, c := range cases {
		if got := IsStrictlyWider(c.from, c.to); got != c.want {
			t.Errorf("IsStrictlyWider(%s→%s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// ---------- 参数配对校验（§6.2） ----------

func TestValidateEscalationArgs(t *testing.T) {
	if err := ValidateEscalationArgs("", ""); err != nil {
		t.Errorf("both empty should pass: %v", err)
	}
	if err := ValidateEscalationArgs("workspace-write", ""); err == nil {
		t.Error("missing justification should fail")
	}
	if err := ValidateEscalationArgs("", "because"); err == nil {
		t.Error("justification without permissions should fail")
	}
	if err := ValidateEscalationArgs("read-only", "x"); err == nil {
		t.Error("read-only is not an escalation target")
	}
	if err := ValidateEscalationArgs("danger-full-access", "need root"); err != nil {
		t.Errorf("valid pair failed: %v", err)
	}
}

// ---------- 审批编排（§6.3） ----------

type fakeApprover struct{ outcome Outcome }

func (f fakeApprover) Request(ctx context.Context, req Request) Outcome {
	return f.outcome
}

func TestApproveEscalationOutcomes(t *testing.T) {
	ctx := context.Background()
	base := Request{ToolName: "bash", CallID: "c1", SessionID: "s1",
		Mode: ModeWorkspaceWrite, Justification: "need write", Policy: ApprovalAsk}

	t.Run("allowed-once", func(t *testing.T) {
		m, err := ApproveEscalation(ctx, fakeApprover{OutcomeAllowedOnce}, ModeReadOnly, base)
		if err != nil || m != ModeWorkspaceWrite {
			t.Fatalf("got mode=%v err=%v", m, err)
		}
	})
	t.Run("rejected", func(t *testing.T) {
		_, err := ApproveEscalation(ctx, fakeApprover{OutcomeRejected}, ModeReadOnly, base)
		want := `the user rejected escalating this bash to "workspace-write"`
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v want %q", err, want)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		_, err := ApproveEscalation(ctx, fakeApprover{OutcomeCancelled}, ModeReadOnly, base)
		want := `approval for escalating to "workspace-write" was cancelled`
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v want %q", err, want)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		_, err := ApproveEscalation(ctx, fakeApprover{OutcomeUnavailable}, ModeReadOnly, base)
		if err == nil || !strings.Contains(err.Error(), "no approval channel is available") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("non-wider-direct-throw", func(t *testing.T) {
		// 非严格更宽：直接抛，不请求审批（§6.3a）。
		_, err := ApproveEscalation(ctx, fakeApprover{OutcomeAllowedOnce}, ModeWorkspaceWrite, base)
		want := `sandbox escalation to "workspace-write" is not strictly wider than this call's current "workspace-write" mode`
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v want %q", err, want)
		}
	})
	t.Run("no-approver-fail-closed", func(t *testing.T) {
		// 无审批服务：fail-closed（§6.3b）。
		_, err := ApproveEscalation(ctx, nil, ModeReadOnly, base)
		if err == nil || !strings.Contains(err.Error(), "no approval channel") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("never-policy", func(t *testing.T) {
		// policy=never：直接 rejected（fail-closed 不弹窗，§6.4）。
		req := base
		req.Policy = ApprovalNever
		_, err := ApproveEscalation(ctx, fakeApprover{OutcomeAllowedOnce}, ModeReadOnly, req)
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("err=%v", err)
		}
	})
}

// ---------- 标记原文（§4） ----------

func TestMarkersExactText(t *testing.T) {
	if got, want := DenialMarker(ModeReadOnly),
		"[sandbox: file access denied under read-only mode]"; got != want {
		t.Errorf("DenialMarker=%q want %q", got, want)
	}
	if got, want := EscalationHintMarker("command"),
		"[sandbox: escalation available — retry this exact command once with sandbox_permissions (the narrowest wider mode that suffices) + justification; the approval prompt asks the user]"; got != want {
		t.Errorf("EscalationHintMarker=%q", got)
	}
	if got := RunnerFailureMarker(ModeWorkspaceWrite); !strings.Contains(got, "the sandbox runner itself failed under workspace-write mode") {
		t.Errorf("RunnerFailureMarker=%q", got)
	}
}

// ---------- containment（§5.3） ----------

func TestIsPathUnder(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "a", "b.txt")
	outside := filepath.Join(root, "..", "escape.txt")
	if !IsPathUnder(root, inside) {
		t.Error("inside should be under root")
	}
	if IsPathUnder(root, filepath.Clean(outside)) {
		t.Error("cleaned escape should not be under root")
	}
	if !IsPathUnder("/tmp", "/tmp/x/y") {
		t.Error("/tmp/x/y should be under /tmp")
	}
	if IsPathUnder("/tmp", "/tmpother/file") {
		t.Error("/tmpother must not match /tmp prefix")
	}
}

func TestWritableRoots(t *testing.T) {
	p := Policy{Mode: ModeReadOnly, WorkspaceRoot: "/w"}
	if r := p.WritableRoots(); len(r) != 1 || r[0] != "/dev/null" {
		t.Errorf("read-only roots = %v", r)
	}
	w := Policy{Mode: ModeWorkspaceWrite, WorkspaceRoot: "/w"}
	roots := w.WritableRoots()
	if len(roots) == 0 {
		t.Fatal("workspace-write should grant roots")
	}
	dedup := map[string]bool{}
	for _, r := range roots {
		if dedup[r] {
			t.Errorf("duplicate root %s", r)
		}
		dedup[r] = true
	}
}

// ---------- 探测与 confinement e2e（需要 Landlock；无则跳过） ----------

func skipIfNoLandlock(t *testing.T) string {
	sup := ProbeLandlock()
	if !sup.Available {
		t.Skip("kernel Landlock unavailable")
	}
	return "landlock"
}

func runConfined(t *testing.T, p Policy, argv []string) (exitErr error, stdout, stderr string) {
	t.Helper()
	runner := skipIfNoLandlock(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, err := WrapContext(ctx, runner, p, "", argv)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return err, out.String(), errb.String()
}

func TestConfinedReadOnlyDeniesWrite(t *testing.T) {
	target := filepath.Join(t.TempDir(), "f.txt")
	_, _, stderr := runConfined(t, Policy{Mode: ModeReadOnly, WorkspaceRoot: t.TempDir()},
		[]string{"/bin/bash", "-c", "echo x > " + target})
	if !strings.Contains(strings.ToLower(stderr), "permission denied") {
		t.Fatalf("expected landlock denial, stderr=%q", stderr)
	}
	// 目标文件不应被写出。
	if _, err := os.Stat(target); err == nil {
		t.Error("write should not have landed under read-only mode")
	}
}

func TestConfinedWorkspaceWriteAllowsRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ok.txt")
	_, _, stderr := runConfined(t, Policy{Mode: ModeWorkspaceWrite, WorkspaceRoot: root},
		[]string{"/bin/bash", "-c", "echo hi > " + target})
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "hi\n" {
		t.Fatalf("file content=%q err=%v", b, err)
	}
}

func TestConfinedWorkspaceWriteDeniesOutside(t *testing.T) {
	// 构造无歧义拒绝：父进程对真实工作区可写（宿主沙箱允许），
	// 但子进程的授权集=[别的 root, /tmp]，不含真实工作区 → 子进程 Landlock 必拒。
	otherRoot := t.TempDir()
	probe, err := filepath.Abs("../.sandbox-deny-probe")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(probe)
	defer os.Remove(probe)
	_, _, stderr := runConfined(t, Policy{Mode: ModeWorkspaceWrite, WorkspaceRoot: otherRoot},
		[]string{"/bin/bash", "-c", "echo x > " + probe})
	if !strings.Contains(strings.ToLower(stderr), "permission denied") {
		t.Fatalf("expected denial outside roots, stderr=%q", stderr)
	}
	if _, err := os.Stat(probe); err == nil {
		t.Error("write should not have landed outside roots")
	}
}

func TestClassifyFailure(t *testing.T) {
	denied, runnerFailed := ClassifyFailure("landlock", 1, "bash: /x: Permission denied")
	if !denied || runnerFailed {
		t.Errorf("landlock EACCES should classify denied: %v %v", denied, runnerFailed)
	}
	denied, runnerFailed = ClassifyFailure("bwrap", 1, "bwrap: something broke")
	if denied || !runnerFailed {
		t.Errorf("bwrap runner signature should classify runnerFailed")
	}
	denied, _ = ClassifyFailure("bwrap", 1, "touch: cannot touch: Read-only file system")
	if !denied {
		t.Error("EROFS should classify denied (bwrap)")
	}
	_, runnerFailed = ClassifyFailure("landlock", 125, "landlock-run: bad")
	if !runnerFailed {
		t.Error("exit 125 should classify runnerFailed (landlock)")
	}
}

func TestHandleSandboxExecArgv(t *testing.T) {
	if err := HandleSandboxExec([]string{"--mode"}); err == nil {
		t.Error("missing value should error")
	}
	if err := HandleSandboxExec([]string{"--mode", "read-only"}); err == nil {
		t.Error("missing -- should error")
	}
	if err := HandleSandboxExec([]string{"bogus"}); err == nil {
		t.Error("unexpected arg should error")
	}
	if err := HandleSandboxExec([]string{"--mode", "bogus", "--", "true"}); err == nil {
		t.Error("unknown mode must fail closed")
	}
}
