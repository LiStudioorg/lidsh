//go:build linux

// sandbox-exec 子命令执行端：applyLandlock 自限制后 exec 目标命令。
// 复刻 landlock-run launcher 语义：失败即非零退出不 exec（fail-closed）。
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// ExecRestricted 供 cmd/lidsh 的 sandbox-exec 子命令调用：
// 自我施加 Landock 限制后 exec argv（进程映像被替换，限制随进程存活）。
func ExecRestricted(mode Mode, root string, argv []string) error {
	p := Policy{Mode: mode, WorkspaceRoot: root}
	if p.Mode == ModeDangerFullAccess {
		return execv(argv)
	}
	if err := applyLandlock(p); err != nil {
		// launcher 级失败：退出码 125，stderr 带 landlock-run: 签名（§3.3）。
		fmt.Fprintln(os.Stderr, "landlock-run:", err)
		os.Exit(runnerFailureExit)
	}
	return execv(argv)
}

func execv(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("sandbox-exec: empty argv")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}
