// Agent 执行核心：复刻 dsh-agent-loop 的 turn/step 状态机（core-loop.md §3）。
//
// 语义要点（逐条对齐 docs/_parts/core-loop.md 引用的 dsh-agent-loop/lib/index.js）：
//   - Prompt 入口创建 user/message 并驱动 kick()→turn() 循环
//   - turn：append turn/start，循环 step 直到收束；turn/end 落 reason
//   - step：一次模型调用 prepareRequest→stream→assemble→工具执行
//   - 工具执行见 tools_exec.go；崩溃恢复见 recover.go
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// Phase 记录当前 turn/step（事件与崩溃恢复用）。
type Phase struct {
	Turn int
	Step int
}

// Agent 是一次会话的执行体。
type Agent struct {
	Sess     *session.Session
	Resolver Resolver
	Tools    *tools.Registry

	// 默认模型参数（M1a：单一 provider/model；后续由 header proposal 驱动）。
	Provider string
	Model    string
	Reason   llm.ReasoningEffort

	// System 是 system prompt 文本（surface node 0 system/message 的来源）。
	System string

	// CWD 是会话工作目录。
	CWD string

	// Sandbox 是沙箱 standing 配置（M2；nil=未挂载）。
	Sandbox *tools.SandboxOptions

	Signal context.Context

	phase   Phase
	sig     context.CancelFunc
	started bool
}

// Options 是 New 的输入。
type Options struct {
	Sess     *session.Session
	Resolver Resolver
	Tools    *tools.Registry
	Provider string
	Model    string
	Reason   llm.ReasoningEffort
	System   string
	CWD      string
	// Sandbox 是挂载沙箱组合的 standing 配置（M2）；nil=未挂载
	// confinement executor（工具不感知沙箱，提权字段不 advertise）。
	Sandbox *tools.SandboxOptions
}

// New 构造 agent。
func New(opts Options) *Agent {
	sig, cancel := context.WithCancel(context.Background())
	return &Agent{
		Sess: opts.Sess, Resolver: opts.Resolver, Tools: opts.Tools,
		Provider: opts.Provider, Model: opts.Model, Reason: opts.Reason,
		System: opts.System, CWD: opts.CWD,
		Sandbox: opts.Sandbox,
		Signal:  sig, sig: cancel,
	}
}

// Cancel 取消当前 turn（cancel RPC 对应物；取消失效后可再 Prompt）。
func (a *Agent) Cancel() { a.sig() }

// Prompt 把用户输入送入并驱动 agent 直到回复完成。
func (a *Agent) Prompt(content, mode string) (*Result, error) {
	if mode == "" {
		mode = "followup"
	}
	if mode != "followup" && mode != "steer" {
		return nil, fmt.Errorf("agent: unknown prompt mode %q", mode)
	}

	if !a.started {
		// 首轮：注入 system prompt（surface node 0 的 system/message）。
		if a.System != "" {
			msg := systemMessage(a.System)
			if _, err := a.Sess.Append(session.EventSystemMessage,
				session.SystemMessageData{Turn: 0, Step: 0, Message: msg},
				session.AppendOp(), nil); err != nil {
				return nil, err
			}
		}
		a.started = true
	}

	// 一次 Prompt = 一个 turn（headless 单发；inbox 只含本条消息）。
	resetCtx, cancel := context.WithCancel(context.Background())
	a.sig = cancel
	a.Signal = resetCtx
	inbox := &inbox{items: []inboxItem{{content: content}}}

	return a.turn(inbox), nil
}

// turn 复刻 turn()（index.js:919-1007）。
func (a *Agent) turn(inbox *inbox) *Result {
	a.phase.Turn++
	a.phase.Step = 0
	turn := a.phase.Turn

	if _, err := a.Sess.Append(session.EventTurnStart, session.TurnStartData{Turn: turn}, nil, nil); err != nil {
		return &Result{Kind: "error", Err: err}
	}

	// 首轮把用户消息落 user/message（surface）。
	if msg := inbox.claim(); msg != "" {
		if _, err := a.Sess.Append(session.EventUserMessage,
			*createUserMessage(msg), session.AppendOp(), nil); err != nil {
			return &Result{Kind: "error", Err: err}
		}
	}

	var endReason session.TurnEndReason
	var stops *Result

	for {
		if err := a.Signal.Err(); err != nil {
			a.appendTurnEnd(turn, session.TurnEndReason{Kind: "aborted", Failure: raw(jsonMarshal(map[string]any{
				"name": "AbortError", "code": "ABORTED", "message": err.Error()}))})
			return &Result{Kind: "aborted", Err: err}
		}

		stepEnd := a.step()
		if stepEnd != nil {
			stops = stepEnd
			endReason = stepEnd.Reason
			break
		}
	}

	if endReason.Kind == "" {
		endReason = session.TurnEndReason{Kind: "completed"}
	}
	a.appendTurnEnd(turn, endReason)
	return stops
}

func (a *Agent) appendTurnEnd(turn int, reason session.TurnEndReason) {
	_, _ = a.Sess.Append(session.EventTurnEnd, session.TurnEndData{Turn: turn, Reason: reason}, nil, nil)
}

// step 复刻 step()：一次模型调用 + 可能的工具执行。
// 返回非 nil 表示 turn 收束。
func (a *Agent) step() *Result {
	a.phase.Step++
	turn, step := a.phase.Turn, a.phase.Step

	if _, err := a.Sess.Append(session.EventStepStart, session.StepData{Turn: turn, Step: step}, nil, nil); err != nil {
		return &Result{Kind: "error", Err: err}
	}
	defer a.Sess.Append(session.EventStepEnd, session.StepData{Turn: turn, Step: step}, nil, nil)

	adapter, err := a.Resolver.Adapter(a.Provider)
	if err != nil {
		return a.stepErr(turn, llm.Failure{Message: err.Error(), Code: llm.ErrUnknown})
	}

	// 构造消息集：surface 派生（已含 system/user/历史 assistant+tool）。
	messages := a.Sess.DeriveMessages()

	acc := session.NewAccumulator()
	asm := newAssembler(a.Provider, a.Model)
	stream, err := adapter.Stream(a.Signal, llm.GenerateOptions{
		Provider: a.Provider, Model: a.Model, Messages: messages,
		Tools: a.Tools.Schemas(), Reasoning: a.Reason,
		Signal: a.Signal, SessionID: a.Sess.Header.ID,
	})
	if err != nil {
		return a.stepErr(turn, llm.Failure{Message: err.Error(), Code: llm.ErrTransport})
	}

	var usage *llm.TokenUsage
	for ch := range stream {
		acc.Push(nowMillis(), ch)
		switch ch.Type {
		case llm.ChunkUsage:
			if ch.Usage != nil {
				c := *ch.Usage
				usage = &c
			}
		}
		asm.push(ch)
	}
	blocks := asm.blocks()

	switch asm.finish {
	case llm.FinishError:
		f := asm.failure()
		if f == nil {
			f = &llm.Failure{Message: "model stream error", Code: llm.ErrUnknown}
		}
		a.appendAttempt(turn, acc)
		return a.stepErr(turn, *f)
	case llm.FinishAborted:
		if len(blocks) > 0 && (flattenText(blocks) != "" || hasToolCalls(blocks)) {
			a.appendAssistant(turn, blocks, acc, usage, true)
			a.appendTurnEnd(turn, session.TurnEndReason{Kind: "aborted"})
		} else {
			a.appendAttempt(turn, acc)
		}
		return &Result{Kind: "aborted", Err: a.Signal.Err()}
	}

	// 成功：assistant/message。
	a.appendAssistant(turn, blocks, acc, usage, false)

	if asm.finish == llm.FinishMaxTokens {
		return &Result{Kind: "max-tokens"}
	}
	if !hasToolCalls(blocks) {
		return &Result{Kind: "completed"} // 最终回答出现处
	}

	// 执行工具调用；concluded → 收束。
	concluded, rerr := a.executeToolCalls(turn, step, blocks)
	if rerr != nil {
		return a.stepErr(turn, *rerr)
	}
	if concluded {
		return &Result{Kind: "completed"}
	}
	return nil // 继续下一步
}

func (a *Agent) stepErr(turn int, f llm.Failure) *Result {
	reason := session.TurnEndReason{Kind: "error", Failure: raw(jsonMarshal(f))}
	a.appendTurnEnd(turn, reason)
	return &Result{Kind: "error", Err: &f, Reason: reason}
}

func (a *Agent) appendAssistant(turn int, blocks []llm.ContentBlock, acc *session.StreamAccumulator, usage *llm.TokenUsage, interrupted bool) {
	if len(blocks) == 0 && !interrupted {
		return
	}
	msg := frameAssistantMessage(a.Provider, a.Model, blocks)
	_, _ = a.Sess.Append(session.EventAssistantMessage,
		session.AssistantMessageData{Turn: turn, Step: a.phase.Step, Message: msg,
			Stream: acc.Items(), Usage: usage, Interrupted: interrupted},
		session.AppendOp(), nil)
}

func (a *Agent) appendAttempt(turn int, acc *session.StreamAccumulator) {
	_, _ = a.Sess.Append(session.EventAssistantAttempt,
		session.AssistantAttemptData{Turn: turn, Step: a.phase.Step, Stream: acc.Items()}, nil, nil)
}

// Result 是一次 Prompt 的收束结果。
type Result struct {
	Kind   string // completed|max-tokens|aborted|error
	Err    error
	Reason session.TurnEndReason
}

// ---------- 小工具 ----------

func systemMessage(system string) *llm.Message {
	return &llm.Message{
		ID:      session.NewMessageID(),
		Role:    llm.RoleSystem,
		Content: []llm.ContentBlock{{Type: "text", Text: system}},
		Source:  llm.MessageSource{Kind: "plugin"},
	}
}

func raw(b []byte) json.RawMessage { return json.RawMessage(b) }

func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
