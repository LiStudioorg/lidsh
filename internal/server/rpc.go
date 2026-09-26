// HTTP 一元 RPC 派发（§5.2）：session/create、session/prompt、session/cancel、
// session/list、$events/result。响应恒 HTTP 200，业务错误经 result.error。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"lidsh/internal/agent"
	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// dispatchRPC 返回成功/失败的 response（HTTP 层负责写体）。
func (s *Server) dispatchRPC(endpoint string, req protocolRPCRequest) protocolRPCResponse {
	rpcID := req.RPCID
	switch endpoint {
	case "session/create":
		return s.rpcSessionCreate(rpcID, req)
	case "session/prompt":
		return s.rpcSessionPrompt(rpcID, req)
	case "session/cancel":
		return s.rpcSessionCancel(rpcID, req)
	case "session/list":
		return s.rpcSessionList(rpcID, req)
	case "$events/result":
		return s.rpcEventsResult(rpcID, req)
	default:
		return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
			Result: protocolRPCResult{Ok: bptr(false),
				Error: &protocolErrorBody{Code: "gateway/not-found", Message: "no such RPC: " + endpoint}}}
	}
}

// ---- session/create ----

type createArgs struct {
	WorkspaceID string `json:"workspaceId,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
	AgentPreset string `json:"agentPreset,omitempty"`
}

func (s *Server) rpcSessionCreate(rpcID string, req protocolRPCRequest) protocolRPCResponse {
	var p protocolRPCPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	var a createArgs
	if err := json.Unmarshal(p.Args, &a); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	id := a.SessionID
	if id == "" {
		id = session.NewID()
	}
	meta := session.Meta{AgentPreset: a.AgentPreset}
	if meta.AgentPreset == "" {
		meta.AgentPreset = "standard"
	}
	hdr := session.NewHeader(id, s.Opts.Workdir, meta)
	sess := session.New(hdr)

	// 持久化：建会话目录、持写锁、写 header 帧、事件增量落盘（复刻 headless 核心）。
	ent, err := s.newPersisted(id, sess, hdr)
	if err != nil {
		return rpcFailResult(rpcID, "session/conflict", err.Error())
	}

	// 事件广播给订阅该会话的 follow 连接。
	sess.OnEvent(func(e *session.Event) {
		s.emitEvent(id, e)
	})

	// goal 装配：服务（fold 重建）+ 续轮驱动 + activation 广播面。
	if err := s.mountGoal(ent); err != nil {
		return rpcFailResult(rpcID, "session/corrupt", err.Error())
	}

	s.mu.Lock()
	s.sessions[id] = ent
	s.mu.Unlock()

	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true),
			Value: mustRaw(map[string]any{"sessionId": id, "agentPreset": meta.AgentPreset})}}
}

// newPersisted 打开会话目录与增量日志，并挂 OnEvent→AppendEvent 写路径。
func (s *Server) newPersisted(id string, sess *session.Session, hdr session.Header) (*entry, error) {
	dir := session.SessionDir(s.Opts.Home, s.Opts.Workdir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session/dir: %w", err)
	}
	lease, err := session.AcquireLease(dir)
	if err != nil {
		return nil, fmt.Errorf("session/lock: %w", err)
	}
	logPath := filepath.Join(dir, session.LogFileName(session.FormatVersion, "zstd"))
	lw, err := session.OpenLogWriter(logPath, "zstd", 0)
	if err != nil {
		lease.Release()
		return nil, fmt.Errorf("session/log: %w", err)
	}
	hb, err := json.Marshal(hdr)
	if err != nil {
		lw.Close()
		lease.Release()
		return nil, err
	}
	if err := lw.Append([][]byte{hb}); err != nil {
		lw.Close()
		lease.Release()
		return nil, fmt.Errorf("session/log header: %w", err)
	}
	// 事件落盘：OnEvent 里先写盘再广播（调用方再挂广播 handler，二者各自独立）。
	sess.OnEvent(func(e *session.Event) { _ = lw.AppendEvent(e) })
	ent := &entry{sess: sess, dir: dir, lw: lw}
	// 惰性 agent 参数（system = 基座 + goal/ralph policy sections，
	// systemPrompt.section 的拼接面对应物）。
	system := goalGuidance(s.Opts.System, goalDefaultBlockedAfter) + "\n\n" + ralphGuidanceSection()
	ent.agentOpts = agent.Options{
		Sess: sess,
		Resolver: agent.NewStaticResolver(map[string]llm.Adapter{
			s.Opts.Provider: s.Opts.Adapter,
		}),
		Tools:    s.Tools,
		Provider: s.Opts.Provider,
		Model:    s.Opts.Model,
		Reason:   s.Opts.Reason,
		System:   system,
		CWD:      sess.Header.CWD,
		Sandbox:  s.Opts.Sandbox,
	}
	return ent, nil
}

// goalDefaultBlockedAfter 是 goal 工具的 blockedAfterConsecutiveRounds。
const goalDefaultBlockedAfter = 3

// ---- session/prompt ----

type promptArgs struct {
	Request *promptRequest `json:"request"`
}
type promptRequest struct {
	RequestID string          `json:"requestId"`
	SessionID string          `json:"sessionId"`
	Mode      string          `json:"mode"` // queue | steer
	Content   json.RawMessage `json:"content"`
}

// 提取 prompt 的纯文本（M1b 只支持 text part；image/file 留后续）。
func promptText(content json.RawMessage) (string, error) {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return "", err
	}
	var out string
	for _, p := range parts {
		if p.Type == "text" {
			out += p.Text
		}
	}
	return out, nil
}

func (s *Server) rpcSessionPrompt(rpcID string, req protocolRPCRequest) protocolRPCResponse {
	var p protocolRPCPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	var a promptArgs
	if err := json.Unmarshal(p.Args, &a); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	if a.Request == nil {
		return rpcFailResult(rpcID, "gateway/bad-request", "request required")
	}
	if a.Request.Mode != "" && a.Request.Mode != "queue" && a.Request.Mode != "steer" {
		return rpcFailResult(rpcID, "gateway/bad-request", "unknown prompt mode")
	}
	text, err := promptText(a.Request.Content)
	if err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", "invalid content: "+err.Error())
	}

	s.mu.Lock()
	ent, ok := s.sessions[a.Request.SessionID]
	s.mu.Unlock()
	if !ok {
		return rpcFailResult(rpcID, "session/not-found", "session not found")
	}
	if text == "" {
		return rpcFailResult(rpcID, "gateway/bad-request", "empty prompt")
	}

	// 后台跑 agent；返回 accepted:true（§5.2 session/prompt 结果是 {accepted}）。
	ent.StartHuman(text)
	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true), Value: mustRaw(map[string]any{"accepted": true})}}
}

// ---- session/cancel ----

func (s *Server) rpcSessionCancel(rpcID string, req protocolRPCRequest) protocolRPCResponse {
	var p protocolRPCPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(p.Args, &a)
	s.mu.Lock()
	ent := s.sessions[a.SessionID]
	s.mu.Unlock()
	if ent != nil {
		ent.Cancel()
	}
	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true), Value: mustRaw(map[string]any{"accepted": true})}}
}

// ---- session/list ----

func (s *Server) rpcSessionList(rpcID string, req protocolRPCRequest) protocolRPCResponse {
	s.mu.Lock()
	items := make([]map[string]any, 0, len(s.sessions))
	for id, e := range s.sessions {
		items = append(items, map[string]any{
			"sessionId":   id,
			"createdAt":   e.sess.Header.CreatedAt,
			"cwd":         e.sess.Header.CWD,
			"agentPreset": e.sess.Header.AgentPreset,
		})
	}
	s.mu.Unlock()
	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true), Value: mustRaw(map[string]any{"items": items})}}
}

// ---- $events/result（§5.5 瀑布回环） ----

type eventsResultArgs struct {
	ClientID string         `json:"clientId"`
	EventID  string         `json:"eventId"`
	Outcome  *eventsOutcome `json:"outcome"`
}

type eventsOutcome struct {
	Kind  string          `json:"kind"` // next | result | rejected
	Value json.RawMessage `json:"value,omitempty"`
	Error *struct {
		Name    string          `json:"name"`
		Message string          `json:"message"`
		Code    string          `json:"code,omitempty"`
		Details json.RawMessage `json:"details,omitempty"`
	} `json:"error,omitempty"`
}

func (s *Server) rpcEventsResult(rpcID string, req protocolRPCRequest) protocolRPCResponse {
	var p protocolRPCPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	var a eventsResultArgs
	if err := json.Unmarshal(p.Args, &a); err != nil {
		return rpcFailResult(rpcID, "gateway/bad-request", err.Error())
	}
	if a.Outcome == nil {
		return rpcFailResult(rpcID, "gateway/bad-request", "outcome required")
	}
	switch a.Outcome.Kind {
	case "next":
		s.submitResult(a.ClientID, a.EventID, waterfallOutcome{kind: "next"})
	case "result":
		s.submitResult(a.ClientID, a.EventID, waterfallOutcome{kind: "result", value: a.Outcome.Value})
	case "rejected":
		code, msg := "Error", "rejected"
		if a.Outcome.Error != nil {
			if a.Outcome.Error.Code != "" {
				code = a.Outcome.Error.Code
			}
			if a.Outcome.Error.Message != "" {
				msg = a.Outcome.Error.Message
			}
		}
		s.submitResult(a.ClientID, a.EventID, waterfallOutcome{kind: "rejected", errCode: code, errMsg: msg})
	default:
		return rpcFailResult(rpcID, "gateway/bad-request", "unknown outcome kind")
	}
	// 客户端应答始终 ok:true（未知 clientId/已 settle 的 result 被静默丢弃；
	// 该场景网关 client 端返回 {ok:true,value:undefined}，§5.5）。
	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true)}}
}

// ---- helpers ----

func rpcFailResult(rpcID, code, message string) protocolRPCResponse {
	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(false),
			Error: &protocolErrorBody{Code: code, Message: message, Details: json.RawMessage(`{}`)}}}
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func bptr(b bool) *bool { return &b }

// ---- wire envelope 形状（server 侧另存，避免与 protocol 包耦合）----

type protocolRPCPayload struct {
	Args json.RawMessage `json:"args"`
}

type protocolRPCResult struct {
	Ok    *bool              `json:"ok"`
	Value json.RawMessage    `json:"value,omitempty"`
	Error *protocolErrorBody `json:"error,omitempty"`
}

type protocolRPCResponse struct {
	Type   string            `json:"type"`
	RPCID  string            `json:"rpcId"`
	Result protocolRPCResult `json:"result"`
}

// ---- HTTP 写 ---- //

func writeRPC(w http.ResponseWriter, rpcID string, res protocolRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func writeRPCFail(w http.ResponseWriter, rpcID, code, message string) {
	writeRPC(w, rpcID, rpcFailResult(rpcID, code, message))
}

func readAllCapped(r io.Reader, n int64) ([]byte, error) {
	limited := io.LimitReader(r, n)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) >= n {
		return nil, errors.New("body too large")
	}
	return b, nil
}
