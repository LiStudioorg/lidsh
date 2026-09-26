// Package llm 是 provider 无关的流式生成层，复刻 dsh-llm 的抽象。
//
// 契约来源（实测 @deepseek-ai/dsh@0.1.5-rc.3，详见 docs/_parts/llm-core.md）：
//   - LlmAdapter 抽象类只需实现 stream(options) → AsyncIterable<StreamChunk>
//     （dsh-llm/lib/types/index.d.ts:182）
//   - StreamChunk 共 7 种（dsh-llm/lib/types/types.d.ts:359-389）
//   - usage 必须在 finish 之前（同上）
//   - TokenUsage 是不相交计数：inputTokens 不含缓存命中（同上）
//   - 重试：normal 模式 5 次，backoff 500ms~10s、jitter 0.1，可重试码
//     EMPTY_RESPONSE/RATE_LIMIT/SERVER/TIMEOUT/TRANSPORT（dsh-llm/lib/index.js:232-242）
package llm

import (
	"context"
	"encoding/json"
	"time"
)

// Role 是消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentBlock 是消息内容块。DSH 的 ContentBlock union 共 6 个 variant
// （file/text/image/reasoning/tool-call/tool-result）。
type ContentBlock struct {
	Type string `json:"type"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=reasoning：模型的思维链文本，与正文分开呈现（前端折叠区）
	// 复用 Text 字段，靠 Type 区分。

	// type=image / type=file
	Attachment *AttachmentRef `json:"attachment,omitempty"`

	// type=tool-call
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // JSON 文本，增量拼接后的完整值

	// type=tool-result
	ToolCallID string         `json:"toolCallId,omitempty"`
	Content    []ContentBlock `json:"content,omitempty"`
	IsError    bool           `json:"isError,omitempty"`
}

// AttachmentRef 是内容寻址的附件引用。DSH 把二进制留在 append-only 会话日志之外
// （dsh-base patch 注释），消息里只放引用。
type AttachmentRef struct {
	AttachmentID string `json:"attachmentId"`
	Name         string `json:"name,omitempty"`
	MediaType    string `json:"mediaType,omitempty"`
	Bytes        int64  `json:"bytes"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

// MessageSource 是消息产出方（MessageSourceMap 的核心四种 kind；插件可扩 kind，
// 用 RawMessage 承载以保持可扩展，message.d.ts:94-104）。
type MessageSource struct {
	Kind   string `json:"kind"` // user|plugin|model|tool
	Plugin string `json:"plugin,omitempty"`
	// kind=model（AssistantProvenance）
	Provider    string          `json:"provider,omitempty"`
	Model       string          `json:"model,omitempty"`
	ReplayState json.RawMessage `json:"replayState,omitempty"`
	// kind=tool
	CallID string `json:"callId,omitempty"`
	// kind=plugin 的 ContextForm 标签
	Form    string `json:"form,omitempty"`    // instructions|catalog|snapshot|notice|relay|recall
	Summary string `json:"summary,omitempty"` // notice 必填，≤120 字符
	// kind=goal（GoalMessageSource，goal domain.d.ts:34-40）：goal 续轮归因，
	// fold 校验 round===roundsStarted+1 且 ≤ maxGoalRounds。
	GoalID   string `json:"goalId,omitempty"`
	Revision int    `json:"revision,omitempty"`
	Round    int    `json:"round,omitempty"`
}

// Message 是一条会话消息（message.d.ts:120-129）。
type Message struct {
	ID      string         `json:"id"`
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
	Source  MessageSource  `json:"source"`
}

// ToolDefinition 是发给模型的工具声明。
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"` // JSON Schema
}

// ReasoningEffort 是思考强度档位。DeepSeek 侧 effort=off 会关闭 thinking
// （dsh-llm-deepseek：thinking:{type:'disabled'}）。
type ReasoningEffort string

const (
	EffortOff    ReasoningEffort = "off"
	EffortLow    ReasoningEffort = "low"
	EffortMedium ReasoningEffort = "medium"
	EffortHigh   ReasoningEffort = "high"
	EffortMax    ReasoningEffort = "max"
)

// Purpose 标注一次调用的用途，供上游计费/日志区分。DSH 有 compaction 与
// session-title 两类内部用途（dsh-llm/lib/types/types.d.ts:404-444）。
type Purpose string

const (
	PurposeChat       Purpose = ""
	PurposeCompaction Purpose = "compaction"
	PurposeTitle      Purpose = "session-title"
)

// GenerateOptions 是一次生成的完整输入。
type GenerateOptions struct {
	Provider string
	Model    string

	System    string
	Messages  []Message
	Tools     []ToolDefinition
	Reasoning ReasoningEffort

	Temperature *float64
	MaxTokens   *int
	Stop        []string

	// Signal 承载取消；实现必须在取消后尽快停止产出并返回 AbortError。
	Signal context.Context

	SessionID string
	Purpose   Purpose
}

// TokenUsage 是不相交计数（DSH 约定）：InputTokens 不含缓存命中部分。
// DeepSeek 的 prompt_tokens 含缓存命中，适配器负责减去（见 llm-core.md 第 6 节）。
type TokenUsage struct {
	InputTokens     int64 `json:"inputTokens"`
	OutputTokens    int64 `json:"outputTokens"`
	CacheReadTokens int64 `json:"cacheReadTokens,omitempty"`
	ReasoningTokens int64 `json:"reasoningTokens,omitempty"`
	TotalTokens     int64 `json:"totalTokens,omitempty"`
}

// FinishReason 是结束原因。aborted/error 携带底层失败。
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool-calls"
	FinishMaxTokens FinishReason = "max-tokens"
	FinishAborted   FinishReason = "aborted"
	FinishError     FinishReason = "error"
)

// BlockType 标识一个内容块的开始/结束。
type BlockType string

const (
	BlockText      BlockType = "text"
	BlockReasoning BlockType = "reasoning"
	BlockToolCall  BlockType = "tool-call"
)

// Chunk 是流式产出的一个片段，对应 DSH 的 7 种 StreamChunk。
// 用 Type 做判别式，各字段只在对应 Type 下有效。
type Chunk struct {
	Type string `json:"type"`

	// block-start / block-end
	BlockIndex int       `json:"blockIndex,omitempty"`
	BlockType  BlockType `json:"blockType,omitempty"`
	ToolCallID string    `json:"toolCallId,omitempty"`
	ToolName   string    `json:"toolName,omitempty"`

	// text-delta / reasoning-delta
	Delta string `json:"delta,omitempty"`

	// tool-call-delta：按 wire index 聚合，id/name 只认首个非空值。
	ToolCallIndex  int    `json:"toolCallIndex,omitempty"`
	ArgumentsDelta string `json:"argumentsDelta,omitempty"`

	// block-end 携带完整块（types.d.ts:377-380）
	Block *ContentBlock `json:"block,omitempty"`

	// usage
	Usage *TokenUsage `json:"usage,omitempty"`

	// finish
	Finish FinishReason `json:"finish,omitempty"`
	Fail   *Failure     `json:"error,omitempty"`
}

// 流式片段类型常量。
const (
	ChunkBlockStart     = "block-start"
	ChunkTextDelta      = "text-delta"
	ChunkReasoningDelta = "reasoning-delta"
	ChunkToolCallDelta  = "tool-call-delta"
	ChunkBlockEnd       = "block-end"
	ChunkUsage          = "usage"
	ChunkFinish         = "finish"
)

// ErrorCode 是规范化的失败码。DSH 的规范码子集（llm-core.md 第 4 节）。
type ErrorCode string

const (
	ErrContextWindowExceeded ErrorCode = "CONTEXT_WINDOW_EXCEEDED"
	ErrQuota                 ErrorCode = "QUOTA"
	ErrEmptyResponse         ErrorCode = "EMPTY_RESPONSE"
	ErrInvalidCredential     ErrorCode = "INVALID_CREDENTIAL"
	ErrRateLimit             ErrorCode = "RATE_LIMIT"
	ErrServer                ErrorCode = "SERVER"
	ErrTimeout               ErrorCode = "TIMEOUT"
	ErrTransport             ErrorCode = "TRANSPORT"
	ErrAborted               ErrorCode = "ABORTED"
	ErrStreamClosed          ErrorCode = "STREAM_CLOSED"
	ErrUnknown               ErrorCode = "UNKNOWN"
)

// Failure 是一次生成的失败，携带上游诊断信息。
type Failure struct {
	Message              string    `json:"message"`
	Code                 ErrorCode `json:"code"`
	Status               int       `json:"status,omitempty"`
	ProviderRetryAfterMs int64     `json:"providerRetryAfterMs,omitempty"`
	RequestID            string    `json:"requestId,omitempty"`
}

func (e *Failure) Error() string { return string(e.Code) + ": " + e.Message }

// Retryable 报告该失败是否值得重试（对齐 DSH 的可重试码集合）。
func (e *Failure) Retryable() bool {
	switch e.Code {
	case ErrEmptyResponse, ErrRateLimit, ErrServer, ErrTimeout, ErrTransport:
		return true
	}
	return false
}

// Model 是模型目录里的一项。
type Model struct {
	ID            string  `json:"id"`
	Name          string  `json:"name,omitempty"`
	ContextWindow int     `json:"contextWindow"`
	MaxTokens     int     `json:"maxTokens"`
	SupportsTools bool    `json:"supportsTools"`
	SupportsImage bool    `json:"supportsImage"`
	SupportsCache bool    `json:"supportsCache"`
	InputCost     float64 `json:"inputCostPerMTok,omitempty"`
	OutputCost    float64 `json:"outputCostPerMTok,omitempty"`
}

// ProviderInfo 描述一个 provider 的身份与能力。
type ProviderInfo struct {
	Provider string
	Models   []Model
}

// Adapter 是一个 provider 的实现。DSH 的 LlmAdapter 只有 stream 是必须实现的，
// 其余有默认——Go 里没有类继承，这里把最小接口与可选接口分开。
type Adapter interface {
	// Stream 产出一段流。实现方保证：usage 在 finish 之前，finish 是最后一个
	// 片段，取消（Signal 结束）后尽快返回不 panic。
	Stream(ctx context.Context, opts GenerateOptions) (<-chan Chunk, error)

	// Info 返回 provider 身份与模型目录。
	Info() ProviderInfo

	// ResolveModel 把目录 id 解析为 wire 模型名。
	ResolveModel(id string) (string, error)
}

// ListModelsOption 是可选能力接口的默认实现载体。
type Option func(*Settings)

// Settings 是重试与看门狗的公共配置，由 llm-retry 行的 patch 配置驱动。
type Settings struct {
	// Retries 是 normal 模式的尝试次数上限（DSH 默认 5）。
	Retries int
	// BackoffMin/Max 是指数退避的边界（DSH: 500ms ~ 10s）。
	BackoffMin time.Duration
	BackoffMax time.Duration
	// Jitter 是退避抖动比例（DSH: 0.1）。
	Jitter float64
	// IdleTimeout 是流内空闲看门狗（DeepSeek 侧默认 300s）。
	IdleTimeout time.Duration
}

// DefaultSettings 复刻 DSH 的默认重试参数（dsh-llm/lib/index.js:232-242）。
func DefaultSettings() Settings {
	return Settings{
		Retries:     5,
		BackoffMin:  500 * time.Millisecond,
		BackoffMax:  10 * time.Second,
		Jitter:      0.1,
		IdleTimeout: 300 * time.Second,
	}
}
