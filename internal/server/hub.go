// $events 广播 hub：活跃 WS 连接集合，用于把 Cordis 事件转发给每个浏览器客户端
// （§5.5）。M1b 只广播 session 事件；waterfall 审批流留 M1b 后续。
package server

import "sync"

type hub struct {
	mu    sync.Mutex
	conns map[*conn]struct{}
}

func newHub() *hub { return &hub{conns: map[*conn]struct{}{}} }

func (h *hub) add(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[c] = struct{}{}
}

func (h *hub) drop(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

// broadcast 向每个连接发送一条 $events 流帧。发送失败（连接已关）的连接被移除。
func (h *hub) broadcast(frame any) {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		if err := c.writeLine(frame); err != nil {
			h.drop(c)
		}
	}
}

func (h *hub) size() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}
