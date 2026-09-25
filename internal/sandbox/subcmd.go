// sandbox-exec 子命令的 argv 解析与分发（cmd/lidsh 与测试二进制共用）。
//
// Wrap 经 os.Executable() 再执行当前二进制并传 sandbox-exec 前缀；生产二进制
// 与 go test 的测试二进制都能处理（测试经 TestMain 回执拦截同一路径）。
package sandbox

import "fmt"

// HandleSandboxExec 处理内部子命令 sandbox-exec：
//
//	sandbox-exec --mode <m> [--root <dir>] -- <argv...>
//
// 自我施加 Landlock 限制后 exec 目标命令（进程映像被替换，限制随进程存活）。
func HandleSandboxExec(argv []string) error {
	mode := ModeReadOnly
	root := ""
	i := 0
	for i < len(argv) {
		switch argv[i] {
		case "--mode":
			if i+1 >= len(argv) {
				return fmt.Errorf("sandbox-exec: --mode requires a value")
			}
			i++
			mode = Mode(argv[i])
			// 未知模式直接拒绝（fail-closed，绝不静默降级）。
			switch mode {
			case ModeReadOnly, ModeWorkspaceWrite, ModeDangerFullAccess:
			default:
				return fmt.Errorf("sandbox-exec: unknown --mode %q (want read-only|workspace-write|danger-full-access)", mode)
			}
		case "--root":
			if i+1 >= len(argv) {
				return fmt.Errorf("sandbox-exec: --root requires a value")
			}
			i++
			root = argv[i]
		case "--":
			return ExecRestricted(mode, root, argv[i+1:])
		default:
			return fmt.Errorf("sandbox-exec: unexpected argument %q", argv[i])
		}
		i++
	}
	return fmt.Errorf("sandbox-exec: missing '--' before target command")
}
