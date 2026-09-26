// 上下文回注消息的框架化（core-session.md §1：框架化文本是生产者责任，
// 投影层永不包壳）。goal §7.3 收尾 notice 走 <goal_complete>/<goal_blocked>。
package agent

// renderContextNotice 把回注正文包成带标签的 notice 文本；plugin 转 kebab
// 标签（"tool-goal" → "tool-goal"，等价原样）。
func renderContextNotice(plugin, text string) string {
	return "<" + plugin + ">\n" + text + "\n</" + plugin + ">"
}
