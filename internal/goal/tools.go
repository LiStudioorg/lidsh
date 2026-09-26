// goal 工具面：get_goal / create_goal / update_goal（dsh-tool-goal 逐字复刻，
// lib/index.js）。权威链（authority.js）：
//   - goalToolExecution：需要 calling agent（lidsh 以 ec.Ctx != nil 为等价谓词；
//     无 = GOAL_TOOL_AGENT_REQUIRED）。
//   - requireDirectHuman：本 turn 含真人 user/message（DirectHuman 快照）。
//   - completionAuthority：direct-human 或"当前 goal 的精确被准入轮"
//     （GoalRound 归因 id/revision/round 与投影一致）。
//
// 错误文本逐字。
package goal

import (
	"encoding/json"
	"fmt"
	"strings"

	"lidsh/internal/tools"
)

// 逐字描述（tool-goal :110-112,306）。
const (
	createDescription = "Create one persisted same-session completion goal when the current direct human request is a long-running objective that should continue across autonomous goal rounds. You may infer that intent without requiring the user to say \"create a goal\". Do not use this for trivial single-turn work. Execution rejects non-human and subagent authority."
	getDescription    = "Read the current same-session goal, including its exact id/revision, objective, phase, completed continuation rounds, round limit, blocker reason when present, and whether another continuation is armed. Call this before updating a goal."
	updateDescription = "Update the exact current goal revision. edit, pause, and resume require a direct top-level human request. During an automatic continuation of the current goal, complete and blocked are also allowed. blocked is rejected before the configured minimum round count; the model remains responsible for judging that the same condition persisted across those rounds and must explain it in blocked_reason."
)

var updateActions = []string{"edit", "pause", "resume", "complete", "blocked"}

// toolErrf 返回带码的工具执行错误（tools.ToolError 别名通道）。
func toolErrf(code, msg string) error { return &tools.ToolError{Code: code, Msg: msg} }

// boundContextSummary（dsh-llm :21-23 逐字）。
func boundContextSummary(s string) string {
	if len([]rune(s)) <= 120 {
		return s
	}
	r := []rune(s)
	return string(r[:119]) + "…"
}

// provider 解析每会话 goal 服务（nil = 该 ec 无 goal 服务挂载）。
type provider func(ec *tools.ExecContext) *Service

// execution 校验：goal 工具需要 calling agent（ec.Ctx 非空）。
func execution(ec *tools.ExecContext) (*tools.ToolContext, error) {
	if ec == nil || ec.Ctx == nil {
		return nil, toolErrf("GOAL_TOOL_AGENT_REQUIRED", "goal tools require a calling agent")
	}
	return ec.Ctx, nil
}

func requireDirectHuman(tc *tools.ToolContext) error {
	if tc.DirectHuman {
		return nil
	}
	return toolErrf("GOAL_TOOL_AUTHORITY_REQUIRED",
		"this goal operation requires a direct human turn on a top-level agent")
}

type authority struct {
	goalRound bool
}

func completionAuthority(tc *tools.ToolContext, svc *Service) (*authority, error) {
	if tc.DirectHuman {
		return &authority{}, nil
	}
	if tc.GoalRound != nil {
		if v := svc.View(); v != nil &&
			tc.GoalRound.GoalID == v.ID && tc.GoalRound.Revision == v.Revision &&
			tc.GoalRound.Round == v.RoundsDone {
			return &authority{goalRound: true}, nil
		}
	}
	return nil, toolErrf("GOAL_TOOL_AUTHORITY_REQUIRED",
		"complete and blocked require a direct human turn or the current goal round")
}

// hasText / hasRoundCap（tool-goal :200-211）：strict-schema 空串/0 填充视为缺省。
func hasText(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}

func goalText(v any) string {
	s, _ := v.(string)
	return s
}

func hasRoundCap(v any) bool {
	n, ok := asInt(v)
	return ok && n != 0
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// goalRef（tool-goal :213-219 逐字）。
func goalRef(goalID any, revision any) (string, int, error) {
	id, _ := goalID.(string)
	rev, ok := asInt(revision)
	if id == "" || strings.TrimSpace(id) != id || !ok || rev < 1 {
		return "", 0, toolErrf("GOAL_TOOL_INVALID_UPDATE",
			"goal_id must be non-empty and revision must be a positive safe integer")
	}
	return id, rev, nil
}

// goalValue 输出（{goal:null} | {goal:{...}}，GOAL_VALUE_SCHEMA）。
func goalValue(v *View) map[string]any {
	if v == nil {
		return map[string]any{"goal": nil}
	}
	g := map[string]any{
		"id": v.ID, "revision": v.Revision, "objective": v.Objective,
		"phase": v.Phase, "roundsStarted": v.RoundsDone,
		"maxGoalRounds": v.MaxRounds, "activation": v.Activation,
	}
	if v.Phase == PhaseBlocked {
		g["blockedReason"] = map[string]any{"code": v.BlockedCode, "message": v.BlockedMsg}
	}
	return map[string]any{"goal": g}
}

func renderGoalValue(v *View) string {
	b, err := json.Marshal(goalValue(v))
	if err != nil {
		return `{"goal":null}`
	}
	return string(b)
}

// present 卡片（generic，args-only pending presentation）。
func present(title, kind, rawInput string) *tools.CallView {
	cv := &tools.CallView{Card: "generic", Title: title, Kind: kind}
	if rawInput != "" {
		cv.RawInput = rawInput
	}
	return cv
}

// RegisterGoalTools 注册三个 goal 工具。provider 每会话解析 goal 服务。
func RegisterGoalTools(reg *tools.Registry, provider provider) func() {
	var offs []func()

	offs = append(offs, reg.Register(tools.Definition{
		Name:              "get_goal",
		Description:       getDescription,
		InputSchema:       map[string]any{"type": "object", "properties": map[string]any{}},
		IsConcurrencySafe: func(map[string]any) bool { return true },
		Execute: func(args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
			if _, err := execution(ec); err != nil {
				return nil, err
			}
			svc := provider(ec)
			if svc == nil {
				return nil, toolErrf("GOAL_AGENT_NOT_LIVE", "no goal service is mounted for this session")
			}
			return &tools.Result{Content: renderGoalValue(svc.View())}, nil
		},
		PresentCall: func(map[string]any) *tools.CallView {
			return present("Read current goal", "read", "")
		},
	}))

	offs = append(offs, reg.Register(tools.Definition{
		Name:        "create_goal",
		Description: createDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{"type": "string",
					"description": "The concrete completion objective inferred from the direct human request."},
				"max_goal_rounds": map[string]any{"type": "number",
					"description": "Optional positive safe-integer limit on automatic continuation rounds."},
			},
			"required": []string{"objective"},
		},
		Execute: func(args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
			tc, err := execution(ec)
			if err != nil {
				return nil, err
			}
			if err := requireDirectHuman(tc); err != nil {
				return nil, err
			}
			svc := provider(ec)
			if svc == nil {
				return nil, toolErrf("GOAL_AGENT_NOT_LIVE", "no goal service is mounted for this session")
			}
			objective, _ := args["objective"].(string)
			maxRounds := 0
			if v, ok := args["max_goal_rounds"]; ok && v != nil {
				n, ok := asInt(v)
				if !ok || n < 1 {
					return nil, &goalError{Code: CodeInvalidMaxRounds,
						Msg: "maxGoalRounds must be a positive safe integer"}
				}
				maxRounds = n
			}
			view, err := svc.Create(objective, maxRounds)
			if err != nil {
				return nil, err
			}
			return &tools.Result{Content: renderGoalValue(view)}, nil
		},
		PresentCall: func(args map[string]any) *tools.CallView {
			obj, _ := args["objective"].(string)
			return present("Create goal", "other", obj)
		},
	}))

	offs = append(offs, reg.Register(tools.Definition{
		Name:        "update_goal",
		Description: updateDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"goal_id": map[string]any{"type": "string",
					"description": "Exact id returned by get_goal."},
				"revision": map[string]any{"type": "number",
					"description": "Exact positive revision returned by get_goal."},
				"action": map[string]any{"type": "string", "enum": updateActions,
					"description": "edit | pause | resume | complete | blocked"},
				"objective": map[string]any{"type": "string",
					"description": "Replacement objective; valid only with action edit."},
				"max_goal_rounds": map[string]any{"type": "number",
					"description": "Replacement cap; valid only with action edit."},
				"blocked_reason": map[string]any{"type": "string",
					"description": "Concrete blocking condition; required only with action blocked."},
			},
			"required": []string{"goal_id", "revision", "action"},
		},
		Execute: func(args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
			tc, err := execution(ec)
			if err != nil {
				return nil, err
			}
			svc := provider(ec)
			if svc == nil {
				return nil, toolErrf("GOAL_AGENT_NOT_LIVE", "no goal service is mounted for this session")
			}
			id, rev, err := goalRef(args["goal_id"], args["revision"])
			if err != nil {
				return nil, err
			}
			action, _ := args["action"].(string)
			if !contains(updateActions, action) {
				return nil, toolErrf("GOAL_TOOL_INVALID_UPDATE",
					fmt.Sprintf("unknown goal action %q", action))
			}

			switch action {
			case "edit":
				if err := requireDirectHuman(tc); err != nil {
					return nil, err
				}
				if hasText(args["blocked_reason"]) {
					return nil, toolErrf("GOAL_TOOL_INVALID_UPDATE",
						"blocked_reason is valid only with action blocked")
				}
				var objective *string
				var maxRounds *int
				if hasText(args["objective"]) {
					o := goalText(args["objective"])
					objective = &o
				}
				if hasRoundCap(args["max_goal_rounds"]) {
					n, _ := asInt(args["max_goal_rounds"])
					maxRounds = &n
				}
				view, err := svc.Edit(id, rev, objective, maxRounds)
				if err != nil {
					return nil, err
				}
				return &tools.Result{Content: renderGoalValue(view)}, nil

			case "pause", "resume":
				if err := requireDirectHuman(tc); err != nil {
					return nil, err
				}
				if hasText(args["objective"]) || hasRoundCap(args["max_goal_rounds"]) || hasText(args["blocked_reason"]) {
					return nil, toolErrf("GOAL_TOOL_INVALID_UPDATE",
						"objective and max_goal_rounds are valid only with action edit; blocked_reason is valid only with action blocked")
				}
				if action == "resume" {
					if cur := svc.View(); cur != nil && cur.ID == id && cur.Revision == rev && cur.Phase == PhasePaused {
						return nil, toolErrf("GOAL_TOOL_RESUME_PAUSED",
							"the model cannot resume a paused goal; the user must resume it")
					}
				}
				var view *View
				if action == "pause" {
					view, err = svc.Pause(id, rev)
				} else {
					view, err = svc.Resume(id, rev)
				}
				if err != nil {
					return nil, err
				}
				return &tools.Result{Content: renderGoalValue(view)}, nil
			}

			// complete / blocked
			auth, err := completionAuthority(tc, svc)
			if err != nil {
				return nil, err
			}
			if hasText(args["objective"]) || hasRoundCap(args["max_goal_rounds"]) {
				return nil, toolErrf("GOAL_TOOL_INVALID_UPDATE",
					"objective and max_goal_rounds are valid only with action edit")
			}
			if action == "complete" && hasText(args["blocked_reason"]) {
				return nil, toolErrf("GOAL_TOOL_INVALID_UPDATE",
					"blocked_reason is valid only with action blocked")
			}
			if action == "blocked" && !hasText(args["blocked_reason"]) {
				return nil, toolErrf("GOAL_TOOL_INVALID_UPDATE",
					"blocked_reason is required with action blocked")
			}
			if action == "blocked" && auth.goalRound {
				if v := svc.View(); v != nil && v.RoundsDone < svc.BlockedAfter() {
					return nil, toolErrf("GOAL_TOOL_BLOCK_THRESHOLD",
						fmt.Sprintf("blocked requires at least %d consecutive goal rounds; current round is %d",
							svc.BlockedAfter(), v.RoundsDone))
				}
			}

			var view *View
			if action == "complete" {
				view, err = svc.Complete(id, rev)
			} else {
				view, err = svc.Block(id, rev, "model-reported", goalText(args["blocked_reason"]))
			}
			if err != nil {
				return nil, err
			}

			res := &tools.Result{Content: renderGoalValue(view)}
			if auth.goalRound {
				// goal-round 权限下的收尾 notice（exec.deferContext →
				// AdditionalContexts；plugin form=notice）。
				body := WrapNotice(view.Objective, "")
				if action == "blocked" {
					body = WrapNotice(view.Objective, goalText(args["blocked_reason"]))
				}
				res.AdditionalContexts = []tools.ContextInjection{{
					Text:    body,
					Plugin:  "tool-goal",
					Summary: boundContextSummary(fmt.Sprintf("%s: %s", action, view.Objective)),
				}}
			}
			return res, nil
		},
		PresentCall: func(args map[string]any) *tools.CallView {
			action, _ := args["action"].(string)
			title := "Complete goal"
			switch action {
			case "blocked":
				title = "Mark goal"
			case "edit":
				title = "Edit goal"
			case "pause":
				title = "Pause goal"
			case "resume":
				title = "Resume goal"
			}
			raw := ""
			switch {
			case hasText(args["blocked_reason"]):
				raw = goalText(args["blocked_reason"])
			case hasText(args["objective"]):
				raw = goalText(args["objective"])
			case hasRoundCap(args["max_goal_rounds"]):
				n, _ := asInt(args["max_goal_rounds"])
				raw = fmt.Sprintf("%d", n)
			default:
				raw, _ = args["goal_id"].(string)
			}
			return present(title, "other", raw)
		},
	}))

	return func() {
		for i := len(offs) - 1; i >= 0; i-- {
			offs[i]()
		}
	}
}
