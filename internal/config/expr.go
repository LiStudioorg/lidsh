package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Env 是表达式求值可见的环境变量读取器，便于测试替换。
type Env interface {
	Getenv(string) string
}

type osEnv struct{}

func (osEnv) Getenv(k string) string { return os.Getenv(k) }

// OS 是默认的环境变量读取器。
var OS Env = osEnv{}

// jsExprTags 复刻 DSH 的 `!!js` 自定义标签：
//
//	construct: (data) => ({ __jsExpr: data })   // dsh-app-boot/lib/index.js:20
//
// 值在**激活期**（而非解析期）经 loader realm 求值
// （cordis-plugin-loader/lib/index.js:296 `isJsExpr(value) → evaluate(...)`）。
// yaml.v3 对内建前缀形态可能给缩写或全形，两种都要认。
var jsExprTags = map[string]bool{
	"!!js":                 true,
	"tag:yaml.org,2002:js": true,
}

// 支持的表达式文法（Go 里没有 JS 引擎，这里实现 DSH patch 实际用到的子集，
// 并在遇到不认识的表达式时报错而不是静默给出错误路径）：
//
//	dshHomePath('sub', ...)  →  filepath.Join(home, sub, ...)
//	homePath(...)            →  同上别名
//	env('NAME')              →  环境变量值
//	'literal' | "literal"    →  字面量
var (
	callRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\((.*)\)$`)
	// 逐参数前缀匹配（不能锚定结尾，否则多参数表达式解析不了）。
	argRe    = regexp.MustCompile(`^\s*('[^']*'|"[^"]*")\s*(,\s*)?`)
	scalarRe = regexp.MustCompile(`^('[^']*'|"[^"]*")$`)
)

// resolveExprs 递归替换 YAML 树里的 `!!js` 节点，返回新节点（不改输入）。
func resolveExprs(node *yaml.Node, home string, env Env) (*yaml.Node, error) {
	if node == nil {
		return nil, nil
	}
	if env == nil {
		env = OS
	}
	out := *node
	out.Content = nil
	if node.Alias != nil {
		out.Alias = nil
	}

	if jsExprTags[node.Tag] && node.Kind == yaml.ScalarNode {
		value, err := evalExpr(node.Value, home, env)
		if err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}, nil
	}

	for _, child := range node.Content {
		resolved, err := resolveExprs(child, home, env)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, resolved)
	}
	return &out, nil
}

// evalExpr 求值一条 `!!js` 表达式。未知形式返回错误——静默降级会让配置树指向
// 错误路径，比启动失败危险得多。
func evalExpr(src, home string, env Env) (string, error) {
	src = strings.TrimSpace(src)

	if m := scalarRe.FindStringSubmatch(src); m != nil {
		return unquote(m[1])
	}

	m := callRe.FindStringSubmatch(src)
	if m == nil {
		return "", fmt.Errorf("unsupported !!js expression %q (supported: dshHomePath('a','b'), env('NAME'), 'literal')", src)
	}
	fn, argSrc := m[1], m[2]

	args, err := parseArgs(argSrc)
	if err != nil {
		return "", fmt.Errorf("!!js %s: %w", fn, err)
	}

	switch fn {
	case "dshHomePath", "homePath":
		parts := append([]string{home}, args...)
		return filepath.Clean(filepath.Join(parts...)), nil
	case "env":
		if len(args) != 1 {
			return "", fmt.Errorf("!!js env: expected 1 argument, got %d", len(args))
		}
		return env.Getenv(args[0]), nil
	default:
		return "", fmt.Errorf("unknown !!js function %q in %q", fn, src)
	}
}

func parseArgs(src string) ([]string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, nil
	}
	var out []string
	for src != "" {
		m := argRe.FindStringSubmatch(src)
		if m == nil {
			return nil, fmt.Errorf("cannot parse argument list %q", src)
		}
		v, err := unquote(m[1])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		src = strings.TrimSpace(src[len(m[0]):])
	}
	return out, nil
}

func unquote(s string) (string, error) {
	if len(s) >= 2 && (s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1], nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strconv.Unquote(s)
	}
	return "", fmt.Errorf("expected quoted argument, got %q", s)
}

// ResolveHome 定位 lidsh 的家目录，形状复刻 dsh-home-paths（环境变量优先，否则
// 家目录下的点目录），但用 lidsh 自己的名字：复刻工具不得读写真实 DSH 的
// $DSH_HOME / ~/.dsh，否则会把它的数据目录与 profile 层混进来。
//
// 优先级：$LIDSH_HOME → ~/.lidsh
func ResolveHome(env Env) string {
	if env == nil {
		env = OS
	}
	if v := env.Getenv("LIDSH_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".lidsh"
	}
	return filepath.Join(home, ".lidsh")
}

// HomeEnvVar 是覆盖家目录的环境变量名，供帮助文本与诊断引用。
const HomeEnvVar = "LIDSH_HOME"

// BootstrapEnvBlocklist 复刻 dsh-app-boot 的 BOOTSTRAP_NAMES：`.env` 里禁止覆盖
// 这些名字（dsh-app-boot/lib/index.js:948+），防止配置文件劫持进程启动环境。
var BootstrapEnvBlocklist = []string{
	"PATH", "HOME", "USERPROFILE", "SHELL",
	"NODE_OPTIONS", "NODE_PATH", "NODE_EXTRA_CA_CERTS",
	"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT",
	"BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS",
	"PERL5OPT", "PERL5LIB",
}

// LoadDotEnv 读一个 .env 并跳过黑名单键；黑名单命中时通过 warn 报告（DSH 同样
// 拒绝而非静默接受）。文件不存在不算错误。
func LoadDotEnv(path string, warn func(string, ...any)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if warn == nil {
		warn = func(string, ...any) {}
	}
	blocked := map[string]bool{}
	for _, name := range BootstrapEnvBlocklist {
		blocked[name] = true
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if blocked[key] {
			warn("failed to load .env: %s:%d refuses to set %s", filepath.Base(path), i+1, key)
			continue
		}
		value = strings.TrimSpace(value)
		if v, err := strconv.Unquote(value); err == nil && len(value) > 1 {
			value = v
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return nil
}
