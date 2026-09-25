package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lidsh/internal/llm"

	"github.com/gorilla/websocket"
)

// fakeAdapter 返回固定文本流的适配器。
type fakeAdapter struct{}

func (fakeAdapter) Stream(ctx context.Context, opts llm.GenerateOptions) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		ch <- llm.Chunk{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockText}
		ch <- llm.Chunk{Type: llm.ChunkTextDelta, BlockIndex: 0, Delta: "server answer"}
		ch <- llm.Chunk{Type: llm.ChunkBlockEnd, BlockIndex: 0}
		ch <- llm.Chunk{Type: llm.ChunkUsage, Usage: &llm.TokenUsage{InputTokens: 4, OutputTokens: 3}}
		ch <- llm.Chunk{Type: llm.ChunkFinish, Finish: llm.FinishStop}
	}()
	return ch, nil
}

func (fakeAdapter) Info() llm.ProviderInfo                 { return llm.ProviderInfo{Provider: "fake"} }
func (fakeAdapter) ResolveModel(id string) (string, error) { return id, nil }

// postRPC 发一个 HTTP 一元 RPC 请求并解析响应。
func postRPC(t *testing.T, ts *httptest.Server, endpoint string, args any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"type": "client-request", "rpcId": "rpc-1", "method": endpoint,
		"payload": map[string]any{"args": args},
	})
	resp, err := http.Post(ts.URL+"/api/"+endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("endpoint %s: bad response JSON: %v (%s)", endpoint, err, buf.String())
	}
	return out
}

// TestWSFollowRing 验证最小闭环：create → prompt → follow 收到 snapshot + 事件。
func TestWSFollowRing(t *testing.T) {
	srv := New(Options{
		Home: "/tmp", Workdir: "/tmp", Provider: "fake", Model: "m",
		System: "test system", Adapter: fakeAdapter{},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1) create session。
	create := postRPC(t, ts, "session/create", map[string]any{"workspaceId": "w", "agentPreset": "standard"})
	if !resultOK(create) {
		t.Fatalf("create failed: %v", create)
	}
	sessionID := nested(create, "result", "value", "sessionId")

	// 2) 打开 WS，开 session/follow 流。
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/remote.mux"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	openPayload := map[string]any{
		"type": "open", "streamId": "s1", "endpoint": "session/follow",
		"payload": map[string]any{"args": map[string]any{
			"request": map[string]any{
				"address": map[string]any{"kind": "session", "sessionId": sessionID},
			},
		}},
	}
	if err := conn.WriteJSON(openPayload); err != nil {
		t.Fatal(err)
	}

	// 3) prompt 触发 agent。
	prompt := postRPC(t, ts, "session/prompt", map[string]any{"request": map[string]any{
		"requestId": "r1", "sessionId": sessionID, "mode": "queue",
		"content": []map[string]any{{"type": "text", "text": "hi"}},
	}})
	if !resultOK(prompt) {
		t.Fatalf("prompt failed: %v", prompt)
	}

	// 4) 读 WS 帧：先 snapshot，再 event 流。
	deadline := time.Now().Add(5 * time.Second)
	var sawSnapshot, sawEvent bool
	var eventType string
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame["type"] {
		case "item":
			val, _ := frame["value"].(map[string]any)
			switch val["type"] {
			case "snapshot":
				sawSnapshot = true
			case "event":
				sawEvent = true
				if ev, ok := val["event"].(map[string]any); ok {
					eventType = mustStr(ev["type"])
				}
				// 等到 assistant 回答落盘后即停止。
				if eventType == "assistant/message" {
					goto done
				}
			}
		}
	}
done:
	if !sawSnapshot {
		t.Error("did not receive snapshot frame")
	}
	if !sawEvent {
		t.Error("did not receive event frame")
	}
	if eventType != "assistant/message" {
		t.Errorf("expected assistant/message event, got %q", eventType)
	}
}

// TestEventsReady 验证 $events 流第一帧是 ready。
func TestEventsReady(t *testing.T) {
	srv := New(Options{Home: "/tmp", Workdir: "/tmp", Provider: "fake", Model: "m", Adapter: fakeAdapter{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/remote.mux"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "open", "streamId": "e1", "endpoint": "$events",
		"payload": map[string]any{"args": map[string]any{}}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("bad frame %s", data)
	}
	if frame["type"] != "ready" {
		t.Fatalf("first $events frame = %v, want ready", frame["type"])
	}
	// ready 帧须携带 host.home。
	if _, ok := frame["host"].(map[string]any); !ok {
		t.Error("ready frame missing host.home")
	}
}

func resultOK(resp map[string]any) bool {
	ok, _ := resp["result"].(map[string]any)["ok"].(bool)
	return ok
}

func nested(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, _ := cur.(map[string]any)
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

func mustStr(v any) string {
	s, _ := v.(string)
	return s
}
