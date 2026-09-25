package config

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// yamlNode 持有一条 config 的原始 YAML 值节点。
//
// 保留节点而非直接反序列化成 struct，有两个必要的好处：
//  1. patch 的「整行覆盖不深合并」语义要求合并期字段保真；
//  2. `!!js` 表达式（如 `root: !!js dshHomePath('sessions')`）必须在**激活期**
//     求值，而不是解析期——DSH 的 home 路径由注入的 helper 提供
//     （dsh-app-boot/lib/index.js:1530 ctx.provide("dshHomePath", ...)）。
type yamlNode struct {
	node *yaml.Node
}

// UnmarshalYAML 直接持有原始节点。自定义实现是必需的：若让 yaml 递归填充非导出
// 的 *yaml.Node 字段，它会被跳过，config 将静默变成空——patch 的整行覆盖无声失效。
func (n *yamlNode) UnmarshalYAML(value *yaml.Node) error {
	n.node = cloneNode(value)
	return nil
}

// MarshalYAML 把持有的节点写回文档。yaml.v3 对 *yaml.Node 有特殊处理。
func (n yamlNode) MarshalYAML() (any, error) {
	if n.node == nil {
		return nil, nil
	}
	return n.value(), nil
}

// value 归一化节点形态：解码进 *yaml.Node 字段得到的是值节点，而手工构造或
// yaml.Marshal 产出的是 DocumentNode。两种来源都要能处理，否则同一份配置在不同
// 代码路径下表现不同。
func (n yamlNode) value() *yaml.Node {
	if n.node == nil {
		return nil
	}
	if n.node.Kind == yaml.DocumentNode {
		if len(n.node.Content) != 1 {
			return nil
		}
		return n.node.Content[0]
	}
	return n.node
}

func (n yamlNode) String() string {
	v := n.value()
	if v == nil {
		return ""
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(out))
}

func (n yamlNode) IsZero() bool { return n.value() == nil }

// cloneEntries 深拷贝条目树。DSH 用 structuredClone（applyEntryPatches 第一行），
// 等价于这里的手工深拷贝。
func cloneEntries(in []*Entry) []*Entry {
	out := make([]*Entry, 0, len(in))
	for _, e := range in {
		if e == nil {
			continue
		}
		out = append(out, e.clone())
	}
	return out
}

func (e *Entry) clone() *Entry {
	dup := &Entry{ID: e.ID, Name: e.Name, Group: e.Group, Disabled: e.Disabled, Source: e.Source}
	dup.Config = yamlNode{node: cloneNode(e.Config.node)}
	if e.extras != nil {
		dup.extras = make(map[string]any, len(e.extras))
		for k, v := range e.extras {
			dup.extras[k] = v
		}
	}
	if e.children != nil {
		dup.children = cloneEntries(e.children)
	}
	return dup
}

func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	out := *n
	out.Content = nil
	out.Alias = nil
	if n.Content != nil {
		out.Content = make([]*yaml.Node, 0, len(n.Content))
		for _, c := range n.Content {
			out.Content = append(out.Content, cloneNode(c))
		}
	}
	return &out
}

// childEntries 惰性拆出 group 行的子条目。
//
// DSH 把 group 的子条目直接放在 config 数组里（applyEntryPatches 的
// `entry.group && Array.isArray(entry.config)`），这里派生成 children 以便索引与
// 追加，序列化时再回写 config，行为等价。
func (e *Entry) childEntries() []*Entry {
	if !e.Group {
		return nil
	}
	if e.children != nil {
		return e.children
	}
	if e.Config.IsZero() {
		return nil
	}
	body := e.Config.value()
	if body == nil || body.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range body.Content {
		child := &Entry{}
		if err := child.decodeNode(item); err != nil {
			return e.children // 尽力而为：畸形子行不阻断整棵树
		}
		e.children = append(e.children, child)
	}
	e.childrenDirty = true
	return e.children
}

// decodeNode 从 YAML 序列项还原一行条目。
func (e *Entry) decodeNode(node *yaml.Node) error {
	var body struct {
		ID       string     `yaml:"id"`
		Name     string     `yaml:"name"`
		Group    bool       `yaml:"group"`
		Disabled any        `yaml:"disabled"`
		Config   *yaml.Node `yaml:"config"`
	}
	if err := node.Decode(&body); err != nil {
		return fmt.Errorf("decode entry: %w", err)
	}
	e.ID, e.Name, e.Group, e.Disabled = body.ID, body.Name, body.Group, body.Disabled
	if body.Config != nil {
		e.Config = yamlNode{node: cloneNode(body.Config)}
	}
	if e.Group {
		_ = e.childEntries()
	}
	return nil
}

// appendChildren 往 group 行追加子条目。DSH 是 target.config.push(...insert)。
func (e *Entry) appendChildren(entries []*Entry) error {
	e.childEntries()
	e.children = append(e.children, entries...)
	e.childrenDirty = true
	e.Config = yamlNode{}
	return nil
}

// ConfigNode 返回该行生效的 config 值节点（group 行由 children 重建）。
func (e *Entry) ConfigNode() *yaml.Node {
	if !e.Group {
		return e.Config.value()
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, c := range e.childEntries() {
		seq.Content = append(seq.Content, c.node())
	}
	return seq
}

// node 渲染单行为 YAML 映射节点。
func (e *Entry) node() *yaml.Node {
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	scalar := func(s string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	}
	add := func(k, v string) {
		mapping.Content = append(mapping.Content, scalar(k), scalar(v))
	}
	if e.ID != "" {
		add("id", e.ID)
	}
	if e.Name != "" {
		add("name", e.Name)
	}
	if e.Group {
		add("group", "true")
	}
	if e.Disabled != nil {
		mapping.Content = append(mapping.Content, scalar("disabled"), mustNode(e.Disabled))
	}
	if cfg := e.ConfigNode(); cfg != nil {
		mapping.Content = append(mapping.Content, scalar("config"), cfg)
	}
	return mapping
}

func mustNode(v any) *yaml.Node {
	buf, err := yaml.Marshal(v)
	if err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	var node yaml.Node
	if err := yaml.Unmarshal(buf, &node); err != nil || len(node.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	return node.Content[0]
}

// setField 按 DSH 的 `target[key] = value` 整行覆盖语义写入一个字段
// （applyEntryPatches:102-105）。
func (e *Entry) setField(key string, value any) error {
	switch key {
	case "name":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
		e.Name = s
	case "group":
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("expected bool, got %T", value)
		}
		e.Group = b
	case "disabled":
		e.Disabled = value
	case "config":
		e.Config = yamlNode{node: mustNode(value)}
		e.children = nil
		e.childrenDirty = false
	default:
		// DSH 允许 patch 写任意键，未知键同样整行覆盖。
		if e.extras == nil {
			e.extras = map[string]any{}
		}
		e.extras[key] = value
	}
	return nil
}

func (e *Entry) markSource(name string) {
	e.Source = name
}

func (e *Entry) markSourceTree(name string) {
	e.markSource(name)
	for _, c := range e.childEntries() {
		c.markSourceTree(name)
	}
}

func (p *Patch) markSource(name string) {
	for _, e := range p.Insert {
		e.markSourceTree(name)
	}
}

// DecodeConfig 把该行的 config 解码进 target，并在此（激活期）求值 `!!js` 表达式。
func (e *Entry) DecodeConfig(target any, home string, env Env) error {
	node := e.ConfigNode()
	if node == nil {
		return nil
	}
	resolved, err := resolveExprs(node, home, env)
	if err != nil {
		return fmt.Errorf("entry %q: %w", e.ID, err)
	}
	buf, err := yaml.Marshal(resolved)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(buf, target); err != nil {
		return fmt.Errorf("entry %q: decode config: %w", e.ID, err)
	}
	return nil
}

// IsDisabled 返回该行的有效禁用状态。DSH 里 disabled 可以是布尔，也可以是
// `!!js` 表达式，由 loader realm 求值（cordis-plugin-loader/lib/index.js:378）。
func (e *Entry) IsDisabled(home string, env Env) bool {
	switch v := e.Disabled.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		if out, err := evalExpr(stripJS(v), home, env); err == nil {
			return out == "true" || out == "1"
		}
		return v != "" && v != "false"
	default:
		return true
	}
}

func stripJS(s string) string {
	return s
}

// Extras 返回 patch 写入的非标准键。
func (e *Entry) Extras() map[string]any { return e.extras }

// MarshalJSON 输出含派生 children 的通用 JSON 视图。
func (e *Entry) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID       string          `json:"id,omitempty"`
		Name     string          `json:"name,omitempty"`
		Group    bool            `json:"group,omitempty"`
		Disabled any             `json:"disabled,omitempty"`
		Config   json.RawMessage `json:"config,omitempty"`
		Children []*Entry        `json:"children,omitempty"`
		Source   string          `json:"source,omitempty"`
	}
	a := alias{ID: e.ID, Name: e.Name, Group: e.Group, Disabled: e.Disabled, Source: e.Source}
	if e.Group {
		a.Children = e.childEntries()
	} else if raw := e.Config.String(); raw != "" {
		var v any
		if err := yaml.Unmarshal([]byte(raw), &v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				a.Config = b
			}
		}
	}
	return json.Marshal(a)
}

// Children 返回 Group 行的子条目；非 group 行返回 nil。
func (e *Entry) Children() []*Entry { return e.childEntries() }
