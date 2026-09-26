// goal 的 server 装配：每会话 goal.Service + round Driver + 事件广播。
//
// 事件面（core-control.md §7.6）：
//   - goal/changed：会话事件流里的投影变化（经 $events 转发给订阅端）；
//   - goal/activation-changed：emit 帧 {type:'emit',event,args:[{sessionId,
//     goal:{id,revision,activation}}]}（进程本地权限变化的实时面）。
package server

import (
	"encoding/json"

	"lidsh/internal/goal"
	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// goalProvider 是工具层的每会话 goal 服务解析器。
func (s *Server) goalProvider(ec *tools.ExecContext) *goal.Service {
	if ec == nil || ec.Ctx == nil || ec.Ctx.SessionID == "" {
		return nil
	}
	s.mu.Lock()
	ent := s.sessions[ec.Ctx.SessionID]
	s.mu.Unlock()
	if ent == nil {
		return nil
	}
	return ent.goalSvc
}

// mountGoal 为 entry 构造 goal 服务与驱动器（会话创建/装载后调用）。
// fold 失败（corrupt 日志）→ 返回错误，调用方拒绝会话。
func (s *Server) mountGoal(ent *entry) error {
	svc, err := goal.NewService(ent.sess, goal.DefaultBlockedAfter, goal.DefaultMaxGoalRounds)
	if err != nil {
		return err
	}
	ent.goalSvc = svc
	d := goal.NewDriver(svc, ent)
	ent.goalDrv = d

	// 投影变化 → goal/changed 事件广播（视图即载荷）。
	svc.OnChange(func(v *goal.View) {
		if v == nil {
			return
		}
		s.broadcastGoalChanged(ent.sess.Header.ID, v)
	})
	// durable admitted 面：goal 续轮 user/message 落盘 → 推进进程镜像
	// （driver 的 attempt.phase='admitted' 检测对应物）。
	ent.sess.OnEvent(func(e *session.Event) {
		if e.Type != session.EventUserMessage {
			return
		}
		var m llm.Message
		if json.Unmarshal(e.Data, &m) != nil || m.Source.Kind != "goal" {
			return
		}
		svc.AdmitRound(m.Source.GoalID, m.Source.Revision, m.Source.Round)
	})
	// 启动即 disarmed；服务构造后请求一次驱动（幂等，未 armed 直接返回）。
	d.RequestDrive()
	return nil
}

// broadcastGoalChanged 把 goal/changed 转发给该会话 follow 订阅者。
func (s *Server) broadcastGoalChanged(sessionID string, v *goal.View) {
	s.emitEvent(sessionID, &session.Event{
		Type: "goal/changed",
		Data: mustRaw(goalViewPayload(v)),
	})
}

// broadcastActivationChanged 发 emit 帧（$events 白名单事件）。
func (s *Server) broadcastActivationChanged(sessionID string, v *goal.View) {
	goalRef := map[string]any{}
	if v != nil {
		goalRef = map[string]any{"id": v.ID, "revision": v.Revision, "activation": v.Activation}
	}
	s.hub.broadcast(map[string]any{
		"type":  "emit",
		"event": "goal/activation-changed",
		"args":  []any{map[string]any{"sessionId": sessionID, "goal": goalRef}},
	})
}

func goalViewPayload(v *goal.View) map[string]any {
	out := map[string]any{
		"id": v.ID, "revision": v.Revision, "objective": v.Objective,
		"phase": v.Phase, "roundsStarted": v.RoundsDone,
		"maxGoalRounds": v.MaxRounds, "activation": v.Activation,
		"createdAt": v.CreatedAt, "updatedAt": v.UpdatedAt,
	}
	if v.Phase == goal.PhaseBlocked {
		out["blockedReason"] = map[string]any{"code": v.BlockedCode, "message": v.BlockedMsg}
	}
	return out
}

// goalGuidance 把 goal 工具 policy section 并入 system prompt
// （systemPrompt.section "tool:goal" 对应物）。
func goalGuidance(base string, blockedAfter int) string {
	return base + "\n\n" + goal.Guidance(blockedAfter)
}

// registerGoalToolsFor 把三个 goal 工具挂进共享注册表（provider 走 server）。
func (s *Server) registerGoalToolsFor(reg *tools.Registry) {
	goal.RegisterGoalTools(reg, s.goalProvider)
}

// GoalViewJSON 暴露给 session/get 之类的展示（M3 前端用）。
func (s *Server) GoalViewJSON(sessionID string) any {
	s.mu.Lock()
	ent := s.sessions[sessionID]
	s.mu.Unlock()
	if ent == nil || ent.goalSvc == nil {
		return nil
	}
	v := ent.goalSvc.View()
	if v == nil {
		return map[string]any{"goal": nil}
	}
	return map[string]any{"goal": goalViewPayload(v)}
}

// ArmGoal/RawAccess 等 GUI 控制面：直接改进程本地权限并广播。
func (s *Server) SetGoalActivation(sessionID, activation string) bool {
	s.mu.Lock()
	ent := s.sessions[sessionID]
	s.mu.Unlock()
	if ent == nil || ent.goalSvc == nil {
		return false
	}
	switch activation {
	case goal.Armed:
		ent.goalSvc.Arm()
	case goal.Disarmed:
		ent.goalSvc.Disarm()
	default:
		return false
	}
	s.broadcastActivationChanged(sessionID, ent.goalSvc.View())
	return true
}
