// M2 沙箱 standing 配置构造（web/headless 共用）。
//
// 环境变量：
//
//	LIDSH_SANDBOX           read-only | workspace-write | danger-full-access
//	                        默认 danger-full-access（与实测 DSH 部署一致：schema
//	                        默认 read-only，部署经配置注入 danger-full-access）。
//	                        danger-full-access/未设置 → 不挂 confinement executor
//	                        （提权字段不 advertise，bash 直跑）。
//	LIDSH_SANDBOX_APPROVAL  ask | never，默认 ask（never：提权直接拒绝，fail-closed）。
//
// runner 探测失败不阻断启动：Wrap 执行期 fail-closed（§3.1 同语义）。
package app

import (
	"fmt"
	"os"

	"lidsh/internal/sandbox"
	"lidsh/internal/tools"
)

// buildSandboxConfig 按环境构建 standing 沙箱配置；nil=未挂载。
// headless=true 时不绑审批通道（无浏览器，提权 fail-closed：§6.3b）。
func buildSandboxConfig(workdir string, headless bool) *tools.SandboxOptions {
	raw := os.Getenv("LIDSH_SANDBOX")
	if raw == "" {
		return nil // 默认不挂载（danger-full-access 直跑）
	}
	var mode sandbox.Mode
	switch sandbox.Mode(raw) {
	case sandbox.ModeReadOnly, sandbox.ModeWorkspaceWrite, sandbox.ModeDangerFullAccess:
		mode = sandbox.Mode(raw)
	default:
		fmt.Fprintf(os.Stderr, "lidsh: unknown LIDSH_SANDBOX %q (want read-only|workspace-write|danger-full-access); running unsandboxed\n", raw)
		return nil
	}
	if mode == sandbox.ModeDangerFullAccess {
		return nil // danger：不挂 executor
	}
	runner, err := sandbox.DetectRunner()
	if err != nil {
		// 探测失败不阻断：执行期 fail-closed（Wrap 返回不可用错误）。
		fmt.Fprintf(os.Stderr, "lidsh: sandbox runner unavailable (%v); tools will fail closed at run\n", err)
		runner = ""
	}
	policy := sandbox.ApprovalAsk
	if p := os.Getenv("LIDSH_SANDBOX_APPROVAL"); p == string(sandbox.ApprovalNever) {
		policy = sandbox.ApprovalNever
	}
	_ = headless // web 的审批通道由 server.New 绑定（瀑布回环）；headless 留 nil。
	return &tools.SandboxOptions{
		Mode:   mode,
		Root:   sandbox.CanonicalPath(workdir),
		Runner: runner,
		Policy: policy,
	}
}
