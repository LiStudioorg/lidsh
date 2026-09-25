// lidsh 是 DSH（DeepSeek Harness）的 Go + Vue 3 复刻。
//
// 命令文法逐条对照 dsh（README.md "Entry modes" / "App arguments"）：
//
//	lidsh --profile <name>                 启动该 profile
//	lidsh --profile <n> --from-default-profile <t>   从模板建 profile 再启动
//	lidsh web                              等价 --profile web
//	lidsh --profile <name> --dump-config   打印组装后的配置树，不启动
//	lidsh --dump-default-config            打印内置层组装结果
//	lidsh --profile <name> --patch <yml>   追加覆盖层
//	lidsh plugin --profile <name> ...      profile 插件管理（Go 版给提示）
//
// launcher 只解析自己的 flag，第一个不认识的 token 起全部交给 profile 内的 app
// 解析（DSH: "The first token the launcher does not recognize starts the app's
// arguments"）。非法命令、跨模式选项、配置错误、启动失败一律非零退出。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"lidsh/internal/app"
	"lidsh/internal/config"
)

const usage = `lidsh — DeepSeek Harness (Go + Vue 3 reproduction)

Usage:
  lidsh --profile <name> [app args...]     boot the named profile
  lidsh --profile <name> --from-default-profile <template>
  lidsh web [app args...]                  alias of --profile web
  lidsh --profile <name> --dump-config     print the composed tree, don't boot
  lidsh --dump-default-config              print the built-in layer composition
  lidsh --profile <name> --patch <yaml>    extra overlay layer (repeatable)
  lidsh --help                             this help

Profiles live under $LIDSH_HOME/profiles/<name>. The invoking directory is the
default workspace root. The name "desktop" is reserved and rejected.
`

// reservedProfiles 复刻 DSH 对 Electron 保留名的处理：拒绝 boot / config-dump /
// plugin-management 三类请求。
var reservedProfiles = map[string]bool{"desktop": true}

// launchOptions 是 launcher 自己的 flag；其后参数原样转交 app。
type launchOptions struct {
	profile          string
	fromTemplate     string
	dumpConfig       bool
	dumpDefault      bool
	patches          []string
	dumpDefaultModel bool
	appArgs          []string
}

// parseLauncherArgs 复刻 DSH 的严格性：只认自己的 flag，遇到第一个非 flag token
// 即停止解析并把它及其后参数交给 app。
func parseLauncherArgs(argv []string) (*launchOptions, error) {
	opts := &launchOptions{}
	i := 0
	for i < len(argv) {
		arg := argv[i]
		if !strings.HasPrefix(arg, "-") {
			break
		}
		next := func(flagName string) (string, error) {
			if i+1 >= len(argv) {
				return "", fmt.Errorf("%s requires a value", flagName)
			}
			i++
			return argv[i], nil
		}
		switch arg {
		case "--help", "-h":
			return nil, flag.ErrHelp
		case "--profile", "-p":
			v, err := next("--profile")
			if err != nil {
				return nil, err
			}
			opts.profile = v
		case "--from-default-profile":
			v, err := next("--from-default-profile")
			if err != nil {
				return nil, err
			}
			opts.fromTemplate = v
		case "--dump-config":
			opts.dumpConfig = true
		case "--dump-default-config":
			opts.dumpDefault = true
		case "--patch":
			v, err := next("--patch")
			if err != nil {
				return nil, err
			}
			opts.patches = append(opts.patches, v)
		default:
			// DSH 拒绝跨模式选项；这里同样拒绝未知 flag 而非忽略。
			return nil, fmt.Errorf("unknown launcher option %q (run `lidsh --help`)", arg)
		}
		i++
	}
	opts.appArgs = argv[i:]

	// `lidsh web` 是 `--profile web` 的别名。
	if opts.profile == "" && len(opts.appArgs) > 0 && opts.appArgs[0] == "web" {
		opts.profile = "web"
		opts.appArgs = opts.appArgs[1:]
	}
	if opts.profile == "" && !opts.dumpDefault {
		return nil, fmt.Errorf("--profile is required (or use `lidsh web`)")
	}
	if opts.profile != "" && reservedProfiles[opts.profile] {
		return nil, fmt.Errorf("profile %q is reserved and cannot be booted, dumped, or managed", opts.profile)
	}
	return opts, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			fmt.Print(usage)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "lidsh: %v\n", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	opts, err := parseLauncherArgs(argv)
	if err != nil {
		return err
	}

	home := config.ResolveHome(config.OS)
	// 复刻 loadEnv：读 cwd/.env，但黑名单键拒绝写入。
	if err := config.LoadDotEnv(filepath.Join(mustGetwd(), ".env"),
		func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "lidsh: "+format+"\n", args...)
		}); err != nil {
		return err
	}

	if opts.dumpDefault {
		root, err := composeDefault()
		if err != nil {
			return err
		}
		fmt.Print(config.Dump(root))
		return nil
	}

	profileDir := filepath.Join(home, "profiles", opts.profile)

	// 从模板创建：DSH 的 --from-default-profile 只接受未使用的、非内置模板名。
	if opts.fromTemplate != "" {
		tmpl, ok := config.ProfileTemplates[opts.fromTemplate]
		if !ok {
			return fmt.Errorf("unknown default profile template %q (known: %s)",
				opts.fromTemplate, strings.Join(templateNames(), ", "))
		}
		if _, err := os.Stat(filepath.Join(profileDir, "package.json")); err == nil {
			return fmt.Errorf("profile %q already exists at %s", opts.profile, profileDir)
		}
		if err := config.InitProfile(profileDir, tmpl.Bundles, tmpl.PatchReload); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "lidsh: created profile %q at %s\n", opts.profile, profileDir)
	}

	// 读 profile manifest 决定层序；不存在时按内置模板自动初始化
	// （DSH: web/headless/sdk/sdk-minimal/acp 首次使用自动初始化）。
	manifest, err := config.ReadProfileManifest(profileDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var bundles []string
	if manifest != nil {
		bundles = manifest.DSH.Profile.Bundles
	} else {
		tmpl, ok := config.ProfileTemplates[opts.profile]
		if !ok {
			return fmt.Errorf("profile %q is not initialized and has no shipped template; create it with --from-default-profile", opts.profile)
		}
		if err := config.InitProfile(profileDir, tmpl.Bundles, tmpl.PatchReload); err != nil {
			return err
		}
		bundles = tmpl.Bundles
	}
	if len(bundles) == 0 {
		return fmt.Errorf("profile %q declares no bundles", opts.profile)
	}

	layers, err := config.BuildProfileLayers(profileDir, home, bundles, opts.patches, app.BundlePatches(), warn)
	if err != nil {
		return err
	}
	root, err := config.Compose(layers, warn)
	if err != nil {
		return err
	}

	// DSH: "--dump-config to inspect the composed tree without booting it"。
	if opts.dumpConfig {
		fmt.Print(config.Dump(root))
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return app.Boot(ctx, app.BootOptions{
		Profile:    opts.profile,
		ProfileDir: profileDir,
		Home:       home,
		Workdir:    mustGetwd(),
		Entries:    root,
		AppArgs:    opts.appArgs,
	})
}

// composeDefault 组装内置层（--dump-default-config）。
func composeDefault() ([]*config.Entry, error) {
	bundles := config.ProfileTemplates["web"].Bundles
	layers, err := config.BuildProfileLayers(defaultDumpHome(), config.ResolveHome(config.OS), bundles, nil, app.BundlePatches(), warn)
	if err != nil {
		return nil, err
	}
	return config.Compose(layers, warn)
}

// defaultDumpHome 只为 --dump-default-config 准备一个无 profile 覆盖层的空目录，
// 使输出只反映内置层。
func defaultDumpHome() string {
	if d, err := os.MkdirTemp("", "lidsh-default-"); err == nil {
		return d
	}
	return config.ResolveHome(config.OS)
}

func templateNames() []string {
	out := make([]string, 0, len(config.ProfileTemplates))
	for k := range config.ProfileTemplates {
		out = append(out, k)
	}
	return out
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lidsh: "+format+"\n", args...)
}
