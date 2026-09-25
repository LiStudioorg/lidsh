//go:build !linux

// sandbox-exec 子命令执行端（非 Linux）：无 Landlock，fail-closed（§3.3 语义）。
package sandbox

import (
	"errors"
)

// ExecRestricted 非 Linux 上不可用：runner 失败退出码 125。
func ExecRestricted(mode Mode, root string, argv []string) error {
	return errors.New("landlock-run: sandbox-exec is not supported on this platform")
}

// ProbeLandlock 非 Linux 无 Landlock 内核接口。
func ProbeLandlock() LandlockSupport { return LandlockSupport{} }

// applyLandlock 非 Linux 不可用。
func applyLandlock(p Policy) error {
	return errors.New("landlock-run: not supported on this platform")
}
