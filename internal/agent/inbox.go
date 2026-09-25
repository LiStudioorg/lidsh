// inbox 是本阶段的最简待处理输入队列（复刻 Inbox 的 claim 语义子集）。
// M1a 单发：一个 Prompt 一条消息。next-step 回注上下文（工具 additionalContexts）
// 由 executeToolCalls 在收束评估时处理。
package agent

type inboxItem struct {
	content string
}

type inbox struct {
	items []inboxItem
	done  bool
}

// claim 取走下一条待处理输入（DSH inbox.claim：消费并标记）。
func (in *inbox) claim() string {
	if len(in.items) == 0 {
		in.done = true
		return ""
	}
	it := in.items[0]
	in.items = in.items[1:]
	return it.content
}
