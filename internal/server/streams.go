// 远程流端点实现：session/follow（快照 + 事件订阅）、$events（ready + 广播）。
// §5.4-5.6。
package server

import (
	"encoding/json"
	"lidsh/internal/session"
)

// startStream 按 endpoint 启动一个流。返回 false 表示端点未知。
func (s *Server) startStream(c *conn, f protocolOpen, active <-chan struct{}) bool {
	switch f.Endpoint {
	case "session/follow":
		go s.runFollow(c, f, active)
		return true
	case "$events":
		go s.runEvents(c, f, active)
		return true
	default:
		return false
	}
}

// ---------- $events 流（§5.5） ----------

// runEvents 每代流第一帧 ready，然后挂到 hub 广播直到取消。
func (s *Server) runEvents(c *conn, f protocolOpen, active <-chan struct{}) {
	ready := map[string]any{
		"type":     "ready",
		"clientId": newUUID(),
		"host":     map[string]any{"home": s.Opts.Home},
	}
	if err := c.writeLine(ready); err != nil {
		return
	}
	<-active
}

// ---------- session/follow 流（§5.6） ----------

type followState struct {
	sessionID string
	streamID  string
	conn      *conn
}

func (s *Server) runFollow(c *conn, f protocolOpen, active <-chan struct{}) {
	var p protocolFollowPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		_ = c.writeLine(protocolError{Type: "error", StreamID: f.StreamID,
			Error: protocolErrorBody{Code: "gateway/bad-request", Message: "invalid follow args"}})
		return
	}
	sessionID := p.Args.Request.Address.SessionID
	if sessionID == "" {
		_ = c.writeLine(protocolError{Type: "error", StreamID: f.StreamID,
			Error: protocolErrorBody{Code: "gateway/bad-request", Message: "address.sessionId required"}})
		return
	}
	if p.Args.Request.Address.Kind != "" && p.Args.Request.Address.Kind != "session" {
		_ = c.writeLine(protocolError{Type: "error", StreamID: f.StreamID,
			Error: protocolErrorBody{Code: "gateway/bad-request", Message: "unsupported address kind"}})
		return
	}

	s.mu.Lock()
	ent, ok := s.sessions[sessionID]
	cursor := 0
	if ok {
		cursor = ent.sess.Len()
	}
	s.mu.Unlock()
	if !ok {
		_ = c.writeLine(protocolError{Type: "error", StreamID: f.StreamID,
			Error: protocolErrorBody{Code: "session/not-found", Message: "session not found"}})
		return
	}

	st := &followState{sessionID: sessionID, streamID: f.StreamID, conn: c}
	s.subscribe(st)
	defer s.unsubscribe(st)

	// 快照帧：以 cursor（订阅时已用）作为 asOfSeq 仍引用打开时的确定值。
	s.sendSnapshot(st, f, cursor)

	<-active
}

func (s *Server) subscribe(st *followState) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.subs[st.sessionID] == nil {
		s.subs[st.sessionID] = map[*conn]struct{}{}
	}
	s.subs[st.sessionID][st.conn] = struct{}{}
	// conn 侧记录 sessionID→streamID，供 emitEvent 反查帧目标。
	st.conn.mu.Lock()
	st.conn.follows[st.sessionID] = st.streamID
	st.conn.mu.Unlock()
}

func (s *Server) unsubscribe(st *followState) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	st.conn.mu.Lock()
	delete(st.conn.follows, st.sessionID)
	st.conn.mu.Unlock()
	if set, ok := s.subs[st.sessionID]; ok {
		delete(set, st.conn)
		if len(set) == 0 {
			delete(s.subs, st.sessionID)
		}
	}
}

// emitEvent 把一个会话事件推给所有订阅了该会话的 follow 连接。
// 由 session 的 OnEvent 处理器调用（见 rpcSessionCreate）。
func (s *Server) emitEvent(sessionID string, e *session.Event) {
	s.subMu.Lock()
	set := s.subs[sessionID]
	conns := make([]*conn, 0, len(set))
	for c := range set {
		conns = append(conns, c)
	}
	s.subMu.Unlock()

	frame := map[string]any{
		"type":  "event",
		"event": wireEvent(e),
	}
	for _, c := range conns {
		// 该 conn 订阅此会话的 follow streamId。
		c.mu.Lock()
		sid := c.follows[sessionID]
		c.mu.Unlock()
		_ = c.writeLine(protocolItem{Type: "item", StreamID: sid, Value: mustRaw(frame)})
	}
}

func (s *Server) sendSnapshot(st *followState, f protocolOpen, cursor int) {
	s.mu.Lock()
	ent := s.sessions[st.sessionID]
	var events []*session.Event
	if ent != nil {
		events = ent.sess.Log()
	}
	s.mu.Unlock()

	records := make([]map[string]any, 0)
	// 快照携带 cursor 之前的全部历史（M1b 简化：一次性全量历史）。
	for _, e := range events {
		records = append(records, map[string]any{"type": "event", "event": wireEvent(e)})
	}

	frame := map[string]any{
		"type":        "snapshot",
		"header":      headerWire(ent.sess.Header),
		"cursor":      cursor,
		"records":     records,
		"hasMore":     false,
		"projections": map[string]any{"asOfSeq": cursor, "values": map[string]any{}},
	}
	_ = st.conn.writeLine(protocolItem{Type: "item", StreamID: f.StreamID, Value: mustRaw(frame)})
}

// ---------- wire 事件 ----------

// wireEvent 把 Session 事件序列化为 wire 形状（SessionWireEvent，§5.6.2）。
func wireEvent(e *session.Event) map[string]any {
	ev := map[string]any{
		"type": e.Type,
		"seq":  e.Seq,
		"time": e.Time,
	}
	if len(e.Data) > 0 {
		var d any
		if json.Unmarshal(e.Data, &d) == nil {
			ev["data"] = d
		}
	}
	if len(e.SurfaceOp) > 0 {
		var so any
		if json.Unmarshal(e.SurfaceOp, &so) == nil {
			ev["surfaceOp"] = so
		}
	}
	if len(e.SourceEventSeqs) > 0 {
		ev["sourceEventSeqs"] = e.SourceEventSeqs
	}
	return ev
}

func headerWire(h session.Header) map[string]any {
	out := map[string]any{
		"version":   h.Version,
		"id":        h.ID,
		"createdAt": h.CreatedAt,
		"cwd":       h.CWD,
		"isSeeded":  h.IsSeeded,
	}
	if h.ParentSession != "" {
		out["parentSession"] = h.ParentSession
	}
	if h.Origin != "" {
		out["origin"] = h.Origin
	}
	if h.DelegationDepth != 0 {
		out["delegationDepth"] = h.DelegationDepth
	}
	if h.AgentPreset != "" {
		out["agentPreset"] = h.AgentPreset
	}
	return out
}

// ---------- wire 形状 ----------

type protocolFollowPayload struct {
	Args struct {
		Request struct {
			Address struct {
				Kind      string `json:"kind"`
				SessionID string `json:"sessionId"`
			} `json:"address"`
			MaxMessages     int  `json:"maxMessages,omitempty"`
			AssistantStream bool `json:"assistantStream,omitempty"`
		} `json:"request"`
	} `json:"args"`
}

type protocolItem struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Value    json.RawMessage `json:"value,omitempty"`
}
