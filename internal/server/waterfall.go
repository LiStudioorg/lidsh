// 审批/裁决瀑布（§5.5）：Host 把需要浏览器裁决的瀑布事件下发到每个 $events
// 客户端，浏览器经 HTTP 回环 POST /api/$events/result 交回 outcome。
//
// 语义（gateway/index.js + gateway client.js）：
//   - 事件投递给每个 open 了 $events 的 client（带各自 clientId）。
//   - outcome 三种：kind=next（本客户端不处理，让 Host 走 next）、
//     kind=result（第一个 result 决定瀑布返回值）、kind=rejected（以重建 Error 失败）。
//   - 多浏览器在线时谁先回 result 谁定胜负；clientId 不认识 → 该 result 被丢弃
//     （仍回 {ok:true,value:undefined}）。
//   - 事件被取消/超时/Agent 释放 → 下发 {type:"cancel",eventId} 让前端清 UI。
//
// M1b 接驳：agent 内核尚无审批/steering 钩子，本文件实现传输机制与可测的
// RequestWaterfall/result 回环；真实 approval/request 生产者后续里程碑接入。
package server

import (
	"encoding/json"
	"sync"
	"time"

	"lidsh/internal/session"
)

// waterfallOutcome 是一次瀑布的终局（来自浏览器 outcome）。
type waterfallOutcome struct {
	kind    string // "next" | "result" | "rejected"
	value   json.RawMessage
	errCode string
	errMsg  string
}

// waterfall 是一次在途瀑布（eventId 唯一）。
type waterfall struct {
	eventID string
	event   string
	agentID string
	request json.RawMessage

	ch chan waterfallOutcome // 缓冲 1；结果/拒绝到达即 close 让等待方返回；全 next 也 close

	mu         sync.Mutex
	settled    bool
	nextCount  int
	delivered  map[*conn]bool // 已投递连接（幂等）
	totalConns int            // 投递时刻的 $events 客户端数
}

// wfResult 把瀑布终局映射为返回（value 或重建的 error 码/消息）。
type wfResult struct {
	value    any
	errCode  string
	errMsg   string
	settled  bool
	rejected bool
}

// dispatchWaterfall 向当前所有 $events 客户端广播瀑布帧，返回等待终局的结果。
// eventID 本次随机生成；load 是请求体（每个客户端收到同一份）。
func (s *Server) dispatchWaterfall(event, agentID string, load json.RawMessage) wfResult {
	eventID := session.NewUUID()
	w := &waterfall{
		eventID:   eventID,
		event:     event,
		agentID:   agentID,
		request:   load,
		ch:        make(chan waterfallOutcome, 1),
		delivered: map[*conn]bool{},
	}

	s.wfMu.Lock()
	s.waterfalls[eventID] = w
	s.wfMu.Unlock()
	defer func() {
		s.wfMu.Lock()
		delete(s.waterfalls, eventID)
		s.wfMu.Unlock()
	}()

	// 收集投递目标（open 了 $events 的连接）。
	conns := s.eventsConns()
	w.totalConns = len(conns)

	frame := map[string]any{
		"type":    "waterfall",
		"event":   event,
		"eventId": eventID,
		"agentId": agentID,
		"request": load,
	}
	for _, c := range conns {
		if err := c.writeLine(frame); err != nil {
			continue
		}
		w.mu.Lock()
		w.delivered[c] = true
		w.mu.Unlock()
	}

	// 等待终局（超时保护，防客户端失联挂死）。
	select {
	case oc := <-w.ch:
		switch oc.kind {
		case "rejected":
			return wfResult{rejected: true, errCode: oc.errCode, errMsg: oc.errMsg, settled: true}
		default:
			if oc.value != nil && string(oc.value) != "null" {
				var v any
				_ = json.Unmarshal(oc.value, &v)
				return wfResult{value: v, settled: true}
			}
			return wfResult{settled: true}
		}
	case <-time.After(waterfallTimeout):
		// 超时：通知客户端取消 UI。
		s.broadcastEventsCancel(eventID)
		return wfResult{}
	}
}

const waterfallTimeout = 60 * time.Second

// submitResult 由 $events/result RPC 调用：把一个浏览器的 outcome 交给瀑布。
// 返回是否被采纳（未知 clientId/已 settle 时为 false，仍回 ok）。
func (s *Server) submitResult(clientID, eventID string, oc waterfallOutcome) bool {
	s.wfMu.Lock()
	w, ok := s.waterfalls[eventID]
	s.wfMu.Unlock()
	if !ok {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.settled {
		return false
	}
	switch oc.kind {
	case "next":
		w.nextCount++
		// 所有已投递客户端都 next → 视为无值结果（未 settle，但应尽快返回）。
		if w.nextCount >= w.totalConns && w.totalConns > 0 {
			w.settled = true
			select {
			case w.ch <- waterfallOutcome{kind: "next"}:
			default:
			}
		}
		return true
	case "result", "rejected":
		w.settled = true
		select {
		case w.ch <- oc:
		default:
		}
		return true
	}
	return false
}

// eventsConns 返回所有 open 了 $events 的连接。
func (s *Server) eventsConns() []*conn {
	s.hub.mu.Lock()
	conns := make([]*conn, 0, len(s.hub.conns))
	for c := range s.hub.conns {
		c.mu.Lock()
		if c.events {
			conns = append(conns, c)
		}
		c.mu.Unlock()
	}
	s.hub.mu.Unlock()
	return conns
}

// broadcastEventsCancel 通知所有 $events 客户端某瀑布已取消（清 UI）。
func (s *Server) broadcastEventsCancel(eventID string) {
	frame := map[string]any{"type": "cancel", "eventId": eventID}
	for _, c := range s.eventsConns() {
		_ = c.writeLine(frame)
	}
}
