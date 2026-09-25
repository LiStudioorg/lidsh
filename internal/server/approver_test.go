package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lidsh/internal/sandbox"
	"lidsh/internal/tools"

	"github.com/gorilla/websocket"
)

// TestSandboxApprovalWaterfallE2E 验证 M2 沙箱提权的审批全链路：
// server 挂载沙箱 → agent 侧 Approver 发起 approval/request 瀑布 →
// 浏览器（本测试）经 $events 收到 waterfall 帧 → POST $events/result 回
// allowed-once → Approver 拿到获批 mode。
func TestSandboxApprovalWaterfallE2E(t *testing.T) {
	sb := &tools.SandboxOptions{
		Mode:   sandbox.ModeReadOnly,
		Root:   t.TempDir(),
		Runner: "",
		Policy: sandbox.ApprovalAsk,
	}
	srv := New(Options{
		Home: t.TempDir(), Workdir: t.TempDir(),
		Provider: "fake", Model: "m", Adapter: fakeAdapter{},
		Sandbox: sb,
	})
	if srv.Opts.Sandbox.Approver == nil {
		t.Fatal("server.New must bind the waterfall approver")
	}
	// 挂载组合必须注册带提权字段的 bash（advertise，§1.2/§6.1）。
	if b := srv.Tools.Get("bash"); b == nil {
		t.Fatal("bash missing")
	} else if _, ok := b.InputSchema["properties"].(map[string]any)["sandbox_permissions"]; !ok {
		t.Fatal("sandboxed composition must advertise sandbox_permissions")
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 打开 $events，拿 clientId。
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/remote.mux"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "open", "streamId": "ev1", "endpoint": "$events",
		"payload": map[string]any{"args": map[string]any{}}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if json.Unmarshal(data, &ready) != nil || ready["type"] != "ready" {
		t.Fatalf("first frame = %s", data)
	}
	clientID := mustStr(ready["clientId"])

	// 后台发起一次真实审批（模拟 agent 侧 bash 提权请求）。
	outCh := make(chan sandbox.Outcome, 1)
	go func() {
		outCh <- srv.Opts.Sandbox.Approver.Request(context.Background(), sandbox.Request{
			ToolName: "bash", CallID: "call_1", SessionID: "ses_e2e",
			Mode: sandbox.ModeWorkspaceWrite, Justification: "need to write the report",
			Policy: sandbox.ApprovalAsk,
		})
	}()

	// 收 waterfall 帧，校验载荷形状（§6.5）。
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
		t.Fatalf("want approval/request waterfall, got %v", wf)
	}
	if wf["agentId"] != "ses_e2e" {
		t.Errorf("agentId = %v", wf["agentId"])
	}
	req, _ := wf["request"].(map[string]any)
	if req == nil || req["toolName"] != "bash" || req["callId"] != "call_1" {
		t.Errorf("waterfall request payload wrong: %v", req)
	}
	if !strings.Contains(mustStr(req["reason"]), "escalate sandbox to workspace-write") {
		t.Errorf("reason = %q", mustStr(req["reason"]))
	}
	eventID := mustStr(wf["eventId"])

	// 浏览器应答 allowed-once（HTTP 回环，§5.5）。
	resp := postRPC(t, ts, "$events/result", map[string]any{
		"clientId": clientID, "eventId": eventID,
		"outcome": map[string]any{"kind": "result", "value": "allowed-once"},
	})
	if !resultOK(resp) {
		t.Fatalf("$events/result failed: %v", resp)
	}

	select {
	case oc := <-outCh:
		if oc != sandbox.OutcomeAllowedOnce {
			t.Fatalf("approver outcome = %v, want allowed-once", oc)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approver did not settle")
	}
}
