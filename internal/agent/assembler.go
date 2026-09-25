// assembler 把 chunk 流聚合成 assistant 消息的内容块（复刻 dsh-llm 的
// AssistantAssembler 语义：block-start 开块、delta 累积、block-end 定稿）。
package agent

import (
	"strings"

	"lidsh/internal/llm"
)

type assembler struct {
	provider string
	model    string

	finish llm.FinishReason
	fail   *llm.Failure

	// 已定稿的块（顺序）
	content []llm.ContentBlock
	open    map[int]blockKind
	// 文本/推理增量按 index 累积；工具调用按 wire index 聚合。
	pendingT    map[int]*strings.Builder
	pendingR    map[int]*strings.Builder
	toolScratch map[int]*toolScratch
}

type blockKind int

const (
	kbText blockKind = iota
	kbReasoning
	kbToolCall
)

type toolScratch struct {
	id   string
	name string
	args strings.Builder
}

func newAssembler(provider, model string) *assembler {
	return &assembler{
		provider: provider, model: model,
		open:        map[int]blockKind{},
		pendingT:    map[int]*strings.Builder{},
		pendingR:    map[int]*strings.Builder{},
		toolScratch: map[int]*toolScratch{},
	}
}

func (a *assembler) push(c llm.Chunk) {
	switch c.Type {
	case llm.ChunkBlockStart:
		k := blockKindFrom(c.BlockType)
		if k >= 0 {
			a.open[c.BlockIndex] = k
		}
	case llm.ChunkTextDelta:
		b := a.pendingT[c.BlockIndex]
		if b == nil {
			b = &strings.Builder{}
			a.pendingT[c.BlockIndex] = b
		}
		b.WriteString(c.Delta)
	case llm.ChunkReasoningDelta:
		b := a.pendingR[c.BlockIndex]
		if b == nil {
			b = &strings.Builder{}
			a.pendingR[c.BlockIndex] = b
		}
		b.WriteString(c.Delta)
	case llm.ChunkToolCallDelta:
		ts := a.toolScratch[c.BlockIndex]
		if ts == nil {
			ts = &toolScratch{}
			a.toolScratch[c.BlockIndex] = ts
		}
		if ts.id == "" {
			ts.id = c.ToolCallID
		}
		if ts.name == "" {
			ts.name = c.ToolName
		}
		ts.args.WriteString(c.ArgumentsDelta)
	case llm.ChunkBlockEnd:
		a.close(c.BlockIndex, c.Block)
	case llm.ChunkFinish:
		a.finish = c.Finish
		if c.Fail != nil {
			f := *c.Fail
			a.fail = &f
		}
	case llm.ChunkUsage:
		// usage 已在 agent 层记账；此处不改变内容块。
	default:
		// 未知块类型忽略（fail-safe，不静默损坏内容）。
	}
}

func blockKindFrom(bt llm.BlockType) blockKind {
	switch bt {
	case llm.BlockText:
		return kbText
	case llm.BlockReasoning:
		return kbReasoning
	case llm.BlockToolCall:
		return kbToolCall
	}
	return -1
}

// close 定稿一个块：优先用 chunk 自带的完整 Block，否则按累积增量组装。
func (a *assembler) close(index int, complete *llm.ContentBlock) {
	if complete != nil {
		a.content = append(a.content, *complete)
	} else {
		if b := a.build(index); b != nil {
			a.content = append(a.content, *b)
		}
	}
	delete(a.open, index)
	delete(a.pendingT, index)
	delete(a.pendingR, index)
	delete(a.toolScratch, index)
}

// build 从累积增量构造块（无 block-end 定稿块时兜底）。
func (a *assembler) build(index int) *llm.ContentBlock {
	switch a.open[index] {
	case kbText:
		if b := a.pendingT[index]; b != nil && b.Len() > 0 {
			return &llm.ContentBlock{Type: "text", Text: b.String()}
		}
	case kbReasoning:
		if b := a.pendingR[index]; b != nil && b.Len() > 0 {
			return &llm.ContentBlock{Type: "reasoning", Text: b.String()}
		}
	case kbToolCall:
		if ts := a.toolScratch[index]; ts != nil {
			return &llm.ContentBlock{Type: "tool-call", ID: ts.id, Name: ts.name, Arguments: ts.args.String()}
		}
	}
	return nil
}

// blocks 返回按出现顺序定稿的块（fail-closed：未关闭的块也兜底 build）。
func (a *assembler) blocks() []llm.ContentBlock {
	var out []llm.ContentBlock
	out = append(out, a.content...)
	for idx := range a.open {
		if b := a.build(idx); b != nil {
			out = append(out, *b)
		}
	}
	return out
}

func (a *assembler) failure() *llm.Failure {
	if a.fail != nil {
		f := *a.fail
		return &f
	}
	if a.finish == llm.FinishError {
		return &llm.Failure{Message: "model stream error", Code: llm.ErrUnknown}
	}
	return nil
}
