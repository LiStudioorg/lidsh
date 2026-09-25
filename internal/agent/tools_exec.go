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
	ec := &tools.ExecContext{Signal: a.Signal, AgentCWD: a.CWD, CWD: a.CWD}
	tr, err := a.Tools.Execute(call, ec)
	if err != nil {
		// registry 层异常：合成 isError 失败结果。
		text := fmt.Sprintf("Error: %v", err)
		msg := frameToolResultMessage(blk.ID, text, true)
		_, _ = a.Sess.Append(session.EventToolResult,
			session.ToolResultData{Turn: turn, Step: step, Message: msg,
				Error: &struct {
					Name string `json:"name"`
					Code string `json:"code"`
				}{Name: "Error", Code: "UNKNOWN"}},
			session.AppendOp(), []int{callSeq})
		return nil, nil
	}

	msg := frameToolResultMessage(blk.ID, tr.Content, tr.IsError)
	_, _ = a.Sess.Append(session.EventToolResult,
		session.ToolResultData{Turn: turn, Step: step, Message: msg,
			Error: toolError(tr)},
		session.AppendOp(), []int{callSeq})
	return tr, nil
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
