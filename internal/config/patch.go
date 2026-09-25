// Package config 复刻 DSH 的配置树组装：把若干 patch 层按序打到一个空的 loader
// 条目根上，得到一棵条目树，供服务容器按依赖驱动激活。
//
// 语义来源（实测 @deepseek-ai/dsh@0.1.5-rc.3）：
//   - applyEntryPatches：dsh-app-boot/lib/index.js:59-108（"THE patch semantics"）
//   - 层序：README.md "The tree composes over an empty root" + loadProfile 路径解析
//   - 整行覆盖不深合并：dsh-base/cordis.patch.yml 文件头注释原文
//     "A patch replaces the targeted row's whole `config` rather than merging into it"
package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Entry 是一条 loader 条目行：{ id, name, config?, group?, disabled?, ... }。
//
// Config 保留为原始 YAML 节点，是「整行覆盖不深合并」这条语义的关键——一旦在
// 合并之前解码成具体 struct，就会把各层的部分字段烤成一次合并，与 DSH 行为漂移。
// 消费方在激活时再按需解码。
type Entry struct {
	ID       string `yaml:"id,omitempty" json:"id,omitempty"`
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Group    bool   `yaml:"group,omitempty" json:"group,omitempty"`
	Disabled any    `yaml:"disabled,omitempty" json:"disabled,omitempty"`

	// Config 对 group 行是子条目列表（DSH 里 group 的子条目就放在 config 数组里），
	// 对普通行是该模块的配置文档。一律持原始节点，推迟解码。
	Config yamlNode `yaml:"config,omitempty" json:"config,omitempty"`

	// Source 记录该行最后一次被哪个层写入，供 --dump-config 溯源。非 YAML 字段。
	Source string `yaml:"-" json:"-"`

	// children 是 Group 行的子条目（DSH 存于 config 数组，这里派生出来便于索引）。
	children []*Entry
	// childrenDirty 标记 children 由 config 惰性拆出，ConfigNode 需要回写。
	childrenDirty bool
	// extras 承接 patch 写入的非标准键，DSH 允许 `target[key] = value` 任意键。
	extras map[string]any
}

// Patch 是一条 patch 行：{ id?, name?, insert?, ...整行覆盖字段 }。
type Patch struct {
	ID     string   `yaml:"id,omitempty" json:"id,omitempty"`
	Name   string   `yaml:"name,omitempty" json:"name,omitempty"`
	Insert []*Entry `yaml:"insert,omitempty" json:"insert,omitempty"`

	// Overrides 承接除 id/name/insert 之外的全部字段，逐字段整行覆盖。
	Overrides map[string]any `yaml:",inline" json:"-"`
}

// Layer 是一个带名字的 patch 来源（bundle 名 / profile / home / --patch）。
type Layer struct {
	Name    string
	Entries []*Entry // 仅 insert 语义（bundle 层通常整体 insert 一组行）
	Patches []*Patch
}

// entryMap 是 id → entry 的递归索引，group 子条目一并索引
// （对应 applyEntryPatches 的 buildMap）。
type entryMap map[string]*Entry

func (m entryMap) build(entries []*Entry) {
	for _, e := range entries {
		if e == nil {
			continue
		}
		if e.ID != "" {
			m[e.ID] = e
		}
		if children := e.childEntries(); e.Group && children != nil {
			m.build(children)
		}
	}
}

// ApplyEntryPatches 把 patches 依次打到 data 上，返回与输入完全脱钩的新树。
//
// 逐条对齐 applyEntryPatches 的行为：
//  1. 先深拷贝，输入永不变更（DSH 注释：否则热重载永远无法回滚被删掉的 patch）；
//  2. 递归建 id 索引，group 子条目同样入索引；
//  3. insert+id：目标必须是 group，否则告警跳过；追加后立刻 buildMap(insert)，
//     使同层后续 patch 能引用刚插入的行；
//  4. insert 无 id：追加到根；
//  5. 非 insert：id 必填；未命中 → 告警跳过；name 不匹配 → 告警并**整条跳过**；
//  6. 其余字段整行覆盖（config 整体替换，不深合并）；
//  7. 未命中只告警不报错。
//
// warn 为 nil 时静默。
func ApplyEntryPatches(data []*Entry, patches []*Patch, warn func(format string, args ...any)) []*Entry {
	out := cloneEntries(data)
	if len(patches) == 0 {
		return out
	}
	if warn == nil {
		warn = func(string, ...any) {}
	}

	index := entryMap{}
	index.build(out)

	for _, patch := range patches {
		if patch == nil {
			continue
		}

		if len(patch.Insert) > 0 {
			insert := cloneEntries(patch.Insert)
			if patch.ID != "" {
				target := index[patch.ID]
				if target == nil {
					warn("patch insert: entry %q not found", patch.ID)
					continue
				}
				if !target.Group {
					warn("patch insert: entry %q is not a group", patch.ID)
					continue
				}
				if err := target.appendChildren(insert); err != nil {
					warn("patch insert: entry %q %v", patch.ID, err)
					continue
				}
			} else {
				out = append(out, insert...)
			}
			// 插入即入索引：同层后续 patch 可指向本层刚插入的行。
			index.build(insert)
			continue
		}

		if patch.ID == "" {
			warn("patch: id is required for non-insert patches")
			continue
		}
		target := index[patch.ID]
		if target == nil {
			warn("patch: entry %q not found", patch.ID)
			continue
		}
		if patch.Name != "" && patch.Name != target.Name {
			warn("patch: name mismatch for %q (expected %q, got %q), skipping",
				patch.ID, target.Name, patch.Name)
			continue
		}
		for key, value := range patch.Overrides {
			if key == "id" {
				continue
			}
			if err := target.setField(key, value); err != nil {
				warn("patch: entry %q field %q: %v", patch.ID, key, err)
			}
		}
	}
	return out
}

// Compose 按 DSH 层序把多层折叠成最终条目树。
// 每层的 Entries 作为一次「无 id 的 insert」整体追加，Patches 随后应用，
// 因此单个 bundle 层既能插行又能改前面 bundle 的行。
func Compose(layers []Layer, warn func(string, ...any)) ([]*Entry, error) {
	var root []*Entry
	for _, layer := range layers {
		w := func(format string, args ...any) {
			warn("%s: "+format, append([]any{layer.Name}, args...)...)
		}
		if warn == nil {
			w = func(string, ...any) {}
		}
		if len(layer.Entries) > 0 {
			root = ApplyEntryPatches(root, []*Patch{{Insert: layer.Entries}}, w)
		}
		if len(layer.Patches) > 0 {
			for _, p := range layer.Patches {
				if p != nil {
					p.markSource(layer.Name)
				}
			}
			root = ApplyEntryPatches(root, layer.Patches, w)
		}
		for _, e := range layer.Entries {
			e.markSourceTree(layer.Name)
		}
	}
	return root, nil
}

// Dump 渲染与 DSH --dump-config 同构的可读视图：缩进树 + 每行来源。
func Dump(entries []*Entry) string {
	var b strings.Builder
	var walk func(entries []*Entry, depth int)
	walk = func(entries []*Entry, depth int) {
		for _, e := range entries {
			if e == nil {
				continue
			}
			indent := strings.Repeat("  ", depth)
			flags := ""
			if e.Group {
				flags += " group"
			}
			if e.Disabled != nil && e.Disabled != false {
				flags += " disabled"
			}
			fmt.Fprintf(&b, "%s- %s (%s)%s", indent, e.ID, e.Name, flags)
			if raw := e.Config.String(); raw != "" && !e.Group {
				fmt.Fprintf(&b, "  config=%s", compactJSON(raw))
			}
			if e.Source != "" {
				fmt.Fprintf(&b, "   # <- %s", e.Source)
			}
			b.WriteString("\n")
			if e.Group {
				walk(e.childEntries(), depth+1)
			}
		}
	}
	walk(entries, 0)
	return b.String()
}

func compactJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(out)
}
