// 工具执行调度：复刻 executeToolCalls/runGroup（core-loop.md §3.5）。
//
// 执行序（对每批工具调用）：
//  1. append "tool/call"（先落盘后派发）
//  2. registry.Execute → result
//  3. append "tool/result"（surfaceOp append，sourceEventSeqs 指回 call）
//  4. result.ConcludesTurn → concluded
//
// M1a 并发模型：全部按 exclusive 串行执行（DSH 默认 maxParallelToolCalls=10，
// 但 exclusive 工具保留顺序；本阶段保守串行，并行留 M1b）。
package agent

import (
	"fmt"

	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// executeToolCalls 派发 assistant 消息里的全部 tool-call 块。
// 返回 concluded（某工具 concludesTurn=true）或错误（调度阶段）。
func (a *Agent) executeToolCalls(turn, step int, blocks []llm.ContentBlock) (bool, *llm.Failure) {
	concluded := false
	var fail *llm.Failure
	for _, blk := range blocks {
		if blk.Type != "tool-call" {
			continue
		}
		tr, err := a.runTool(turn, step, blk)
		if err != nil {
			fail = err
			break
		}
		if tr.ConcludesTurn {
			concluded = true
		}
	}
	return concluded, fail
}

// runTool 执行单个工具调用：tool/call → Execute → tool/result。
func (a *Agent) runTool(turn, step int, blk llm.ContentBlock) (*tools.Result, *llm.Failure) {
	args := blk.Arguments
	if args == "" {
		args = "{}"
	}
	// tool/call 先落盘（parseArguments 保留原文）；记下事件 seq 供 result 引用。
	callEv, err := a.Sess.Append(session.EventToolCall,
		session.ToolCallData{Turn: turn, Step: step, CallID: blk.ID, Name: blk.Name, Arguments: args},
		nil, nil)
	if err != nil {
		return nil, &llm.Failure{Message: err.Error(), Code: llm.ErrUnknown}
	}
	callSeq := callEv.Seq

	call := tools.ToolCall{CallID: blk.ID, RootCallID: blk.ID, Name: blk.Name, Arguments: args}
	ec := &tools.ExecContext{Signal: a.Signal, AgentCWD: a.CWD, CWD: a.CWD, Ctx: a.toolCtx}
	// M2：挂载沙箱组合时，每次调用派生 SandboxContext（§6.3 载荷含 callId/agent）。
	if a.Sandbox != nil {
		ec.Sandbox = a.Sandbox.Context(a.Sess.Header.ID, blk.ID)
	}
	tr, err := a.Tools.Execute(call, ec)
	if err != nil {
		// registry 层异常：合成 isError 失败结果（码透传，goal 工具需要
		// GOAL_TOOL_* / GOAL_* 码面）。
		code := tools.ErrorCode(err)
		text := fmt.Sprintf("Error: %v", err)
		msg := frameToolResultMessage(blk.ID, text, true)
		_, _ = a.Sess.Append(session.EventToolResult,
			session.ToolResultData{Turn: turn, Step: step, Message: msg,
				Error: &struct {
					Name string `json:"name"`
					Code string `json:"code"`
				}{Name: "ToolError", Code: code}},
			session.AppendOp(), []int{callSeq})
		return nil, nil
	}

	msg := frameToolResultMessage(blk.ID, tr.Content, tr.IsError)
	_, _ = a.Sess.Append(session.EventToolResult,
		session.ToolResultData{Turn: turn, Step: step, Message: msg,
			Error: toolError(tr)},
		session.AppendOp(), []int{callSeq})
	// deferContext 对应物：goal §7.3 收尾 notice 经 plugin form=notice 回注
	// （下一步生效；框架化文本是生产者责任）。
	for _, inj := range tr.AdditionalContexts {
		a.queueContextNotice(turn, step, inj)
	}
	return tr, nil
}

// queueContextNotice 把一条回注上下文落成 plugin notice 消息（非 surface
// 通道走 tool/result 之外的 user/message 面：DSH deferContext 生成
// createUserMessage(source kind=plugin form=notice)）。
func (a *Agent) queueContextNotice(turn, step int, inj tools.ContextInjection) {
	plugin := inj.Plugin
	if plugin == "" {
		plugin = "tools"
	}
	msg := &llm.Message{
		ID:      session.NewMessageID(),
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: inj.Text}},
		Source: llm.MessageSource{Kind: "plugin", Plugin: plugin,
			Form: "notice", Summary: inj.Summary},
	}
	_, _ = a.Sess.Append(session.EventUserMessage, *msg, session.AppendOp(), nil)
}

func toolError(tr *tools.Result) *struct {
	Name string `json:"name"`
	Code string `json:"code"`
} {
	if !tr.IsError {
		return nil
	}
	return &struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}{Name: "Error", Code: "UNKNOWN"}
}
