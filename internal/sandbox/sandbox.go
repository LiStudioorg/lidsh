// Package sandbox 复刻 dsh-sandbox / dsh-sandbox-policy 的沙箱语义层。
//
// 本里程碑（M2 sandbox 提权）聚焦「语义层 + 审批闭环」，不做内核级隔离
// （bwrap/Landlock 是 DSH 的 runner 职责，超范围）：实现模式枚举、严格更宽
// 升级阶梯、approval 编排、拒绝/升级标记、workspace-write 判定算法与写 fence。
//
// 契约来源：docs/_parts/bash-sandbox.md §2/§4/§5/§6（文件:行号 均引自该文档）。
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mode 是沙箱模式枚举（bash-sandbox.md §2.1）。
type Mode string

const (
	ModeReadOnly         Mode = "read-only"
	ModeWorkspaceWrite   Mode = "workspace-write"
	ModeDangerFullAccess Mode = "danger-full-access"
)

// AllModes 是运行时枚举数组（§2.1）。
var AllModes = []Mode{ModeReadOnly, ModeWorkspaceWrite, ModeDangerFullAccess}

// WiderModes 是严格更宽升级表（§2.3）：key 是不变模式，值是它可一步升到的更宽模式。
var WiderModes = map[Mode][]Mode{
	ModeReadOnly:       {ModeWorkspaceWrite, ModeDangerFullAccess},
	ModeWorkspaceWrite: {ModeDangerFullAccess},
}

// IsStrictlyWider 判断 to 是否严格比 from 更宽（非严格更宽 → 调用方直接抛，不提示人）。
func IsStrictlyWider(from, to Mode) bool {
	for _, m := range WiderModes[from] {
		if m == to {
			return true
		}
	}
	return false
}

// Policy 是一次调用的沙箱策略（§2.2：显式获批 mode > session 覆盖 > 部署默认）。
type Policy struct {
	Mode          Mode   // 生效模式
	WorkspaceRoot string // workspace 根（session.header.cwd canonical 化）
}

// WritableRoots 返回当前模式的可写根集（§5.1）：
// read-only → []；workspace-write → canonical(workspaceRoot, /tmp, os.tmpdir()) 去重。
func (p Policy) WritableRoots() []string {
	if p.Mode == ModeDangerFullAccess {
		return nil // 完全绕过，无限制
	}
	if p.Mode == ModeReadOnly {
		return []string{"/dev/null"} // 必需 sink
	}
	roots := []string{p.WorkspaceRoot, "/tmp", os.TempDir()}
	seen := map[string]bool{}
	var out []string
	for _, r := range roots {
		c := CanonicalPath(r)
		if c == "" {
			continue
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// ---------- 标记（§4） ----------

// DenialMarker 是拒绝标记的唯一生产函数（sandboxDenialMarker，§4）。
func DenialMarker(mode Mode) string {
	return fmt.Sprintf("[sandbox: file access denied under %s mode]", mode)
}

// EscalationHintMarker 是升级提示（§4，sandboxDenialMarker 配套）。
func EscalationHintMarker(subject string) string {
	return fmt.Sprintf(
		"[sandbox: escalation available — retry this exact %s once with sandbox_permissions (the narrowest wider mode that suffices) + justification; the approval prompt asks the user]",
		subject)
}

// RunnerFailureMarker 是 runner 自身失败（区别于拒绝，§4）。
func RunnerFailureMarker(mode Mode) string {
	return fmt.Sprintf(
		"[sandbox: the sandbox runner itself failed under %s mode — the command did not run; this is a sandbox problem, not a command failure]", mode)
}

// SandboxUnavailableMsg 是 fail-closed 的不可用文案。
const SandboxUnavailableMsg = "sandbox is unavailable: install bubblewrap or use a Landlock-enforcing kernel, then retry"

// ---------- canonical 化与 containment（§5.3） ----------

// CanonicalPath 复刻 canonicalPath()：realpath 失败回退原拼写（§5.3）。
func CanonicalPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// IsPathUnder 判定 target 是否落在 root 之下（isPathUnder，§5.3）：先 lexical
// 前缀匹配，不等则回退文件系统身份（逐级上溯比对 dev/ino）。
func IsPathUnder(root, target string) bool {
	root = CanonicalPath(root)
	target = CanonicalPath(target)

	// lexical 前缀：root、root+sep 前缀。
	if target == root {
		return true
	}
	if strings.HasPrefix(target, root) && len(target) > len(root) && target[len(root)] == os.PathSeparator {
		return true
	}

	// 回退：文件系统身份（realpath 已做，此处对上溯的每一级比对）。
	return sameInodeAncestry(root, target)
}

// sameInodeAncestry 逐级上溯 target 祖先，与 root 的 dev/ino 比对（§5.3
// 「stat(root) 后逐级上溯 target 祖先比对 dev/ino」）。
func sameInodeAncestry(root, target string) bool {
	ri, err := os.Stat(root)
	if err != nil {
		return false
	}
	cur := target
	for {
		ti, err := os.Stat(cur)
		if err != nil {
			return false
		}
		if os.SameFile(ri, ti) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}
