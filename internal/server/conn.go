// WS 连接处理：读 open/cancel、管理流注册表、串行写帧（§5.3-5.4）。
package server

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 心跳参数（§5.3：默认 2s ping，连续 2 次无 pong 终止）。
const (
	heartbeatInterval = 2 * time.Second
	maxMissed         = 2
)

// conn 是一条 WS 连接：持有一组活跃流（streamId → cancel）。
type conn struct {
	srv  *Server
	ws   *websocket.Conn
	open bool

	mu      sync.Mutex
	streams map[string]func() // streamId → 取消函数
	follows map[string]string // sessionID → follow 流的 streamId（emit 反查）

	// $events 流注册：events=true 表示该连接打开过 $events（有 clientId）。
	events         bool
	eventsClientID string

	writeMu sync.Mutex
}

func newConn(s *Server, ws *websocket.Conn) *conn {
	c := &conn{srv: s, ws: ws, open: true, streams: map[string]func(){},
		follows: map[string]string{}} // ongoing: map[string]bool{}
	return c
}

// run 进入读写循环，直到连接关闭。
func (c *conn) run() {
	// 心跳 goroutine。
	done := make(chan struct{})
	defer close(done)
	go c.pingLoop(done)

	c.ws.SetReadLimit(1 << 20)
	c.ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			break // 连接关闭/读超时
		}
		if !handleClientFrame(c, data) {
			break
		}
	}
	c.closeAll()
}

func (c *conn) pingLoop(done <-chan struct{}) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				c.close()
				return
			}
		}
	}
}

// handleClientFrame 解析一帧浏览器消息；返回 false 表示协议错误应关连接。
func handleClientFrame(c *conn, data []byte) bool {
	// 只接受 JSON 文本帧；JSON 非法 → close(1008)（§5.3）。
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		c.protoClose(1008, "invalid Remote stream request")
		return false
	}
	switch head.Type {
	case "open":
		var f protocolOpen
		if err := json.Unmarshal(data, &f); err != nil || f.StreamID == "" || f.Endpoint == "" {
			c.protoClose(1008, "invalid Remote stream request")
			return false
		}
		return c.openStream(f)
	case "cancel":
		var f protocolCancel
		if err := json.Unmarshal(data, &f); err != nil || f.StreamID == "" {
			c.protoClose(1008, "invalid Remote stream request")
			return false
		}
		c.cancelStream(f.StreamID)
		return true
	default:
		c.protoClose(1008, "invalid Remote stream request")
		return false
	}
}

// openStream 启动一个端点流；端点不存在 → error 帧。重复 streamId → 协议错误。
func (c *conn) openStream(f protocolOpen) bool {
	c.mu.Lock()
	if _, dup := c.streams[f.StreamID]; dup {
		c.mu.Unlock()
		c.protoClose(1008, "duplicate streamId")
		return false
	}
	c.mu.Unlock()

	active := make(chan struct{})
	c.mu.Lock()
	c.streams[f.StreamID] = func() { close(active) }
	c.mu.Unlock()

	ok := c.srv.startStream(c, f, active)
	if !ok {
		// 端点未知：error 帧，移除注册（§5.4 error）。
		c.writeLine(protocolError{Type: "error", StreamID: f.StreamID,
			Error: protocolErrorBody{Code: "gateway/not-found", Message: "unknown endpoint " + f.Endpoint}})
		c.mu.Lock()
		delete(c.streams, f.StreamID)
		c.mu.Unlock()
		return true
	}
	return true
}

func (c *conn) cancelStream(id string) {
	c.mu.Lock()
	cancel, ok := c.streams[id]
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

func (c *conn) closeAll() {
	c.mu.Lock()
	streams := c.streams
	c.streams = map[string]func(){}
	c.mu.Unlock()
	for _, cancel := range streams {
		cancel()
	}
	c.close()
}

func (c *conn) close() {
	c.writeMu.Lock()
	c.open = false
	c.ws.Close()
	c.writeMu.Unlock()
}

func (c *conn) protoClose(code int, msg string) {
	_ = c.ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, msg), time.Now().Add(time.Second))
	c.close()
}

// writeLine 串行写一帧到连接（若连接仍开放）。
func (c *conn) writeLine(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if !c.open {
		return fmt.Errorf("connection closed")
	}
	return c.ws.WriteJSON(v)
}

// ---------- 帧形状 ----------

type protocolOpen struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Endpoint string          `json:"endpoint"`
	Payload  json.RawMessage `json:"payload"`
}

type protocolCancel struct {
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
}

type protocolErrorBody struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

type protocolError struct {
	Type     string            `json:"type"`
	StreamID string            `json:"streamId"`
	Error    protocolErrorBody `json:"error"`
}
