// tool-result 确定性裁剪（dsh-compaction-tool-result-pruner 逐行复刻）。
//
// 规则：surface 上所有 tool/result 节点，text block 总 code point 超阈值才裁；
// 保留 headChars 头 + 首个跨删除区的 text block 插一次 marker + tailChars 尾；
// 写回是两条事件：compaction/prune（影子价）紧跟 tool/result replace。
package compaction

import (
	"fmt"
	"unicode/utf8"

	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// PruneMarker 是删除区的固定替换文本（PRUNE_MARKER，逐字）。
const PruneMarker = "\n\n[... tool result middle pruned ...]\n\n"

// PruneDefaults 是默认预算（DEFAULTS: 8192/4096/1024）。
var PruneDefaults = PruneConfig{ThresholdChars: 8192, HeadChars: 4096, TailChars: 1024}

// PruneConfig 是裁剪预算；Validate 复刻 resolveConfig 的约束与逐字错误。
type PruneConfig struct {
	ThresholdChars int
	HeadChars      int
	TailChars      int
}

// Validate 校验并返回自身（DSH resolveConfig 的加载期姿态）。
func (c PruneConfig) Validate() (PruneConfig, error) {
	if c.ThresholdChars <= 0 {
		return c, fmt.Errorf("ToolResultPruneConfig: thresholdChars (%d) must be a positive integer", c.ThresholdChars)
	}
	if c.HeadChars < 0 {
		return c, fmt.Errorf("ToolResultPruneConfig: headChars (%d) must be a non-negative integer", c.HeadChars)
	}
	if c.TailChars < 0 {
		return c, fmt.Errorf("ToolResultPruneConfig: tailChars (%d) must be a non-negative integer", c.TailChars)
	}
	emitted := c.HeadChars + runeLen(PruneMarker) + c.TailChars
	if emitted > c.ThresholdChars {
		return c, fmt.Errorf("ToolResultPruneConfig: headChars + marker + tailChars (%d) must be at most thresholdChars (%d)",
			emitted, c.ThresholdChars)
	}
	return c, nil
}

// measureContent：text block 的 code point 总数（非 text 记 0）。
func measureContent(blocks []llm.ContentBlock) int {
	chars := 0
	for _, b := range blocks {
		if b.Type == "text" {
			chars += utf8.RuneCountInString(b.Text)
		}
	}
	return chars
}

// PruneResult 是单节点裁剪的度量。
type PruneResult struct {
	Pruned       []PrunedItem `json:"pruned"`
	CharsRemoved int          `json:"charsRemoved"`
}

// PrunedItem 是一条落地的替换。
type PrunedItem struct {
	OriginalSeq    int    `json:"originalSeq"`
	ReplacementSeq int    `json:"replacementSeq"`
	CallID         string `json:"callId"`
	CharsBefore    int    `json:"charsBefore"`
	CharsAfter     int    `json:"charsAfter"`
}

// pruneContent 裁一个节点的内容；预算内返回 nil。
func (c PruneConfig) pruneContent(blocks []llm.ContentBlock) ([]llm.ContentBlock, error) {
	total := measureContent(blocks)
	if total <= c.ThresholdChars {
		return nil, nil
	}
	removedStart := c.HeadChars
	removedEnd := total - c.TailChars

	pruned := make([]llm.ContentBlock, 0, len(blocks))
	consumed := 0
	markerInserted := false
	for _, b := range blocks {
		if b.Type != "text" {
			pruned = append(pruned, b)
			continue
		}
		points := []rune(b.Text)
		blockStart := consumed
		blockEnd := blockStart + len(points)
		headEnd := minmax(len(points), max(0, removedStart-blockStart))
		tailStart := minmax(len(points), max(0, removedEnd-blockStart))
		marker := ""
		if blockStart < removedEnd && blockEnd > removedStart && !markerInserted {
			marker = PruneMarker
			markerInserted = true
		}
		text := string(points[:headEnd]) + marker + string(points[tailStart:])
		if text != "" {
			nb := b
			nb.Text = text
			pruned = append(pruned, nb)
		}
		consumed = blockEnd
	}
	if !markerInserted {
		return nil, fmt.Errorf("tool-result prune: failed to locate the removed text span")
	}
	after := measureContent(pruned)
	if after > c.ThresholdChars || after >= total {
		return nil, fmt.Errorf("tool-result prune: replacement must be smaller and within threshold")
	}
	return pruned, nil
}

// PruneSession 裁当前 surface 上全部超预算 tool/result（pruneSession 逐行）。
func PruneSession(sess *session.Session, cfg PruneConfig, meter func(*llm.Message) int) (*PruneResult, error) {
	if _, err := cfg.Validate(); err != nil {
		return nil, err
	}
	snapshot := append([]session.SurfaceNode(nil), sess.Surface()...)
	var candidates []session.SurfaceNode
	for _, n := range snapshot {
		e := sess.EventAt(n.EventSeq)
		if e != nil && e.Type == session.EventToolResult {
			candidates = append(candidates, n)
		}
	}
	res := &PruneResult{Pruned: []PrunedItem{}}
	for _, n := range candidates {
		e := sess.EventAt(n.EventSeq)
		var d session.ToolResultData
		if err := jsonUnmarshal(e.Data, &d); err != nil {
			return res, err
		}
		if d.Message == nil || len(d.Message.Content) == 0 {
			continue
		}
		outer := d.Message.Content[0]
		content, err := cfg.pruneContent(outer.Content)
		if err != nil {
			return res, err
		}
		if content == nil {
			continue
		}
		charsBefore := measureContent(outer.Content)
		charsAfter := measureContent(content)

		// 影子价事件必须同步紧邻在 replace 之前。
		if _, err := sess.Append(session.EventCompactionPrune, map[string]any{
			"shadowedRange":      map[string]any{"start": n.EventSeq, "end": n.EventSeq},
			"shadowedSeqs":       []int{n.EventSeq},
			"shadowedTokenCount": meter(d.Message),
		}, nil, nil); err != nil {
			return res, err
		}

		msg := *d.Message
		newOuter := outer
		newOuter.Content = content
		msg.Content = []llm.ContentBlock{newOuter}
		repl, err := sess.Append(session.EventToolResult, struct {
			Turn    int          `json:"turn"`
			Step    int          `json:"step"`
			Message *llm.Message `json:"message"`
			Error   *struct {
				Name string `json:"name"`
				Code string `json:"code"`
			} `json:"error,omitempty"`
			Meta interface{} `json:"meta,omitempty"`
		}{Turn: d.Turn, Step: d.Step, Message: &msg, Error: d.Error, Meta: rawOrNil(d.Meta)},
			session.ReplaceOp(n.EventSeq, n.EventSeq), []int{n.EventSeq})
		if err != nil {
			return res, err
		}
		res.Pruned = append(res.Pruned, PrunedItem{
			OriginalSeq: n.EventSeq, ReplacementSeq: repl.Seq,
			CallID: msg.Source.CallID, CharsBefore: charsBefore, CharsAfter: charsAfter})
		res.CharsRemoved += charsBefore - charsAfter
	}
	return res, nil
}

func minmax(cap_, v int) int {
	if v > cap_ {
		return cap_
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func rawOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
