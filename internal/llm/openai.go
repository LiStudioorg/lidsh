// OpenAI 兼容 chat-completions 适配器（DeepSeek 官方端点即此协议）。
//
// 行为逐条对照 dsh-llm-deepseek（详见 docs/_parts/llm-core.md 第 2 节）：
//   - POST {baseURL}/chat/completions，baseURL 默认 https://api.deepseek.com
//   - 请求体固定 stream:true + stream_options:{include_usage:true}
//   - thinking:{type} 与 reasoning_effort 是顶层字段（非 extra_body）
//   - tool_calls 按 wire index 聚合；id/name 只认首个非空值（”/null 视为不变）
//   - reasoning_content 首帧空串不开块
//   - finish 与 usage 全部推迟到 [DONE]：先 block-end，再 usage，最后 finish
//   - [DONE] 前 EOF → STREAM_CLOSED；stop 且零块 → EMPTY_RESPONSE
//   - usage：inputTokens = prompt_tokens − cacheRead（prompt_tokens 含缓存命中）
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAIConfig 是一个 OpenAI 兼容端点的连接配置。
type OpenAIConfig struct {
	// Provider 是路由名（如 deepseek-official）。
	Provider string
	// BaseURL 默认 https://api.deepseek.com。
	BaseURL string
	// APIKey 是明文 key（凭证解析在 settings/credentials 层完成）。
	APIKey string
	// Models 是模型目录。
	Models []Model
	// UserAgent 与归属头（DSH: product/version (+url)）。
	UserAgent string
	// HTTPClient 为空时用带合理超时的默认 client（流式不限制总时长）。
	HTTPClient *http.Client
	// IdleTimeout 是流内空闲看门狗（DSH 默认 300s）。
	IdleTimeout time.Duration
	// SessionIDHeader 非空时逐请求附带（DSH: x-deepseek-harness-session-id）。
	SessionIDHeader string
	// CompactHeader 非空时在 purpose=compaction 的请求上附带（DSH: 同名头值 "1"）。
	CompactHeader string
	// SupportsThinking 控制是否发送 thinking/reasoning_effort 顶层字段。
	// true=DeepSeek 形态；false=通用 OpenAI（只发 reasoning_effort 若设置）。
	SupportsThinking bool
	// DefaultEffort 是未指定档位时的默认（DeepSeek profile 默认 high）。
	DefaultEffort ReasoningEffort
}

// OpenAIAdapter 实现 Adapter。
type OpenAIAdapter struct {
	cfg OpenAIConfig
}

var _ Adapter = (*OpenAIAdapter)(nil)

// NewOpenAI 构造适配器并填默认值。
func NewOpenAI(cfg OpenAIConfig) *OpenAIAdapter {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com"
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			// 不设全局 Timeout：流式响应可以很长，靠 idle watchdog 兜底。
			Transport: http.DefaultTransport,
		}
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 300 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "lidsh/0.1.0"
	}
	if len(cfg.Models) == 0 && cfg.SupportsThinking {
		// DeepSeek 形态的默认目录（dsh-llm-deepseek DEFAULT_MODELS 的收缩面：
		// DEFAULT_CONTEXT_WINDOW = 1e6）。
		cfg.Models = []Model{
			{ID: "deepseek-chat", Name: "DeepSeek Chat", ContextWindow: 1_000_000, SupportsTools: true},
			{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", ContextWindow: 1_000_000, SupportsTools: true},
		}
	}
	return &OpenAIAdapter{cfg: cfg}
}

func (a *OpenAIAdapter) Info() ProviderInfo {
	return ProviderInfo{Provider: a.cfg.Provider, Models: a.cfg.Models}
}

func (a *OpenAIAdapter) ResolveModel(id string) (string, error) {
	for _, m := range a.cfg.Models {
		if m.ID == id {
			return m.ID, nil
		}
	}
	// DSH 的目录是 advisory，不校验请求（llm-core.md 1.1 listModels 行）：
	// 未知 id 原样透传，由服务端裁决。
	return id, nil
}

// ---------- wire 类型 ----------

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"` // string | []wirePart | null
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// DeepSeek thinking 模式的 tool-call 轮需要回放 reasoning_content。
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	File *struct {
		FileID string `json:"file_id"`
	} `json:"file,omitempty"`
}

type wireCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	Index int `json:"index"`
}

type wireRequest struct {
	Model         string        `json:"model"`
	Messages      []wireMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Thinking *struct {
		Type string `json:"type"`
	} `json:"thinking,omitempty"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	Tools           []wireTool `json:"tools,omitempty"`
	Temperature     *float64   `json:"temperature,omitempty"`
	MaxTokens       *int       `json:"max_tokens,omitempty"`
	Stop            []string   `json:"stop,omitempty"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Role             string     `json:"role"`
			Content          *string    `json:"content"`
			ReasoningContent *string    `json:"reasoning_content"`
			ToolCalls        []wireCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
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
	} `json:"usage"`
}

// ---------- 请求构造 ----------

// BuildRequest 把 GenerateOptions 序列化成 wire 请求体。导出供测试。
func (a *OpenAIAdapter) BuildRequest(opts GenerateOptions, model string) ([]byte, error) {
	req := wireRequest{
		Model:       model,
		Messages:    serializeMessages(opts),
		Stream:      true,
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		Stop:        opts.Stop,
	}
	req.StreamOptions = &struct {
		IncludeUsage bool `json:"include_usage"`
	}{IncludeUsage: true}

	for _, t := range opts.Tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		wt.Function.Parameters = t.InputSchema
		req.Tools = append(req.Tools, wt)
	}

	a.applyThinking(&req, opts)
	return json.Marshal(req)
}

// applyThinking 复刻 resolveThinking（dsh-llm-deepseek/lib/index.js:31-41）：
// purpose=session-title 强制 disabled；effort=off → thinking disabled 且不发
// reasoning_effort；low|high|max → thinking enabled + reasoning_effort；未指定
// → profile 默认（DeepSeek 默认 high）。
func (a *OpenAIAdapter) applyThinking(req *wireRequest, opts GenerateOptions) {
	effort := opts.Reasoning
	if effort == "" {
		effort = a.cfg.DefaultEffort
	}
	if opts.Purpose == PurposeTitle {
		if a.cfg.SupportsThinking {
			req.Thinking = &struct {
				Type string `json:"type"`
			}{Type: "disabled"}
		}
		return
	}
	switch effort {
	case EffortOff:
		if a.cfg.SupportsThinking {
			req.Thinking = &struct {
				Type string `json:"type"`
			}{Type: "disabled"}
		}
	case EffortLow, EffortHigh, EffortMax:
		if a.cfg.SupportsThinking {
			req.Thinking = &struct {
				Type string `json:"type"`
			}{Type: "enabled"}
		}
		req.ReasoningEffort = string(effort)
	case EffortMedium:
		// DeepSeek wire 只有 low|high|max；medium 归 high（保守取高档）。
		if a.cfg.SupportsThinking {
			req.Thinking = &struct {
				Type string `json:"type"`
			}{Type: "enabled"}
		}
		req.ReasoningEffort = string(EffortHigh)
	}
}

// serializeMessages 复刻 serializeMessages（index.js:134-162）：
// system→string；assistant 拼接 text 与 reasoning 回放 + tool_calls；
// 纯文本 user 折叠为 string；tool-result 是独立 {role:tool} 消息。
func serializeMessages(opts GenerateOptions) []wireMessage {
	var out []wireMessage
	if opts.System != "" {
		out = append(out, wireMessage{Role: "system", Content: opts.System})
	}
	for _, m := range opts.Messages {
		switch m.Role {
		case RoleTool:
			for _, b := range m.Content {
				if b.Type != "tool-result" {
					continue
				}
				text := flattenText(b.Content)
				if text == "" {
					text = "(no output)"
				}
				out = append(out, wireMessage{Role: "tool", ToolCallID: b.ToolCallID, Content: text})
			}
		case RoleAssistant:
			var text, reasoning strings.Builder
			var calls []wireCall
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					text.WriteString(b.Text)
				case "reasoning":
					reasoning.WriteString(b.Text)
				case "tool-call":
					calls = append(calls, wireCall{
						ID: b.ID, Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: b.Name, Arguments: b.Arguments},
					})
				}
			}
			wm := wireMessage{Role: "assistant", Content: text.String(), ToolCalls: calls}
			if reasoning.Len() > 0 {
				wm.ReasoningContent = reasoning.String()
			}
			// OpenAI 形态下 content 与 tool_calls 互斥时允许 content 为空串。
			out = append(out, wm)
		default: // user
			if plain, ok := plainUserText(m.Content); ok {
				out = append(out, wireMessage{Role: "user", Content: plain})
				continue
			}
			var parts []wirePart
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					parts = append(parts, wirePart{Type: "text", Text: b.Text})
				case "image":
					if b.Attachment != nil {
						p := wirePart{Type: "image_url"}
						p.ImageURL = &struct {
							URL string `json:"url"`
						}{URL: b.Attachment.AttachmentID} // 上层解析成 data: URL
						parts = append(parts, p)
					}
				}
			}
			out = append(out, wireMessage{Role: "user", Content: parts})
		}
	}
	return out
}

func plainUserText(blocks []ContentBlock) (string, bool) {
	if len(blocks) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" {
			return "", false
		}
		b.WriteString(blk.Text)
	}
	return b.String(), true
}

func flattenText(blocks []ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// ---------- 错误映射 ----------

// httpErrorCode 复刻 dsh-llm-deepseek/lib/index.js:1526-1542。
func httpErrorCode(status int, body []byte) (ErrorCode, string) {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	message := ""
	hay := strings.ToLower(string(body))
	if json.Unmarshal(body, &payload) == nil {
		message = payload.Error.Message
		hay = strings.ToLower(payload.Error.Message + " " + payload.Error.Type + " " + fmt.Sprint(payload.Error.Code))
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}

	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrAuth, message
	case status == http.StatusRequestEntityTooLarge:
		return ErrInvalidRequest, message
	case strings.Contains(hay, "quota"):
		return ErrQuota, message
	case status == http.StatusTooManyRequests:
		return ErrRateLimit, message
	case status == http.StatusBadRequest:
		if strings.Contains(hay, "context") && (strings.Contains(hay, "length") || strings.Contains(hay, "window") || strings.Contains(hay, "token")) {
			return ErrContextWindowExceeded, message
		}
		return ErrInvalidRequest, message
	case status >= 500:
		return ErrServer, message
	default:
		return ErrorCode("HTTP_" + strconv.Itoa(status)), message
	}
}

// 补充错误码（对齐 DSH 运行时观察到的码集）。
const (
	ErrAuth           ErrorCode = "AUTH"
	ErrInvalidRequest ErrorCode = "INVALID_REQUEST"
)

// parseRetryAfter 支持秒数与 HTTP-date 两种形式（index.js:1507-1515）。
func parseRetryAfter(v string) int64 {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return int64(secs) * 1000
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d.Milliseconds()
		}
	}
	return 0
}

// ---------- 流式实现 ----------

// Stream 发起请求并翻译 SSE 为 Chunk 流。返回的 channel 由本函数关闭。
func (a *OpenAIAdapter) Stream(ctx context.Context, opts GenerateOptions) (<-chan Chunk, error) {
	model, err := a.ResolveModel(opts.Model)
	if err != nil {
		return nil, err
	}
	if a.cfg.APIKey == "" {
		return nil, &Failure{Message: "missing API key credential", Code: ErrInvalidCredential}
	}
	body, err := a.BuildRequest(opts, model)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("user-agent", a.cfg.UserAgent)
	if a.cfg.SessionIDHeader != "" && opts.SessionID != "" {
		req.Header.Set(a.cfg.SessionIDHeader, opts.SessionID)
	}
	if a.cfg.CompactHeader != "" && opts.Purpose == PurposeCompaction {
		req.Header.Set(a.cfg.CompactHeader, "1")
	}

	resp, err := a.cfg.HTTPClient.Do(req)
	if err != nil {
		// ctx 取消与传输失败分流（index.js:1641-1645）。
		if ctx.Err() != nil {
			return nil, &Failure{Message: ctx.Err().Error(), Code: ErrAborted}
		}
		return nil, &Failure{Message: err.Error(), Code: ErrTransport}
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		requestID := responseRequestID(resp.Header)
		code, message := httpErrorCode(resp.StatusCode, bodyBytes)
		return nil, &Failure{
			Message:              message,
			Code:                 code,
			Status:               resp.StatusCode,
			ProviderRetryAfterMs: parseRetryAfter(resp.Header.Get("retry-after")),
			RequestID:            requestID,
		}
	}

	out := make(chan Chunk, 64)
	go a.pump(ctx, resp, out)
	return out, nil
}

func responseRequestID(h http.Header) string {
	if v := h.Get("x-request-id"); v != "" {
		return v
	}
	return h.Get("x-deepseek-request-id")
}

// pump 逐帧读 SSE 并产出 Chunk。翻译规则全部集中在这里，与 translate
// （index.js:1208-1319）逐条对应。
func (a *OpenAIAdapter) pump(ctx context.Context, resp *http.Response, out chan<- Chunk) {
	defer close(out)
	defer resp.Body.Close()

	send := func(c Chunk) bool {
		select {
		case out <- c:
			return true
		case <-ctx.Done():
			return false
		}
	}

	var st translator
	done := false
	idle := a.cfg.IdleTimeout
	if idle <= 0 {
		idle = 300 * time.Second
	}

	lines := make(chan sseLine)
	go scanSSE(resp.Body, lines, idle)

	for {
		var ev sseLine
		select {
		case <-ctx.Done():
			send(Chunk{Type: ChunkFinish, Finish: FinishAborted, Fail: &Failure{Message: ctx.Err().Error(), Code: ErrAborted}})
			return
		case ev = <-lines:
		}
		if ev.err != nil {
			code := ErrTransport
			msg := ev.err.Error()
			if ev.err == errStreamClosed {
				code = ErrStreamClosed
				msg = "SSE stream ended without [DONE]"
			} else if ev.err == errIdleTimeout {
				code = ErrTimeout
				msg = "stream idle timeout"
			}
			send(Chunk{Type: ChunkFinish, Finish: FinishError, Fail: &Failure{Message: msg, Code: code}})
			return
		}
		if ev.done {
			done = true
			break
		}
		if ev.data == "" {
			continue
		}
		var wc wireChunk
		if err := json.Unmarshal([]byte(ev.data), &wc); err != nil {
			send(Chunk{Type: ChunkFinish, Finish: FinishError,
				Fail: &Failure{Message: "malformed SSE payload: " + err.Error(), Code: ErrMalformed}})
			return
		}
		for _, c := range st.push(&wc) {
			if !send(c) {
				return
			}
		}
	}

	if !done {
		send(Chunk{Type: ChunkFinish, Finish: FinishError,
			Fail: &Failure{Message: "SSE stream ended without [DONE]", Code: ErrStreamClosed}})
		return
	}
	for _, c := range st.finalize() {
		if !send(c) {
			return
		}
	}
}

var (
	errStreamClosed = fmt.Errorf("stream closed")
	errIdleTimeout  = fmt.Errorf("idle timeout")
)

type sseLine struct {
	data string
	done bool
	err  error
}

// scanSSE 解析 event-stream 框架（data: 行、注释行、空行分隔、[DONE] 哨兵）。
// 与 eventsource-parser 的分工等价：这里只管帧，业务翻译在 translator。
// 注释帧只作为 transport 活动信号（喂 idle watchdog，index.js:1107 注释）。
func scanSSE(r io.Reader, out chan<- sseLine, idle time.Duration) {
	defer close(out)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)

	type scanResult struct {
		line string
		ok   bool
	}
	res := make(chan scanResult, 8)
	go func() {
		for sc.Scan() {
			res <- scanResult{line: sc.Text()}
		}
		res <- scanResult{ok: false}
	}()

	var data strings.Builder
	flush := func() (keepGoing bool) {
		if data.Len() == 0 {
			return true
		}
		payload := data.String()
		data.Reset()
		if payload == "[DONE]" {
			out <- sseLine{done: true}
			return false
		}
		out <- sseLine{data: payload}
		return true
	}

	for {
		select {
		case <-time.After(idle):
			out <- sseLine{err: errIdleTimeout}
			return
		case r := <-res:
			if !r.ok {
				// EOF：未到 [DONE]（无论是否还有未 flush 的 data）都是异常。
				out <- sseLine{err: errStreamClosed}
				return
			}
			line := r.line
			switch {
			case line == "":
				if !flush() {
					return
				}
			case strings.HasPrefix(line, ":"):
				// 注释帧：活动信号，无内容。
			case strings.HasPrefix(line, "data:"):
				v := strings.TrimPrefix(line, "data:")
				v = strings.TrimPrefix(v, " ")
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(v)
			default:
				// event:/id:/retry: 字段本适配器不需要。
			}
		}
	}
}

// ErrMalformed 是 SSE 载荷解析失败的规范码。
const ErrMalformed ErrorCode = "MALFORMED_RESPONSE"

// ---------- chunk 翻译 ----------

// translator 把 wire chunk 序列聚合为块流。规则（index.js:1208-1319）：
//   - 本适配器自配 index，按流顺序递增；
//   - tool_calls 按 **wire index** 复用/新建 harness 块；
//   - id/name 只认首个非空值，续帧的 ”/null 视为不变（acceptIdentity）；
//   - finish 与 usage 不即时发，push 只发块流事件，finish() 里统一收尾：
//     全部 block-end → usage → finish；
//   - stop/缺省 finish 且零块 → EMPTY_RESPONSE error。
type translator struct {
	blocks    []*tBlock
	nextIndex int
	usage     *TokenUsage
	finish    *Chunk
	byWire    map[int]*tBlock // wire tool_call index → 块
}

// tBlock 是流式聚合中的一个内容块。index 是 harness 块序（block-start/delta/end
// 用它关联）；id 对 tool-call 块是 wire 工具调用 id。
type tBlock struct {
	index int
	kind  BlockType
	id    string
	name  string
	buf   strings.Builder
}

func (t *translator) openBlock(kind BlockType) *tBlock {
	b := &tBlock{kind: kind, index: t.nextIndex}
	t.nextIndex++
	t.blocks = append(t.blocks, b)
	return b
}

func (t *translator) push(wc *wireChunk) []Chunk {
	var out []Chunk
	for _, choice := range wc.Choices {
		d := choice.Delta
		// reasoning_content：首帧空串不开块（types.d.ts:127-130）。
		if d.ReasoningContent != nil && *d.ReasoningContent != "" {
			b := t.extend(BlockReasoning)
			if b.wasNew {
				out = append(out, Chunk{Type: ChunkBlockStart, BlockIndex: b.block.index, BlockType: BlockReasoning})
			}
			b.block.buf.WriteString(*d.ReasoningContent)
			out = append(out, Chunk{Type: ChunkReasoningDelta, BlockIndex: b.block.index, Delta: *d.ReasoningContent})
		}
		if d.Content != nil && *d.Content != "" {
			b := t.extend(BlockText)
			if b.wasNew {
				out = append(out, Chunk{Type: ChunkBlockStart, BlockIndex: b.block.index, BlockType: BlockText})
			}
			b.block.buf.WriteString(*d.Content)
			out = append(out, Chunk{Type: ChunkTextDelta, BlockIndex: b.block.index, Delta: *d.Content})
		}
		// tool_calls 按 **wire index** 复用/新建 harness 块。
		for _, call := range d.ToolCalls {
			if t.byWire == nil {
				t.byWire = map[int]*tBlock{}
			}
			b, known := t.byWire[call.Index]
			if !known {
				b = t.openBlock(BlockToolCall)
				t.byWire[call.Index] = b
				out = append(out, Chunk{Type: ChunkBlockStart, BlockIndex: b.index, BlockType: BlockToolCall})
			}
			b.id = acceptIdentity(b.id, call.ID)
			b.name = acceptIdentity(b.name, call.Function.Name)
			b.buf.WriteString(call.Function.Arguments)
			out = append(out, Chunk{
				Type: ChunkToolCallDelta, BlockIndex: b.index,
				ToolCallID: b.id, ToolName: b.name,
				ToolCallIndex: call.Index, ArgumentsDelta: call.Function.Arguments,
			})
		}
		if choice.FinishReason != nil {
			t.finish = mapFinishReason(*choice.FinishReason)
		}
	}
	if wc.Usage != nil {
		t.usage = mapUsage(wc.Usage)
	}
	return out
}

type extendResult struct {
	block  *tBlock
	wasNew bool
}

// extend 找到最近一个仍打开的同 kind 块（即 blocks 尾部就是它），否则新开。
// 不同 kind 交错时前一块自然关闭——与 DSH 的 per-kind current block 等价。
func (t *translator) extend(kind BlockType) extendResult {
	if n := len(t.blocks); n > 0 && t.blocks[n-1].kind == kind {
		return extendResult{block: t.blocks[n-1]}
	}
	return extendResult{block: t.openBlock(kind), wasNew: true}
}

// acceptIdentity：id/name 只认首个非空值；”/null 视为不变（index.js:1178-1180）。
func acceptIdentity(current, incoming string) string {
	if current != "" {
		return current
	}
	return incoming
}

// mapFinishReason（index.js:1131-1144）。
func mapFinishReason(r string) *Chunk {
	switch r {
	case "stop", "":
		return &Chunk{Type: ChunkFinish, Finish: FinishStop}
	case "tool_calls":
		return &Chunk{Type: ChunkFinish, Finish: FinishToolCalls}
	case "length":
		return &Chunk{Type: ChunkFinish, Finish: FinishMaxTokens}
	default:
		return &Chunk{Type: ChunkFinish, Finish: FinishError,
			Fail: &Failure{Message: fmt.Sprintf("model stopped: %s", r), Code: ErrorCode(strings.ToUpper(r))}}
	}
}

// mapUsage（index.js:1154-1166）：cacheRead = prompt_tokens_details.cached_tokens
// ?? prompt_cache_hit_tokens；inputTokens = prompt_tokens − cacheRead。
func mapUsage(u *struct {
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
}) *TokenUsage {
	out := &TokenUsage{
		InputTokens:  int64(u.PromptTokens),
		OutputTokens: int64(u.CompletionTokens),
	}
	var cacheRead int
	if u.PromptDetails != nil {
		cacheRead = u.PromptDetails.CachedTokens
	} else if u.PromptCacheHit != nil {
		cacheRead = *u.PromptCacheHit
	}
	out.CacheReadTokens = int64(cacheRead)
	out.InputTokens -= int64(cacheRead)
	if u.CompletionDetails != nil {
		out.ReasoningTokens = int64(u.CompletionDetails.ReasoningTokens)
	}
	total := u.PromptTokens + u.CompletionTokens
	if u.TotalTokens != 0 && u.TotalTokens == total {
		out.TotalTokens = int64(total)
	}
	return out
}

// finalize 收尾：block-end → usage → finish；零块 stop 替换为 EMPTY_RESPONSE
// （index.js:1225-1246）。方法名避开 finish 字段。
func (t *translator) finalize() []Chunk {
	var out []Chunk
	for _, b := range t.blocks {
		blk := ContentBlock{Type: string(b.kind)}
		switch b.kind {
		case BlockText, BlockReasoning:
			blk.Text = b.buf.String()
		case BlockToolCall:
			blk.ID = b.id
			blk.Name = b.name
			blk.Arguments = b.buf.String()
		}
		out = append(out, Chunk{
			Type: ChunkBlockEnd, BlockIndex: b.index,
			ToolCallID: b.id, ToolName: b.name,
			Block: &blk,
		})
	}
	if t.usage != nil {
		out = append(out, Chunk{Type: ChunkUsage, Usage: t.usage})
	}
	fin := t.finish
	if fin == nil {
		fin = &Chunk{Type: ChunkFinish, Finish: FinishStop}
	}
	if len(t.blocks) == 0 && fin.Finish == FinishStop {
		fin = &Chunk{Type: ChunkFinish, Finish: FinishError,
			Fail: &Failure{Message: "model returned an empty response", Code: ErrEmptyResponse}}
	}
	out = append(out, *fin)
	return out
}
