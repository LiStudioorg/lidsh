// Package session 复刻 DSH 的会话核心：事件溯源 + JSONL+zstd 持久化。
//
// 语义来源（实测 @deepseek-ai/dsh@0.1.5-rc.3，详见 docs/_parts/core-session.md，
// 行号均指向该文档引用的 DSH 文件）：
//   - 事件日志是唯一真相源，LLM 消息历史是派生投影（surface）
//   - seq = 日志中事件下标，从 0 连续；time = Unix epoch 毫秒
//   - 只有 4 种事件产生 LLM 消息：system/message、user/message、
//     assistant/message、tool/result；其余是 log-only 轨迹
//   - replace 事件本身也进 append-only 日志，原事件永不删
//   - 未识别事件类型：无 ignorable:true 标记 → 拒绝读日志（fail-closed）
//   - 磁盘：{root}/{projectKey(cwd)}/{encodeSegment(id)}/session.v3.jsonl.zstd
package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"lidsh/internal/llm"
)

// FormatVersion 是当前逻辑格式版本（SESSION_FORMAT_VERSION=3）。
// 升级规则（types.d.ts:39-53）：由写方 bump；只有 header/envelope/核心事件语义/
// surface 机制变化才 bump；新增普通事件靠 per-event ignorable 兜底。
const FormatVersion = 3

// ID 是会话 id。DSH 的品牌类型运行时就是 string。
type ID = string

// NewUUID 返回 randomUUID v4 形态的字符串（等价 crypto.randomUUID）。
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("session: entropy: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func newUUID() string { return NewUUID() }

// NewID 生成 API 会话 id：`session-${uuid}`（dsh-api-session-controller:573）。
func NewID() ID { return "session-" + newUUID() }

// NewMessageID 生成消息 id（createMessage: id=randomUUID）。
func NewMessageID() string { return newUUID() }

// ---------- Header ----------

// Header 是会话不可变元数据，落盘为 JSONL 第一行，不进事件日志。
// 缺省字段省略键而非写 null（omitempty）；退役字段 sandboxMode/approvalPolicy
// 出现即拒（读校验里做）。
type Header struct {
	Type            string `json:"type"` // 恒为 "session"
	Version         int    `json:"version"`
	ID              ID     `json:"id"`
	CreatedAt       int64  `json:"createdAt"`
	CWD             string `json:"cwd,omitempty"`
	ParentSession   ID     `json:"parentSession,omitempty"`
	IsSeeded        bool   `json:"isSeeded"`
	Origin          string `json:"origin,omitempty"` // "subagent"
	DelegationDepth int    `json:"delegationDepth"`
	AgentPreset     string `json:"agentPreset,omitempty"`

	// InheritedEventCount：seeded 头必须携带。
	InheritedEventCount int `json:"inheritedEventCount,omitempty"`
}

// Meta 是创建会话时的可选元数据（CreateSessionOptions.meta）。
type Meta struct {
	ParentSession       ID
	IsSeeded            bool
	InheritedEventCount int
	Origin              string
	DelegationDepth     int
	AgentPreset         string
}

// NewHeader 构造 header。
func NewHeader(id ID, cwd string, meta Meta) Header {
	return Header{
		Type: "session", Version: FormatVersion, ID: id,
		CreatedAt: millis(), CWD: cwd,
		ParentSession: meta.ParentSession, IsSeeded: meta.IsSeeded,
		InheritedEventCount: meta.InheritedEventCount,
		Origin:              meta.Origin, DelegationDepth: meta.DelegationDepth,
		AgentPreset: meta.AgentPreset,
	}
}

// ---------- Event ----------

// Event 是事件 envelope（types.d.ts:460-483）。Data 保留原始 JSON：未知类型的
// 载荷在 ignorable 路径下无需可解码。
type Event struct {
	Type      string          `json:"type"`
	Seq       int             `json:"seq"`
	Time      int64           `json:"time"`
	Data      json.RawMessage `json:"data"`
	Ignorable *bool           `json:"ignorable,omitempty"`

	// SurfaceOp：append 是裸字符串 "append"，replace 是对象；仅 surface 事件
	// 允许携带（types.d.ts:436-458）。
	SurfaceOp json.RawMessage `json:"surfaceOp,omitempty"`
	// SourceEventSeqs 引用的更早事件 seq（tool/result 必指回自己的 tool/call）。
	SourceEventSeqs []int `json:"sourceEventSeqs,omitempty"`
}

// AppendOp 返回 append 的 raw 形态。
func AppendOp() json.RawMessage { return json.RawMessage(`"append"`) }

// ReplaceOp 构造 replace 的 raw 形态。
func ReplaceOp(start, end int) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"op": "replace", "startSeq": start, "endSeq": end})
	return b
}

// 核心事件类型。
const (
	EventTurnStart        = "turn/start"
	EventTurnEnd          = "turn/end"
	EventStepStart        = "step/start"
	EventStepEnd          = "step/end"
	EventUserMessage      = "user/message"
	EventSystemMessage    = "system/message"
	EventAssistantMessage = "assistant/message"
	EventAssistantAttempt = "assistant/attempt"
	EventToolCall         = "tool/call"
	EventToolResult       = "tool/result"
	EventRequestHeader    = "request/header"
	EventRequestContext   = "request/context"
	EventSessionEndSeed   = "session/end-seed"
)

// 扩展事件类型（DSH 同名事件，payload 形状见 core-control.md）。
const (
	EventSessionTitle       = "session/title"
	EventApprovalAsked      = "approval/asked"
	EventApprovalDecided    = "approval/decided"
	EventApprovalPolicy     = "approval/policy"
	EventTodoWrite          = "todo/write"
	EventPlanMode           = "plan/mode"
	EventSandboxMode        = "sandbox/mode"
	EventModelSelection     = "model/selection"
	EventLLMRetry           = "llm/retry"
	EventLLMRetryStarted    = "llm/retry-started"
	EventCompactionStart    = "compaction/start"
	EventCompactionSummary  = "compaction/summary"
	EventCompactionEnd      = "compaction/end"
	EventCompactionPrune    = "compaction/prune"
	EventSubagentCatalog    = "subagent/catalog"
	EventSubagentDescriptor = "subagent/descriptor"
	EventGoalChange         = "goal/change"
	EventCommandRun         = "command/run"
	EventCommandDone        = "command/done"
	EventDeliverables       = "deliverables/presented"
)

// KnownEventTypes 是读本 build 认识的事件名单（fail-closed 校验用，对齐
// KNOWN_SESSION_EVENT_TYPES 的核心子集 + lidsh 扩展）。
var KnownEventTypes = map[string]bool{
	EventTurnStart: true, EventTurnEnd: true, EventStepStart: true, EventStepEnd: true,
	EventUserMessage: true, EventSystemMessage: true, EventAssistantMessage: true,
	EventAssistantAttempt: true, EventToolCall: true, EventToolResult: true,
	EventRequestHeader: true, EventRequestContext: true, EventSessionEndSeed: true,
	EventSessionTitle: true, EventApprovalAsked: true, EventApprovalDecided: true,
	EventApprovalPolicy: true, EventTodoWrite: true, EventPlanMode: true,
	EventSandboxMode: true, EventModelSelection: true, EventLLMRetry: true,
	EventLLMRetryStarted: true, EventCompactionStart: true, EventCompactionSummary: true,
	EventCompactionEnd: true, EventCompactionPrune: true, EventSubagentCatalog: true,
	EventSubagentDescriptor: true, EventGoalChange: true, EventCommandRun: true,
	EventCommandDone: true, EventDeliverables: true,
}

// IsSurfaceType 判定事件是否产生 LLM 消息（SurfaceEventType 全集，4 项）。
func IsSurfaceType(t string) bool {
	switch t {
	case EventUserMessage, EventSystemMessage, EventAssistantMessage, EventToolResult:
		return true
	}
	return false
}

// ---------- 事件数据形状 ----------

// Message 就是 LLM 层的消息表示；session 层不复制定义。
type Message = llm.Message

// TurnStartData …… 各事件 data 的强类型形状。
type TurnStartData struct {
	Turn int `json:"turn"`
}

type TurnEndData struct {
	Turn   int           `json:"turn"`
	Reason TurnEndReason `json:"reason"`
}

type StepData struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

// SystemMessageData / AssistantMessageData 共享 message 载体。
type SystemMessageData struct {
	Turn    int      `json:"turn"`
	Step    int      `json:"step"`
	Message *Message `json:"message"`
}

type AssistantMessageData struct {
	Turn        int                   `json:"turn"`
	Step        int                   `json:"step"`
	Message     *Message              `json:"message"`
	Stream      []AssistantStreamItem `json:"stream"`
	Usage       *llm.TokenUsage       `json:"usage,omitempty"`
	Interrupted bool                  `json:"interrupted,omitempty"`
}

type AssistantAttemptData struct {
	Turn   int                   `json:"turn"`
	Step   int                   `json:"step"`
	Stream []AssistantStreamItem `json:"stream"`
}

type ToolCallData struct {
	Turn      int    `json:"turn"`
	Step      int    `json:"step"`
	CallID    string `json:"callId"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 模型原文，未解析
}

type ToolResultData struct {
	Turn    int      `json:"turn"`
	Step    int      `json:"step"`
	Message *Message `json:"message"`
	Error   *struct {
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"error,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
}

// TurnEndReason 全 variant（types.d.ts:165-199）。
type TurnEndReason struct {
	Kind    string          `json:"kind"`              // completed|aborted|blocked|error|max-tokens|interrupted
	Failure json.RawMessage `json:"failure,omitempty"` // error 时是 LlmFailure
}

// ---------- AssistantStreamRecord ----------

// AssistantStreamItem 是 assistant 流的无损紧凑表示（assistant-stream.d.ts:16-40）：
//
//	{type:'text-chunks'|'reasoning-chunks', time0, index, dt[], texts[]}
//	{type:'tool-call-chunks', time0, index, dt[], id, name?, args[]}
//	{type:'chunk', time, chunk}  // 非 delta 的原始块（block-start/usage/finish…）
type AssistantStreamItem struct {
	Type  string     `json:"type"`
	Time0 int64      `json:"time0,omitempty"`
	Time  int64      `json:"time,omitempty"`
	Index int        `json:"index,omitempty"`
	DT    []int64    `json:"dt,omitempty"`
	Texts []string   `json:"texts,omitempty"`
	Args  []string   `json:"args,omitempty"`
	ID    string     `json:"id,omitempty"`
	Name  string     `json:"name,omitempty"`
	Chunk *llm.Chunk `json:"chunk,omitempty"`
}

// StreamAccumulator 增量打包流为紧凑记录（AssistantStreamAccumulator：push
// {time,chunk}，同 kind 同 index 的连续 delta run 合并进 dt/texts）。
// run 用下标引用 items（append 会搬移底层数组，指针会悬垂）。
type StreamAccumulator struct {
	items   []AssistantStreamItem
	prevT   int64
	lastRun int // 当前 text/reasoning run 的下标，-1 表示无
	lastTC  int // 当前 tool-call run 的下标，-1 表示无
}

// Push 摄入一个带时间的 chunk。
func (a *StreamAccumulator) Push(t int64, c llm.Chunk) {
	delta := t - a.prevT
	if a.prevT == 0 {
		delta = 0
	}
	a.prevT = t

	switch c.Type {
	case llm.ChunkTextDelta, llm.ChunkReasoningDelta:
		kind := "text-chunks"
		if c.Type == llm.ChunkReasoningDelta {
			kind = "reasoning-chunks"
		}
		if a.lastRun >= 0 && a.items[a.lastRun].Type == kind && a.items[a.lastRun].Index == c.BlockIndex {
			it := &a.items[a.lastRun]
			it.DT = append(it.DT, delta)
			it.Texts = append(it.Texts, c.Delta)
			return
		}
		a.items = append(a.items, AssistantStreamItem{
			Type: kind, Time0: t, Index: c.BlockIndex, DT: []int64{delta}, Texts: []string{c.Delta}})
		a.lastRun = len(a.items) - 1
		a.lastTC = -1
	case llm.ChunkToolCallDelta:
		if a.lastTC >= 0 && a.items[a.lastTC].Index == c.BlockIndex {
			it := &a.items[a.lastTC]
			it.DT = append(it.DT, delta)
			it.Args = append(it.Args, c.ArgumentsDelta)
			if c.ToolName != "" && it.Name == "" {
				it.Name = c.ToolName
			}
			if c.ToolCallID != "" && it.ID == "" {
				it.ID = c.ToolCallID
			}
			return
		}
		a.items = append(a.items, AssistantStreamItem{
			Type: "tool-call-chunks", Time0: t, Index: c.BlockIndex,
			DT: []int64{delta}, Args: []string{c.ArgumentsDelta}, ID: c.ToolCallID, Name: c.ToolName})
		a.lastTC = len(a.items) - 1
		a.lastRun = -1
	default:
		// block-start / block-end / usage / finish 原样入档。
		cp := c
		a.items = append(a.items, AssistantStreamItem{Type: "chunk", Time: t, Chunk: &cp})
		a.lastRun = -1
		a.lastTC = -1
	}
}

// NewAccumulator 构造打包器。
func NewAccumulator() *StreamAccumulator { return &StreamAccumulator{lastRun: -1, lastTC: -1} }

// Items 返回紧凑流。
func (a *StreamAccumulator) Items() []AssistantStreamItem { return a.items }

// ExpandAssistantStream 无损展开回 chunk 序列（expandAssistantStream），
// 前端回放与测试往返都靠它。
func ExpandAssistantStream(items []AssistantStreamItem) []llm.Chunk {
	var out []llm.Chunk
	for _, it := range items {
		switch it.Type {
		case "text-chunks", "reasoning-chunks":
			deltaType := llm.ChunkTextDelta
			if it.Type == "reasoning-chunks" {
				deltaType = llm.ChunkReasoningDelta
			}
			for _, txt := range it.Texts {
				out = append(out, llm.Chunk{Type: deltaType, BlockIndex: it.Index, Delta: txt})
			}
		case "tool-call-chunks":
			for _, arg := range it.Args {
				out = append(out, llm.Chunk{Type: llm.ChunkToolCallDelta, BlockIndex: it.Index,
					ToolCallID: it.ID, ToolName: it.Name, ArgumentsDelta: arg})
			}
		case "chunk":
			if it.Chunk != nil {
				out = append(out, *it.Chunk)
			}
		}
	}
	return out
}

// ---------- Surface 投影 ----------

// SurfaceNode 是 surface 上的一个节点：来源事件 seq + 投影出的消息。
type SurfaceNode struct {
	EventSeq int
	Message  *Message
}

// FoldSurface 折叠事件日志为 surface 节点序列（foldSurface + deriveEventMessage）：
//  1. 仅 surface 类型参与；system/assistant 的空 content 投影为 null 不入面；
//  2. replace：按**位置**遮蔽 [start,end]（端点是节点的 event seq；数值上允许
//     start>end，按出现顺序解释）并插入本节点。
func FoldSurface(events []*Event) ([]SurfaceNode, error) {
	var nodes []SurfaceNode
	for _, e := range events {
		if !IsSurfaceType(e.Type) {
			continue
		}
		if len(e.SurfaceOp) == 0 {
			return nil, fmt.Errorf("event %d (%s): message-producing event must carry surfaceOp", e.Seq, e.Type)
		}
		msg, err := DeriveEventMessage(e)
		if err != nil {
			return nil, fmt.Errorf("event %d (%s): %w", e.Seq, e.Type, err)
		}
		if msg == nil {
			continue
		}
		var op struct {
			Op       string `json:"op"`
			StartSeq *int   `json:"startSeq"`
			EndSeq   *int   `json:"endSeq"`
		}
		if json.Unmarshal(e.SurfaceOp, &op) == nil && op.Op == "replace" {
			if op.StartSeq == nil || op.EndSeq == nil {
				return nil, fmt.Errorf("event %d: replace requires startSeq and endSeq", e.Seq)
			}
			start, err := positionOf(nodes, *op.StartSeq)
			if err != nil {
				return nil, fmt.Errorf("event %d: replace start: %w", e.Seq, err)
			}
			end, err := positionOf(nodes, *op.EndSeq)
			if err != nil {
				return nil, fmt.Errorf("event %d: replace end: %w", e.Seq, err)
			}
			if start > end {
				start, end = end, start
			}
			// 被遮蔽节点从面中移除；原事件仍留在 append-only 日志里。
			merged := make([]SurfaceNode, 0, len(nodes)-(end-start)+2)
			merged = append(merged, nodes[:start]...)
			merged = append(merged, SurfaceNode{EventSeq: e.Seq, Message: msg})
			merged = append(merged, nodes[end+1:]...)
			nodes = merged
			continue
		}
		nodes = append(nodes, SurfaceNode{EventSeq: e.Seq, Message: msg})
	}
	return nodes, nil
}

// positionOf 找 eventSeq 对应节点在 surface 上的位置（按出现顺序）。
func positionOf(nodes []SurfaceNode, eventSeq int) (int, error) {
	for i, n := range nodes {
		if n.EventSeq == eventSeq {
			return i, nil
		}
	}
	return 0, fmt.Errorf("event seq %d is not on the current surface", eventSeq)
}

// DeriveEventMessage 是 THE per-node projection rule（surface.js:75-110）：
//   - user/message → data 原样（框架化文本是生产者责任，投影层永不包壳）
//   - system/assistant → data.message；content 为空 → nil（不注入空消息）
//   - tool/result → data.message
//   - 其他 → nil
func DeriveEventMessage(e *Event) (*Message, error) {
	switch e.Type {
	case EventUserMessage:
		var m Message
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return nil, err
		}
		return &m, nil
	case EventSystemMessage, EventAssistantMessage:
		var d SystemMessageData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return nil, err
		}
		if d.Message == nil || len(d.Message.Content) == 0 {
			return nil, nil
		}
		return d.Message, nil
	case EventToolResult:
		var d ToolResultData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return nil, err
		}
		return d.Message, nil
	}
	return nil, nil
}

// ---------- 时间 ----------

func millis() int64 { return time.Now().UnixMilli() }
