package agent

import (
	"context"
	"strings"
	"testing"

	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// fakeAdapter 是可编程的流式适配器：queue 里的每项作为一个流返回。
type fakeAdapter struct {
	provider string
	model    string
	streams  [][]llm.Chunk // 每次 Stream 调用消费一个
	err      error
}

func (f *fakeAdapter) Stream(ctx context.Context, opts llm.GenerateOptions) (<-chan llm.Chunk, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		default:
		}
		var batch []llm.Chunk
		if len(f.streams) > 0 {
			batch = f.streams[0]
			f.streams = f.streams[1:]
		}
		for _, c := range batch {
			ch <- c
		}
	}()
	return ch, nil
}

func (f *fakeAdapter) Info() llm.ProviderInfo {
	return llm.ProviderInfo{Provider: f.provider}
}

func (f *fakeAdapter) ResolveModel(id string) (string, error) { return id, nil }

// 一个纯文本回答的流：block-start text → delta → block-end → usage → finish stop。
func textStream(text string) []llm.Chunk {
	return []llm.Chunk{
		{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockText},
		{Type: llm.ChunkTextDelta, BlockIndex: 0, Delta: text},
		{Type: llm.ChunkBlockEnd, BlockIndex: 0},
		{Type: llm.ChunkUsage, Usage: &llm.TokenUsage{InputTokens: 5, OutputTokens: 3}},
		{Type: llm.ChunkFinish, Finish: llm.FinishStop},
	}
}

func TestAgentSimpleTurn(t *testing.T) {
	s := session.New(session.NewHeader(session.NewID(), "/tmp", session.Meta{AgentPreset: "standard"}))
	reg := tools.NewRegistry()
	// 不注册工具：模型不调用工具。
	fa := &fakeAdapter{provider: "fake", model: "m", streams: [][]llm.Chunk{textStream("hello")}}
	a := New(Options{
		Sess: s, Resolver: NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools: reg, Provider: "fake", Model: "m", System: "you are a test agent", CWD: "/tmp",
	})

	res, err := a.Prompt("hi", "followup")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "completed" {
		t.Fatalf("result kind = %q, want completed (%+v)", res.Kind, res.Err)
	}

	// 事件序列应至少包含：system/message, turn/start, step/start,
	// assistant/message, step/end, turn/end。
	types := []string{}
	for _, e := range s.Log() {
		types = append(types, e.Type)
	}
	need := []string{
		session.EventSystemMessage,
		session.EventUserMessage,
		session.EventTurnStart,
		session.EventStepStart,
		session.EventAssistantMessage,
		session.EventStepEnd,
		session.EventTurnEnd,
	}
	for _, want := range need {
		found := false
		for _, got := range types {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("event %q missing from log: got %v", want, types)
		}
	}
}

// fakeTool 记录调用。
type recordingReg struct {
	*tools.Registry
	callID string
}

func TestAgentToolCall(t *testing.T) {
	s := session.New(session.NewHeader(session.NewID(), "/tmp", session.Meta{AgentPreset: "standard"}))
	reg := tools.NewRegistry()
	var gotCall bool
	_ = reg.Register(tools.Definition{
		Name:        "bash",
		Description: "run a command",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"}},
		Execute: func(args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
			gotCall = true
			return &tools.Result{Content: "output", IsError: false}, nil
		},
	})
	// 模型先调 bash，然后返回最终回答。
	toolCallStream := func() []llm.Chunk {
		return []llm.Chunk{
			{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockToolCall, ToolCallID: "c1", ToolName: "bash"},
			{Type: llm.ChunkToolCallDelta, BlockIndex: 0, ToolCallID: "c1", ToolName: "bash", ArgumentsDelta: `{"command":"echo hi"}`},
			{Type: llm.ChunkBlockEnd, BlockIndex: 0},
			{Type: llm.ChunkUsage, Usage: &llm.TokenUsage{InputTokens: 5, OutputTokens: 5}},
			{Type: llm.ChunkFinish, Finish: llm.FinishToolCalls},
		}
	}
	finalStream := textStream("done")
	fa := &fakeAdapter{provider: "fake", model: "m", streams: [][]llm.Chunk{toolCallStream(), finalStream}}
	a := New(Options{
		Sess: s, Resolver: NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools: reg, Provider: "fake", Model: "m", CWD: "/tmp",
	})

	res, err := a.Prompt("run it", "followup")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "completed" {
		t.Fatalf("result kind = %q, want completed (%+v)", res.Kind, res.Err)
	}
	if !gotCall {
		t.Error("bash tool was not executed")
	}

	// 日志应有 tool/call 与 tool/result。
	types := []string{}
	for _, e := range s.Log() {
		types = append(types, e.Type)
	}
	foundCall, foundResult := false, false
	for _, ty := range types {
		if ty == session.EventToolCall {
			foundCall = true
		}
		if ty == session.EventToolResult {
			foundResult = true
		}
	}
	if !foundCall || !foundResult {
		t.Errorf("want tool/call and tool/result in %v", types)
	}
	// tool/result 的 sourceEventSeqs 应指向 tool/call。
	for _, e := range s.Log() {
		if e.Type == session.EventToolResult && len(e.SourceEventSeqs) != 1 {
			t.Errorf("tool/result sourceEventSeqs = %v, want one ref", e.SourceEventSeqs)
		}
	}
}

func TestAgentFinalAnswerContent(t *testing.T) {
	s := session.New(session.NewHeader(session.NewID(), "/tmp", session.Meta{AgentPreset: "standard"}))
	reg := tools.NewRegistry()
	fa := &fakeAdapter{provider: "fake", model: "m", streams: [][]llm.Chunk{textStream("the answer is 42")}}
	a := New(Options{
		Sess: s, Resolver: NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools: reg, Provider: "fake", Model: "m", CWD: "/tmp",
	})
	if _, err := a.Prompt("what is 6*7?", "followup"); err != nil {
		t.Fatal(err)
	}
	// surface 最后一条 assistant 消息应与输入文本拼接。
	msgs := s.DeriveMessages()
	var last []string
	for _, m := range a.Sess.Log() {
		_ = m
	}
	if len(msgs) == 0 {
		t.Fatal("no messages derived")
	}
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			var sb strings.Builder
			for _, blk := range m.Content {
				if blk.Type == "text" {
					sb.WriteString(blk.Text)
				}
			}
			last = append(last, sb.String())
		}
	}
	if len(last) == 0 || !strings.Contains(last[len(last)-1], "42") {
		t.Errorf("final assistant text = %v, want contains 42", last)
	}
}
