package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lidsh/internal/session"

	"github.com/gorilla/websocket"
)

// ---- 1) 持久化写路径：create 落盘 header，prompt 落盘事件 ----

func TestPersistenceWritePath(t *testing.T) {
	home := t.TempDir()
	workdir := filepath.Join(home, "repo")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Home: home, Workdir: workdir, Provider: "fake", Model: "m",
		System: "s", Adapter: fakeAdapter{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	create := postRPC(t, ts, "session/create", map[string]any{"workspaceId": "w", "agentPreset": "standard"})
	if !resultOK(create) {
		t.Fatalf("create failed: %v", create)
	}
	id := nested(create, "result", "value", "sessionId")

	dir := session.SessionDir(home, workdir, id)
	logPath := filepath.Join(dir, session.LogFileName(session.FormatVersion, "zstd"))
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected session log at %s: %v", logPath, err)
	}
	// 目录里应有写锁文件。
	if _, err := os.Stat(filepath.Join(dir, "session.lock")); err != nil {
		t.Errorf("expected session.lock: %v", err)
	}

	// prompt 触发 agent → 事件落盘后读回。
	prompt := postRPC(t, ts, "session/prompt", map[string]any{"request": map[string]any{
		"requestId": "r1", "sessionId": id, "mode": "queue",
		"content": []map[string]any{{"type": "text", "text": "hi"}},
	}})
	if !resultOK(prompt) {
		t.Fatalf("prompt failed: %v", prompt)
	}
	// 等日志增长（agent 后台跑）。
	deadline := time.Now().Add(5 * time.Second)
	var events []*session.Event
	for time.Now().Before(deadline) {
		events = loadLog(t, logPath)
		if hasEvent(events, "assistant/message") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !hasEvent(events, "assistant/message") {
		t.Fatalf("persisted log has no assistant/message event; got %d events", len(events))
	}
	if !hasEvent(events, "user/message") {
		t.Errorf("persisted log missing user/message event")
	}
}

func loadLog(t *testing.T, path string) []*session.Event {
	t.Helper()
	hdr, evs, _, err := session.LoadLog(path, "zstd")
	if err != nil {
		t.Fatalf("LoadLog: %v", err)
	}
	_ = hdr
	return evs
}

func hasEvent(events []*session.Event, typ string) bool {
	for _, e := range events {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// ---- 2) 审批瀑布回环：$events waterfall 帧 + $events/result ----

func TestWaterfallLoopback(t *testing.T) {
	srv := New(Options{Home: t.TempDir(), Workdir: "/tmp", Provider: "fake", Model: "m",
		Adapter: fakeAdapter{}})
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

	// ready 帧得 clientId。
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if json.Unmarshal(data, &ready) != nil || ready["type"] != "ready" {
		t.Fatalf("first frame = %s, want ready", data)
	}
	clientID := mustStr(ready["clientId"])

	// 后台发起瀑布（模拟 Host 侧 approval/request 生产者）。
	resCh := make(chan wfResult, 1)
	go func() {
		resCh <- srv.dispatchWaterfall("approval/request", "ses_1",
			json.RawMessage(`{"toolName":"bash","args":{"command":"ls"},"sandboxMode":"danger"}`))
	}()

	// WS 收到 waterfall 帧，取 eventId。
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err = conn.ReadMessage()
	if err != nil {
		t.Fatal("no waterfall frame: ", err)
	}
	var wf map[string]any
	if err := json.Unmarshal(data, &wf); err != nil {
		t.Fatalf("bad frame %s", data)
	}
	if wf["type"] != "waterfall" || wf["event"] != "approval/request" {
		t.Fatalf("want waterfall approval/request, got %v", wf)
	}
	eventID := mustStr(wf["eventId"])

	// 浏览器 POST $events/result。
	resp := postRPC(t, ts, "$events/result", map[string]any{
		"clientId": clientID, "eventId": eventID,
		"outcome": map[string]any{"kind": "result", "value": "allowed-once"},
	})
	if !resultOK(resp) {
		t.Fatalf("$events/result failed: %v", resp)
	}

	select {
	case r := <-resCh:
		if !r.settled || r.rejected {
			t.Fatalf("waterfall not settled as result: %+v", r)
		}
		if r.value != "allowed-once" {
			t.Errorf("waterfall value = %v, want allowed-once", r.value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dispatchWaterfall did not return")
	}
}

func TestWaterfallUnknownClientDropped(t *testing.T) {
	srv := New(Options{Home: t.TempDir(), Workdir: "/tmp", Provider: "fake", Model: "m",
		Adapter: fakeAdapter{}})
	// 未知 clientId 的 result 应被丢弃（返回 ok:true）。
	resp := postRPC(t, httptest.NewServer(srv.Handler()), "$events/result", map[string]any{
		"clientId": "unknown", "eventId": "nope",
		"outcome": map[string]any{"kind": "result", "value": "x"},
	})
	if !resultOK(resp) {
		t.Fatalf("unknown-client result should still be ok: %v", resp)
	}
}

// ---- 3) 附件上传接收：POST /api/session/uploadFileBinary ----

func TestFileUpload(t *testing.T) {
	home := t.TempDir()
	srv := New(Options{Home: home, Workdir: "/tmp", Provider: "fake", Model: "m", Adapter: fakeAdapter{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	create := postRPC(t, ts, "session/create", map[string]any{"agentPreset": "standard"})
	if !resultOK(create) {
		t.Fatal("create failed")
	}
	sid := nested(create, "result", "value", "sessionId")

	payload := []byte("hello file bytes")
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/session/uploadFileBinary?sessionId="+sid+"&name=notes.txt",
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, body %s", resp.StatusCode, body)
	}
	var out struct {
		Ok    bool `json:"ok"`
		Value struct {
			ReceiptID string `json:"receiptId"`
			File      struct {
				AttachmentID string `json:"attachmentId"`
				Name         string `json:"name"`
				Bytes        int    `json:"bytes"`
			} `json:"file"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("bad upload response: %v (%s)", err, body)
	}
	if !out.Ok {
		t.Fatalf("upload not ok: %s", body)
	}
	if out.Value.File.Bytes != len(payload) {
		t.Errorf("bytes = %d, want %d", out.Value.File.Bytes, len(payload))
	}
	if !strings.HasPrefix(out.Value.File.AttachmentID, "sha256:") || len(out.Value.File.AttachmentID) != 71 {
		t.Errorf("unexpected attachmentId %q", out.Value.File.AttachmentID)
	}

	// 内容寻址对象已存储。
	sha := strings.TrimPrefix(out.Value.File.AttachmentID, "sha256:")
	objPath := filepath.Join(home, "attachments", "v1", "file-objects", sha[:2], sha)
	if b, err := os.ReadFile(objPath); err != nil || string(b) != string(payload) {
		t.Errorf("stored object wrong: err=%v", err)
	}

	// 收据可在会话作用域解析。
	sf, ok := srv.resolveReceipt(sid, out.Value.ReceiptID)
	if !ok || sf.name != "notes.txt" {
		t.Errorf("receipt not staged or wrong name: ok=%v sf=%+v", ok, sf)
	}
	// 退役后不再可解析。
	srv.retireReceipt(sid, out.Value.ReceiptID)
	if _, ok := srv.resolveReceipt(sid, out.Value.ReceiptID); ok {
		t.Error("receipt should be retired")
	}
}
