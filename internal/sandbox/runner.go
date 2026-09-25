// confinement runner 链（bash-sandbox.md §3）：Linux 首选 bwrap，回退原生
// Landlock（self-restrict-then-exec，经本二进制的 sandbox-exec 子命令再执行）；
// 全不可用 → fail-closed（SandboxUnavailableError 语义）。
//
// 拒绝分类（§3.4/§4）：非零退出 + stderr 命中 DENIAL_SIGNATURES → denied。
// runner 自身失败：landlock launcher 退出码 125；bwrap 签名 "bwrap: "。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrSandboxUnavailable 是 fail-closed 的不可用错误（§3.1）。
var ErrSandboxUnavailable = errors.New(SandboxUnavailableMsg)

// DenialSignature 是 stderr 拒绝方言（大小写不敏感子串匹配，§3.4/§4）。
type runnerSignatures struct {
	denial []string // 命令被拒（DENIAL_SIGNATURES）
	runner []string // runner 本身坏了（runnerFailureRules）
}

var signatures = map[string]runnerSignatures{
	"bwrap":    {denial: []string{"read-only file system"}, runner: []string{"bwrap: "}},
	"landlock": {denial: []string{"permission denied"}, runner: []string{"landlock-run: "}},
}

// runnerFailureExit 是 launcher 级失败退出码（landlock：125，§3.3）。
const runnerFailureExit = 125

// DetectRunner 按链探测（linux: bwrap → landlock；§3.1 PLATFORM_CHAINS）。
// 返回可用 runner 名（"bwrap"/"landlock"）或错误（fail-closed）。
func DetectRunner() (string, error) {
	if probeBwrap() {
		return "bwrap", nil
	}
	if ProbeLandlock().Available {
		return "landlock", nil
	}
	return "", ErrSandboxUnavailable
}

// probeBwrap 复刻 defaultProbeBwrap（§3.1）：read-only profile 跑 true，exit 0 即可用。
func probeBwrap() bool {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return false
	}
	args := append(bwrapProfileArgs(Policy{Mode: ModeReadOnly}), "--", "true")
	cmd := exec.Command(path, args...)
	return cmd.Run() == nil
}

// bwrapProfileArgs 复刻 bwrapProfileArgs（§3.2）。
func bwrapProfileArgs(p Policy) []string {
	args := []string{"--ro-bind", "/", "/", "--dev", "/dev", "--unshare-pid", "--proc", "/proc", "--die-with-parent"}
	if p.Mode == ModeWorkspaceWrite {
		args = append(args, "--tmpfs", "/tmp")
		if p.WorkspaceRoot != "" {
			args = append(args, "--bind", p.WorkspaceRoot, p.WorkspaceRoot)
		}
	}
	return args
}

// Wrap 把 argv 包装成受限执行命令（无 deadline 版）。danger-full-access 直接透传（§2.1）。
// policy 之外需要 runner（DetectRunner 的结果）与工作目录。
func Wrap(runner string, p Policy, workdir string, argv []string) (*exec.Cmd, error) {
	return WrapContext(context.Background(), runner, p, workdir, argv)
}

// WrapContext 同 Wrap，但命令绑定 ctx 的 deadline/取消（bash 的 timeoutMs 用）。
func WrapContext(ctx context.Context, runner string, p Policy, workdir string, argv []string) (*exec.Cmd, error) {
	if p.Mode == ModeDangerFullAccess {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = workdir
		return cmd, nil
	}
	switch runner {
	case "bwrap":
		args := append(bwrapProfileArgs(p), "--")
		args = append(args, argv...)
		cmd := exec.CommandContext(ctx, "bwrap", args...)
		cmd.Dir = workdir
		return cmd, nil
	case "landlock":
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve self: %w", err)
		}
		args := []string{"sandbox-exec", "--mode", string(p.Mode)}
		if p.WorkspaceRoot != "" {
			args = append(args, "--root", p.WorkspaceRoot)
		}
		args = append(args, "--")
		args = append(args, argv...)
		cmd := exec.CommandContext(ctx, self, args...)
		cmd.Dir = workdir
		return cmd, nil
	case "":
		return nil, ErrSandboxUnavailable
	default:
		return nil, fmt.Errorf("sandbox: unknown runner %q", runner)
	}
}

// ClassifyFailure 在命令非零退出后分类（§3.4）：denied=命令被沙箱拒绝；
// runnerFailed=runner 自身故障（提示用户这是沙箱问题而非命令失败）；ok=普通退出。
func ClassifyFailure(runner string, exitCode int, stderr string) (denied, runnerFailed bool) {
	sig := signatures[runner]
	low := strings.ToLower(stderr)
	for _, s := range sig.denial {
		if strings.Contains(low, strings.ToLower(s)) {
			return true, false
		}
	}
	for _, s := range sig.runner {
		if strings.Contains(low, strings.ToLower(s)) || exitCode == runnerFailureExit {
			return false, true
		}
	}
	return false, false
}
