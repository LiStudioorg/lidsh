package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lidsh/internal/container"

	"lidsh/internal/config"
)

// TestDeclaredModulesMatchPatches 守住一条不变量：内置 patch 层引用的每个 name
// 都必须在注册表里注册。装配阶段遇到未知模块会失败，这条测试让它在 `go test`
// 就暴露，而不是等启动。
func TestDeclaredModulesMatchPatches(t *testing.T) {
	declared := map[string]bool{}
	for _, n := range declaredModules {
		declared[n] = true
	}

	used := map[string]bool{}
	var collect func(entries []*config.Entry)
	collect = func(entries []*config.Entry) {
		for _, e := range entries {
			if e == nil {
				continue
			}
			if e.Name != "" {
				used[e.Name] = true
			}
			collect(e.Children())
		}
	}
	for name, bp := range BundlePatches() {
		for _, p := range bp.Patches {
			collect(p.Insert)
		}
		for _, e := range bp.Entries {
			collect([]*config.Entry{e})
		}
		_ = name
	}

	for name := range used {
		if !declared[name] {
			t.Errorf("patch layer references unregistered module %q", name)
		}
	}
	for name := range declared {
		if !used[name] {
			t.Errorf("module %q is registered but referenced by no patch layer", name)
		}
	}
}

// TestBootActivatesWholeTree 验证内置层能完整装配：group 展开、!!js 求值、
// 依赖驱动的激活顺序全部走通。
func TestBootActivatesWholeTree(t *testing.T) {
	home := t.TempDir()
	profileDir := home + "/profiles/web"
	if err := config.InitProfile(profileDir, config.ProfileTemplates["web"].Bundles, "live"); err != nil {
		t.Fatal(err)
	}
	layers, err := config.BuildProfileLayers(profileDir, home, config.ProfileTemplates["web"].Bundles, nil, BundlePatches(), nil)
	if err != nil {
		t.Fatal(err)
	}
	root, err := config.Compose(layers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root) == 0 {
		t.Fatal("empty tree")
	}

	dump := config.Dump(root)
	for _, want := range []string{"- llm (lidsh-llm)", "- tools (lidsh-tools) group", "  - tool-bash (lidsh-tool-bash)", "- webserver (lidsh-host-webserver)"} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump missing %q\n%s", want, dump)
		}
	}

	// 装配：容器需要能解析全部 name。
	reg := newTestRegistry(t)
	app := newTestApp(t, reg, home)
	if err := app.Activate(root); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if len(app.Activated()) != len(countRows(t, root)) {
		t.Errorf("activated %d, tree has %d non-group rows", len(app.Activated()), len(countRows(t, root)))
	}
}

// TestUserPatchOverridesBundle 复刻 DSH 的玩法：用户在 profile 的 cordis.patch.yml
// 里改一行配置，不需要重启即可生效（patchReload: live 由 watcher 承担，M1b 实现）。
func TestUserPatchOverridesBundle(t *testing.T) {
	home := t.TempDir()
	profileDir := home + "/profiles/web"
	if err := config.InitProfile(profileDir, []string{"base"}, "live"); err != nil {
		t.Fatal(err)
	}
	patch := `
- id: agent-default-model
  config:
    provider: my-provider
    model: my-model
- id: authorization
  config:
    mode: never
- id: hmr
  config:
    root: ['.']
`
	if err := writeFile(profileDir, config.ProfilePatchFilename, patch); err != nil {
		t.Fatal(err)
	}
	layers, err := config.BuildProfileLayers(profileDir, home, []string{"base"}, nil, BundlePatches(), nil)
	if err != nil {
		t.Fatal(err)
	}
	root, err := config.Compose(layers, nil)
	if err != nil {
		t.Fatal(err)
	}

	byID := index(root)
	got := byID["agent-default-model"].Config.String()
	if !strings.Contains(got, "my-model") || strings.Contains(got, "deepseek-flash") {
		t.Errorf("model row not overridden wholesale: %s", got)
	}

	// !!js 求值在激活期仍然工作。
	var cfg struct {
		Root string `yaml:"root"`
	}
	if err := byID["session-persistence-jsonl"].DecodeConfig(&cfg, home, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.Root, "/sessions") {
		t.Errorf("sessions root = %q, want .../sessions", cfg.Root)
	}

	// 未命中的 patch 行只告警不失败（DSH 同款）。
	var warns []string
	layers2, err := config.BuildProfileLayers(profileDir, home, []string{"base"},
		[]string{"- id: nonexistent\n  config:\n    x: 1\n"}, BundlePatches(),
		func(f string, a ...any) { warns = append(warns, f) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Compose(layers2, func(f string, a ...any) { warns = append(warns, f) }); err != nil {
		t.Fatal(err)
	}
	if len(warns) == 0 {
		t.Error("unmatched patch should warn, not fail")
	}
}

func index(entries []*config.Entry) map[string]*config.Entry {
	out := map[string]*config.Entry{}
	var walk func([]*config.Entry)
	walk = func(es []*config.Entry) {
		for _, e := range es {
			out[e.ID] = e
			walk(e.Children())
		}
	}
	walk(entries)
	return out
}

func countRows(t *testing.T, entries []*config.Entry) []string {
	var out []string
	var walk func([]*config.Entry)
	walk = func(es []*config.Entry) {
		for _, e := range es {
			if e.Group {
				walk(e.Children())
				continue
			}
			out = append(out, e.ID)
		}
	}
	walk(entries)
	return out
}

func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func newTestRegistry(t *testing.T) *container.Registry {
	t.Helper()
	reg := container.NewRegistry()
	Register(reg)
	return reg
}

func newTestApp(t *testing.T, reg *container.Registry, home string) *container.App {
	t.Helper()
	return container.New(context.Background(), reg, home, t.TempDir(), func(string, ...any) {})
}
