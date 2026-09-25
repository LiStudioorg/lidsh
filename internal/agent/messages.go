// Package agent 复刻 DSH 的 agent 执行核心（dsh-agent-loop）：
// turn/step 状态机、工具调度、surface 治理。
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// createUserMessage 复刻 createUserMessage：把用户输入帧化为一条 user/message。
// DSH 的 createUserMessage 接受字符串或结构化内容；这里框架化纯文本，
// source.kind='user'。
func createUserMessage(content string) *llm.Message {
	return &llm.Message{
		ID:      session.NewMessageID(),
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: content}},
		Source:  llm.MessageSource{Kind: "user"},
	}
}

// frameToolResultMessage 复刻模型可见的 tool/result 消息：role=user，
// content=[text]，source.kind='tool' + callId。DSH 用 role=tool 的消息，
// 我们把 tool result 作为 user 角色的 tool 文本（OpenAI 兼容层把它映射回
// tool role——见 llm.openai 的 serializeMessages）。此处构造与
// createToolResultMessage（dsh-session lib）一致的基础形状。
func frameToolResultMessage(callID string, text string, isError bool) *llm.Message {
	blocks := []llm.ContentBlock{{Type: "text", Text: text}}
	return &llm.Message{
		ID:      session.NewMessageID(),
		Role:    llm.RoleUser,
		Content: blocks,
		Source:  llm.MessageSource{Kind: "tool", CallID: callID},
	}
}

// frameAssistantMessage 由 assembler 的块定稿为 assistant/message。
// source.kind='model'，携带 provider/model 溯源。
func frameAssistantMessage(provider, model string, blocks []llm.ContentBlock) *llm.Message {
	return &llm.Message{
		ID:      session.NewMessageID(),
		Role:    llm.RoleAssistant,
		Content: blocks,
		Source:  llm.MessageSource{Kind: "model", Provider: provider, Model: model},
	}
}

// flattenText 把文本块拼成一段（跳过工具块），用于空 content 判定（surface
// 投影：空 content → 不注入消息）。
func flattenText(blocks []llm.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// hasToolCalls 判定 assistant 消息里是否有未结的 tool-call 块。
func hasToolCalls(blocks []llm.ContentBlock) bool {
	for _, blk := range blocks {
		if blk.Type == "tool-call" {
			return true
		}
	}
	return false
}

// parseArguments 复刻 parseArguments（index.js:540-547）：JSON 解析失败保留
// 原文字符串；空串→{}。返回解析后的 map 或错误（调用方用原文字符串）。
func parseArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		// 数组或非对象：DSH 遇到非对象 JSON 也保留原文——统一返回错误。
		var arr []any
		if aerr := json.Unmarshal([]byte(raw), &arr); aerr == nil {
			return nil, errors.New("tool arguments must be an object")
		}
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	return m, nil
}
