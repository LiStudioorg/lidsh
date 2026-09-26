// compaction 包测试：计量、tool-pairing、选区、pruner、事务。
package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// ---- 测试用 session 构造 ----

func newSess(t *testing.T) *session.Session {
	t.Helper()
	return session.New(session.NewHeader(session.NewID(), t.TempDir(), session.Meta{}))
}

func appendText(t *testing.T, sess *session.Session, typ string, data any, turn int) *session.Event {
	t.Helper()
	var op json.RawMessage
	var msg *llm.Message
	switch typ {
	case session.EventUserMessage:
		msg = data.(*llm.Message)
		op = session.AppendOp()
		data = msg
	case session.EventSystemMessage:
		d := data.(session.SystemMessageData)
		d.Turn = turn
		op = session.AppendOp()
		data = d
	case session.EventAssistantMessage:
		d := data.(session.AssistantMessageData)
		d.Turn = turn
		op = session.AppendOp()
		data = d
	}
	e, err := sess.Append(typ, data, op, nil)
	if err != nil {
		t.Fatalf("append %s: %v", typ, err)
	}
	return e
}

func userMsg(text string) *llm.Message {
	return &llm.Message{ID: session.NewMessageID(), Role: llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: text}},
		Source:  llm.MessageSource{Kind: "user"}}
}

func asstMsg(blocks ...llm.ContentBlock) *session.AssistantMessageData {
	return &session.AssistantMessageData{Message: &llm.Message{
		ID: session.NewMessageID(), Role: llm.RoleAssistant, Content: blocks,
		Source: llm.MessageSource{Kind: "model", Provider: "fake", Model: "m"}}}
}

func sysMsg(text string) session.SystemMessageData {
	return session.SystemMessageData{Message: &llm.Message{
		ID: session.NewMessageID(), Role: llm.RoleSystem,
		Content: []llm.ContentBlock{{Type: "text", Text: text}},
		Source:  llm.MessageSource{Kind: "plugin"}}}
}

func toolCall(id string) llm.ContentBlock {
	return llm.ContentBlock{Type: "tool-call", ID: id, Name: "bash", Arguments: `{"command":"ls"}`}
}

func appendToolResult(t *testing.T, sess *session.Session, turn, step int, callID, content string, withOp bool) *session.Event {
	t.Helper()
	d := session.ToolResultData{Turn: turn, Step: step, Message: &llm.Message{
		ID: session.NewMessageID(), Role: llm.RoleTool,
		Content: []llm.ContentBlock{{Type: "tool-result", ToolCallID: callID,
			Content: []llm.ContentBlock{{Type: "text", Text: content}}}},
		Source: llm.MessageSource{Kind: "tool", CallID: callID}}}
	var op json.RawMessage
	var srcs []int
	if withOp {
		op = session.AppendOp()
	}
	e, err := sess.Append(session.EventToolResult, d, op, srcs)
	if err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	return e
}

// ---- 计量 ----

func TestEstimateMessage(t *testing.T) {
	// text 12 chars → ceil(12/4)=3 + block 4 + role 4 = 11。
	m := &llm.Message{Role: llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: "hello world!"}}}
	if got := EstimateMessage(m); got != 11 {
		t.Fatalf("user text: %d want 11", got)
	}
	// system：无 block overhead：ceil(12/4)+4 = 7。
	sys := &llm.Message{Role: llm.RoleSystem,
		Content: []llm.ContentBlock{{Type: "text", Text: "hello world!"}}}
	if got := EstimateMessage(sys); got != 7 {
		t.Fatalf("system: %d want 7", got)
	}
}

// ---- tool-pairing ----

func TestPairingBalance(t *testing.T) {
	sess := newSess(t)
	appendText(t, sess, session.EventSystemMessage, sysMsg("s"), 0)
	appendText(t, sess, session.EventUserMessage, userMsg("hi"), 1)
	sess.Append(session.EventTurnStart, session.TurnStartData{Turn: 1}, nil, nil)
	appendText(t, sess, session.EventAssistantMessage,
		*asstMsg(toolCall("c1"), toolCall("c2")), 1)
	bal, err := balanceOf(sess)
	if err != nil {
		t.Fatal(err)
	}
	// surface 位置：0=system 1=user 2=assistant(2 calls)。
	// assistant 之后切点：2 个在途 tool-call → 不平衡。
	if bal.balancedAfter(2) {
		t.Fatal("after dual tool-call assistant must be unbalanced")
	}
	appendToolResult(t, sess, 1, 1, "c1", "r1", true)
	bal, _ = balanceOf(sess)
	if bal.balancedAfter(3) {
		t.Fatal("after one of two results still unbalanced")
	}
	appendToolResult(t, sess, 1, 1, "c2", "r2", true)
	bal, _ = balanceOf(sess)
	if !bal.balancedAfter(4) {
		t.Fatal("after both results balanced")
	}
}

func TestPairingCorruptOrphan(t *testing.T) {
	sess := newSess(t)
	appendToolResult(t, sess, 1, 1, "nope", "r", true) // 无匹配 call → corrupt
	if _, err := balanceOf(sess); err == nil ||
		!strings.Contains(err.Error(), "has no matching tool-call (corrupt surface)") {
		t.Fatalf("want orphan error, got %v", err)
	}
}

// ---- 选区 ----

func TestSelectRangeBasics(t *testing.T) {
	sess := newSess(t)
	appendText(t, sess, session.EventSystemMessage, sysMsg("system prompt"), 0)
	big := strings.Repeat("x", 400) // ~104 tokens
	appendText(t, sess, session.EventUserMessage, userMsg(big), 1)
	appendText(t, sess, session.EventAssistantMessage, *asstMsg(
		llm.ContentBlock{Type: "text", Text: big}), 1)
	appendText(t, sess, session.EventUserMessage, userMsg("recent question"), 2)

	m := Measure(sess)
	// retainTokens=0 → whole range from first non-system node。
	r, err := selectCompactableRange(sess, m, 0)
	if err != nil {
		t.Fatal(err)
	}
	// retainTokens=0：accumulated >= 0 从尾第一个节点即 break →
	// keepFromIdx = last idx → range = nodes[1..last-1]。
	if r == nil || r.start != m.Nodes[1].Seq {
		t.Fatalf("range start must skip system head: %+v", r)
	}
	// retain 巨大 → keepFromIdx 到 firstIdx → null。
	if r2, _ := selectCompactableRange(sess, m, 1<<20); r2 != nil {
		t.Fatal("huge retain must yield null")
	}
}

func TestSelectRangeKeepsToolPairIntact(t *testing.T) {
	sess := newSess(t)
	appendText(t, sess, session.EventSystemMessage, sysMsg("s"), 0)
	big := strings.Repeat("y", 400)
	appendText(t, sess, session.EventUserMessage, userMsg(big), 1)
	appendText(t, sess, session.EventAssistantMessage, *asstMsg(toolCall("c1")), 1)
	appendToolResult(t, sess, 1, 1, "c1", "result body", true)
	appendText(t, sess, session.EventAssistantMessage,
		*asstMsg(llm.ContentBlock{Type: "text", Text: "done"}), 1)
	appendText(t, sess, session.EventUserMessage, userMsg("tail question here"), 2)

	m := Measure(sess)
	// retain 大到必须把 tool 对之前也裁掉时，切点必须回退到平衡位。
	r, err := selectCompactableRange(sess, m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected range")
	}
	// 区内消息数守恒于 seq 集合。
	if len(r.shadowedSeqs) < 2 {
		t.Fatalf("shadowed: %v", r.shadowedSeqs)
	}
	bal, _ := balanceOf(sess)
	if !bal.balancedBefore(r.startIdx) || !bal.balancedAfter(r.endIdx) {
		t.Fatalf("range boundaries must be balanced: %+v", r)
	}
}

// ---- pruner ----

func TestPruneConfigValidate(t *testing.T) {
	if _, err := (PruneConfig{ThresholdChars: 0}).Validate(); err == nil ||
		!strings.Contains(err.Error(), "thresholdChars (0) must be a positive integer") {
		t.Fatalf("threshold: %v", err)
	}
	c := PruneConfig{ThresholdChars: 20, HeadChars: 16, TailChars: 0}
	if _, err := c.Validate(); err == nil ||
		!strings.Contains(err.Error(), "headChars + marker + tailChars") {
		t.Fatalf("emit budget: %v", err)
	}
	if _, err := PruneDefaults.Validate(); err != nil {
		t.Fatalf("defaults valid: %v", err)
	}
}

func TestPruneContentSlicing(t *testing.T) {
	cfg := PruneConfig{ThresholdChars: 100, HeadChars: 40, TailChars: 10}
	long := strings.Repeat("a", 300)
	blocks := []llm.ContentBlock{
		{Type: "text", Text: long[:150]},
		{Type: "text", Text: long[150:]},
	}
	out, err := cfg.pruneContent(blocks)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, b := range out {
		joined += b.Text
	}
	if strings.Count(joined, PruneMarker) != 1 {
		t.Fatalf("marker must appear exactly once: %q", joined)
	}
	if !strings.HasPrefix(joined, strings.Repeat("a", 40)) {
		t.Fatal("head 40 retained")
	}
	if !strings.HasSuffix(joined, strings.Repeat("a", 10)) {
		t.Fatal("tail 10 retained")
	}
	if measureContent(out) > 100 {
		t.Fatalf("after=%d must be <= threshold", measureContent(out))
	}
	// 阈值内 → nil。
	if o, _ := cfg.pruneContent([]llm.ContentBlock{{Type: "text", Text: "short"}}); o != nil {
		t.Fatal("within budget must return nil")
	}
}

func TestPruneSessionWritesPairEvents(t *testing.T) {
	sess := newSess(t)
	appendText(t, sess, session.EventSystemMessage, sysMsg("s"), 0)
	e := appendToolResult(t, sess, 1, 1, "c1", strings.Repeat("z", 20000), true)
	cfg := PruneConfig{ThresholdChars: 8192, HeadChars: 4096, TailChars: 1024}
	res, err := PruneSession(sess, cfg, EstimateMessage)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pruned) != 1 || res.Pruned[0].OriginalSeq != e.Seq {
		t.Fatalf("pruned: %+v", res.Pruned)
	}
	// 两条事件：compaction/prune 紧跟 replace tool/result。
	log := sess.Log()
	if log[e.Seq+1].Type != session.EventCompactionPrune {
		t.Fatalf("next event: %s", log[e.Seq+1].Type)
	}
	rep := log[e.Seq+2]
	if rep.Type != session.EventToolResult || string(rep.SurfaceOp) !=
		fmt.Sprintf(`{"endSeq":%d,"op":"replace","startSeq":%d}`, e.Seq, e.Seq) {
		t.Fatalf("replacement op: %s %s", rep.Type, rep.SurfaceOp)
	}
	if len(rep.SourceEventSeqs) != 1 || rep.SourceEventSeqs[0] != e.Seq {
		t.Fatalf("cites: %v", rep.SourceEventSeqs)
	}
	// 面上仍是同一位置（replace 生效）。
	found := false
	for _, n := range sess.Surface() {
		if n.EventSeq == rep.Seq {
			found = true
		}
	}
	if !found {
		t.Fatal("replacement not on surface")
	}
}

// ---- 引擎事务 ----

// fakeHost 是最小 compaction.Host。
type fakeHost struct {
	sess      *session.Session
	target    Target
	summary   string
	summaryFn func(messages []llm.Message) string
	lastMsgs  []llm.Message
}

func (h *fakeHost) Session() *session.Session         { return h.sess }
func (h *fakeHost) Target() Target                    { return h.target }
func (h *fakeHost) ToolSchemas() []llm.ToolDefinition { return nil }

func (h *fakeHost) StreamSummary(ctx context.Context, provider, model string,
	messages []llm.Message, tools []llm.ToolDefinition, maxTokens int) (
	[]llm.ContentBlock, *llm.TokenUsage, llm.FinishReason, *llm.Failure, error) {
	h.lastMsgs = messages
	text := h.summary
	if h.summaryFn != nil {
		text = h.summaryFn(messages)
	}
	return []llm.ContentBlock{{Type: "text", Text: text}}, nil, llm.FinishStop, nil, nil
}

func busyHistory(t *testing.T, sess *session.Session) {
	t.Helper()
	appendText(t, sess, session.EventSystemMessage, sysMsg("system prompt here"), 0)
	big := strings.Repeat("abcdefgh ", 200) // ~500 tokens
	appendText(t, sess, session.EventUserMessage, userMsg(big), 1)
	appendText(t, sess, session.EventAssistantMessage,
		*asstMsg(llm.ContentBlock{Type: "text", Text: big}), 1)
	appendText(t, sess, session.EventUserMessage, userMsg("latest question"), 2)
}

func TestManualCompactTransaction(t *testing.T) {
	sess := newSess(t)
	busyHistory(t, sess)
	host := &fakeHost{sess: sess, target: Target{Provider: "fake", Model: "m", ContextWindow: 1000},
		summary: "short summary"}
	eng, err := NewEngine(host, Config{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.CompactNow(context.Background(), "cmd-1")
	if err != nil || res == nil {
		t.Fatalf("compact: %v %v", res, err)
	}

	// 指令消息最后一条（source kind=plugin plugin=dsh-compaction-basic）。
	last := host.lastMsgs[len(host.lastMsgs)-1]
	if last.Source.Plugin != "dsh-compaction-basic" ||
		!strings.HasPrefix(last.Content[0].Text, "You are now acting as a compaction engine") {
		t.Fatalf("instruction msg: %+v", last.Source)
	}
	// 前缀含 system head。
	if host.lastMsgs[0].Role != llm.RoleSystem {
		t.Fatal("replay prefix must start with system")
	}

	log := sess.Log()
	// 事件顺序：start → summary → user/message(replace) → end。
	var startSeq, summarySeq, replaceSeq, endSeq int
	kinds := []string{}
	for _, e := range log {
		kinds = append(kinds, e.Type)
		switch e.Type {
		case session.EventCompactionStart:
			startSeq = e.Seq
		case session.EventCompactionSummary:
			summarySeq = e.Seq
		case session.EventUserMessage:
			var d session.Message
			if json.Unmarshal(e.Data, &d) == nil && d.Source.CompactionID != "" {
				replaceSeq = e.Seq
			}
		case session.EventCompactionEnd:
			endSeq = e.Seq
		}
	}
	if !(startSeq < summarySeq && summarySeq < replaceSeq && replaceSeq < endSeq) {
		t.Fatalf("event order: %v", kinds)
	}
	// replace 引用 [startSeq, summarySeq, …shadowed]。
	rep := log[replaceSeq]
	if len(rep.SourceEventSeqs) < 3 || rep.SourceEventSeqs[0] != startSeq || rep.SourceEventSeqs[1] != summarySeq {
		t.Fatalf("refs: %v", rep.SourceEventSeqs)
	}
	// checkpoint 归因 + 框架。
	var cp session.Message
	if err := json.Unmarshal(rep.Data, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Source.Kind != "plugin" || cp.Source.Plugin != "compact" ||
		cp.Source.CompactionID != res.CompactionID || cp.Source.SourceCommandID != "cmd-1" {
		t.Fatalf("checkpoint source: %+v", cp.Source)
	}
	if !strings.HasPrefix(cp.Content[0].Text, CheckpointPreamble+"\n\n"+SummaryOpenTag) ||
		cp.Content[len(cp.Content)-1].Text != SummaryCloseTag {
		t.Fatalf("framing: %+v", cp.Content)
	}
	// surface 只剩 system + checkpoint + 保留尾。
	for _, n := range sess.Surface() {
		if n.EventSeq == log[startSeq].Seq {
			t.Fatal("compaction/start must not hit surface")
		}
	}
	surf := sess.Surface()
	if len(surf) < 2 || surf[len(surf)-1].EventSeq == 0 {
		t.Fatalf("surface: %+v", surf)
	}
	if res.ShadowedTokenCount <= 0 || len(res.ShadowedSeqs) == 0 {
		t.Fatalf("result: %+v", res)
	}
}

func TestManualBusyStates(t *testing.T) {
	// open turn → busy。
	sess := newSess(t)
	busyHistory(t, sess)
	sess.Append(session.EventTurnStart, session.TurnStartData{Turn: 1}, nil, nil)
	eng, _ := NewEngine(&fakeHost{sess: sess,
		target: Target{Provider: "f", Model: "m", ContextWindow: 1000}, summary: "s"}, Config{})
	_, err := eng.CompactNow(context.Background(), "")
	var mc *ManualCompactionError
	if err == nil {
		t.Fatal("want busy")
	}
	if e, ok := err.(*ManualCompactionError); !ok || e.Code != "busy" {
		t.Fatalf("want ManualCompactionError busy, got %T %v", err, err)
	}
	_ = mc

	// 未匹配的 compaction/start → durable 锁 busy（standalone 场景）。
	sess2 := newSess(t)
	busyHistory(t, sess2)
	sess2.Append(session.EventCompactionStart, map[string]any{"compactionId": "x", "turn": nil}, nil, nil)
	eng2, _ := NewEngine(&fakeHost{sess: sess2,
		target: Target{Provider: "f", Model: "m", ContextWindow: 1000}, summary: "s"}, Config{})
	_, err = eng2.CompactNow(context.Background(), "")
	if e, ok := err.(*ManualCompactionError); !ok || e.Code != "busy" ||
		!strings.Contains(e.Msg, "the session compaction lock is already active") {
		t.Fatalf("want lock busy, got %v", err)
	}
}

func TestShrinkFailureLeavesEndWithError(t *testing.T) {
	sess := newSess(t)
	busyHistory(t, sess)
	huge := strings.Repeat("way too big summary ", 500)
	eng, _ := NewEngine(&fakeHost{sess: sess,
		target: Target{Provider: "f", Model: "m", ContextWindow: 1000}, summary: huge}, Config{})
	_, err := eng.CompactNow(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("want summary failure, got %v", err)
	}
	// end 事件带 error；surface 未被 replace。
	log := sess.Log()
	last := log[len(log)-1]
	if last.Type != session.EventCompactionEnd {
		t.Fatalf("last event: %s", last.Type)
	}
	if !strings.Contains(string(last.Data), `"error"`) {
		t.Fatalf("end must carry error: %s", last.Data)
	}
	for _, e := range log {
		var d session.Message
		if e.Type == session.EventUserMessage && json.Unmarshal(e.Data, &d) == nil &&
			d.Source.Plugin == "compact" {
			t.Fatal("failed compaction must not land checkpoint")
		}
	}
}

func TestPressureTriggerFlow(t *testing.T) {
	sess := newSess(t)
	appendText(t, sess, session.EventSystemMessage, sysMsg("sys"), 0)
	big := strings.Repeat("abcdefgh ", 100) // ~254 tokens per msg
	appendText(t, sess, session.EventUserMessage, userMsg(big), 1)
	appendText(t, sess, session.EventAssistantMessage,
		*asstMsg(llm.ContentBlock{Type: "text", Text: big}), 1)
	sess.Append(session.EventTurnStart, session.TurnStartData{Turn: 1}, nil, nil)
	appendText(t, sess, session.EventUserMessage, userMsg("question now"), 1)

	host := &fakeHost{sess: sess,
		target:  Target{Provider: "f", Model: "m", ContextWindow: 500}, // threshold=400
		summary: "tiny"}
	eng, _ := NewEngine(host, Config{})
	// 当前 total≈ sys スポンサーサイト… 算一下是否过阈值。
	m := Measure(sess)
	if m.TotalTokens < 400 {
		t.Fatalf("setup: tokens=%d must exceed threshold 400", m.TotalTokens)
	}
	res, err := eng.CompactIfNeeded(context.Background(), "pressure")
	if err != nil {
		t.Fatalf("pressure: %v", err)
	}
	if res == nil {
		t.Fatal("pressure compaction expected")
	}
	// numbered bracket：start/summary/end 的 turn 归属 = open turn 1。
	log := sess.Log()
	var sd map[string]any
	for _, e := range log {
		if e.Type == session.EventCompactionStart {
			json.Unmarshal(e.Data, &sd)
		}
	}
	if v, ok := sd["turn"].(float64); !ok || v != 1 {
		t.Fatalf("bracket owner: %v", sd)
	}
}

func TestPressureBelowThresholdNoop(t *testing.T) {
	sess := newSess(t)
	appendText(t, sess, session.EventSystemMessage, sysMsg("tiny"), 0)
	appendText(t, sess, session.EventUserMessage, userMsg("hi"), 1)
	sess.Append(session.EventTurnStart, session.TurnStartData{Turn: 1}, nil, nil)
	host := &fakeHost{sess: sess,
		target: Target{Provider: "f", Model: "m", ContextWindow: 1_000_000}, summary: "x"}
	eng, _ := NewEngine(host, Config{})
	res, err := eng.CompactIfNeeded(context.Background(), "pressure")
	if err != nil || res != nil {
		t.Fatalf("below threshold must noop: %v %v", res, err)
	}
}

func TestConfigRatioGuard(t *testing.T) {
	tr := false
	_ = tr
	if _, err := NewEngine(&fakeHost{target: Target{}},
		Config{ThresholdRatio: 0.5, RetainRatio: 0.6}); err == nil ||
		!strings.Contains(err.Error(), "retainRatio (0.6) must be less than the resolved thresholdRatio (0.5)") {
		t.Fatalf("ratio guard: %v", err)
	}
}
