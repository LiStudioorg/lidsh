package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lidsh/internal/llm"
)

func TestProjectKey(t *testing.T) {
	cases := map[string]string{
		"/root/work/lidsh": "--root-work-lidsh--", // 实测目录名吻合
		"/a//b":            "--a-b--",             // 分隔符折叠
		`C:\Users\x`:       "--C-Users-x--",
		"":                 "_no-cwd",
		"/中文":              "--~4E2D~6587--",
		"/":                "----", // 折叠后空 → "root"？实际是 "--root--"？见下断言
	}
	for in, want := range cases {
		if in == "/" {
			continue
		}
		if got := ProjectKey(in); got != want {
			t.Errorf("ProjectKey(%q) = %q, want %q", in, got, want)
		}
	}
	// "/" → 折叠成 "-" → 去首部 "-" → 空 → "root"
	if got := ProjectKey("/"); got != "--root--" {
		t.Errorf(`ProjectKey("/") = %q, want --root--`, got)
	}
	// 截断 251
	long := "/" + strings.Repeat("a", 300)
	if got := ProjectKey(long); len(got) != 2+251+2 {
		t.Errorf("truncation: len=%d", len(got))
	}
}

func TestEncodeSegmentInjective(t *testing.T) {
	if got := EncodeSegment(".."); got != "~002E~002E" {
		t.Errorf(`encode("..") = %q`, got)
	}
	if got := EncodeSegment("session-abc"); got != "session-abc" {
		t.Errorf("safe id should pass through: %q", got)
	}
	// 路径穿越不逃逸
	evils := []string{"../../etc/passwd", "a/../../b", ".", ".."}
	for _, id := range evils {
		enc := EncodeSegment(id)
		if strings.Contains(enc, "/") || enc == "." || enc == ".." || strings.Contains(enc, "~002E~002E/") {
			t.Errorf("encode(%q)=%q leaks path structure", id, enc)
		}
	}
}

func TestLogFileName(t *testing.T) {
	if got := LogFileName(3, "zstd"); got != "session.v3.jsonl.zstd" {
		t.Errorf("got %q", got)
	}
	if got := LogFileName(0, "none"); got != "session.jsonl" {
		t.Errorf("got %q", got)
	}
}

func TestZstdFramesAppendable(t *testing.T) {
	f1 := CompressFrame([]byte("frame one\n"))
	f2 := CompressFrame([]byte("frame two\n"))
	joined := append(append([]byte{}, f1...), f2...)

	plain, consumed, err := DecodeAllPrefix(joined)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(joined) {
		t.Errorf("consumed %d of %d", consumed, len(joined))
	}
	if string(plain) != "frame one\nframe two\n" {
		t.Errorf("plain = %q", plain)
	}

	// 撕裂尾：截掉第二帧的后半 → 只解出第一帧，consumed 指回完整边界。
	torn := joined[:len(f1)+len(f2)/2]
	plain, consumed, err = DecodeAllPrefix(torn)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "frame one\n" {
		t.Errorf("torn decode plain = %q", plain)
	}
	if consumed != len(f1) {
		t.Errorf("safe boundary = %d, want %d", consumed, len(f1))
	}
}

func TestSessionAppendAndPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	cwd := "/tmp/demo project"
	id := NewID()

	s := New(NewHeader(id, cwd, Meta{AgentPreset: "standard"}))

	// log-only 事件禁止 surfaceOp；surface 事件必须带 surfaceOp——两条校验。
	if _, err := s.Append(EventTurnStart, TurnStartData{Turn: 1}, AppendOp(), nil); err == nil {
		t.Error("log-only event with surfaceOp must fail")
	}
	if _, err := s.Append(EventUserMessage, userMsg("hi"), nil, nil); err == nil {
		t.Error("surface event without surfaceOp must fail")
	}

	if _, err := s.Append(EventTurnStart, TurnStartData{Turn: 1}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(EventUserMessage, userMsg("hello"), AppendOp(), nil); err != nil {
		t.Fatal(err)
	}
	empty := llm.Message{ID: NewMessageID(), Role: llm.RoleSystem, Content: nil,
		Source: llm.MessageSource{Kind: "plugin", Plugin: "p"}}
	if _, err := s.Append(EventSystemMessage, SystemMessageData{Turn: 1, Message: &empty}, AppendOp(), nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 3 {
		t.Fatalf("len = %d", got)
	}
	if s.log[0].Seq != 0 || s.log[2].Seq != 2 {
		t.Error("seq must be contiguous from 0")
	}
	// 空 content 的 system 消息不入 surface（deriveEventMessage → nil）。
	if len(s.Surface()) != 1 {
		t.Errorf("surface = %d nodes, want 1 (empty system message excluded)", len(s.Surface()))
	}

	// 落盘：header 一帧 + 事件一帧。
	logPath := filepath.Join(SessionDir(root, cwd, id), LogFileName(FormatVersion, "zstd"))
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := OpenLogWriter(logPath, "zstd", 0)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := json.Marshal(s.Header)
	if err := w.Append([][]byte{hb}); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.log {
		if err := w.AppendEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	// 重新加载并核对。
	h2, events2, safe, err := LoadLog(logPath, "zstd")
	if err != nil {
		t.Fatal(err)
	}
	if h2.ID != id || h2.CWD != cwd || h2.Version != FormatVersion {
		t.Errorf("header roundtrip = %+v", h2)
	}
	if len(events2) != 3 || safe != fileSize(t, logPath) {
		t.Errorf("events=%d safe=%d size=%d", len(events2), safe, fileSize(t, logPath))
	}
	if events2[1].Type != EventUserMessage || string(events2[1].SurfaceOp) != `"append"` {
		t.Errorf("event 1 = %+v", events2[1])
	}

	// Restore + 投影。
	s2, err := Restore(h2, events2)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s2.DeriveMessages()
	if len(msgs) != 1 || msgs[0].Role != llm.RoleUser {
		t.Errorf("derived messages = %+v", msgs)
	}
}

func TestUnknownEventFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.v3.jsonl.zstd")
	header, _ := json.Marshal(NewHeader("s1", "/x", Meta{}))
	known, _ := json.Marshal(Event{Type: EventTurnStart, Seq: 0, Time: 1, Data: json.RawMessage(`{"turn":1}`)})
	unknown, _ := json.Marshal(Event{Type: "alien/event", Seq: 1, Time: 2, Data: json.RawMessage(`{}`)})
	ignorable, _ := json.Marshal(Event{Type: "alien/other", Seq: 2, Time: 3, Data: json.RawMessage(`{}`), Ignorable: boolp(true)})

	w, _ := OpenLogWriter(path, "zstd", 0)
	w.Append([][]byte{header, known, unknown})
	w.Close()
	if _, _, _, err := LoadLog(path, "zstd"); err == nil {
		t.Error("unknown non-ignorable event must reject the log (fail-closed)")
	} else if !strings.Contains(err.Error(), "not ignorable") {
		t.Errorf("err = %v", err)
	}

	w, _ = OpenLogWriter(path, "zstd", 0)
	os.Remove(path)
	w, _ = OpenLogWriter(path, "zstd", 0)
	w.Append([][]byte{header, known, ignorable})
	w.Close()
	_, events, _, err := LoadLog(path, "zstd")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("ignorable unknown should be skipped, got %d events", len(events))
	}
}

func TestRetiredHeaderFieldsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	data := `{"type":"session","version":3,"id":"x","createdAt":1,"isSeeded":false,"delegationDepth":0,"approvalPolicy":"ask"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadLog(path, "none"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Errorf("retired field must be rejected, got %v", err)
	}
}

func TestTornTailRecoveryPlain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	header, _ := json.Marshal(NewHeader("s1", "/x", Meta{}))
	ev, _ := json.Marshal(Event{Type: EventTurnStart, Seq: 0, Time: 1, Data: json.RawMessage(`{"turn":1}`)})
	// 最后一行无换行（撕裂尾）
	data := string(header) + "\n" + string(ev) + "\n" + `{"type":"turn/en`
	os.WriteFile(path, []byte(data), 0o644)

	_, events, safe, err := LoadLog(path, "none")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("events = %d", len(events))
	}
	if int(safe) != len(header)+1+len(ev)+1 {
		t.Errorf("safe offset %d should stop after the complete line", safe)
	}

	// 截尾后可继续追加。
	w, err := OpenLogWriter(path, "none", safe)
	if err != nil {
		t.Fatal(err)
	}
	ev2, _ := json.Marshal(Event{Type: EventTurnEnd, Seq: 1, Time: 2,
		Data: json.RawMessage(`{"turn":1,"reason":{"kind":"completed"}}`)})
	w.Append([][]byte{ev2})
	w.Close()
	_, events, _, err = LoadLog(path, "none")
	if err != nil || len(events) != 2 {
		t.Errorf("after repair-append: %v events=%d", err, len(events))
	}
}

func TestSurfaceReplace(t *testing.T) {
	// compaction 的核心动作：一条 replace 事件遮蔽旧区间，原事件留在日志里。
	s := New(NewHeader("s", "/x", Meta{}))
	u := func(text string) llm.Message { return userMsg(text) }
	seqs := map[string]int{}
	for _, txt := range []string{"old1", "old2", "old3"} {
		e, err := s.Append(EventUserMessage, u(txt), AppendOp(), nil)
		if err != nil {
			t.Fatal(err)
		}
		seqs[txt] = e.Seq
	}
	// replace [old1..old2] → 摘要节点
	summary := llm.Message{ID: NewMessageID(), Role: llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: "SUMMARY"}},
		Source:  llm.MessageSource{Kind: "plugin", Plugin: "compaction", Form: "snapshot"}}
	_, err := s.Append(EventUserMessage, summary,
		ReplaceOp(seqs["old1"], seqs["old2"]), []int{seqs["old1"], seqs["old2"]})
	if err != nil {
		t.Fatal(err)
	}
	nodes := s.Surface()
	if len(nodes) != 2 {
		t.Fatalf("surface = %d nodes, want 2 (SUMMARY + old3)", len(nodes))
	}
	if nodes[0].Message.Content[0].Text != "SUMMARY" || nodes[1].Message.Content[0].Text != "old3" {
		t.Errorf("surface = %+v", nodes)
	}
	if s.Len() != 4 {
		t.Errorf("log must keep all 4 events (append-only)")
	}

	// replace 端点不在面上 → validateNext 拒绝
	if _, err := s.Append(EventUserMessage, u("x"), ReplaceOp(999, 999), nil); err == nil {
		t.Error("replace with off-surface endpoint must be rejected")
	}
}

func TestInterruptedTurnClosers(t *testing.T) {
	s := New(NewHeader("s", "/x", Meta{}))
	s.Append(EventTurnStart, TurnStartData{Turn: 1}, nil, nil)
	s.Append(EventStepStart, StepData{Turn: 1, Step: 1}, nil, nil)

	assistant := llm.Message{ID: NewMessageID(), Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			{Type: "tool-call", ID: "call_a", Name: "bash", Arguments: `{}`},
			{Type: "tool-call", ID: "call_b", Name: "read", Arguments: `{}`},
		},
		Source: llm.MessageSource{Kind: "model", Provider: "p", Model: "m"}}
	s.Append(EventAssistantMessage, AssistantMessageData{Turn: 1, Step: 1, Message: &assistant}, AppendOp(), nil)
	// call_a 有 tool/call（已开始但未销账）；call_b 连 tool/call 都没有。
	s.Append(EventToolCall, ToolCallData{Turn: 1, Step: 1, CallID: "call_a", Name: "bash", Arguments: `{}`}, nil, nil)

	closers := InterruptedTurnClosers(s.log)
	// 期望：tool/result(call_a, OUTCOME_UNKNOWN) → tool/result(call_b, NOT_STARTED) → step/end → turn/end
	if len(closers) != 4 {
		t.Fatalf("closers = %d: %+v", len(closers), closers)
	}
	var d0 ToolResultData
	json.Unmarshal(closers[0].Data, &d0)
	if d0.Error.Code != CodeToolOutcomeUnknown {
		t.Errorf("call_a code = %v", d0.Error.Code)
	}
	if string(closers[0].SurfaceOp) != `"append"` || len(closers[0].SourceEventSeqs) != 1 {
		t.Errorf("synthesized result must be a surface event pointing at its call: %+v", closers[0])
	}
	var d1 ToolResultData
	json.Unmarshal(closers[1].Data, &d1)
	if d1.Error.Code != CodeToolNotStarted {
		t.Errorf("call_b code = %v", d1.Error.Code)
	}
	if closers[2].Type != EventStepEnd || closers[3].Type != EventTurnEnd {
		t.Errorf("closers[2..3] = %s %s", closers[2].Type, closers[3].Type)
	}
	var re TurnEndData
	json.Unmarshal(closers[3].Data, &re)
	if re.Reason.Kind != "interrupted" {
		t.Errorf("turn end reason = %v", re.Reason.Kind)
	}
	// seq 续接、time 复用
	base := s.Len()
	lastTime := s.log[len(s.log)-1].Time
	for i, c := range closers {
		if c.Seq != base+i {
			t.Errorf("closer %d seq = %d, want %d", i, c.Seq, base+i)
		}
		if c.Time != lastTime {
			t.Errorf("closer %d must reuse last event time", i)
		}
	}

	// 平衡的日志不产生任何收尾事件。
	if got := InterruptedTurnClosers(append(append([]*Event{}, s.log...), closers...)); len(got) != 0 {
		t.Errorf("balanced log should produce no closers, got %d", len(got))
	}
}

func TestLeaseExclusive(t *testing.T) {
	dir := t.TempDir()
	l1, err := AcquireLease(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLease(dir); err == nil {
		t.Error("second lease must be refused")
	}
	l1.Release()
	l2, err := AcquireLease(dir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	l2.Release()
	// 锁文件不删除（稳定 inode 约定）
	if _, err := os.Stat(filepath.Join(dir, "session.lock")); err != nil {
		t.Error("lease file must survive release")
	}
}

func boolp(b bool) *bool { return &b }

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func userMsg(text string) llm.Message {
	return llm.Message{
		ID:      NewMessageID(),
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: text}},
		Source:  llm.MessageSource{Kind: "user"},
	}
}

func TestStreamAccumulatorRoundTrip(t *testing.T) {
	acc := NewAccumulator()
	seq := []llm.Chunk{
		{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockReasoning},
		{Type: llm.ChunkReasoningDelta, BlockIndex: 0, Delta: "Let"},
		{Type: llm.ChunkReasoningDelta, BlockIndex: 0, Delta: "'s go"},
		{Type: llm.ChunkBlockStart, BlockIndex: 1, BlockType: llm.BlockText},
		{Type: llm.ChunkTextDelta, BlockIndex: 1, Delta: "hello"},
		{Type: llm.ChunkToolCallDelta, BlockIndex: 2, ToolCallID: "c1", ToolName: "bash", ArgumentsDelta: `{"a"`},
		{Type: llm.ChunkToolCallDelta, BlockIndex: 2, ArgumentsDelta: `:1}`},
		{Type: llm.ChunkUsage, Usage: &llm.TokenUsage{InputTokens: 1, OutputTokens: 2}},
		{Type: llm.ChunkFinish, Finish: llm.FinishStop},
	}
	for i, c := range seq {
		acc.Push(int64(1000+i*10), c)
	}
	items := acc.Items()
	// 3 个 delta run（reasoning 合并 2、text 单、tool-call 合并 2）+ block-start×3 + usage + finish
	var runs, raws int
	for _, it := range items {
		if it.Type == "chunk" {
			raws++
		} else {
			runs++
		}
	}
	// raw chunk = 2 个显式 block-start + usage + finish（tool-call 测试数据未带
	// 显式 block-start，首 delta 直接开 run，与 translator 行为一致）。
	if runs != 3 || raws != 4 {
		t.Errorf("packed: runs=%d raws=%d items=%+v", runs, raws, items)
	}
	for _, it := range items {
		if it.Type != "chunk" && len(it.DT) != len(it.Texts)+len(it.Args) {
			t.Errorf("dt alignment broken: %+v", it)
		}
	}

	// 展开后与原始 delta 序列逐一对应。
	expanded := ExpandAssistantStream(items)
	var gotDeltas []string
	for _, c := range expanded {
		switch c.Type {
		case llm.ChunkTextDelta, llm.ChunkReasoningDelta:
			gotDeltas = append(gotDeltas, c.Delta)
		case llm.ChunkToolCallDelta:
			gotDeltas = append(gotDeltas, c.ArgumentsDelta)
		}
	}
	want := []string{"Let", "'s go", "hello", `{"a"`, `:1}`}
	if strings.Join(gotDeltas, "|") != strings.Join(want, "|") {
		t.Errorf("roundtrip deltas = %v, want %v", gotDeltas, want)
	}
}
