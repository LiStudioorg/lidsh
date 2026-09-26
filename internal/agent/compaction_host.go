// compaction.Host 的 agent 实现 + 自动触发挂点（core-control.md §6.2）。
//
//   - pressure：step 循环内 compactIfNeeded(agent,'pressure')，失败只 warn
//     后继续 turn（DSH 监听器姿态：logger.warn → next()）；
//   - context-overflow：请求失败 code=CONTEXT_WINDOW_EXCEEDED 时进入，
//     跳过阈值/retain，prune + retainTokens=0 最大幅度收缩，成功且 surface
//     前进才 retry；重试按 agent 计数（maxOverflowRetries），idle/新
//     assistant/message 清零（lidsh：turn 结束清零）。
package agent

import (
	"context"

	"lidsh/internal/compaction"
	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// Session 实现 compaction.Host。
var _ compaction.Host = (*Agent)(nil)

// Session 满足 Host.Session。
func (a *Agent) Session() *session.Session { return a.Sess }

// Target 是压缩目标（agent 的当前路由）。
func (a *Agent) Target() compaction.Target {
	return compaction.Target{Provider: a.Provider, Model: a.Model,
		ContextWindow: a.ContextWindow}
}

// ToolSchemas 是 durable header 的工具 schema 面（KV-cache 前缀重放）。
func (a *Agent) ToolSchemas() []llm.ToolDefinition {
	if a.Tools == nil {
		return nil
	}
	return a.Tools.Schemas()
}

// StreamSummary 发一次 purpose=compaction 的流并聚合成 blocks。
func (a *Agent) StreamSummary(ctx context.Context, provider, model string,
	messages []llm.Message, tools []llm.ToolDefinition, maxTokens int) (
	[]llm.ContentBlock, *llm.TokenUsage, llm.FinishReason, *llm.Failure, error) {

	adapter, err := a.Resolver.Adapter(provider)
	if err != nil {
		return nil, nil, "", nil, err
	}
	mt := maxTokens
	stream, err := adapter.Stream(ctx, llm.GenerateOptions{
		Provider: provider, Model: model, Messages: messages, Tools: tools,
		MaxTokens: &mt, Purpose: llm.PurposeCompaction,
		Signal: ctx, SessionID: a.Sess.Header.ID,
	})
	if err != nil {
		return nil, nil, "", nil, err
	}
	asm := newAssembler(provider, model)
	var usage *llm.TokenUsage
	for ch := range stream {
		if ch.Type == llm.ChunkUsage && ch.Usage != nil {
			c := *ch.Usage
			usage = &c
		}
		asm.push(ch)
	}
	return asm.blocks(), usage, asm.finish, asm.failure(), nil
}

// CompactNow 暴露手动压缩（/compact 对应物；turn 打开时报 busy）。
func (a *Agent) CompactNow(ctx context.Context, commandID string) (*compaction.Result, error) {
	if a.Compaction == nil {
		return nil, &compaction.ManualCompactionError{Code: "busy",
			Msg: "Compaction is unavailable because this process has an active compaction, or the agent is not idle."}
	}
	return a.Compaction.CompactNow(ctx, commandID)
}

// pressureCheck 是 step 前的压力检查（auto=true 才跑；失败仅记日志面）。
// DSH 姿态：warn → 继续 turn。返回的 warn 文本由调用方丢弃即可（语义即
// "不阻断"）；context-overflow 不在这里。
func (a *Agent) pressureCheck() {
	if a.Compaction == nil || !a.Compaction.Auto() {
		return
	}
	_, _ = a.Compaction.CompactIfNeeded(a.Signal, "pressure")
}

// 收缩成功且 surface 前进 → retry=true。
func (a *Agent) overflowRecover() (retry bool) {
	if a.Compaction == nil || !a.Compaction.Auto() {
		return false
	}
	if a.overflowRetries >= compaction.DefaultMaxOverflowRetries+1 {
		return false
	}
	a.overflowRetries++
	sess := a.Sess
	before := len(sess.Surface())
	res, err := a.Compaction.CompactIfNeeded(a.Signal, "context-overflow")
	if err != nil || res == nil {
		return false
	}
	// surface.replaceGeneration 前进的等价检测：发生了 replace 提交。
	return len(sess.Surface()) != before || res.EndSeq > 0
}
