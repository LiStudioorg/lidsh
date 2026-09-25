package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadEntries 解析一个 cordis.patch.yml 形状的条目文件（顶层 YAML 数组）。
// bundle 层用这个：它整体是一组 insert 行。
func LoadEntries(path string) ([]*Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}
	var entries []*Entry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}
	for _, e := range entries {
		if e != nil {
			_ = e.childEntries()
		}
	}
	return entries, nil
}

// ParsePatchesYAML 从内存解析 patch 层。内置 bundle 层的文本形态与 DSH 的
// cordis.patch.yml 一致：顶层是 `- insert: [...]` 的 patch 数组。
func ParsePatchesYAML(data []byte) ([]*Patch, error) {
	var raw []yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse built-in bundle: %w", err)
	}
	patches := make([]*Patch, 0, len(raw))
	for i := range raw {
		p := &Patch{}
		if err := raw[i].Decode(p); err != nil {
			return nil, fmt.Errorf("built-in bundle row %d: %w", i+1, err)
		}
		for _, e := range p.Insert {
			if e != nil {
				_ = e.childEntries()
			}
		}
		patches = append(patches, p)
	}
	return patches, nil
}

// LoadPatches 解析一个用户 patch 层（顶层数组，元素是 patch 行）。
// 与 LoadEntries 同文件形态，差别在解释方式：DSH 的 profile/home 层是 patch 列表。
func LoadPatches(path string) ([]*Patch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read patch file %s: %w", path, err)
	}
	// 空文件（模板里的 `[]`）解析成 nil，合法。
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "[]" {
		return nil, nil
	}
	var raw []yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse patch file %s: %w", path, err)
	}
	patches := make([]*Patch, 0, len(raw))
	for i := range raw {
		p := &Patch{}
		if err := raw[i].Decode(p); err != nil {
			return nil, fmt.Errorf("patch file %s row %d: %w", path, i+1, err)
		}
		// insert 里的行也需要归一化 group 子条目。
		for _, e := range p.Insert {
			if e != nil {
				_ = e.childEntries()
			}
		}
		patches = append(patches, p)
	}
	return patches, nil
}

// LoadPatchInline 解析 --patch 覆盖层传入的内联 YAML。
func LoadPatchInline(src string) ([]*Patch, error) {
	tmp, err := os.CreateTemp("", "lidsh-patch-*.yml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(src); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return LoadPatches(tmp.Name())
}

// ProfileManifest 是 profile 目录的 package.json 中 DSH 关心的部分：
//
//	{ "name": ..., "dependencies": {...},
//	  "dsh": { "profile": { "bundles": [...], "patchReload": "live|startup" } } }
//
// （dsh-app-boot 的 initProfile 写出这个形状，lib/index.js:379-398）
type ProfileManifest struct {
	Name         string            `json:"name"`
	Dependencies map[string]string `json:"dependencies"`
	DSH          struct {
		Profile struct {
			Bundles     []string `json:"bundles"`
			PatchReload string   `json:"patchReload"`
		} `json:"profile"`
		Bundle struct {
			Patch string `json:"patch"`
		} `json:"bundle"`
	} `json:"dsh"`
}

// ReadProfileManifest 读 profile 目录的 package.json。
func ReadProfileManifest(dir string) (*ProfileManifest, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m ProfileManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// WriteProfileManifest 写出/更新 profile manifest，保持 2 空格缩进 + 结尾换行
// （DSH 用 JSON.stringify(manifest, void 0, 2) + "\n"）。
func WriteProfileManifest(dir string, m *ProfileManifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "package.json"), append(data, '\n'), 0o644)
}

// ProfileTemplates 逐字复刻 DSH 的 PROFILE_TEMPLATES（dsh-app-boot/lib/index.js:328-349）。
// 名称 → bundle 层序 + 用户 patch 文件的生命周期。
var ProfileTemplates = map[string]struct {
	Bundles     []string
	PatchReload string
}{
	"web":         {Bundles: []string{"base", "web-app"}, PatchReload: "live"},
	"headless":    {Bundles: []string{"base", "headless"}, PatchReload: "startup"},
	"sdk":         {Bundles: []string{"base", "sdk-app"}, PatchReload: "startup"},
	"acp":         {Bundles: []string{"base", "acp-app"}, PatchReload: "startup"},
	"sdk-minimal": {Bundles: []string{"sdk-minimal"}, PatchReload: "startup"},
}

// DefaultProfileBundles 是 `lidsh plugin` 初始化无名模板 profile 时的初始层
// （dsh-app-boot/lib/index.js:357）。
var DefaultProfileBundles = []string{"base"}

// DefaultPatchReload 是自定义 profile 的用户 patch 生命周期（同上 :359）。
const DefaultPatchReload = "live"

// ProfilePatchFilename 是 profile/home 用户层文件名（DSH: cordis.patch.yml）。
const ProfilePatchFilename = "cordis.patch.yml"

// ProfilePatchTemplate 逐字复刻 DSH 写出的空用户层内容（含注释，:360-364）。
const ProfilePatchTemplate = `# Your patch layer for this lidsh profile, applied after every bundle layer:
# a top-level YAML array of loader patch entries (id-targeted config
# overrides, disables, and insert lists; ` + "`!!js`" + ` expressions allowed).
[]
`

// InitProfile 初始化 profile 目录：manifest + 空用户层。已存在的文件不覆盖，
// 因此重复执行在已初始化的目录上是 no-op（DSH initProfile 的同款约定）。
func InitProfile(dir string, bundles []string, patchReload string) error {
	if patchReload == "" {
		patchReload = DefaultPatchReload
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "package.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		m := &ProfileManifest{Name: "lidsh-profile-" + filepath.Base(dir)}
		m.Dependencies = map[string]string{}
		m.DSH.Profile.Bundles = append([]string(nil), bundles...)
		m.DSH.Profile.PatchReload = patchReload
		if err := WriteProfileManifest(dir, m); err != nil {
			return err
		}
	}
	patchPath := filepath.Join(dir, ProfilePatchFilename)
	if _, err := os.Stat(patchPath); os.IsNotExist(err) {
		if err := os.WriteFile(patchPath, []byte(ProfilePatchTemplate), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// BuildProfileLayers 按 DSH 的层序组装一个 profile 的全部层：
//
//	bundles 顺序 → profile cordis.patch.yml → $DSH_HOME/cordis.patch.yml → --patch
//
// bundlePatches 提供 bundle 名 → 其 patch 常量（Go 里没有 npm 解析，模块在编译期
// 注册，patch 也跟着编译进来）。
func BuildProfileLayers(profileDir, dshHome string, bundles []string, userPatches []string, bundlePatches map[string]BundlePatch, warn func(string, ...any)) ([]Layer, error) {
	var layers []Layer
	for _, name := range bundles {
		bp, ok := bundlePatches[name]
		if !ok {
			return nil, fmt.Errorf("unknown bundle %q (registered: %v)", name, registeredBundles(bundlePatches))
		}
		layers = append(layers, Layer{Name: "bundle:" + name, Entries: bp.Entries, Patches: bp.Patches})
	}

	profilePatch := filepath.Join(profileDir, ProfilePatchFilename)
	patches, err := LoadPatches(profilePatch)
	if err != nil {
		return nil, err
	}
	layers = append(layers, Layer{Name: "profile", Patches: patches})

	homePatch := filepath.Join(dshHome, ProfilePatchFilename)
	patches, err = LoadPatches(homePatch)
	if err != nil {
		return nil, err
	}
	layers = append(layers, Layer{Name: "home", Patches: patches})

	for i, src := range userPatches {
		var ps []*Patch
		var err error
		if _, statErr := os.Stat(src); statErr == nil {
			ps, err = LoadPatches(src)
		} else {
			ps, err = LoadPatchInline(src)
		}
		if err != nil {
			return nil, fmt.Errorf("--patch[%d]: %w", i, err)
		}
		layers = append(layers, Layer{Name: fmt.Sprintf("--patch[%d]", i), Patches: ps})
	}
	_ = warn
	return layers, nil
}

// BundlePatch 是一个 bundle 编译进来的 patch 层。DSH 里它来自包目录的
// `dsh.bundle.patch` 指向的 cordis.patch.yml；Go 里在编译期注册。
type BundlePatch struct {
	Entries []*Entry
	Patches []*Patch
}

func registeredBundles(m map[string]BundlePatch) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
