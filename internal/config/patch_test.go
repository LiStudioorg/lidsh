package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// warns 收集告警，便于断言 DSH 的「告警但跳过」行为。
type warns struct{ lines []string }

func (w *warns) fn(format string, args ...any) {
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

// mustEntries 解析一段条目 YAML，失败即终止测试。
func mustEntries(t *testing.T, src string) []*Entry {
	t.Helper()
	var entries []*Entry
	if err := yaml.Unmarshal([]byte(src), &entries); err != nil {
		t.Fatalf("parse entries: %v", err)
	}
	for _, e := range entries {
		if e != nil {
			_ = e.childEntries()
		}
	}
	return entries
}

// mustPatches 解析一段 patch YAML，失败即终止测试。
func mustPatches(t *testing.T, src string) []*Patch {
	t.Helper()
	var patches []*Patch
	if err := yaml.Unmarshal([]byte(src), &patches); err != nil {
		t.Fatalf("parse patches: %v", err)
	}
	return patches
}

func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func getenv(k string) string { return os.Getenv(k) }

func TestApplyInsertAtRoot(t *testing.T) {
	base := []*Entry{{ID: "llm", Name: "lidsh-llm"}}
	out := ApplyEntryPatches(base, []*Patch{{Insert: []*Entry{{ID: "agent", Name: "lidsh-agent"}}}}, nil)

	if len(out) != 2 {
		t.Fatalf("root insert should append, got %d entries", len(out))
	}
	if out[1].ID != "agent" {
		t.Errorf("appended entry = %q, want agent", out[1].ID)
	}
	// DSH: 输入永不变更（applyEntryPatches 第一行 structuredClone）。
	if len(base) != 1 {
		t.Errorf("input mutated: base has %d entries, want 1", len(base))
	}
}

func TestConfigOverrideIsWholeReplaceNotMerge(t *testing.T) {
	// DSH patch 文件头原文："A patch replaces the targeted row's whole config
	// rather than merging into it"。这里必须验证旧键消失而非被合并保留。
	entries := mustEntries(t, `
- id: session-title
  name: lidsh-session-title
  config:
    fallbackMaxWords: 5
    maxTitleBytes: 80
`)
	patches := mustPatches(t, `
- id: session-title
  config:
    maxTitleBytes: 200
`)
	w := &warns{}
	out := ApplyEntryPatches(entries, patches, w.fn)

	got := out[0].Config.String()
	if !strings.Contains(got, "maxTitleBytes: 200") {
		t.Errorf("config not overridden: %s", got)
	}
	if strings.Contains(got, "fallbackMaxWords") {
		t.Errorf("config was deep-merged, want whole replace; got: %s", got)
	}
	if len(w.lines) != 0 {
		t.Errorf("unexpected warnings: %v", w.lines)
	}
}

func TestNameMismatchSkipsWholePatch(t *testing.T) {
	// DSH: name 不匹配 → 告警并**整条跳过**，其余字段也不能被写入。
	entries := mustEntries(t, `
- id: agent
  name: lidsh-agent
  config:
    a: 1
`)
	patches := mustPatches(t, `
- id: agent
  name: wrong-name
  config:
    a: 999
`)
	w := &warns{}
	out := ApplyEntryPatches(entries, patches, w.fn)

	if !strings.Contains(out[0].Config.String(), "1") {
		t.Errorf("patch was partially applied despite name mismatch: %s", out[0].Config.String())
	}
	if len(w.lines) != 1 || !strings.Contains(w.lines[0], "name mismatch") {
		t.Errorf("want name mismatch warning, got %v", w.lines)
	}
}

func TestUnknownIdWarnsAndSkips(t *testing.T) {
	entries := mustEntries(t, "- id: llm\n  name: lidsh-llm\n")
	patches := mustPatches(t, "- id: ghost\n  config:\n    x: 1\n")
	w := &warns{}
	out := ApplyEntryPatches(entries, patches, w.fn)
	if len(out) != 1 {
		t.Fatalf("unknown id must not add entries, got %d", len(out))
	}
	if len(w.lines) != 1 || !strings.Contains(w.lines[0], "not found") {
		t.Errorf("want not-found warning, got %v", w.lines)
	}
}

func TestInsertIntoGroupIndexesInsertedRowsSameLayer(t *testing.T) {
	// DSH: 插入后立刻 buildMap(insert)，使同层后续 patch 能引用刚插入的行。
	entries := mustEntries(t, `
- id: tools
  name: lidsh-tools
  group: true
  config:
    - id: bash
      name: lidsh-tool-bash
`)
	patches := mustPatches(t, `
- id: tools
  insert:
    - id: web
      name: lidsh-tool-web
      config:
        timeoutMs: 1000
- id: web
  config:
    timeoutMs: 2000
`)
	w := &warns{}
	out := ApplyEntryPatches(entries, patches, w.fn)
	if len(w.lines) != 0 {
		t.Fatalf("unexpected warnings: %v", w.lines)
	}

	group := out[0]
	children := group.childEntries()
	if len(children) != 2 {
		t.Fatalf("group children = %d, want 2", len(children))
	}
	if children[1].ID != "web" {
		t.Errorf("inserted child = %q, want web", children[1].ID)
	}
	if got := children[1].Config.String(); !strings.Contains(got, "2000") {
		t.Errorf("same-layer patch could not target inserted row: %s", got)
	}
}

func TestInsertIntoNonGroupWarns(t *testing.T) {
	entries := mustEntries(t, "- id: llm\n  name: lidsh-llm\n")
	patches := mustPatches(t, "- id: llm\n  insert:\n    - id: x\n      name: n\n")
	w := &warns{}
	out := ApplyEntryPatches(entries, patches, w.fn)
	if len(w.lines) != 1 || !strings.Contains(w.lines[0], "not a group") {
		t.Errorf("want not-a-group warning, got %v", w.lines)
	}
	if len(out[0].childEntries()) != 0 {
		t.Error("non-group row must not gain children")
	}
}

func TestDisabledOverride(t *testing.T) {
	entries := mustEntries(t, "- id: hmr\n  name: lidsh-hmr\n  disabled: true\n")
	patches := mustPatches(t, "- id: hmr\n  disabled: false\n")
	out := ApplyEntryPatches(entries, patches, nil)
	if out[0].IsDisabled("", nil) {
		t.Error("disabled override to false not honored")
	}
}

func TestComposeLayerOrderLastWriteWins(t *testing.T) {
	// DSH 层序：bundle → profile → home → --patch，后层覆盖前层。
	bundles := map[string]BundlePatch{
		"base": {Entries: mustEntries(t, `
- id: agent-default-model
  name: lidsh-agent-default-model
  config:
    provider: deepseek-official
    model: deepseek-flash
`)},
	}
	homePatch := `
- id: agent-default-model
  config:
    provider: custom
    model: my-model
`
	dir := t.TempDir()
	if err := InitProfile(dir, []string{"base"}, "live"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir, ProfilePatchFilename, homePatch); err != nil {
		t.Fatal(err)
	}
	// InitProfile 不覆盖已存在文件——顺便验证这条约定。
	manifest, err := ReadProfileManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DSH.Profile.PatchReload != "live" || len(manifest.DSH.Profile.Bundles) != 1 {
		t.Errorf("manifest = %+v", manifest.DSH.Profile)
	}

	layers, err := BuildProfileLayers(dir, dir, []string{"base"}, nil, bundles, nil)
	if err != nil {
		t.Fatal(err)
	}
	// bundle → profile → home（home 层文件不存在时仍占一层，patch 为空）。
	if len(layers) != 3 {
		t.Fatalf("layers = %d, want bundle + profile + home", len(layers))
	}
	if layers[0].Name != "bundle:base" || layers[1].Name != "profile" || layers[2].Name != "home" {
		t.Errorf("layer order = %v", []string{layers[0].Name, layers[1].Name, layers[2].Name})
	}
	root, err := Compose(layers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != 1 {
		t.Fatalf("root = %d entries", len(root))
	}
	got := root[0].Config.String()
	if !strings.Contains(got, "my-model") {
		t.Errorf("profile layer did not win over bundle: %s", got)
	}
}

func TestJSExprEvaluatesAtActivation(t *testing.T) {
	entries := mustEntries(t, `
- id: session-persistence-jsonl
  name: lidsh-session-persistence
  config:
    root: !!js dshHomePath('sessions')
    nested:
      dir: !!js dshHomePath('storages', 'json')
`)
	var cfg struct {
		Root   string `yaml:"root"`
		Nested struct {
			Dir string `yaml:"dir"`
		} `yaml:"nested"`
	}
	if err := entries[0].DecodeConfig(&cfg, "/home/u/.dsh", nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Root != "/home/u/.dsh/sessions" {
		t.Errorf("root = %q", cfg.Root)
	}
	if cfg.Nested.Dir != "/home/u/.dsh/storages/json" {
		t.Errorf("nested dir = %q", cfg.Nested.Dir)
	}
	// 表达式必须在激活期求值：解析后原文仍在。
	if !strings.Contains(entries[0].Config.String(), "dshHomePath") {
		t.Error("config was eagerly evaluated at parse time")
	}
}

func TestJSExprUnknownFormErrors(t *testing.T) {
	entries := mustEntries(t, "- id: x\n  name: n\n  config:\n    root: !!js someFn('a')\n")
	var cfg map[string]any
	err := entries[0].DecodeConfig(&cfg, "/h", nil)
	if err == nil {
		t.Fatal("unknown !!js function must fail loud, not silently mis-path")
	}
	if !strings.Contains(err.Error(), "unknown !!js function") {
		t.Errorf("err = %v", err)
	}
}

func TestDumpRendersTree(t *testing.T) {
	entries := mustEntries(t, `
- id: tools
  name: lidsh-tools
  group: true
  config:
    - id: bash
      name: lidsh-tool-bash
- id: hmr
  name: lidsh-hmr
  disabled: true
`)
	out := Dump(entries)
	for _, want := range []string{"- tools (lidsh-tools) group", "  - bash (lidsh-tool-bash)", "- hmr (lidsh-hmr) disabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q:\n%s", want, out)
		}
	}
}

func TestPatchTemplateFileParses(t *testing.T) {
	// DSH 的空用户层模板是 `[]` 加注释，必须能当作"无 patch"解析。
	dir := t.TempDir()
	if err := InitProfile(dir, DefaultProfileBundles, DefaultPatchReload); err != nil {
		t.Fatal(err)
	}
	ps, err := LoadPatches(dir + "/" + ProfilePatchFilename)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("template should yield no patches, got %d", len(ps))
	}
}

func TestBootstrapEnvBlocklist(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"
	if err := writeFile(dir, ".env", "LIDSH_TEST_OK=1\nPATH=/evil\nLD_PRELOAD=/evil.so\n"); err != nil {
		t.Fatal(err)
	}
	w := &warns{}
	if err := LoadDotEnv(path, w.fn); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIDSH_TEST_OK", "")
	if len(w.lines) != 2 {
		t.Errorf("want 2 blocked warnings, got %v", w.lines)
	}
	if v := getenv("PATH"); v == "/evil" {
		t.Error("PATH was overwritten by .env")
	}
}
