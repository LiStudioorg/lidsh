// HTTP 一元 RPC 派发（§5.2）：session/create、session/prompt、session/cancel、
// session/list、$events/result。响应恒 HTTP 200，业务错误经 result.error。
package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
		// M1b：无 waterfall 待决，恒 ok（浏览器接到的应答；§5.5）。
		return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
			Result: protocolRPCResult{Ok: bptr(true)}}
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
	// 事件广播给订阅该会话的 follow 连接。
	sess.OnEvent(func(e *session.Event) {
		s.emitEvent(id, e)
	})

	s.mu.Lock()
	s.sessions[id] = &entry{sess: sess}
	s.mu.Unlock()

	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true),
			Value: mustRaw(map[string]any{"sessionId": id, "agentPreset": meta.AgentPreset})}}
}

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
	go s.runPrompt(ent, text)
	return protocolRPCResponse{Type: "server-response", RPCID: rpcID,
		Result: protocolRPCResult{Ok: bptr(true), Value: mustRaw(map[string]any{"accepted": true})}}
}

// runPrompt 在后台驱动一次 agent 对话。同一会话的 agent 惰性构造一次并跨
// Prompt 复用（第二次 Prompt 在同会话上继续，surface 已含历史）。
func (s *Server) runPrompt(ent *entry, text string) {
	s.mu.Lock()
	if ent.agent == nil {
		ent.agent = agent.New(agent.Options{
			Sess: ent.sess,
			Resolver: agent.NewStaticResolver(map[string]llm.Adapter{
				s.Opts.Provider: s.Opts.Adapter,
			}),
			Tools:    s.Tools,
			Provider: s.Opts.Provider,
			Model:    s.Opts.Model,
			Reason:   s.Opts.Reason,
			System:   s.Opts.System,
			CWD:      ent.sess.Header.CWD,
		})
	}
	a := ent.agent
	// 事件经 OnEvent 广播给所有 follow 连接（见 follow.go 订阅）。
	s.mu.Unlock()

	// 取消旧的会话级 context 并新建（复用 Prompt 内部的自带 context）。
	_, _ = a.Prompt(text, "followup")
}

// ---- session/cancel ----

func (s *Server) rpcSessionCancel(rpcID string, req protocolRPCRequest) protocolRPCResponse {
	// M1b：cancel 空操作（agent 无跨调用取消句柄），返回 accepted。
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
