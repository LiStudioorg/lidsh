// inbox 是待处理输入队列（复刻 Inbox 的 claim 语义子集 + goal-round-driver
// 的 stale/restore 回放面，core-control.md §7.4）。
//
// 每条输入携带完整 llm.Message 作为 durable 事实：内容 + source 归因
// （user / goal）。claim 只取未 stale 的队头条目；被取代（人工输入抢先）
// 的条目经 restoreOtherClaimed 原样回放。invariant 伴生（lib/invariant.js）：
// goal-sourced user/message 内容必须与由 durable 前缀重建的 prompt 逐字节
// 相等——本实现里 claim 直接携带渲染文本，落盘即逐字投影，天然满足。
package agent

import "lidsh/internal/llm"

type inboxItem struct {
	msg   llm.Message
	stale bool
}

type inbox struct {
	items []inboxItem
	done  bool
}

// claim 取走下一条未 stale 的待处理输入（跳过 stale 条目，DSH inbox.claim：
// 消费并标记）。返回 nil 表示队列已空。
func (in *inbox) claim() *llm.Message {
	for len(in.items) > 0 {
		it := in.items[0]
		in.items = in.items[1:]
		if it.stale {
			continue
		}
		m := it.msg
		return &m
	}
	in.done = true
	return nil
}

// containsFreshGoal 判定队列里是否还有未 stale 的 goal 归因条目。
func (in *inbox) containsFreshGoal() bool {
	for _, it := range in.items {
		if !it.stale && it.msg.Source.Kind == "goal" {
			return true
		}
	}
	return false
}

// markGoalStale 把队首 goal 条目标 stale，返回被标的条目（gate 拒绝用）。
func (in *inbox) markGoalStale() *inboxItem {
	if len(in.items) > 0 && in.items[0].msg.Source.Kind == "goal" && !in.items[0].stale {
		in.items[0].stale = true
		return &in.items[0]
	}
	return nil
}

// restoreOtherClaimed 把同批被 stale 标记的条目复原并 prepend 回队列
// （restoreOtherClaimed，driver :95）；staleFirst 已从队列摘除。
func (in *inbox) restoreOtherClaimed(staleFirst *inboxItem) {
	for i := range in.items {
		if in.items[i].stale {
			in.items[i].stale = false
		}
	}
	if staleFirst != nil {
		f := *staleFirst
		f.stale = false
		in.items = append([]inboxItem{f}, in.items...)
	}
}
