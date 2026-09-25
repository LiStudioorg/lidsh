// Package app 把配置树装配成可运行的应用：注册模块、驱动容器、执行 profile 入口。
package app

import (
	"context"
	"fmt"

	"lidsh/internal/config"
	"lidsh/internal/config/patches"
	"lidsh/internal/container"
)

// BundlePatches 返回编译期内置的 bundle 层。
func BundlePatches() map[string]config.BundlePatch { return patches.All() }

// BootOptions 是一次启动的输入。
type BootOptions struct {
	Profile    string
	ProfileDir string
	Home       string
	Workdir    string
	Entries    []*config.Entry
	AppArgs    []string
}

// Stub 是尚未移植的模块的占位实例。启动继续但明确告警——配置树保持与 DSH 同构，
// 缺口在日志里可见，而不是靠删配置行来掩盖。
type Stub struct{ Name string }

// Register 注册全部模块。已移植的实现从这里替换对应桩。
func Register(reg *container.Registry) {
	// M0：占位实现，逐个里程碑替换。
	stub := func(name string) container.Module {
		return container.Module{
			Decl: container.Decl{},
			Factory: func(a *container.App, id string, cfg map[string]any) (any, error) {
				a.Warn("module %q (%s) is not ported yet, running as stub", id, name)
				return &Stub{Name: name}, nil
			},
		}
	}

	// 与 internal/config/patches 里声明的 name 一一对应。
	for _, name := range declaredModules {
		m := stub(name)
		reg.Register(name, m.Decl, m.Factory)
	}
}

// declaredModules 是内置 patch 层引用的全部模块名。注册表与配置层必须同集合，
// 否则装配会因未知模块失败——这条不变量由 TestDeclaredModulesMatchPatches 守住。
var declaredModules = []string{
	"lidsh-llm",
	"lidsh-session",
	"lidsh-session-persistence-jsonl",
	"lidsh-attachment-local",
	"lidsh-typert-registry",
	"lidsh-api-gateway",
	"lidsh-agent",
	"lidsh-agent-default-model",
	"lidsh-settings-file",
	"lidsh-credentials-local",
	"lidsh-tools",
	"lidsh-tool-bash",
	"lidsh-tool-fs",
	"lidsh-tool-fs-search",
	"lidsh-tool-str-replace-editor",
	"lidsh-tool-present",
	"lidsh-tool-ask-user",
	"lidsh-tool-todo",
	"lidsh-plan-mode",
	"lidsh-skill",
	"lidsh-mcp-client",
	"lidsh-authorization",
	"lidsh-host-webserver",
	"lidsh-api-session-controller",
	"lidsh-api-workspace-controller",
	"lidsh-api-settings-controller",
	"lidsh-api-workspace-files",
	"lidsh-client-file-upload",
	"lidsh-host-frontend-static",
	"lidsh-web-startup",
	"lidsh-headless",
}

// Boot 装配并运行。M0 只装配+报告；profile 入口循环在 M1a/M1b 填充。
func Boot(ctx context.Context, opts BootOptions) error {
	reg := container.NewRegistry()
	Register(reg)

	app := container.New(ctx, reg, opts.Home, opts.Workdir, nil)
	app.Provide("profile", opts.Profile)
	app.Provide("workdir", opts.Workdir)
	app.Provide("profileDir", opts.ProfileDir)

	if err := app.Activate(opts.Entries); err != nil {
		return err
	}
	fmt.Printf("profile %q activated %d services: %v\n",
		opts.Profile, len(app.Activated()), app.IDs())

	switch opts.Profile {
	case "headless":
		return RunHeadless(ctx, opts.Workdir, opts.Home, opts.AppArgs)
	case "web":
		return RunWeb(ctx, opts.Home, opts.Workdir, opts.AppArgs)
	default:
		return fmt.Errorf("profile %q 的入口属后续里程碑；当前用 --dump-config 查看配置树", opts.Profile)
	}
}
