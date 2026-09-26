// Token 启发式计量（dsh-token-meter/lib/types/estimate.js 逐字复刻）。
//
// 常量：CHARS_PER_TOKEN=4、BLOCK_OVERHEAD=4、ROLE_OVERHEAD=4。
// lidsh 收缩（文档允许）：measure 全量重估（无 baseline 缓存面），
// route 价 == 启发价。
package compaction

import (
	"encoding/json"
	"unicode/utf8"

	"lidsh/internal/llm"
	"lidsh/internal/session"
)

const (
	charsPerToken = 4
	blockOverhead = 4
	roleOverhead  = 4
)

// MeasureNode 是 surface 一个节点的价格。
type MeasureNode struct {
	Seq    int
	Tokens int
}

// Measurement 是会话级测量（tokenMeter.measure 对应物）。
type Measurement struct {
	TotalTokens int
	Nodes       []MeasureNode
}

// Measure 全量重估 surface 节点（system 消息按 system 面计）。
func Measure(sess *session.Session) Measurement {
	nodes := sess.Surface()
	m := Measurement{Nodes: make([]MeasureNode, 0, len(nodes))}
	for _, n := range nodes {
		t := EstimateMessage(n.Message)
		m.Nodes = append(m.Nodes, MeasureNode{Seq: n.EventSeq, Tokens: t})
		m.TotalTokens += t
	}
	return m
}

// EstimateContent 按固定密度对内容块计价（estimateContent）。
func EstimateContent(blocks []llm.ContentBlock) int {
	tokens := 0
	for _, b := range blocks {
		switch b.Type {
		case "text", "reasoning":
			tokens += ceilDiv(runeLen(b.Text), charsPerToken) + blockOverhead
		case "tool-call":
			tokens += ceilDiv(runeLen(b.Name), charsPerToken) +
				ceilDiv(runeLen(b.Arguments), charsPerToken) + blockOverhead
		case "tool-result":
			tokens += EstimateContent(b.Content) + blockOverhead
		default:
			tokens += estimateStructuralBlock(b)
		}
	}
	return tokens
}

// estimateStructuralBlock：未知/image 块取 JSON 结构的保守价。
func estimateStructuralBlock(b llm.ContentBlock) int {
	return blockOverhead + ceilDiv(runeLen(string(mustJSON(b))), charsPerToken)
}

// EstimateSystemMessage：system 消息按"文本密度 + role 框架"计（无块开销）。
func EstimateSystemMessage(m *llm.Message) int {
	if m == nil || len(m.Content) == 0 {
		return 0
	}
	chars := 0
	for _, b := range m.Content {
		if b.Type == "text" {
			chars += runeLen(b.Text)
		} else {
			chars += runeLen(string(mustJSON(b)))
		}
	}
	return ceilDiv(chars, charsPerToken) + roleOverhead
}

// EstimateMessage 是一条消息的启发价（estimateMessage）。
func EstimateMessage(m *llm.Message) int {
	if m == nil {
		return 0
	}
	if m.Role == llm.RoleSystem {
		return EstimateSystemMessage(m)
	}
	return EstimateContent(m.Content) + roleOverhead
}

// EstimateToolsTokens：工具 schema 部分的启发价（header.tools 缺失记 0；
// lidsh 无 durable header，宿主可注入等价文本）。
func EstimateToolsTokens(tools []llm.ToolDefinition) int {
	if len(tools) == 0 {
		return 0
	}
	return ceilDiv(runeLen(string(mustJSON(tools))), charsPerToken) + blockOverhead
}

// runeLen 按 Unicode code point 计数（对齐 JS string.length 的近似面：
// 非 BMP 字符 JS 记 2、Go 记 1，对启发式密度无实质影响）。
func runeLen(s string) int { return utf8.RuneCountInString(s) }

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// jsonUnmarshal 是包内统一入口。
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
