// 崩溃恢复：resume 时先补尾再继续（复刻 dsh-agent-loop 的 resume 路径，
// core-loop.md §3.9 + core-session.md §1.8）。
package agent

import (
	"encoding/json"

	"lidsh/internal/session"
)

// Resume 恢复一个会话上的 agent 相位。M1a：从事件日志重建 session，补
// interrupted-turn closers，返回可继续的 Agent（停在下一个 turn 边界）。
// 完整的前端 resume 语义（re-enter loop）在 M1b 工作站接入。
func Resume(s *session.Session, opts Options) (*Agent, Phase, error) {
	closers := session.InterruptedTurnClosers(s.Log())
	for _, c := range closers {
		if _, err := s.Append(c.Type, json.RawMessage(c.Data), c.SurfaceOp, c.SourceEventSeqs); err != nil {
			return nil, Phase{}, err
		}
	}
	a := New(opts)
	a.started = true // 已注入过 system/消息，不再重复
	a.phase = turnBoundaryFrom(s.Log())
	return a, a.phase, nil
}

// turnBoundaryFrom 从事件日志推断当前 turn/step 边界（turnBoundary 投影的
// 极小版本，core-loop.md §3.9）。
func turnBoundaryFrom(events []*session.Event) Phase {
	var p Phase
	for _, e := range events {
		switch e.Type {
		case session.EventTurnStart:
			var d session.TurnStartData
			_ = json.Unmarshal(e.Data, &d)
			p.Turn = d.Turn
			p.Step = 0
		case session.EventStepStart:
			var d session.StepData
			_ = json.Unmarshal(e.Data, &d)
			if d.Turn == p.Turn {
				p.Step = d.Step
			}
		case session.EventTurnEnd:
			// turn 收束：相位归零，供下一个 turn 重新开始。
			p.Turn++
			p.Step = 0
		}
	}
	return p
}
