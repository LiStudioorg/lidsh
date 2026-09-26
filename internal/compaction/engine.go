// CompactionEngine：阈值触发 + 选区 + LLM 摘要 + 事务提交
// （dsh-compaction + dsh-compaction-basic 的合并 Go 复刻，core-control.md §6）。
//
// 事务顺序（compactSurfaceRegion/commitCompactionBody 逐行）：
//
//	validate → assertCompactionInactive（锁 = 未匹配的 compaction/start）
//	append "compaction/start" {compactionId, sourceCommandId?, turn|null}
//	（异步摘要）
//	稳定性复检（自动=whole-surface / 手动=selected-span）
//	append "compaction/summary" {…影子价}   ← 与 replace 同步紧邻
//	append "user/message" checkpoint, replace(start..end),
//	       sourceEventSeqs=[start.seq, summary.seq, …shadowedSeqs]
//	append "compaction/end" {…, error?}
//
// 唯一的 surface mutation 就是那条 user/message replace；system head 永不入区；
// 切点必须 tool-pairing 平衡。
package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// 默认配置（DEFAULT_* 逐值）。
const (
	DefaultThresholdRatio     = 0.8
	DefaultRetainRatio        = 0.16
	DefaultMaxTokens          = 8192
	DefaultCompactionRetries  = 1
	DefaultMaxOverflowRetries = 1
)

// 标签与 checkpoint 文案（summarizer :211-257 逐字）。
const (
	SummaryOpenTag  = "<compacted-summary>"
	SummaryCloseTag = "</compacted-summary>"
)

// CheckpointPreamble 是替换消息首块文案（CHECKPOINT_PREAMBLE 逐字）。
const CheckpointPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

// CompactionInstruction 是摘要指令（COMPACTION_INSTRUCTION 逐字，含
// ${SUMMARY_OPEN_TAG} 展开）。
var CompactionInstruction = "You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.\n" +
	"\n" +
	"Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write \"(none)\" for an empty section — never drop a section.\n" +
	"\n" +
	"## Primary Request and Intent\n" +
	"- [the user's original and evolving goals; quote verbatim where the exact wording matters]\n" +
	"\n" +
	"## Key Technical Concepts\n" +
	"- [technologies, frameworks, patterns, and conventions in play]\n" +
	"\n" +
	"## Files and Code\n" +
	"- [exact path: why it matters, key changes or snippets]\n" +
	"\n" +
	"## Errors and Fixes\n" +
	"- [error: how it was resolved, plus any related user feedback]\n" +
	"\n" +
	"## Pending Jobs\n" +
	"- [explicitly requested work not yet completed]\n" +
	"\n" +
	"## Current Work\n" +
	"- [precisely what was in progress at this checkpoint]\n" +
	"\n" +
	"## Next Step\n" +
	"- [the single next action, directly in line with the most recent request, or \"(none)\"]\n" +
	"\n" +
	"## Critical Context\n" +
	"- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]\n" +
	"\n" +
	"Rules:\n" +
	"- Write concise English engineering prose. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.\n" +
	"- Capture user feedback and explicit instructions faithfully, especially corrections.\n" +
	"- Do NOT mention this summarization request or that the context was compacted.\n" +
	"- Output only the checkpoint text: do not call any tool or take any other action.\n" +
	"- If the conversation already contains a " + SummaryOpenTag + " block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure."

// ManualCompactionError 是手动压缩的可分类失败（code 闭合 union）。
type ManualCompactionError struct {
	Code string // busy|cancelled|changed|summary|commit|persistence
	Msg  string
	Err  error
}

func (e *ManualCompactionError) Error() string { return e.Msg }
func (e *ManualCompactionError) Unwrap() error { return e.Err }

// Config 是引擎配置（BasicCompactionConfig 收缩：单 target，无 modelPolicies）。
type Config struct {
	ThresholdRatio    float64      // 默认 0.8
	RetainRatio       float64      // 默认 0.16
	RetainTokens      int          // >0 时优先于 RetainRatio
	MaxTokens         int          // 摘要调用上限（默认 8192）
	CompactionRetries int          // 默认 1
	Auto              *bool        // nil = true
	Prune             *PruneConfig // nil = 不挂 pruner
}

func (c Config) withDefaults() (Config, error) {
	out := c
	if out.ThresholdRatio == 0 {
		out.ThresholdRatio = DefaultThresholdRatio
	}
	if out.RetainRatio == 0 && out.RetainTokens == 0 {
		out.RetainRatio = DefaultRetainRatio
	}
	if out.MaxTokens == 0 {
		out.MaxTokens = DefaultMaxTokens
	}
	if out.CompactionRetries == 0 {
		out.CompactionRetries = DefaultCompactionRetries
	}
	if out.RetainRatio >= out.ThresholdRatio {
		return out, fmt.Errorf("BasicCompactionConfig: retainRatio (%v) must be less than the resolved thresholdRatio (%v)",
			out.RetainRatio, out.ThresholdRatio)
	}
	if out.Prune != nil {
		if _, err := out.Prune.Validate(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Target 是压缩目标模型（contextWindow 由宿主解析）。
type Target struct {
	Provider      string
	Model         string
	ContextWindow int // 0 = 无容量信息 → TargetPressureConfigError 面
}

// Host 是引擎对宿主的窄接口（agent 实现；避免 import 环）。
type Host interface {
	Session() *session.Session
	Target() Target
	// ToolSchemas 是 durable header 的工具 schema 面（KV-cache 前缀重放用）。
	ToolSchemas() []llm.ToolDefinition
	// StreamSummary 发一次 purpose=compaction 的流并收束为 blocks。
	StreamSummary(ctx context.Context, provider, model string,
		messages []llm.Message, tools []llm.ToolDefinition, maxTokens int) (
		blocks []llm.ContentBlock, usage *llm.TokenUsage, finish llm.FinishReason, failure *llm.Failure, err error)
}

// Engine 是每会话压缩引擎（一把进程内锁 + durable 锁）。
type Engine struct {
	host Host
	cfg  Config

	mu sync.Mutex // 同会话并发压缩互斥（durable 锁的进程面镜像）
}

// NewEngine 构造并校验配置。
func NewEngine(host Host, cfg Config) (*Engine, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Engine{host: host, cfg: c}, nil
}

// Auto 返回 auto 开关（默认 true）。
func (e *Engine) Auto() bool { return e.cfg.Auto == nil || *e.cfg.Auto }

// ---- 选区（selectCompactableRange :393-416 逐行） ----

type region struct {
	start, end   int
	startIdx     int
	endIdx       int
	shadowedSeqs []int
}

func systemHeadSeq(sess *session.Session) (int, bool) {
	nodes := sess.Surface()
	if len(nodes) == 0 {
		return 0, false
	}
	head := sess.EventAt(nodes[0].EventSeq)
	if head != nil && head.Type == session.EventSystemMessage {
		return nodes[0].EventSeq, true
	}
	return 0, false
}

// selectCompactableRange：从首个非 system 节点起、保留 priced 尾、不切断
// tool-call/result 对；retainTokens=0 是最大幅度收缩（overflow/manual 面）。
func selectCompactableRange(sess *session.Session, m Measurement, retainTokens int) (*region, error) {
	nodes := sess.Surface()
	if len(m.Nodes) == 0 || len(nodes) == 0 {
		return nil, nil
	}
	if len(nodes) != len(m.Nodes) {
		return nil, fmt.Errorf("compaction: token-meter surface does not match the current session surface")
	}
	for i := range nodes {
		if nodes[i].EventSeq != m.Nodes[i].Seq {
			return nil, fmt.Errorf("compaction: token-meter surface does not match the current session surface")
		}
	}
	_, hasHead := systemHeadSeq(sess)
	firstIdx := 0
	if hasHead {
		firstIdx = 1
	}
	accumulated := 0
	keepFromIdx := len(nodes)
	for i := len(nodes) - 1; i >= 0; i-- {
		accumulated += m.Nodes[i].Tokens
		keepFromIdx = i
		if accumulated >= retainTokens {
			break
		}
	}
	if keepFromIdx <= firstIdx {
		return nil, nil
	}
	bal, err := balanceOf(sess)
	if err != nil {
		return nil, err
	}
	for keepFromIdx > firstIdx && !bal.balancedBefore(keepFromIdx) {
		keepFromIdx--
	}
	if keepFromIdx <= firstIdx {
		return nil, nil
	}
	return &region{
		start: nodes[firstIdx].EventSeq, end: nodes[keepFromIdx-1].EventSeq,
		startIdx: firstIdx, endIdx: keepFromIdx - 1,
		shadowedSeqs: seqsOf(nodes[firstIdx:keepFromIdx]),
	}, nil
}

func seqsOf(nodes []session.SurfaceNode) []int {
	out := make([]int, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.EventSeq)
	}
	return out
}

// validateSurfaceRegion（:533-549 逐字错误）。
func validateSurfaceRegion(sess *session.Session, start, end int) (*region, error) {
	nodes := sess.Surface()
	startIdx, endIdx := -1, -1
	for i, n := range nodes {
		if n.EventSeq == start {
			startIdx = i
		}
		if n.EventSeq == end {
			endIdx = i
		}
	}
	if startIdx == -1 {
		return nil, fmt.Errorf("compactRegion: start seq %d not found in surface", start)
	}
	if endIdx == -1 {
		return nil, fmt.Errorf("compactRegion: end seq %d not found in surface", end)
	}
	if startIdx > endIdx {
		return nil, fmt.Errorf("compactRegion: start seq %d (position %d) is after end seq %d (position %d) on the surface",
			start, startIdx, end, endIdx)
	}
	bal, err := balanceOf(sess)
	if err != nil {
		return nil, err
	}
	if !bal.balancedBefore(startIdx) {
		return nil, fmt.Errorf("compactRegion: start seq %d is not a balanced boundary (would split a step's tool-call/result pair)", start)
	}
	if !bal.balancedAfter(endIdx) {
		return nil, fmt.Errorf("compactRegion: end seq %d is not a balanced boundary (would split a step, or the step is still open)", end)
	}
	return &region{start: start, end: end, startIdx: startIdx, endIdx: endIdx,
		shadowedSeqs: seqsOf(nodes[startIdx : endIdx+1])}, nil
}

// ---- 入口状态（inspectCompactionEntryState 对应物） ----

type entryState struct {
	openTurn               *int
	unmatchedCompactionSeq int // -1 = 无
	latestEndSeedSeq       int // -1 = 无
}

func inspectEntryState(sess *session.Session) entryState {
	st := entryState{unmatchedCompactionSeq: -1, latestEndSeedSeq: -1}
	log := sess.Log()
	turnKnown, compKnown := false, false
	for seq := len(log) - 1; seq >= 0; seq-- {
		e := log[seq]
		switch e.Type {
		case session.EventSessionEndSeed:
			if st.latestEndSeedSeq == -1 {
				st.latestEndSeedSeq = e.Seq
			}
		case session.EventCompactionStart:
			if !compKnown {
				st.unmatchedCompactionSeq = e.Seq
				compKnown = true
			}
		case session.EventCompactionEnd:
			compKnown = true
		case session.EventTurnStart:
			if !turnKnown {
				t := 0
				var d session.TurnStartData
				if json.Unmarshal(e.Data, &d) == nil {
					t = d.Turn
				}
				st.openTurn = &t
				turnKnown = true
			}
		case session.EventTurnEnd:
			turnKnown = true
		}
		if turnKnown && compKnown && st.latestEndSeedSeq != -1 {
			break
		}
	}
	return st
}

// assertCompactionInactive：未匹配的 start（且无更晚的 end-seed）= 锁占用。
func assertCompactionInactive(st entryState, stage string) error {
	if st.unmatchedCompactionSeq == -1 || (st.latestEndSeedSeq != -1 && st.latestEndSeedSeq > st.unmatchedCompactionSeq) {
		return nil
	}
	return &ManualCompactionError{Code: "busy",
		Msg: fmt.Sprintf("%s: compaction already in progress; the session compaction lock is already active", stage)}
}

// ---- 触发入口 ----

// CompactIfNeeded 是自动触发主流程（compactIfNeeded :873-922）：
// trigger = "pressure" | "context-overflow"。
func (e *Engine) CompactIfNeeded(ctx context.Context, trigger string) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess := e.host.Session()
	target := e.host.Target()
	m := Measure(sess)

	if trigger == "context-overflow" {
		if e.cfg.Prune != nil {
			if _, err := PruneSession(sess, *e.cfg.Prune, EstimateMessage); err != nil {
				return nil, err
			}
			m = Measure(sess)
		}
		r, err := selectCompactableRange(sess, m, 0)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, nil
		}
		return e.compactRegion(ctx, r.start, r.end, "current-turn")
	}

	// pressure
	st := inspectEntryState(sess)
	if err := assertCompactionInactive(st, "automatic pressure compaction"); err != nil {
		return nil, err
	}
	targetKey := target.Provider + "/" + target.Model
	if target.ContextWindow <= 0 {
		return nil, fmt.Errorf("compaction-basic: no context capacity for %s; configure contextWindow on that adapter model", targetKey)
	}
	thresholdTokens := int(float64(target.ContextWindow) * e.cfg.ThresholdRatio)
	retainTokens := int(float64(target.ContextWindow) * e.cfg.RetainRatio)
	if e.cfg.RetainTokens > 0 {
		retainTokens = e.cfg.RetainTokens
	}
	if retainTokens >= thresholdTokens {
		return nil, fmt.Errorf("BasicCompactionConfig: %s retainTokens (%d) must be less than threshold tokens %d",
			targetKey, retainTokens, thresholdTokens)
	}
	if m.TotalTokens < thresholdTokens {
		return nil, nil
	}
	if e.cfg.Prune != nil {
		if _, err := PruneSession(sess, *e.cfg.Prune, EstimateMessage); err != nil {
			return nil, err
		}
		m = Measure(sess)
	}
	if m.TotalTokens < thresholdTokens {
		return nil, nil
	}
	var result *Result
	for attempt := 0; attempt <= e.cfg.CompactionRetries; attempt++ {
		r, err := selectCompactableRange(sess, m, retainTokens)
		if err != nil {
			return nil, err
		}
		if r == nil {
			if result == nil {
				return nil, nil
			}
			break
		}
		result, err = e.compactRegion(ctx, r.start, r.end, "current-turn")
		if err != nil {
			return result, err
		}
		m = Measure(sess)
		if m.TotalTokens < thresholdTokens {
			return result, nil
		}
	}
	return result, fmt.Errorf("compaction still above threshold after %d compaction attempts (%d estimated tokens >= threshold %d)",
		e.cfg.CompactionRetries+1, m.TotalTokens, thresholdTokens)
}

// CompactNow 是 /compact 手动面（compactNow）：idle（无 open turn）+
// retainTokens=0 选区 + standalone bracket。
func (e *Engine) CompactNow(ctx context.Context, sourceCommandID string) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess := e.host.Session()
	st := inspectEntryState(sess)
	if err := assertCompactionInactive(st, "compaction"); err != nil {
		return nil, err
	}
	if st.openTurn != nil {
		return nil, &ManualCompactionError{Code: "busy",
			Msg: "manual compaction requires an idle agent with no waking queued work"}
	}
	m := Measure(sess)
	r, err := selectCompactableRange(sess, m, 0)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return e.compactRegion(ctx, r.start, r.end, "manual", sourceCommandID)
}

// ---- 事务（compactSurfaceRegion :433+ 逐段） ----

// Result 是成功落地的压缩结果（CompactionResult 收缩）。
type Result struct {
	CompactionID    string             `json:"compactionId"`
	SourceCommandID string             `json:"sourceCommandId,omitempty"`
	StartSeq        int                `json:"startSeq"`
	SummarySeq      int                `json:"summarySeq"`
	EndSeq          int                `json:"endSeq"`
	Summary         []llm.ContentBlock `json:"summary"`
	ShadowedRange   struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"shadowedRange"`
	ShadowedSeqs       []int `json:"shadowedSeqs"`
	ShadowedTokenCount int   `json:"shadowedTokenCount"`
}

func (e *Engine) compactRegion(ctx context.Context, start, end int, owner string, sourceCommandID ...string) (*Result, error) {
	sess := e.host.Session()
	cmdID := ""
	if len(sourceCommandID) > 0 {
		cmdID = sourceCommandID[0]
	}

	selection, err := validateSurfaceRegion(sess, start, end)
	if err != nil {
		return nil, err
	}
	st := inspectEntryState(sess)
	if err := assertCompactionInactive(st, "compaction"); err != nil {
		return nil, err
	}
	var turnPtr *int
	if owner == "manual" {
		if st.openTurn != nil {
			return nil, &ManualCompactionError{Code: "busy",
				Msg: "manual compaction: the session already has an open turn"}
		}
	} else {
		if st.openTurn == nil {
			return nil, fmt.Errorf("compactRegion: no open turn — automatic compaction events must be enclosed in a turn")
		}
		turnPtr = st.openTurn
	}

	compactionID := session.NewUUID()
	lifecycle := map[string]any{"compactionId": compactionID, "turn": turnPtr}
	if cmdID != "" {
		lifecycle["sourceCommandId"] = cmdID
	}
	startEvent, err := sess.Append(session.EventCompactionStart, lifecycle, nil, nil)
	if err != nil {
		return nil, err
	}

	preNodes := Measure(sess).Nodes // prepare 快照（稳定性复检基准）
	summarized, smErr := e.summarizeCompaction(ctx, compactionID, cmdID, selection)
	if summarized != nil {
		summarized.nodes = preNodes
	}
	if smErr == nil {
		// 稳定性复检（自动=whole-surface）。
		if !nodesEqual(Measure(sess).Nodes, preparedNodes(summarized)) {
			smErr = &ManualCompactionError{Code: "changed",
				Msg: "compaction: session surface changed during summarization"}
		}
	}
	if smErr != nil {
		// 每个失败恰一次 end 尝试（失败可故意留孤儿 start）。
		_, _ = sess.Append(session.EventCompactionEnd, withError(lifecycle, smErr), nil, nil)
		if owner == "manual" {
			return nil, classifyManual(smErr)
		}
		return nil, smErr
	}

	// commit：summary + replace 同步紧邻。
	prepared := summarized
	summaryEvent, err := sess.Append(session.EventCompactionSummary, prepared.summaryData, nil, nil)
	if err != nil {
		_, _ = sess.Append(session.EventCompactionEnd, withError(lifecycle, err), nil, nil)
		return nil, e.failClose(owner, "commit", err)
	}
	refs := append([]int{startEvent.Seq, summaryEvent.Seq}, prepared.shadowedSeqs...)
	if _, err := sess.Append(session.EventUserMessage, prepared.checkpointMessage,
		session.ReplaceOp(start, end), refs); err != nil {
		return nil, e.failClose(owner, "commit", err)
	}
	endEvent, err := sess.Append(session.EventCompactionEnd, lifecycle, nil, nil)
	if err != nil {
		return nil, e.failClose(owner, "commit", err)
	}

	res := &Result{
		CompactionID: compactionID, SourceCommandID: cmdID,
		StartSeq: startEvent.Seq, SummarySeq: summaryEvent.Seq, EndSeq: endEvent.Seq,
		Summary: prepared.summaryBlocks, ShadowedSeqs: prepared.shadowedSeqs,
		ShadowedTokenCount: prepared.shadowedTokenCount,
	}
	res.ShadowedRange.Start = start
	res.ShadowedRange.End = end
	return res, nil
}

func (e *Engine) failClose(owner, stage string, err error) error {
	_, _ = e.host.Session().Append(session.EventCompactionEnd, map[string]any{
		"error": map[string]any{"message": err.Error()},
	}, nil, nil)
	if owner == "manual" {
		return &ManualCompactionError{Code: stage, Msg: "manual compaction did not commit cleanly", Err: err}
	}
	return err
}

func classifyManual(err error) error {
	var mc *ManualCompactionError
	if errors.As(err, &mc) && mc.Code == "changed" {
		return &ManualCompactionError{Code: "changed",
			Msg: "the compacted history changed before it could be replaced. The conversation is unchanged; the attempt is recorded in the session log.",
			Err: err}
	}
	return &ManualCompactionError{Code: "summary",
		Msg: "manual compaction could not produce a smaller summary", Err: err}
}

func withError(lifecycle map[string]any, err error) map[string]any {
	out := map[string]any{}
	for k, v := range lifecycle {
		out[k] = v
	}
	out["error"] = map[string]any{"message": err.Error()}
	return out
}

// ---- 摘要（summarizeWithLlm + frameSummary + shrink 校验） ----

type summarizedState struct {
	summaryData        map[string]any
	checkpointMessage  llm.Message
	summaryBlocks      []llm.ContentBlock
	shadowedSeqs       []int
	shadowedTokenCount int
	nodes              []MeasureNode // prepare 时的 whole-surface 价格快照
}

func preparedNodes(s *summarizedState) []MeasureNode { return s.nodes }
func nodesEqual(a, b []MeasureNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildSummarizationInput：重放 KV-cache 前缀（system head + 区内消息）。
func buildSummarizationInput(sess *session.Session, shadowedSeqs []int, tools []llm.ToolDefinition) ([]llm.Message, []llm.ToolDefinition) {
	var messages []llm.Message
	if headSeq, ok := systemHeadSeq(sess); ok {
		if head := sess.EventAt(headSeq); head != nil {
			if m, err := session.DeriveEventMessage(head); err == nil && m != nil {
				messages = append(messages, *m)
			}
		}
	}
	for _, seq := range shadowedSeqs {
		e := sess.EventAt(seq)
		if e == nil {
			continue
		}
		if m, err := session.DeriveEventMessage(e); err == nil && m != nil {
			messages = append(messages, *m)
		}
	}
	return messages, tools
}

func (e *Engine) summarizeCompaction(ctx context.Context, compactionID, cmdID string, sel *region) (*summarizedState, error) {
	sess := e.host.Session()
	target := e.host.Target()
	if target.Provider == "" || target.Model == "" {
		return nil, fmt.Errorf("no provider/model available for summarization: set both BasicCompactionConfig summarization fields, route one request, or set both AgentOptions fields")
	}
	messages, tools := buildSummarizationInput(sess, sel.shadowedSeqs, e.host.ToolSchemas())
	// 指令作为最后一条 user message（source kind=plugin）——保持前缀是原请求前缀。
	messages = append(messages, llm.Message{
		ID: session.NewMessageID(), Role: llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: CompactionInstruction}},
		Source:  llm.MessageSource{Kind: "plugin", Plugin: "dsh-compaction-basic"},
	})

	blocks, usage, finish, failure, err := e.host.StreamSummary(ctx,
		target.Provider, target.Model, messages, tools, e.cfg.MaxTokens)
	if err != nil {
		return nil, err
	}
	switch finish {
	case llm.FinishError, llm.FinishAborted:
		msg := "summarization failed"
		code := ""
		if failure != nil {
			msg, code = failure.Message, string(failure.Code)
		}
		return nil, &llmSummaryError{Msg: msg, Code: code}
	case llm.FinishMaxTokens:
		return nil, &llmSummaryError{Msg: "summarization truncated at the token cap (incomplete checkpoint)", Code: "MAX_TOKENS"}
	}
	// summaryText：含 image → UNSUPPORTED_CONTENT；只保留 text。
	var summary []llm.ContentBlock
	for _, b := range blocks {
		if b.Type == "image" {
			return nil, fmt.Errorf("compaction summary cannot contain image output")
		}
		if b.Type == "text" {
			summary = append(summary, b)
		}
	}
	hasText := false
	for _, b := range summary {
		if len(b.Text) > 0 && string([]rune(b.Text)) != "" && trimSpace(b.Text) != "" {
			hasText = true
		}
	}
	if !hasText {
		return nil, fmt.Errorf("summarization produced no text summary content")
	}

	// frameSummary + checkpoint source {kind:'plugin', plugin:'compact', compactionId}。
	framed := []llm.ContentBlock{
		{Type: "text", Text: CheckpointPreamble + "\n\n" + SummaryOpenTag},
	}
	framed = append(framed, summary...)
	framed = append(framed, llm.ContentBlock{Type: "text", Text: SummaryCloseTag})
	checkpoint := llm.Message{
		ID: session.NewMessageID(), Role: llm.RoleUser, Content: framed,
		Source: llm.MessageSource{Kind: "plugin", Plugin: "compact",
			CompactionID: compactionID, SourceCommandID: cmdID},
	}

	// shrink 校验：包装后必须严格小于被影子的 route 价。
	preMeasure := Measure(sess)
	shadowedSet := map[int]bool{}
	for _, seq := range sel.shadowedSeqs {
		shadowedSet[seq] = true
	}
	shadowedTotal := 0
	for _, n := range preMeasure.Nodes {
		if shadowedSet[n.Seq] {
			shadowedTotal += n.Tokens
		}
	}
	// lidsh：route 价 == 启发价（无 durable header baseline 面）。
	shadowedHeuristic := shadowedTotal
	framedTokens := EstimateMessage(&checkpoint)
	if framedTokens >= shadowedHeuristic {
		return nil, fmt.Errorf("summary is not smaller than the shadowed content (%d estimated framed tokens >= %d)",
			framedTokens, shadowedHeuristic)
	}

	summaryData := map[string]any{
		"compactionId":       compactionID,
		"summary":            summary,
		"llmStreamCall":      true,
		"shadowedRange":      map[string]any{"start": sel.start, "end": sel.end},
		"shadowedSeqs":       sel.shadowedSeqs,
		"shadowedTokenCount": shadowedHeuristic,
		"provider":           target.Provider,
		"model":              target.Model,
		"maxTokens":          e.cfg.MaxTokens,
	}
	if cmdID != "" {
		summaryData["sourceCommandId"] = cmdID
	}
	if usage != nil {
		summaryData["usage"] = usage
	}
	return &summarizedState{
		summaryData:        summaryData,
		checkpointMessage:  checkpoint,
		summaryBlocks:      summary,
		shadowedSeqs:       sel.shadowedSeqs,
		shadowedTokenCount: shadowedHeuristic,
	}, nil
}

type llmSummaryError struct {
	Msg, Code string
}

func (e *llmSummaryError) Error() string { return e.Msg }

func trimSpace(s string) string {
	out := []rune(s)
	for len(out) > 0 && isSpaceR(out[0]) {
		out = out[1:]
	}
	for len(out) > 0 && isSpaceR(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return string(out)
}

func isSpaceR(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
