package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func adapter() *OpenAIAdapter {
	return NewOpenAI(OpenAIConfig{
		Provider:         "deepseek-official",
		APIKey:           "sk-test",
		SupportsThinking: true,
		DefaultEffort:    EffortHigh,
		Models:           []Model{{ID: "deepseek-flash", ContextWindow: 1_000_000, MaxTokens: 256_000}},
	})
}

func TestThinkingMapping(t *testing.T) {
	a := adapter()
	type thinkingBody struct {
		Thinking *struct {
			Type string `json:"type"`
		} `json:"thinking"`
		ReasoningEffort string `json:"reasoning_effort"`
		Stream          bool   `json:"stream"`
		StreamOptions   *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	build := func(opts GenerateOptions) thinkingBody {
		body, err := a.BuildRequest(opts, "deepseek-flash")
		if err != nil {
			t.Fatal(err)
		}
		var b thinkingBody
		if err := json.Unmarshal(body, &b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	// effort 未指定 → profile 默认 high → thinking enabled + reasoning_effort=high
	b := build(GenerateOptions{})
	if b.Thinking == nil || b.Thinking.Type != "enabled" || b.ReasoningEffort != "high" {
		t.Errorf("default effort: %+v", b)
	}
	if !b.Stream || b.StreamOptions == nil || !b.StreamOptions.IncludeUsage {
		t.Errorf("stream/stream_options missing: %+v", b)
	}

	// off → disabled 且不发 reasoning_effort
	b = build(GenerateOptions{Reasoning: EffortOff})
	if b.Thinking == nil || b.Thinking.Type != "disabled" || b.ReasoningEffort != "" {
		t.Errorf("off: %+v", b)
	}

	// session-title 用途强制 disabled（即使 effort=high）
	b = build(GenerateOptions{Reasoning: EffortHigh, Purpose: PurposeTitle})
	if b.Thinking == nil || b.Thinking.Type != "disabled" || b.ReasoningEffort != "" {
		t.Errorf("title purpose: %+v", b)
	}
}

func TestSerializeMessages(t *testing.T) {
	a := adapter()
	opts := GenerateOptions{
		System: "sys",
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "hi"}}},
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: "reasoning", Text: "think"},
				{Type: "text", Text: "answer"},
				{Type: "tool-call", ID: "call_1", Name: "bash", Arguments: `{"command":"ls"}`},
			}},
			{Role: RoleTool, Content: []ContentBlock{
				{Type: "tool-result", ToolCallID: "call_1", Content: []ContentBlock{{Type: "text", Text: "out"}}},
			}},
			{Role: RoleTool, Content: []ContentBlock{
				{Type: "tool-result", ToolCallID: "call_2", Content: nil},
			}},
		},
	}
	body, err := a.BuildRequest(opts, "m")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Role             string          `json:"role"`
			Content          json.RawMessage `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        []wireCall      `json:"tool_calls"`
			ToolCallID       string          `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 5 {
		t.Fatalf("messages = %d, want 5 (system,user,assistant,tool,tool)", len(req.Messages))
	}
	as := req.Messages[2]
	if as.ReasoningContent != "think" {
		t.Errorf("assistant must replay reasoning_content, got %q", as.ReasoningContent)
	}
	if len(as.ToolCalls) != 1 || as.ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Errorf("tool_calls = %+v", as.ToolCalls)
	}
	if string(as.Content) != `"answer"` {
		t.Errorf("assistant content = %s", as.Content)
	}
	tool := req.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || string(tool.Content) != `"out"` {
		t.Errorf("tool message = %+v", tool)
	}
	empty := req.Messages[4]
	if string(empty.Content) != `"(no output)"` {
		t.Errorf("empty tool result should serialize as \"(no output)\", got %s", empty.Content)
	}
}

// ---------- translator ----------

func strp(s string) *string { return &s }

func wire(content, reasoning, args, id, name *string, toolIdx int, finish *string) *wireChunk {
	var wc wireChunk
	wc.Choices = append(wc.Choices, struct {
		Delta struct {
			Role             string     `json:"role"`
			Content          *string    `json:"content"`
			ReasoningContent *string    `json:"reasoning_content"`
			ToolCalls        []wireCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}{})
	c := &wc.Choices[0]
	c.Delta.Content = content
	c.Delta.ReasoningContent = reasoning
	c.FinishReason = finish
	if args != nil || id != nil || name != nil {
		var call wireCall
		call.Index = toolIdx
		if id != nil {
			call.ID = *id
		}
		if name != nil {
			call.Function.Name = *name
		}
		if args != nil {
			call.Function.Arguments = *args
		}
		c.Delta.ToolCalls = []wireCall{call}
	}
	return &wc
}

func TestReasoningEmptyFirstFrameDoesNotOpenBlock(t *testing.T) {
	st := &translator{}
	chunks := st.push(wire(nil, strp(""), nil, nil, nil, 0, nil))
	if len(chunks) != 0 {
		t.Errorf("empty reasoning_content must not open a block, got %v", chunks)
	}
	chunks = st.push(wire(nil, strp("deep"), nil, nil, nil, 0, nil))
	if len(chunks) != 2 || chunks[0].Type != ChunkBlockStart || chunks[1].Type != ChunkReasoningDelta {
		t.Errorf("non-empty reasoning should open block: %+v", chunks)
	}
}

func TestToolCallAggregationByWireIndex(t *testing.T) {
	st := &translator{}
	// 首帧：index=0，带 id 和 name
	st.push(wire(nil, nil, strp(`{"a":`), strp("call_1"), strp("bash"), 0, nil))
	// 续帧：wire 重复空字符串 id/name → 视为不变
	st.push(wire(nil, nil, strp("1}"), strp(""), strp(""), 0, nil))
	// 第二个工具，wire index=1
	st.push(wire(nil, nil, strp("{}"), strp("call_2"), strp("read"), 1, nil))

	if st.nextIndex != 2 {
		t.Errorf("harness blocks = %d, want 2 (same wire index reuses block)", st.nextIndex)
	}
	b0 := st.blocks[0]
	if b0.id != "call_1" || b0.name != "bash" {
		t.Errorf("acceptIdentity violated: id=%q name=%q", b0.id, b0.name)
	}
	if b0.buf.String() != `{"a":1}` {
		t.Errorf("args not concatenated: %q", b0.buf.String())
	}
}

func TestFinishAndUsageDeferredWithEmptyResponseRule(t *testing.T) {
	st := &translator{}
	// wire 上的 finish/usage 不应即时产出任何 chunk
	out := st.push(&wireChunk{
		Choices: []struct {
			Delta struct {
				Role             string     `json:"role"`
				Content          *string    `json:"content"`
				ReasoningContent *string    `json:"reasoning_content"`
				ToolCalls        []wireCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		}{{FinishReason: strp("stop")}},
	})
	if len(out) != 0 {
		t.Errorf("finish must be deferred to [DONE], got %v", out)
	}
	// 零块 + stop → EMPTY_RESPONSE error
	final := st.finalize()
	last := final[len(final)-1]
	if last.Type != ChunkFinish || last.Finish != FinishError || last.Fail.Code != ErrEmptyResponse {
		t.Errorf("empty response rule: %+v", last)
	}
}

func TestFinishOrderBlockEndUsageFinish(t *testing.T) {
	st := &translator{}
	st.push(wire(strp("hel"), nil, nil, nil, nil, 0, nil))
	st.push(wire(strp("lo"), nil, nil, nil, nil, 0, strp("stop")))
	st.usage = &TokenUsage{InputTokens: 3, OutputTokens: 2}

	final := st.finalize()
	// 期望 [block-end, usage, finish]
	if len(final) != 3 {
		t.Fatalf("final chunks = %d: %+v", len(final), final)
	}
	if final[0].Type != ChunkBlockEnd || final[0].Block == nil || final[0].Block.Text != "hello" {
		t.Errorf("block-end must carry assembled block: %+v", final[0])
	}
	if final[1].Type != ChunkUsage || final[2].Type != ChunkFinish || final[2].Finish != FinishStop {
		t.Errorf("order must be block-end→usage→finish: %+v", final)
	}
}

func TestMapFinishReasons(t *testing.T) {
	cases := map[string]FinishReason{"stop": FinishStop, "tool_calls": FinishToolCalls, "length": FinishMaxTokens}
	for wire, want := range cases {
		if got := mapFinishReason(wire); got.Finish != want {
			t.Errorf("%s → %s, want %s", wire, got.Finish, want)
		}
	}
	got := mapFinishReason("content_filter")
	if got.Finish != FinishError || !strings.Contains(got.Fail.Message, "content_filter") {
		t.Errorf("unknown reason must become error: %+v", got)
	}
}

func TestMapUsageSubtractsCacheRead(t *testing.T) {
	hit := 70
	u := &struct {
		PromptTokens     int  `json:"prompt_tokens"`
		CompletionTokens int  `json:"completion_tokens"`
		TotalTokens      int  `json:"total_tokens"`
		PromptCacheHit   *int `json:"prompt_cache_hit_tokens"`
		PromptCacheMiss  *int `json:"prompt_cache_miss_tokens"`
		PromptDetails    *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}{PromptTokens: 100, CompletionTokens: 30, TotalTokens: 130, PromptCacheHit: &hit}

	got := mapUsage(u)
	if got.InputTokens != 30 {
		t.Errorf("inputTokens = %d, want 30 (prompt−cacheRead, DSH 不相交约定)", got.InputTokens)
	}
	if got.CacheReadTokens != 70 || got.OutputTokens != 30 || got.TotalTokens != 130 {
		t.Errorf("usage = %+v", got)
	}
}

func TestHTTPErrCodeMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   ErrorCode
	}{
		{401, `{"error":{"message":"bad key"}}`, ErrAuth},
		{403, `{}`, ErrAuth},
		{413, `{}`, ErrInvalidRequest},
		{429, `{"error":{"message":"rate limited"}}`, ErrRateLimit},
		{400, `{"error":{"message":"insufficient quota for team"}}`, ErrQuota},
		{400, `{"error":{"message":"this model's context length is exceeded"}}`, ErrContextWindowExceeded},
		{400, `{"error":{"message":"bad temperature"}}`, ErrInvalidRequest},
		{500, `oops`, ErrServer},
		{418, ``, ErrorCode("HTTP_418")},
	}
	for _, c := range cases {
		got, _ := httpErrorCode(c.status, []byte(c.body))
		if got != c.want {
			t.Errorf("status %d body %s → %s, want %s", c.status, c.body, got, c.want)
		}
	}
}
