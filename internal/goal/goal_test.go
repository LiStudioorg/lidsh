// goal 包测试：状态机、fold 轮次校验、工具权威面、driver 预约。
package goal

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lidsh/internal/session"
	"lidsh/internal/tools"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	hdr := session.NewHeader(session.NewID(), t.TempDir(), session.Meta{})
	sess := session.New(hdr)
	svc, err := NewService(sess, DefaultBlockedAfter, DefaultMaxGoalRounds)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestCreateEditPauseResume(t *testing.T) {
	svc := newTestService(t)

	v, err := svc.Create("ship M2", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v.Revision != 1 || v.Phase != PhaseActive || v.Activation != Armed || v.RoundsDone != 0 {
		t.Fatalf("unexpected view %+v", v)
	}
	if v.MaxRounds != DefaultMaxGoalRounds {
		t.Fatalf("default max rounds: %d", v.MaxRounds)
	}

	// edit bump revision。
	v, err = svc.Edit(v.ID, v.Revision, strptr("ship M2+M3"), nil)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if v.Revision != 2 || v.Objective != "ship M2+M3" || v.Phase != PhaseActive {
		t.Fatalf("unexpected edit view %+v", v)
	}

	// 过期 ref → GOAL_STALE_REVISION。
	if _, err := svc.Edit(v.ID, 1, strptr("x"), nil); ErrCode(err) != CodeStaleRevision {
		t.Fatalf("stale edit want GOAL_STALE_REVISION, got %v", err)
	}

	// edit 不允许空替换。
	if _, err := svc.Edit(v.ID, v.Revision, nil, nil); ErrCode(err) != CodeInvalidEdit {
		t.Fatalf("empty edit want GOAL_INVALID_EDIT, got %v", err)
	}

	// pause → paused + disarmed。
	v, err = svc.Pause(v.ID, v.Revision)
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if v.Phase != PhasePaused || svc.Activation() != Disarmed {
		t.Fatalf("pause view %+v act=%s", v, svc.Activation())
	}

	// resume → active + armed。
	v, err = svc.Resume(v.ID, v.Revision)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if v.Phase != PhaseActive || svc.Activation() != Armed {
		t.Fatalf("resume view %+v act=%s", v, svc.Activation())
	}

	// 重复 resume（active+armed）→ GOAL_INVALID_TRANSITION。
	if _, err := svc.Resume(v.ID, v.Revision); ErrCode(err) != CodeInvalidTransition {
		t.Fatalf("re-resume want GOAL_INVALID_TRANSITION got %v", err)
	}
}

func TestDuplicateCreateBlocked(t *testing.T) {
	svc := newTestService(t)
	v, _ := svc.Create("a", 0)
	if _, err := svc.Create("b", 0); ErrCode(err) != CodeAlreadyExists {
		t.Fatalf("dup create want GOAL_ALREADY_EXISTS got %v", err)
	}
	// complete 后可替换。
	if _, err := svc.Complete(v.ID, v.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create("b", 0); err != nil {
		t.Fatalf("create after complete: %v", err)
	}
}

func TestBlockAndCompleteTransitions(t *testing.T) {
	svc := newTestService(t)
	v, _ := svc.Create("obj", 0)

	// block 需要合法 code/message。
	if _, err := svc.Block(v.ID, v.Revision, "Bad Code", "msg"); ErrCode(err) != CodeInvalidBlockReason {
		t.Fatalf("bad code want GOAL_INVALID_BLOCK_REASON got %v", err)
	}
	v, err := svc.Block(v.ID, v.Revision, "model-reported", "waiting for user")
	if err != nil {
		t.Fatal(err)
	}
	if v.Phase != PhaseBlocked || v.BlockedCode != "model-reported" || svc.Activation() != Disarmed {
		t.Fatalf("block view %+v act=%s", v, svc.Activation())
	}
	// 已 blocked 不能再 block。
	if _, err := svc.Block(v.ID, v.Revision, "model-reported", "again"); ErrCode(err) != CodeInvalidTransition {
		t.Fatalf("re-block want GOAL_INVALID_TRANSITION got %v", err)
	}
	// blocked → complete 合法。
	if _, err := svc.Complete(v.ID, v.Revision); err != nil {
		t.Fatalf("complete after blocked: %v", err)
	}
	// complete 不能再 complete（ref 用最新 revision）。
	cur := svc.View()
	if _, err := svc.Complete(cur.ID, cur.Revision); ErrCode(err) != CodeInvalidTransition {
		t.Fatalf("re-complete want GOAL_INVALID_TRANSITION got %v", err)
	}
}

// 折叠恢复：goal 事件 + goal 续轮消息 + 变更后再折叠，投影一致。
func TestFoldRebuildRoundAccounting(t *testing.T) {
	hdr := session.NewHeader(session.NewID(), t.TempDir(), session.Meta{})
	sess := session.New(hdr)
	svc, err := NewService(sess, DefaultBlockedAfter, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create("obj", 3); err != nil {
		t.Fatal(err)
	}

	// 模拟 driver：round1 落盘（gate + durable user/message）。
	if err := admitDriverRound(t, sess, svc, 1); err != nil {
		t.Fatalf("round 1: %v", err)
	}

	// 从空服务重建（fold）：svc2 接管后续（单写者镜像）。
	svc2, err := NewService(sess, DefaultBlockedAfter, 3)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	v2 := svc2.View()
	if v2 == nil || v2.RoundsDone != 1 || v2.Activation != Disarmed {
		t.Fatalf("folded view %+v", v2)
	}
	// fold 后是 disarmed，需要显式 arm（GUI/人工 resume 面）。
	svc2.Arm()
	if err := admitDriverRound(t, sess, svc2, 2); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if err := admitDriverRound(t, sess, svc2, 3); err != nil {
		t.Fatalf("round 3: %v", err)
	}
	if _, _, ok := svc2.ReserveNextRound(); ok {
		t.Fatal("round 4 must not be reservable (max=3)")
	}
	// 轮次用尽 → ReserveNextRound 已写 block('round-limit')。
	if v := svc2.View(); v.Phase != PhaseBlocked || v.BlockedCode != "round-limit" {
		t.Fatalf("want round-limit blocked, got %+v", v)
	}
	// fold 一致性：再建服务重建同一投影。
	svc3, err := NewService(sess, DefaultBlockedAfter, 3)
	if err != nil {
		t.Fatalf("fold 2: %v", err)
	}
	if v := svc3.View(); v.RoundsDone != 3 || v.Phase != PhaseBlocked || v.BlockedCode != "round-limit" {
		t.Fatalf("folded final view %+v", v)
	}
}

// 模拟 driver：预约 + gate + goal 消息落盘 + 镜像推进。
func admitDriverRound(t *testing.T, sess *session.Session, svc *Service, round int) error {
	t.Helper()
	r, view, ok := svc.ReserveNextRound()
	if !ok || r != round {
		return fmt.Errorf("reserve got %d,%v want %d", r, ok, round)
	}
	if !svc.GateCheck(view.ID, view.Revision, round) {
		return fmt.Errorf("gate rejected round %d", round)
	}
	msg := sessionMessageFor(view, round)
	if _, err := sess.Append(session.EventUserMessage, msg, session.AppendOp(), nil); err != nil {
		return err
	}
	svc.AdmitRound(view.ID, view.Revision, round)
	return nil
}

func sessionMessageFor(v *View, round int) llmMessageShim {
	return llmMessageShim{ID: session.NewMessageID(), Role: "user",
		Content: []map[string]any{{"type": "text", "text": PromptText(v.Objective, round, v.MaxRounds)}},
		Source: map[string]any{"kind": "goal", "goalId": v.ID,
			"revision": v.Revision, "round": round}}
}

func TestFoldRejectsWrongRound(t *testing.T) {
	// 手写 corrupt 日志：goal 消息 round=2 但 roundsStarted=0。
	hdr := session.NewHeader(session.NewID(), t.TempDir(), session.Meta{})
	sess := session.New(hdr)
	svc, _ := NewService(sess, DefaultBlockedAfter, 5)
	v, _ := svc.Create("obj", 5)

	bad := llmMessageShim{ID: session.NewMessageID(), Role: "user",
		Content: []map[string]any{{"type": "text", "text": "x"}},
		Source:  map[string]any{"kind": "goal", "goalId": v.ID, "revision": v.Revision, "round": 7}}
	if _, err := sess.Append(session.EventUserMessage, bad, session.AppendOp(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(sess, DefaultBlockedAfter, 5); err == nil {
		t.Fatal("fold must reject non-consecutive goal round")
	}
}

func TestResumeAfterExhausted(t *testing.T) {
	svc := newTestService(t)
	v, _ := svc.Create("obj", 1)
	if _, _, ok := svc.ReserveNextRound(); !ok {
		t.Fatal("round 1 reservable")
	}
	// 模拟落盘 round1（耗尽）；重启面（disarmed）后 resume 报预算耗尽。
	msg := sessionMessageFor(v, 1)
	_, _ = svc.sess.Append(session.EventUserMessage, msg, session.AppendOp(), nil)
	svc.AdmitRound(v.ID, v.Revision, 1)
	svc.Disarm()

	if _, err := svc.Resume(v.ID, v.Revision); err == nil ||
		!strings.Contains(err.Error(), "exhausted 1 goal rounds") {
		t.Fatalf("want exhausted message, got %v", err)
	}
	// edit 提高 cap 后可 resume。
	if _, err := svc.Edit(v.ID, v.Revision, nil, intptr(2)); err != nil {
		t.Fatal(err)
	}
	v2 := svc.View()
	if _, err := svc.Resume(v2.ID, v2.Revision); err != nil {
		t.Fatalf("resume after cap raise: %v", err)
	}
}

// ---- 工具层权威面 ----

func execCtx(directHuman bool, gr *tools.GoalRoundSource) *tools.ExecContext {
	return &tools.ExecContext{Ctx: &tools.ToolContext{
		SessionID: "s", DirectHuman: directHuman, GoalRound: gr}}
}

func runTool(t *testing.T, svc *Service, name string, args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
	t.Helper()
	reg := tools.NewRegistry()
	RegisterGoalTools(reg, func(*tools.ExecContext) *Service { return svc })
	res, err := reg.Execute(tools.ToolCall{CallID: "c1", Name: name,
		Arguments: string(mustJSON(t, args))}, ec)
	return res, err
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToolCreateRequiresDirectHuman(t *testing.T) {
	svc := newTestService(t)
	if _, err := runTool(t, svc, "create_goal",
		map[string]any{"objective": "x"}, execCtx(false, nil)); toolsErrCode(err) != "GOAL_TOOL_AUTHORITY_REQUIRED" {
		t.Fatalf("non-human create want authority error got %v", err)
	}
	if _, err := runTool(t, svc, "create_goal",
		map[string]any{"objective": "x"}, execCtx(true, nil)); err != nil {
		t.Fatalf("human create: %v", err)
	}
}

func TestToolNoCallingAgent(t *testing.T) {
	svc := newTestService(t)
	if _, err := runTool(t, svc, "get_goal", nil, &tools.ExecContext{}); toolsErrCode(err) != "GOAL_TOOL_AGENT_REQUIRED" {
		t.Fatalf("want GOAL_TOOL_AGENT_REQUIRED got %v", err)
	}
}

func TestToolModelCannotResumePaused(t *testing.T) {
	svc := newTestService(t)
	v, _ := svc.Create("obj", 0)
	if _, err := runTool(t, svc, "update_goal",
		map[string]any{"goal_id": v.ID, "revision": v.Revision, "action": "pause"},
		execCtx(true, nil)); err != nil {
		t.Fatal(err)
	}
	cur := svc.View()
	_, err := runTool(t, svc, "update_goal",
		map[string]any{"goal_id": cur.ID, "revision": cur.Revision, "action": "resume"},
		execCtx(true, nil))
	if toolsErrCode(err) != "GOAL_TOOL_RESUME_PAUSED" {
		t.Fatalf("want GOAL_TOOL_RESUME_PAUSED got %v", err)
	}
}

func TestToolCompleteAuthorityAndNotice(t *testing.T) {
	svc := newTestService(t)
	v, _ := svc.Create("obj", 10)

	// 非 human 非 goal-round → 拒绝。
	if _, err := runTool(t, svc, "update_goal",
		map[string]any{"goal_id": v.ID, "revision": v.Revision, "action": "complete"},
		execCtx(false, nil)); toolsErrCode(err) != "GOAL_TOOL_AUTHORITY_REQUIRED" {
		t.Fatalf("want authority error got %v", err)
	}

	// goal-round：预约 + 消息落盘（镜像推进 roundsStarted=1）后归因 complete。
	round, view, ok := svc.ReserveNextRound()
	if !ok || round != 1 {
		t.Fatalf("reserve %d %v", round, ok)
	}
	svc.AdmitRound(view.ID, view.Revision, round)
	res, err := runTool(t, svc, "update_goal",
		map[string]any{"goal_id": view.ID, "revision": view.Revision, "action": "complete"},
		execCtx(false, &tools.GoalRoundSource{GoalID: view.ID, Revision: view.Revision, Round: round}))
	if err != nil {
		t.Fatalf("goal-round complete: %v", err)
	}
	if len(res.AdditionalContexts) != 1 ||
		!strings.HasPrefix(res.AdditionalContexts[0].Text, "<goal_complete>") ||
		res.AdditionalContexts[0].Plugin != "tool-goal" {
		t.Fatalf("want wrapup notice, got %+v", res.AdditionalContexts)
	}
	if svc.View().Phase != PhaseComplete {
		t.Fatalf("phase after complete: %+v", svc.View())
	}
}

func TestToolBlockedThreshold(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Create("obj", 10); err != nil {
		t.Fatal(err)
	}
	// goal-round：预约 + 镜像推进到 round1（authority 与轮内视图一致）。
	round, view, _ := svc.ReserveNextRound()
	svc.AdmitRound(view.ID, view.Revision, round)

	// roundsStarted=1 < blockedAfter=3 → 拒绝。
	_, err := runTool(t, svc, "update_goal",
		map[string]any{"goal_id": view.ID, "revision": view.Revision, "action": "blocked",
			"blocked_reason": "stuck"},
		execCtx(false, &tools.GoalRoundSource{GoalID: view.ID, Revision: view.Revision, Round: round}))
	if toolsErrCode(err) != "GOAL_TOOL_BLOCK_THRESHOLD" {
		t.Fatalf("want GOAL_TOOL_BLOCK_THRESHOLD got %v", err)
	}
	if !strings.Contains(err.Error(), "at least 3 consecutive goal rounds") {
		t.Fatalf("threshold message: %v", err)
	}

	// 推进到 roundsStarted=3 后 blocked 放行且写 notice。
	svc.AdmitRound(view.ID, view.Revision, 2)
	svc.AdmitRound(view.ID, view.Revision, 3)
	res, err := runTool(t, svc, "update_goal",
		map[string]any{"goal_id": view.ID, "revision": view.Revision, "action": "blocked",
			"blocked_reason": "waiting for key"},
		execCtx(false, &tools.GoalRoundSource{GoalID: view.ID, Revision: view.Revision, Round: 3}))
	if err != nil {
		t.Fatalf("blocked after threshold: %v", err)
	}
	if len(res.AdditionalContexts) != 1 ||
		!strings.HasPrefix(res.AdditionalContexts[0].Text, "<goal_blocked>") {
		t.Fatalf("want blocked wrapup, got %+v", res.AdditionalContexts)
	}
}

func TestToolUpdateArgValidation(t *testing.T) {
	svc := newTestService(t)
	v, _ := svc.Create("obj", 10)

	cases := []struct {
		name string
		args map[string]any
		code string
	}{
		{"bad ref", map[string]any{"goal_id": " ", "revision": 1, "action": "pause"}, "GOAL_TOOL_INVALID_UPDATE"},
		{"edit with blocked_reason", map[string]any{"goal_id": v.ID, "revision": v.Revision,
			"action": "edit", "objective": "y", "blocked_reason": "z"}, "GOAL_TOOL_INVALID_UPDATE"},
		{"pause with objective", map[string]any{"goal_id": v.ID, "revision": v.Revision,
			"action": "pause", "objective": "y"}, "GOAL_TOOL_INVALID_UPDATE"},
		{"complete with blocked_reason", map[string]any{"goal_id": v.ID, "revision": v.Revision,
			"action": "complete", "blocked_reason": "z"}, "GOAL_TOOL_INVALID_UPDATE"},
		{"blocked without reason", map[string]any{"goal_id": v.ID, "revision": v.Revision,
			"action": "blocked"}, "GOAL_TOOL_INVALID_UPDATE"},
	}
	for _, c := range cases {
		// 用 direct human 让权限面不干扰参数校验（除 blocked 需要 authority）。
		_, err := runTool(t, svc, "update_goal", c.args, execCtx(true, nil))
		if toolsErrCode(err) != c.code {
			t.Fatalf("%s: want %s got %v", c.name, c.code, err)
		}
	}
}

func TestGoalValueShape(t *testing.T) {
	svc := newTestService(t)
	res, err := runTool(t, svc, "get_goal", nil, execCtx(true, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != `{"goal":null}` {
		t.Fatalf("empty goal value: %s", res.Content)
	}
	v, _ := svc.Create("obj", 5)
	res, _ = runTool(t, svc, "get_goal", nil, execCtx(true, nil))
	var out struct {
		Goal map[string]any `json:"goal"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatal(err)
	}
	if out.Goal["id"] != v.ID || out.Goal["revision"] != float64(1) ||
		out.Goal["phase"] != "active" || out.Goal["activation"] != "armed" ||
		out.Goal["maxGoalRounds"] != float64(5) || out.Goal["roundsStarted"] != float64(0) {
		t.Fatalf("goal value: %s", res.Content)
	}
}

func TestPromptTextAndGuidance(t *testing.T) {
	p := PromptText("say hi", 2, 7)
	if !strings.HasPrefix(p, "<goal_round>\nObjective: \"say hi\"\nRound: 2/7\n\n") ||
		!strings.HasSuffix(p, "\n</goal_round>") {
		t.Fatalf("prompt render: %q", p)
	}
	g := Guidance(3)
	if !strings.Contains(g, "at least 3 consecutive goal rounds") {
		t.Fatal("guidance must interpolate blockedAfter")
	}
	w := WrapNotice("obj", "")
	if !strings.Contains(w, "<goal_complete>") || !strings.Contains(w, `Objective: "obj"`) {
		t.Fatal("wrapnotice complete form")
	}
	wb := WrapNotice("obj", "no key")
	if !strings.Contains(wb, "<goal_blocked>") || !strings.Contains(wb, `Blocked: "no key"`) {
		t.Fatal("wrapnotice blocked form")
	}
}

func toolsErrCode(err error) string {
	if te, ok := err.(*tools.ToolError); ok {
		return te.Code
	}
	return ""
}

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// llmMessageShim 用于直接向 session 写带任意 source 的 user/message。
type llmMessageShim struct {
	ID      string           `json:"id"`
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
	Source  any              `json:"source"`
}
