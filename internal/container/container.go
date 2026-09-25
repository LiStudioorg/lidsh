// Package container 是 Cordis 的 Go 对应物：把配置树的条目行实例化为互相注入的
// 服务，按依赖可用性驱动激活（DSH 的 patch 注释原话："activation is
// service-availability driven"，行序不承载加载语义）。
//
// 与 Cordis 的映射：
//
//	Cordis                         lidsh container
//	─────────────────────────────  ─────────────────────────────────────
//	plugin 包 (name)               Registry 里的 Factory（编译期注册）
//	static inject = [...]          Decl{Provides, Inject}
//	ctx.provide(id, value)         Acceptor 里调 c.Provide
//	ctx.on(event, handler)         c.On / c.Emit（类型化 topic）
//	disabled: true                 激活前跳过该行
//	group 行                       仅作命名容器，不实例化
package container

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"lidsh/internal/config"
)

// Decl 描述一个模块的服务面：提供哪些 id、注入哪些 id。
// 对应 Cordis 的 static name / static inject。
type Decl struct {
	Provides []string
	Inject   []string
}

// Factory 实例化一个服务。cfg 已解码为 map（保持模块自治：各自定义 config struct
// 再自行解码），app 是宿主上下文（生命周期 ctx、home 路径、工作区等横切值）。
type Factory func(app *App, id string, cfg map[string]any) (any, error)

// Module 是注册进注册表的一个可实例化单元。
type Module struct {
	Decl    Decl
	Factory Factory
}

// Registry 是模块注册表：name → Module。DSH 里 name 是 npm 包名，运行时解析；
// Go 里编译期注册，缺失在启动时立刻报错（比 Cordis 的运行时报错更早）。
type Registry struct {
	modules map[string]Module
}

func NewRegistry() *Registry {
	return &Registry{modules: map[string]Module{}}
}

// Register 注册一个模块，重复注册 panic（配置错误应在开发期暴露）。
func (r *Registry) Register(name string, decl Decl, factory Factory) {
	if _, dup := r.modules[name]; dup {
		panic("container: duplicate module registration " + name)
	}
	r.modules[name] = Module{Decl: decl, Factory: factory}
}

// Get 查找模块。
func (r *Registry) Get(name string) (Module, bool) {
	m, ok := r.modules[name]
	return m, ok
}

// App 是一次装配的运行实例。
type App struct {
	Ctx      context.Context
	Home     string // $DSH_HOME 的对应物（lidsh 用 $LIDSH_HOME）
	Workdir  string
	Registry *Registry

	mu        sync.Mutex
	provided  map[string]any
	byID      map[string]any // 条目 id → 实例（同 name 多实例时按 id 取）
	pending   map[string]bool
	activated []*EntryInfo
	events    *EventBus
	warn      func(string, ...any)

	// chain 记录当前激活栈，供 Require 报出注入链。装配阶段单线程，无并发。
	chain []string
}

// EntryInfo 是激活记录，供 dump 与诊断。
type EntryInfo struct {
	ID     string
	Name   string
	Config map[string]any
	Err    error
}

// New 构造 App。warn 为 nil 时告警进 stderr。
func New(ctx context.Context, reg *Registry, home, workdir string, warn func(string, ...any)) *App {
	if warn == nil {
		warn = func(format string, args ...any) {
			fmt.Fprintf(stderr(), "lidsh: "+format+"\n", args...)
		}
	}
	return &App{
		Ctx: ctx, Home: home, Workdir: workdir, Registry: reg,
		provided: map[string]any{},
		byID:     map[string]any{},
		pending:  map[string]bool{},
		events:   NewEventBus(),
		warn:     warn,
	}
}

// Warn 输出装配/运行期告警。
func (a *App) Warn(format string, args ...any) { a.warn(format, args...) }

// Events 返回类型化事件总线（对应 Cordis 的 ctx.on/emit）。
func (a *App) Events() *EventBus { return a.events }

// Provide 注册横切服务，等价 ctx.provide(id, value)。
func (a *App) Provide(id string, value any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provided[id] = value
}

// Lookup 按服务 id 查找已提供实例。
func (a *App) Lookup(id string) (any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.provided[id]
	return v, ok
}

// Require 按服务 id 取依赖，缺失时报出注入链（Cordis 同款体验）。
func (a *App) Require(id string) (any, error) {
	if v, ok := a.Lookup(id); ok {
		return v, nil
	}
	return nil, fmt.Errorf("service %q is not available (inject chain: %s)", id, strings.Join(a.chain, " -> "))
}

// Activate 按条目树装配全部服务：
//  1. disabled 行跳过（含 !!js 表达式求值）；
//  2. group 行递归；
//  3. 其余行按 name 查注册表实例化，config 解码为 map 传给 Factory；
//  4. 依赖不满足的行推迟重试（service-availability driven），一轮无进展则报
//     循环/缺失依赖错误——与 Cordis 的"等到依赖可用再激活"语义一致。
func (a *App) Activate(entries []*config.Entry) error {
	type job struct {
		entry *config.Entry
		path  string
	}
	var queue []job
	var flatten func(entries []*config.Entry, path string)
	flatten = func(entries []*config.Entry, path string) {
		for _, e := range entries {
			if e == nil {
				continue
			}
			p := e.ID
			if path != "" {
				p = path + "/" + e.ID
			}
			if e.IsDisabled(a.Home, config.OS) {
				a.warn("entry %q disabled, skipping", p)
				continue
			}
			if e.Group {
				flatten(e.Children(), p)
				continue
			}
			queue = append(queue, job{entry: e, path: p})
		}
	}
	flatten(entries, "")

	// 依赖可用性驱动的反复扫描。
	for len(queue) > 0 {
		progress := false
		var stalled []job
		for _, j := range queue {
			ok, err := a.tryActivate(j.entry, j.path)
			if err != nil {
				a.activated = append(a.activated, &EntryInfo{ID: j.path, Name: j.entry.Name, Err: err})
				return err
			}
			if ok {
				progress = true
			} else {
				stalled = append(stalled, j)
			}
		}
		queue = stalled
		if !progress {
			names := make([]string, 0, len(queue))
			for _, j := range queue {
				names = append(names, j.path)
			}
			sort.Strings(names)
			return fmt.Errorf("cannot activate: unmet dependencies for %s", strings.Join(names, ", "))
		}
	}
	return nil
}

func (a *App) tryActivate(e *config.Entry, path string) (bool, error) {
	mod, ok := a.Registry.Get(e.Name)
	if !ok {
		return false, fmt.Errorf("entry %q: unknown module %q (registry has %d modules)", path, e.Name, len(a.Registry.modules))
	}

	// 依赖检查：Decl.Inject 全部已提供才激活。
	a.mu.Lock()
	var missing []string
	for _, dep := range mod.Decl.Inject {
		if _, ok := a.provided[dep]; !ok {
			missing = append(missing, dep)
		}
	}
	a.mu.Unlock()
	if len(missing) > 0 {
		a.warn("entry %q waits for %s", path, strings.Join(missing, ", "))
		return false, nil
	}

	cfg := map[string]any{}
	if err := e.DecodeConfig(&cfg, a.Home, config.OS); err != nil {
		return false, err
	}

	a.chain = append(a.chain, path)
	defer func() { a.chain = a.chain[:len(a.chain)-1] }()

	inst, err := mod.Factory(a, e.ID, cfg)
	if err != nil {
		return false, fmt.Errorf("entry %q (%s): %w", path, e.Name, err)
	}

	a.mu.Lock()
	a.byID[e.ID] = inst
	for _, p := range mod.Decl.Provides {
		if _, dup := a.provided[p]; dup && p != e.ID {
			// DSH 里同 id 重复 provide 是配置错误；这里同样失败而非静默覆盖。
			a.mu.Unlock()
			return false, fmt.Errorf("entry %q provides duplicate service %q", path, p)
		}
		a.provided[p] = inst
	}
	a.mu.Unlock()

	a.activated = append(a.activated, &EntryInfo{ID: path, Name: e.Name, Config: cfg})
	return true, nil
}

// Activated 返回激活记录（顺序即激活顺序）。
func (a *App) Activated() []*EntryInfo { return a.activated }

// IDs 按激活序返回条目 id，供 dump。
func (a *App) IDs() []string {
	out := make([]string, 0, len(a.activated))
	for _, e := range a.activated {
		out = append(out, e.ID)
	}
	return out
}
