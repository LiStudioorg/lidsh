//go:build linux

// 原生 Landlock confinement（Linux runner 链回退项，复刻 DSH landlock-run：
// node-addon-system/src/main.c「self-restrict-then-exec」）。
//
// 与 DSH 相同的机制：raw syscall 建 ruleset → 逐条 path_beneath 授权 →
// prctl(PR_SET_NO_NEW_PRIVS) → landlock_restrict_self → exec。
// 父进程不自我限制（通过再执行本二进制的 sandbox-exec 子命令实现隔离），
// 与 DSH 用独立 launcher 二进制同构。
//
// ABI：v1（内核 5.13+）即有全部 path 权限位；v2 加 REFER、v3 加 TRUNCATE。
// 内核低于 v3 时 enforcement=partial（复刻 DSH 的 partial enforcement 上报）。
package sandbox

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Landlock syscall 号在 x86_64 与 arm64 的通用系统调用表同号（bash-sandbox.md §3.3）。
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446
	landlockRulePathBeneath  = 1
	landlockCreateVersion    = 1 // LANDLOCK_CREATE_RULESET_VERSION
	prSetNoNewPrivs          = 38
)

// LANDLOCK_ACCESS_FS_* 位（UAPI v1 全集，bits 0-12；v2 +REFER、v3 +TRUNCATE）。
const (
	accessExecute    = 1 << 0
	accessWrite      = 1 << 1
	accessReadFile   = 1 << 2
	accessReadDir    = 1 << 3
	accessRemoveDir  = 1 << 4
	accessRemoveFile = 1 << 5
	accessMakeChar   = 1 << 6
	accessMakeDir    = 1 << 7
	accessMakeReg    = 1 << 8
	accessMakeSock   = 1 << 9
	accessMakeFifo   = 1 << 10
	accessMakeBlock  = 1 << 11
	accessMakeSym    = 1 << 12
	accessRefer      = 1 << 13
	accessTruncate   = 1 << 14
)

// accessAllV1 是 v1 内核能处理的全部权限位。
const accessAllV1 = accessExecute | accessWrite | accessReadFile | accessReadDir |
	accessRemoveDir | accessRemoveFile | accessMakeChar | accessMakeDir | accessMakeReg |
	accessMakeSock | accessMakeFifo | accessMakeBlock | accessMakeSym

// accessRO 是只读授权（root 下可执行/读文件/列目录）。
const accessRO = accessExecute | accessReadFile | accessReadDir

// Linux O_PATH（0x200000，x86_64/arm64 相同；Go stdlib syscall 未导出）。
const oPathFlag = 0x200000

// rulesetAttr 对应 struct landlock_ruleset_attr{ __u64 handled_access_fs; }。
type rulesetAttr struct {
	handledAccessFS uint64
}

// pathBeneathAttr 对应 struct landlock_path_beneath_attr{ __u64 allowed_access; __s32 parent_fd; }。
type pathBeneathAttr struct {
	allowedAccess uint64
	parentFd      int32
}

// LandlockSupport 描述内核 Landlock 能力。
type LandlockSupport struct {
	Available   bool
	ABIVersion  int    // 0=无；1/2/3/4…
	Enforcement string // "full"（ABI≥3）| "partial"（ABI<3，REFER/TRUNCATE 缺失）
}

// ProbeLandlock 探测内核 Landlock ABI 版本（create_ruleset(NULL,0,VERSION)）。
func ProbeLandlock() LandlockSupport {
	// flags=LANDLOCK_CREATE_RULESET_VERSION：返回受支持的最高 ABI 版本。
	r, _, errno := syscall.RawSyscall(sysLandlockCreateRuleset, 0, 0, landlockCreateVersion)
	if errno != 0 || int(r) < 1 {
		return LandlockSupport{Available: false}
	}
	v := int(r)
	ef := "partial"
	if v >= 3 {
		ef = "full"
	}
	return LandlockSupport{Available: true, ABIVersion: v, Enforcement: ef}
}

// grant 是一条 path_beneath 授权。
type grant struct {
	path   string // O_PATH 打开目标（文件或目录）
	access uint64
}

// landlockProfile 复刻 landlockProfileArgs（§3.3）：
// --ro / --rw /dev/null [--rw /tmp --rw <workspaceRoot>]。
func landlockProfile(p Policy) []grant {
	if p.Mode == ModeDangerFullAccess {
		return nil
	}
	g := []grant{{path: "/", access: accessRO}, {path: "/dev/null", access: accessWrite | accessReadFile}}
	if p.Mode == ModeWorkspaceWrite {
		g = append(g,
			grant{path: "/tmp", access: accessAllV1},
			grant{path: p.WorkspaceRoot, access: accessAllV1},
		)
	}
	return g
}

// applyLandlock 在当前进程自我施加 Landlock 限制（sandbox-exec 子命令路径内调用）。
// 成功后本进程及其后代全部受限；失败即返回错误（fail-closed，绝不 exec）。
func applyLandlock(p Policy) error {
	sup := ProbeLandlock()
	if !sup.Available {
		return fmt.Errorf("landlock-run: kernel Landlock unavailable")
	}
	handled := accessAllV1
	if sup.ABIVersion >= 2 {
		handled |= accessRefer
	}
	if sup.ABIVersion >= 3 {
		handled |= accessTruncate
	}
	attr := rulesetAttr{handledAccessFS: uint64(handled)}
	rulesetFd, _, errno := syscall.RawSyscall(sysLandlockCreateRuleset,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock-run: create_ruleset: errno %d", errno)
	}
	defer syscall.Close(int(rulesetFd))

	for _, gr := range landlockProfile(p) {
		fd, err := syscall.Open(gr.path, oPathFlag|syscall.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("landlock-run: open %s: %w", gr.path, err)
		}
		rule := pathBeneathAttr{allowedAccess: uint64(gr.access), parentFd: int32(fd)}
		_, _, errno := syscall.RawSyscall6(sysLandlockAddRule,
			rulesetFd, landlockRulePathBeneath,
			uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		syscall.Close(fd)
		if errno != 0 {
			return fmt.Errorf("landlock-run: add_rule %s: errno %d", gr.path, errno)
		}
	}
	// prctl(PR_SET_NO_NEW_PRIVS, 1)——restrict_self 的前置。
	if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("landlock-run: prctl(NO_NEW_PRIVS): errno %d", errno)
	}
	// landlock_restrict_self：此后本进程不可逆受限。
	if _, _, errno := syscall.RawSyscall(sysLandlockRestrictSelf, rulesetFd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock-run: restrict_self: errno %d", errno)
	}
	return nil
}
