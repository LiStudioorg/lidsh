// Package tools 复刻 dsh-tools 的工具注册表契约（dsh-tools/lib/index.js）。
//
// 契约来源（实测 @deepseek-ai/dsh@0.1.5-rc.3，详见 docs/_parts/core-loop.md §4）：
//   - ToolDefinition（index.d.ts:106-172）：name/description/parameters 发给模型，
//     timeoutMs/isConcurrencySafe/presentCall/presentResult 只在运行内部。
//   - schemas 投影是白名单：只取 name/description/parameters，内部字段永不外泄
//     （:135 注释、:676）。
//   - 注册/注销 register() → () => void（:601）。
//   - execute(exec)（:730）执行一次调用，返回成功/失败扁平投影。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"lidsh/internal/llm"
	"lidsh/internal/sandbox"
)

// Definition 是发给模型的工具声明，同时是注册项（对应 DSH ToolDefinition）。
type Definition struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema（只对模型暴露 name/description/parameters）

	// Execute 执行工具。args 是模型给的参数（已解析为 map）。
	// 返回 content 文本；isError=true 时进 isError 通道。
	Execute func(args map[string]any, ec *ExecContext) (*Result, error)

	// IsConcurrencySafe 默认 false（即 exclusive）；只有返回 true 才并行。
	IsConcurrencySafe func(args map[string]any) bool

	TimeoutMs int // 工具级协作超时，永不发给模型

	// PresentCall/PresentResult 留给 UI 卡片的展示信息（card 标签）。
	PresentCall   func(args map[string]any) *CallView
	PresentResult func(args map[string]any, res *Result) *ResultView
}

// ExecContext 是工具执行上下文（对应 ToolRunContext，index.d.ts:280）。
// 工作目录/沙箱策略不在 tools 包，工具自身经 Callsite 取用。
type ExecContext struct {
	Signal   context.Context // 取消信号（AbortSignal 对应物）
	AgentCWD string          // agent 的会话 cwd
	// CWD 是工具自身解析后的工作目录（bash 的 workdir 参数或 agent cwd）。
	CWD string
	// Sandbox 是本次调用的沙箱上下文；nil=未挂载 confinement executor
	// （工具不感知沙箱，提权字段也不 advertise，bash-sandbox.md §6.1）。
	Sandbox *SandboxContext
}

// SandboxContext 是挂载了沙箱的组合里，每次工具调用携带的沙箱裁决输入。
type SandboxContext struct {
	Policy    sandbox.Policy         // standing 策略（mode + workspaceRoot）
	Runner    string                 // confinement runner（DetectRunner 结果）
	Approver  sandbox.Approver       // 审批通道（nil=无通道，提权 fail-closed）
	Approval  sandbox.ApprovalPolicy // ask | never
	SessionID string                 // 发起 agent（=Session id）
	CallID    string                 // 本次工具调用 id（审批载荷）
}

// ResolvePolicy 返回本调用生效策略：显式获批 mode 仅 stamp 本次调用（§2.2/§6.6）。
func (sc *SandboxContext) ResolvePolicy() sandbox.Policy { return sc.Policy }

// SandboxOptions 是 agent 级 standing 沙箱配置（§2.2 优先级的部署默认项）；
// agent 每次工具调用由此派生 SandboxContext。
type SandboxOptions struct {
	Mode     sandbox.Mode
	Root     string // workspace 根（session cwd canonical 化，§2.2）
	Runner   string
	Approver sandbox.Approver
	Policy   sandbox.ApprovalPolicy
}

// Context 为一次工具调用派生 SandboxContext（CallID=本次调用 id）。
func (so *SandboxOptions) Context(sessionID, callID string) *SandboxContext {
	return &SandboxContext{
		Policy:    sandbox.Policy{Mode: so.Mode, WorkspaceRoot: so.Root},
		Runner:    so.Runner,
		Approver:  so.Approver,
		Approval:  so.Policy,
		SessionID: sessionID,
		CallID:    callID,
	}
}

// Result 是工具执行结果（对应 ToolExecutionSuccess/Failure 的扁平投影）。
type Result struct {
	Content string // 模型可见文本
	IsError bool
	Meta    map[string]any

	// AdditionalContexts 回注上下文（进 next-step 队列，下一步生效）
	AdditionalContexts []string
	ConcludesTurn      bool
}

// ToolCall 是一次工具调用。
type ToolCall struct {
	CallID     string
	RootCallID string
	Name       string
	Arguments  string // 模型原文
}

// CallView 是挂起态展示（presentation.d.ts ToolCallView，通用投影字段）。
type CallView struct {
	Card     string // generic|terminal|diff 等
	Title    string
	Kind     string
	RawInput string
	Content  string
	Location string // path（FileLocation.path 的扁平投影）
	// Diffs 用于 card="diff"（FileDiff{path, oldText, newText}）。
	Diffs []FileDiff
}

// FileDiff 是 diff 卡片的单文件差异展示。
type FileDiff struct {
	Path    string
	OldText *string // nil 表示新增
	NewText string
}

// ResultView 是完成态展示（presentation.d.ts ToolResultView，通用投影字段）。
type ResultView struct {
	Card  string // generic|terminal|diff|search|read|web 等
	Title string
}

// Registry 是工具注册表（对应 ToolRuntime 的注册面）。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Definition
}

// NewRegistry 构造注册表。
func NewRegistry() *Registry {
	return &Registry{tools: map[string]*Definition{}}
}

// Register 注册一个工具。返回注销函数。
func (r *Registry) Register(d Definition) func() {
	if d.Name == "" {
		panic("tools: tool name must not be empty")
	}
	r.mu.Lock()
	if _, dup := r.tools[d.Name]; dup {
		r.mu.Unlock()
		panic("tools: duplicate tool registration " + d.Name)
	}
	// 拷贝一份，避免调用方事后改字段影响注册表。
	reg := d
	r.tools[d.Name] = &reg
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.tools, d.Name)
		r.mu.Unlock()
	}
}

// Get 按名取工具。不存在返回 nil。
func (r *Registry) Get(name string) *Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// Schemas 返回发给模型的工具声明列表：只取 name/description/inputSchema
// （白名单，驼峰 JSON：name/description/parameters）。
func (r *Registry) Schemas() []llm.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, llm.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out
}

// Execute 执行一个工具调用（对应 registry.execute，index.js:730）。
// parseArguments：JSON 解析失败保留原文字符串；空串 → {}。
// 返回内容文本 + isError。
func (r *Registry) Execute(call ToolCall, ec *ExecContext) (*Result, error) {
	t := r.Get(call.Name)
	if t == nil {
		return &Result{
			Content: fmt.Sprintf("Error: unknown tool %q", call.Name),
			IsError: true,
		}, nil
	}
	if t.Execute == nil {
		return &Result{
			Content: fmt.Sprintf("Error: tool %q has no execute handler", call.Name),
			IsError: true,
		}, nil
	}
	args, err := parseArguments(call.Arguments)
	if err != nil {
		return &Result{
			Content: fmt.Sprintf("Error: failed to parse arguments for tool %q: %v", call.Name, err),
			IsError: true,
		}, nil
	}
	return t.Execute(args, ec)
}

// parseArguments 解析模型参数。空串 → {}（空 map）；JSON 解析失败保留原文字符串
// （把原始参数放进一个 map 的 "arguments" 键，模型仍能读到原文而非静默丢弃）。
func parseArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{"arguments": raw}, err
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}
