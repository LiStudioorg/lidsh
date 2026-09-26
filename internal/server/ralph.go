// ralph 的 server 装配：fresh provider（SpawnFresh）+ 固定 ralph 工具。
//
// fresh 语义（subagent-workflow.md §5.1 requireFreshProvider）：子代理在
// 独立会话（Origin=subagent，不继承父事件）上跑，工具集 = 父集收缩
// （删 ralph/goal 工具）+ structured_output（outputSchema 能力）；
// structured_output 被调用 = 子轮结论（ConcludesTurn），参数即结构化报告。
package server

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"lidsh/internal/agent"
	"lidsh/internal/llm"
	"lidsh/internal/ralph"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// structuredOutputToolName 是 fresh child 的结构化出口（workflow 引擎
// 注入的同名工具，STRUCTURED_OUTPUT）。
const structuredOutputToolName = "structured_output"

// spawnFresh 实现 tools.RalphLauncher：一个全新子会话 + 一次性 agent。
func (s *Server) spawnFresh(req tools.RalphSpawnRequest) tools.RalphSpawnOutcome {
	dir, err := os.MkdirTemp("", "lidsh-ralph-")
	if err != nil {
		return tools.RalphSpawnOutcome{FailReason: "ralph child session dir failed: " + err.Error()}
	}
	// 子会话不持久化（内存态；fresh worker 的事实面 = 报告本身）。
	hdr := session.NewHeader(session.NewID(), dir, session.Meta{
		AgentPreset: "standard", Origin: "subagent", DelegationDepth: 1,
	})
	sess := session.New(hdr)

	// fresh 工具集：父集收缩 + structured_output。
	reg := s.Tools.Copy()
	for _, n := range []string{"ralph", "create_goal", "get_goal", "update_goal"} {
		reg.Drop(n)
	}
	var structMu sync.Mutex
	var structured json.RawMessage
	reg.Register(tools.Definition{
		Name:        structuredOutputToolName,
		Description: "Return the structured result for this run. Calling it concludes the run; its arguments are the final structured value.",
		InputSchema: req.OutSchema,
		Execute: func(args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
			structMu.Lock()
			if structured == nil {
				b, err := json.Marshal(args)
				if err == nil {
					structured = b
				}
			}
			structMu.Unlock()
			return &tools.Result{Content: "structured output recorded", ConcludesTurn: true}, nil
		},
	})

	a := agent.New(agent.Options{
		Sess: sess,
		Resolver: agent.NewStaticResolver(map[string]llm.Adapter{
			s.Opts.Provider: s.Opts.Adapter,
		}),
		Tools:    reg,
		Provider: s.Opts.Provider,
		Model:    s.Opts.Model,
		Reason:   s.Opts.Reason,
		System:   ralphChildSystem(s.Opts.System, dir),
		CWD:      s.Opts.Workdir,
		Sandbox:  s.Opts.Sandbox,
	})
	a.Signal = req.Signal

	if _, err := a.Prompt(req.Prompt, "followup"); err != nil {
		return tools.RalphSpawnOutcome{FailReason: "ralph child failed: " + err.Error()}
	}
	structMu.Lock()
	out := structured
	structMu.Unlock()
	if out == nil {
		return tools.RalphSpawnOutcome{FailReason: "Ralph child returned no structured round report"}
	}
	return tools.RalphSpawnOutcome{Structured: out}
}

// ralphChildSystem：fresh 子代理 system prompt = 父 system + structured 约束
// （DSH 侧由 workflow 引擎的 structured runtime 注入等价指令）。
func ralphChildSystem(base, workdir string) string {
	return base + "\n\nYou are a fresh child worker with no parent conversation. Your final deliverable is a single structured_output tool call that matches the declared schema exactly; do not end without it."
}

// ralphDescription / ralphGuidance 逐字（dsh-tool-ralph）。
const ralphDescription = "Run a foreground fresh-agent Ralph loop toward one immutable objective. Use only when the direct human explicitly asks for Ralph or fresh-agent iteration. Each round opens a new child with no parent conversation or prior child session; the shared workspace is long-term memory, and only a bounded structured report crosses rounds. The call returns when a worker reports completion or a concrete blocker, or at the round limit. Ordinary long-running same-session work belongs to goal tools."

const ralphGuidance = "Use the ralph tool ONLY when the direct human explicitly asks for a Ralph loop or fresh-agent iterative execution. Each Ralph round starts a fresh child with no conversation seed and uses the shared workspace as durable memory. Completion and blockers are worker reports, not independent evaluation. Use same-session goal tools for ordinary long-running objectives, and plain subagents or workflows for bounded delegation and fan-out."

// registerRalph 注册固定 ralph 工具。
func (s *Server) registerRalph(reg *tools.Registry) {
	cfg := ralph.Config{MaxRounds: ralph.DefaultMaxRounds,
		MaxHandoffChars: ralph.DefaultMaxHandoffChar,
		MaxResultChars:  ralph.DefaultMaxResultChars}
	reg.Register(tools.Definition{
		Name:        "ralph",
		Description: ralphDescription,
		TimeoutMs:   0, // 前台循环：随父 turn 取消（ec.Signal）
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{"type": "string",
					"description": "The immutable completion objective for every fresh Ralph round."},
				"maxRounds": map[string]any{"type": "number",
					"description": "Optional positive safe-integer round cap, bounded by the deployment ceiling."},
			},
			"required": []string{"objective"},
		},
		Execute: func(args map[string]any, ec *tools.ExecContext) (*tools.Result, error) {
			objective, _ := args["objective"].(string)
			maxRounds := 0
			if v, ok := args["maxRounds"]; ok && v != nil {
				n, ok := asRalphInt(v)
				if !ok || n < 1 {
					return nil, &tools.ToolError{Code: "RALPH_INVALID_ROUNDS",
						Msg: "Ralph maxRounds must be a positive safe integer"}
				}
				maxRounds = n
			}
			ctx := context.Background()
			if ec != nil && ec.Signal != nil {
				ctx = ec.Signal
			}
			out, err := ralph.Run(ctx, objective, maxRounds, cfg, ralphLauncher{s})
			if err != nil {
				return nil, err
			}
			if out.Status == "round-failed" {
				return nil, &tools.ToolError{Code: "RALPH_ROUND_FAILED",
					Msg: ralph.RenderRoundFailure(out, cfg.MaxResultChars)}
			}
			value := map[string]any{
				"runId":         "ralph-" + session.NewUUID(),
				"agentsStarted": out.AgentsStart,
				"result":        out,
			}
			return &tools.Result{Content: ralph.RenderResult(out, cfg.MaxResultChars),
				Meta: map[string]any{"value": value}}, nil
		},
		PresentCall: func(args map[string]any) *tools.CallView {
			obj, _ := args["objective"].(string)
			return &tools.CallView{Card: "generic", Title: "ralph", RawInput: obj}
		},
		PresentResult: func(args map[string]any, res *tools.Result) *tools.ResultView {
			return &tools.ResultView{Card: "generic"}
		},
	})
}

type ralphLauncher struct{ s *Server }

func (l ralphLauncher) SpawnFresh(req tools.RalphSpawnRequest) tools.RalphSpawnOutcome {
	return l.s.spawnFresh(req)
}

func asRalphInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

// goalToolGuidanceSection 是 system prompt 组装时并入的 ralph policy 段。
func ralphGuidanceSection() string { return ralphGuidance }
