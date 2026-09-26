// Session 运行时：append-only 事件日志 + surface 增量折叠 + 崩溃修复。
package session

import (
	"encoding/json"
	"fmt"
	"sync"

	"lidsh/internal/llm"
)

// Session 是运行时会话对象（dsh-session/lib/index.js 的 Go 对应物）。
// 行为面（core-session.md §1.7）：
//   - Append：快照 data（不可 JSON 序列化直接抛）→ seq=log.length、time=now
//     → 校验 → surface.validateNext → push → 广播
//   - 禁止重入（append 内不得再 append）
type Session struct {
	mu sync.Mutex

	Header  Header
	log     []*Event
	surface []SurfaceNode

	// firstLiveSeq：构造函数种子事件的右界（fork/seeded 会话）。
	firstLiveSeq int

	appending bool
	listeners []func(*Event)
}

// New 创建会话。seeded 会话由调用方先 AppendSeed 种子事件（构造器是唯一合法
// 写方，end-seed 由 EndSeed 落）。
func New(header Header) *Session {
	header.Type = "session"
	header.Version = FormatVersion
	return &Session{Header: header}
}

// OnEvent 订阅提交后的事件（session/event 的 post-commit fire-and-forget）。
func (s *Session) OnEvent(fn func(*Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}

// Log 返回事件日志（只读快照引用；调用方不得修改）。
func (s *Session) Log() []*Event { return s.log }

// Len 是事件数（= 下一个 seq）。
func (s *Session) Len() int { return len(s.log) }

// Surface 返回当前 surface 节点。
func (s *Session) Surface() []SurfaceNode { return s.surface }

// EventAt 按 seq（= 日志下标）取事件；越界返回 nil。
func (s *Session) EventAt(seq int) *Event {
	if seq < 0 || seq >= len(s.log) {
		return nil
	}
	return s.log[seq]
}

// DeriveMessages 对 live surface 折 deriveEventMessage 得到请求 messages
// （Session.deriveMessages：surface 是唯一 LLM 可见投影）。
func (s *Session) DeriveMessages() []llm.Message {
	out := make([]llm.Message, 0, len(s.surface))
	for _, n := range s.surface {
		if n.Message != nil {
			out = append(out, *n.Message)
		}
	}
	return out
}

// Append 追加一个事件。data 必须 JSON 可序列化（快照语义）；surface 类型必须
// 带 surfaceOp，log-only 类型禁止携带——违反即返回错误且不落日志。
func (s *Session) Append(eventType string, data any, surfaceOp json.RawMessage, sourceEventSeqs []int) (*Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.appending {
		return nil, fmt.Errorf("session: reentrant append is forbidden")
	}
	s.appending = true
	defer func() { s.appending = false }()

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("session: event data is not JSON-serializable: %w", err)
	}
	e := &Event{Type: eventType, Seq: len(s.log), Time: millis(), Data: raw}
	isSurface := IsSurfaceType(eventType)
	if isSurface {
		if len(surfaceOp) == 0 {
			return nil, fmt.Errorf("event %q must carry surfaceOp", eventType)
		}
		e.SurfaceOp = surfaceOp
		e.SourceEventSeqs = sourceEventSeqs
	} else if len(surfaceOp) > 0 {
		return nil, fmt.Errorf("event %q is log-only and must not carry surfaceOp", eventType)
	}

	// surface.validateNext：replace 的区间端点必须已在面上。
	if isSurface {
		if err := s.validateNextSurface(e); err != nil {
			return nil, err
		}
	}

	if err := s.applySurface(e); err != nil {
		return nil, err
	}
	s.log = append(s.log, e)

	for _, fn := range s.listeners {
		fn(e)
	}
	return e, nil
}

// AppendSeed 追加构造器种子事件（fork 继承前缀用）。
func (s *Session) AppendSeed(eventType string, data any, surfaceOp json.RawMessage) (*Event, error) {
	e, err := s.Append(eventType, data, surfaceOp, nil)
	if err != nil {
		return nil, err
	}
	s.firstLiveSeq = len(s.log)
	return e, nil
}

// EndSeed 写种子结束标记（Session 构造器是唯一合法写方）。
func (s *Session) EndSeed(inherited bool) error {
	_, err := s.Append(EventSessionEndSeed, map[string]any{"inherited": inherited}, nil, nil)
	return err
}

// FirstLiveSeq 是种子右界。
func (s *Session) FirstLiveSeq() int { return s.firstLiveSeq }

func (s *Session) validateNextSurface(e *Event) error {
	var op struct {
		Op       string `json:"op"`
		StartSeq *int   `json:"startSeq"`
		EndSeq   *int   `json:"endSeq"`
	}
	if json.Unmarshal(e.SurfaceOp, &op) != nil {
		return nil // 字符串 "append"
	}
	if op.Op != "replace" {
		return nil
	}
	if op.StartSeq == nil || op.EndSeq == nil {
		return fmt.Errorf("replace requires startSeq and endSeq")
	}
	if _, err := positionOf(s.surface, *op.StartSeq); err != nil {
		return fmt.Errorf("replace startSeq: %w", err)
	}
	if _, err := positionOf(s.surface, *op.EndSeq); err != nil {
		return fmt.Errorf("replace endSeq: %w", err)
	}
	return nil
}

func (s *Session) applySurface(e *Event) error {
	if !IsSurfaceType(e.Type) {
		return nil
	}
	msg, err := DeriveEventMessage(e)
	if err != nil {
		return err
	}
	if msg == nil {
		return nil
	}
	var op struct {
		Op       string `json:"op"`
		StartSeq *int   `json:"startSeq"`
		EndSeq   *int   `json:"endSeq"`
	}
	if json.Unmarshal(e.SurfaceOp, &op) == nil && op.Op == "replace" {
		start, _ := positionOf(s.surface, *op.StartSeq)
		end, _ := positionOf(s.surface, *op.EndSeq)
		if start > end {
			start, end = end, start
		}
		merged := make([]SurfaceNode, 0, len(s.surface)-(end-start)+2)
		merged = append(merged, s.surface[:start]...)
		merged = append(merged, SurfaceNode{EventSeq: e.Seq, Message: msg})
		merged = append(merged, s.surface[end+1:]...)
		s.surface = merged
		return nil
	}
	s.surface = append(s.surface, SurfaceNode{EventSeq: e.Seq, Message: msg})
	return nil
}

// ---------- 从日志重建 ----------

// Restore 从加载的日志重建会话（LoadLog 之后）。
func Restore(header *Header, events []*Event) (*Session, error) {
	s := New(*header)
	s.log = events
	nodes, err := FoldSurface(events)
	if err != nil {
		return nil, err
	}
	s.surface = nodes
	for _, e := range events {
		if e.Type == EventSessionEndSeed {
			s.firstLiveSeq = e.Seq
		}
	}
	return s, nil
}

// ---------- 崩溃修复 ----------

// 修复文案逐字复刻 repair.js:95-108。
const (
	interruptedAfterRecorded = "The tool call was interrupted after it was recorded, but no result was durably recorded. Its outcome is unknown. Do not assume it ran; verify the real state before retrying."
	interruptedNotStarted    = "The tool call was interrupted before the Harness recorded it as started. Retry it if it is still needed."

	CodeToolOutcomeUnknown = "TOOL_OUTCOME_UNKNOWN"
	CodeToolNotStarted     = "TOOL_NOT_STARTED"
)

// InterruptedTurnClosers 复刻 interruptedTurnClosers（repair.js:23-140）：
// 扫日志找不平衡的 open turn/step/pending tool-calls，按序合成收尾事件：
//
//	每个未销账 call → 错误 tool/result → 补 step/end → 补 turn/end{interrupted}
//
// seq 续接、time 复用最后一条真实事件的 time（不造假未来时间）。
// 返回需要追加的事件（可能为空）。
func InterruptedTurnClosers(events []*Event) []*Event {
	type pending struct {
		seq       int
		recorded  bool
		turn, stp int
		name      string
	}
	var openTurn, openStep = -1, -1
	pendingCalls := map[string]*pending{}
	var order []string

	lastTime := int64(0)
	if n := len(events); n > 0 {
		lastTime = events[n-1].Time
	}
	nextSeq := len(events)
	mk := func(t string, data any, surfaceOp json.RawMessage, srcSeqs []int) *Event {
		raw, _ := json.Marshal(data)
		e := &Event{Type: t, Seq: nextSeq, Time: lastTime, Data: raw, SurfaceOp: surfaceOp, SourceEventSeqs: srcSeqs}
		nextSeq++
		return e
	}

	for _, e := range events {
		switch e.Type {
		case EventTurnStart:
			var d TurnStartData
			_ = json.Unmarshal(e.Data, &d)
			openTurn = d.Turn
		case EventTurnEnd:
			openTurn = -1
			openStep = -1
			pendingCalls = map[string]*pending{}
			order = nil
		case EventStepStart:
			var d StepData
			_ = json.Unmarshal(e.Data, &d)
			openStep = d.Step
		case EventStepEnd:
			openStep = -1
		case EventAssistantMessage:
			var d AssistantMessageData
			if json.Unmarshal(e.Data, &d) == nil && d.Message != nil {
				for _, b := range d.Message.Content {
					if b.Type == "tool-call" {
						pendingCalls[b.ID] = &pending{seq: e.Seq, recorded: false, turn: d.Turn, stp: d.Step, name: b.Name}
						order = append(order, b.ID)
					}
				}
			}
		case EventToolCall:
			var d ToolCallData
			if json.Unmarshal(e.Data, &d) == nil {
				if p, ok := pendingCalls[d.CallID]; ok {
					p.recorded = true
				} else {
					pendingCalls[d.CallID] = &pending{seq: e.Seq, recorded: true, turn: d.Turn, stp: d.Step, name: d.Name}
					order = append(order, d.CallID)
				}
			}
		case EventToolResult:
			var d ToolResultData
			if json.Unmarshal(e.Data, &d) == nil && d.Message != nil {
				for _, b := range d.Message.Content {
					if b.Type == "tool-result" {
						delete(pendingCalls, b.ToolCallID)
					}
				}
			}
		}
	}

	if openTurn < 0 && openStep < 0 && len(pendingCalls) == 0 {
		return nil
	}

	var closers []*Event
	for _, id := range order {
		p, ok := pendingCalls[id]
		if !ok {
			continue
		}
		text, code := interruptedNotStarted, CodeToolNotStarted
		if p.recorded {
			text, code = interruptedAfterRecorded, CodeToolOutcomeUnknown
		}
		msg := &Message{
			ID:   NewMessageID(),
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{{
				Type: "tool-result", ToolCallID: id,
				Content: []llm.ContentBlock{{Type: "text", Text: text}},
				IsError: true,
			}},
			Source: llm.MessageSource{Kind: "tool", CallID: id},
		}
		data := ToolResultData{Turn: p.turn, Step: p.stp, Message: msg,
			Error: &struct {
				Name string `json:"name"`
				Code string `json:"code"`
			}{Name: "Interrupted", Code: code}}
		raw, _ := json.Marshal(data)
		// 合成事件复用最后事件的 time；surfaceOp=append，指回原 call 事件。
		srcs := []int{p.seq}
		e := &Event{Type: EventToolResult, Seq: nextSeq, Time: lastTime, Data: raw,
			SurfaceOp: AppendOp(), SourceEventSeqs: srcs}
		nextSeq++
		closers = append(closers, e)
	}
	if openStep >= 0 {
		closers = append(closers, mk(EventStepEnd, StepData{Turn: openTurn, Step: openStep}, nil, nil))
	}
	if openTurn >= 0 {
		closers = append(closers, mk(EventTurnEnd, TurnEndData{Turn: openTurn,
			Reason: TurnEndReason{Kind: "interrupted"}}, nil, nil))
	}
	return closers
}
