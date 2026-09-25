// bash 工具，复刻 dsh-tool-bash。在 /bin/bash 下执行命令并按 §1.3 渲染输出。
//
// 语义要点（docs/_parts/bash-sandbox.md）：
//   - timeoutMs 工具参数自管，默认 120000ms（§1.2）。
//   - 渲染顺序（§1.3）：stdout 在前；stderr 非空 → body 末尾补换行后
//     追加 "[stderr]\n<err>"；全空 → "(no output)"；markers 依次追加各占一行：
//     沙箱拒绝 marker（+升级 hint）、"[timed out after Nms]"、
//     "[killed by signal: SIG]"、"[exit code: N]"。
//   - 非零退出不算 isError；只有超时/取消才 isError（§1.3 注）。
//   - M2：挂载 confinement executor 时 advertise sandbox_permissions +
//     justification（§1.2），执行前先走审批（§6），获批 mode 仅 stamp 本次调用。
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lidsh/internal/sandbox"
)

// defaultBashTimeoutMs 是 bash 工具默认超时（dsh-tool-bash 默认 120000ms）。
const defaultBashTimeoutMs = 120000

// bashInputSchema 是无沙箱组合的 bash schema（不 advertise 提权字段）。
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

// bashSandboxSchema 是挂载了 confinement executor 的 bash schema（§1.2：
// sandbox_permissions/justification 仅在 ESCALATION_TARGETS 非空时出现）。
var bashSandboxSchema = func() map[string]any {
	s := deepCopySchema(bashInputSchema)
	props := s["properties"].(map[string]any)
	props["sandbox_permissions"] = map[string]any{
		"type":        "string",
		"enum":        []any{"workspace-write", "danger-full-access"},
		"description": "The wider sandbox mode this command needs. Only valid as a one-shot retry of a command the sandbox just denied; requires justification and user approval.",
	}
	props["justification"] = map[string]any{
		"type":        "string",
		"description": "Required with sandbox_permissions: one sentence for the user explaining why this exact command needs the wider access.",
	}
	return s
}()

func deepCopySchema(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			out[k] = deepCopySchema(sub)
			continue
		}
		out[k] = v
	}
	return out
}

// RegisterBash 注册无沙箱组合的 bash 工具（不 advertise 提权字段）。
func RegisterBash(r *Registry) func() {
	return r.Register(Definition{
		Name:        "bash",
		Description: bashDescription(false),
		InputSchema: bashInputSchema,
		Execute:     execBash,
		PresentCall: bashPresentCall,
	})
}

// RegisterBashSandbox 注册挂载 confinement executor 的 bash 工具
// （advertise 提权字段；§1.2/§6.1）。
func RegisterBashSandbox(r *Registry) func() {
	return r.Register(Definition{
		Name:        "bash",
		Description: bashDescription(true),
		InputSchema: bashSandboxSchema,
		Execute:     execBash,
		PresentCall: bashPresentCall,
	})
}

// bashDescription 教育模型沙箱标记与提权重试（§4 工具描述原文）。
func bashDescription(sandboxed bool) string {
	d := "Execute a shell command and return its output."
	if sandboxed {
		d += " If the output contains [sandbox: file access denied under <mode> mode], the command was blocked by the sandbox; retry this exact command once with sandbox_permissions (the narrowest wider mode that suffices) and a justification."
	}
	return d
}

func bashPresentCall(args map[string]any) *CallView {
	cmd := asString(anyLookup(args, "command"))
	prev := cmd
	if len(prev) > 80 {
		prev = prev[:80] + "…"
	}
	return &CallView{Card: "terminal", Title: prev}
}

// execBash 执行 bash 命令（无沙箱组合：直接跑；有沙箱：先裁决后 confinement）。
func execBash(args map[string]any, ec *ExecContext) (*Result, error) {
	command := asString(anyLookup(args, "command"))
	if strings.TrimSpace(command) == "" {
		return &Result{Content: "Error: command is empty", IsError: true}, nil
	}

	timeout := time.Duration(anyNumber(args, "timeoutMs", defaultBashTimeoutMs)) * time.Millisecond
	ctx, cancel := context.WithTimeout(ec.Signal, timeout)
	defer cancel()

	workdir := ec.CWD
	if wd := asString(anyLookup(args, "workdir")); wd != "" {
		workdir = wd
	}
	if workdir == "" {
		workdir = ec.AgentCWD
	}

	argv := []string{"/bin/bash", "-c", command}

	// M2 组合守卫（§6.1）：无 sandboxing executor 时字段未 advertise，
	// 仍到达 execute 则抛（fail-closed，防模型盲试提权参数）。
	if ec.Sandbox == nil {
		if p := anyLookup(args, "sandbox_permissions"); p != nil {
			return &Result{Content: "Error: sandbox_permissions is not available in this composition (no confinement executor mounted)", IsError: true}, nil
		}
		return runBashPlain(ctx, workdir, argv, timeout)
	}
	return runBashSandboxed(ctx, args, ec, workdir, argv, timeout)
}

// runBashPlain 无 confinement 直跑（M1a 行为）。
func runBashPlain(ctx context.Context, workdir string, argv []string, timeout time.Duration) (*Result, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	return collectAndRender(cmd, ctx, nil, timeout)
}

// runBashSandboxed 是沙箱执行路径（§6 时序）：
// 1) 参数配对校验；2) 提权审批（获批 mode 仅本次 stamp）；
// 3) confinement runner 执行；4) 拒绝/runner 失败分类 + markers。
func runBashSandboxed(ctx context.Context, args map[string]any, ec *ExecContext, workdir string, argv []string, timeout time.Duration) (*Result, error) {
	sc := ec.Sandbox
	perms := asString(anyLookup(args, "sandbox_permissions"))
	just := asString(anyLookup(args, "justification"))

	// 1) 配对校验（§6.2）。
	if err := sandbox.ValidateEscalationArgs(perms, just); err != nil {
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}

	// 2) 提权审批（§6.3）：显式获批 mode > standing mode。
	policy := sc.Policy
	if perms != "" {
		approved, err := sandbox.ApproveEscalation(ctx, sc.Approver, policy.Mode, sandbox.Request{
			ToolName:      "bash",
			CallID:        sc.CallID,
			SessionID:     sc.SessionID,
			Mode:          sandbox.Mode(perms),
			Justification: just,
			Policy:        sc.Approval,
		})
		if err != nil {
			// 拒绝/取消/不可用：isError 结果（模型可见，可调整后继续）。
			return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
		}
		// 获批 mode 只 stamp 本次调用（§6.6）。
		policy.Mode = approved
	}

	// 3) confinement runner 包装执行（§3）。
	cmd, err := sandbox.WrapContext(ctx, sc.Runner, policy, workdir, argv)
	if err != nil {
		// runner 不可用：fail-closed（§3.1）。
		return &Result{Content: "Error: " + err.Error(), IsError: true}, nil
	}
	return collectAndRender(cmd, ctx, &bashConfine{runner: sc.Runner, mode: policy.Mode}, timeout)
}

// bashConfine 描述本次调用的 confinement 上下文（nil=无沙箱直跑）。
type bashConfine struct {
	runner string
	mode   sandbox.Mode // 本次调用生效 mode（获批 stamp 或 standing）
}

// collectAndRender 收集输出并按 §1.3 渲染。confine=nil 为无沙箱直跑。
func collectAndRender(cmd *exec.Cmd, ctx context.Context, confine *bashConfine, timeout time.Duration) (*Result, error) {
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout := out.String()
	stderr := errBuf.String()

	// 超时/取消：isError（基础设施失败，§1.3 注）。
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			m := []string{fmt.Sprintf("[timed out after %dms]", int(timeout.Milliseconds()))}
			return &Result{Content: renderBashOutput(stdout, stderr, m), IsError: true}, nil
		}
		if ctx.Err() == context.Canceled {
			return &Result{Content: "command cancelled", IsError: true}, nil
		}
	}

	// 非零退出：分类 + markers（§3.4/§1.3）。
	var markers []string
	if err != nil {
		exitCode := exitCodeOf(err)
		denied, runnerFailed := false, false
		if confine != nil {
			denied, runnerFailed = sandbox.ClassifyFailure(confine.runner, exitCode, stderr)
		}
		switch {
		case denied:
			// §1.3/§4：拒绝 marker + 升级 hint（本组合 advertise 了提权）。
			markers = append(markers, sandbox.DenialMarker(confine.mode),
				sandbox.EscalationHintMarker("command"))
		case runnerFailed:
			markers = append(markers, sandbox.RunnerFailureMarker(confine.mode))
		case exitCode != 0:
			markers = append(markers, fmt.Sprintf("[exit code: %d]", exitCode))
			if sig := signalOf(err); sig != "" {
				markers = append(markers, fmt.Sprintf("[killed by signal: %s]", sig))
			}
		}
	}

	return &Result{Content: renderBashOutput(stdout, stderr, markers), IsError: false}, nil
}

// renderBashOutput 复刻 renderResult（§1.3）：stdout 在前、[stderr] 段、
// (no output)、markers 各占一行追加。
func renderBashOutput(stdout, stderr string, markers []string) string {
	var body string
	if stdout != "" {
		body = stdout
	}
	if stderr != "" {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body += "[stderr]\n" + stderr
	}
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		body = "(no output)"
	}
	for _, m := range markers {
		body += "\n" + m
	}
	return body
}

// exitCodeOf 提取进程退出码；拿不到时返回 -1。
func exitCodeOf(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// signalOf 提取终止信号名（wait status），无信号返回空。
func signalOf(err error) string {
	ee, ok := err.(*exec.ExitError)
	if !ok {
		return ""
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	return ws.Signal().String()
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
