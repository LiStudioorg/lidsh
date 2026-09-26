// Package ralph 复刻 dsh-tool-ralph 的前台 fresh-agent 循环
// （core-control/subagent-workflow.md §5.1-§5.3）。部署方持有的固定脚本：
// 模型只供数据（objective/maxRounds），不能改循环、路由、schema 或 handoff
// 校验。每轮一个全新 structured-output 子代（fresh provider：不继承父上下文、
// 不继承前一轮子会话），跨轮只传递有界结构化报告。
package ralph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lidsh/internal/tools"
)

// 配置默认值（dsh-tool-ralph Config :20-25）。
const (
	DefaultMaxRounds      = 256
	DefaultMaxHandoffChar = 16384
	DefaultMaxResultChars = 16384
)

// 部署上限固定 maxRounds=256；resolveMaxRounds 把模型请求钳到上限内。
func resolveMaxRounds(requested, ceiling int) (int, error) {
	v := requested
	if v == 0 {
		v = ceiling
	}
	if v < 1 {
		return 0, fmt.Errorf("Ralph maxRounds must be a positive safe integer")
	}
	if v > ceiling {
		return 0, fmt.Errorf("Ralph maxRounds %d exceeds the deployment ceiling %d", v, ceiling)
	}
	return v, nil
}

// ReportSchema 是固定的子轮报告 schema（reportSchema 逐字）。
var ReportSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status":    map[string]any{"type": "string", "enum": []string{"continue", "complete", "blocked"}},
		"summary":   map[string]any{"type": "string"},
		"evidence":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"nextSteps": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"blocker":   map[string]any{"type": "string"},
	},
	"required":             []string{"status", "summary", "evidence", "nextSteps", "blocker"},
	"additionalProperties": false,
}

// Report 是结构化轮报告。
type Report struct {
	Status    string   `json:"status"`
	Summary   string   `json:"summary"`
	Evidence  []string `json:"evidence"`
	NextSteps []string `json:"nextSteps"`
	Blocker   string   `json:"blocker"`
}

func normalizedText(s string) bool { return s != "" && s == strings.TrimSpace(s) }

func normalizedList(vs []string) bool {
	for _, v := range vs {
		if !normalizedText(v) {
			return false
		}
	}
	return true
}

// validateReport 是固定脚本 validateReport 的逐字移植。
func validateReport(r *Report, maxHandoffChars int) error {
	if r == nil {
		return fmt.Errorf("Ralph child returned no structured round report")
	}
	if !normalizedText(r.Summary) {
		return fmt.Errorf("Ralph round report summary must be non-empty and normalized")
	}
	if !normalizedList(r.Evidence) || !normalizedList(r.NextSteps) {
		return fmt.Errorf("Ralph round report evidence and nextSteps must contain only non-empty normalized strings")
	}
	if r.Blocker != strings.TrimSpace(r.Blocker) {
		return fmt.Errorf("Ralph round report blocker must be a normalized string")
	}
	switch r.Status {
	case "continue":
		if len(r.NextSteps) == 0 || r.Blocker != "" {
			return fmt.Errorf("a continuing Ralph report needs nextSteps and an empty blocker")
		}
	case "complete":
		if len(r.Evidence) == 0 || len(r.NextSteps) != 0 || r.Blocker != "" {
			return fmt.Errorf("a complete Ralph report needs evidence, no nextSteps, and an empty blocker")
		}
	case "blocked":
		if !normalizedText(r.Blocker) {
			return fmt.Errorf("a blocked Ralph report needs a concrete blocker")
		}
	default:
		return fmt.Errorf("Ralph round report status is invalid")
	}
	serialized, _ := json.Marshal(r)
	if len(serialized) > maxHandoffChars {
		return fmt.Errorf("Ralph round report exceeds maxHandoffChars (%d > %d)", len(serialized), maxHandoffChars)
	}
	return nil
}

// renderPrompt 渲染每轮固定 prompt（RALPH_SCRIPT 内 array.join 逐字）。
func renderPrompt(objective string, round, maxRounds int, prior string) string {
	if prior == "" {
		prior = "(none — this is the first round)"
	}
	parts := []string{
		"You are one fresh worker in a foreground Ralph loop. You receive no parent conversation and no prior child session. Do not call the ralph tool: this round already is its worker.",
		"Immutable objective:\n" + objective,
		fmt.Sprintf("Ralph round: %d of %d.", round, maxRounds),
		"The shared workspace and its current working tree are the long-term memory and source of truth. Inspect them before acting, preserve existing work, perform concrete in-scope work, and verify what you change. Treat the previous report only as a bounded handoff; confirm it against the workspace.",
		"Previous structured handoff:\n" + prior,
		"Return one report with exact normalized strings. Use status continue with at least one nextSteps entry while useful work remains; complete only with concrete evidence and no nextSteps; blocked only when no meaningful progress is possible without human input or an external-state change. blocker must be empty unless blocked.",
	}
	return strings.Join(parts, "\n\n")
}

// RunOutcome 是循环终值（workflow terminal value 对应物）。
type RunOutcome struct {
	Status      string  `json:"status"` // complete|blocked|round-failed|budget-limited
	RoundsDone  int     `json:"roundsStarted"`
	Report      *Report `json:"report,omitempty"`
	LastReport  *Report `json:"lastReport,omitempty"`
	AgentsStart int     `json:"agentsStarted"`
	FailReason  string  `json:"failReason,omitempty"` // round-failed 呈现文本
}

// Config 是固定部署配置（resolveConfig 默认值）。
type Config struct {
	MaxRounds       int
	MaxHandoffChars int
	MaxResultChars  int
}

func (c Config) withDefaults() Config {
	if c.MaxRounds == 0 {
		c.MaxRounds = DefaultMaxRounds
	}
	if c.MaxHandoffChars == 0 {
		c.MaxHandoffChars = DefaultMaxHandoffChar
	}
	if c.MaxResultChars == 0 {
		c.MaxResultChars = DefaultMaxResultChars
	}
	return c
}

// Run 执行固定 Ralph 循环（fresh worker per round + bounded handoff）。
func Run(ctx context.Context, objective string, requestedRounds int, cfg Config, l tools.RalphLauncher) (*RunOutcome, error) {
	cfg = cfg.withDefaults()
	maxRounds, err := resolveMaxRounds(requestedRounds, cfg.MaxRounds)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(objective) == "" {
		return nil, fmt.Errorf("Ralph objective must be a non-empty string")
	}

	var previous *Report
	for round := 1; round <= maxRounds; round++ {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		prior := ""
		if previous != nil {
			b, _ := json.Marshal(previous)
			prior = string(b)
		}
		prompt := renderPrompt(objective, round, maxRounds, prior)
		out := l.SpawnFresh(tools.RalphSpawnRequest{
			Signal:    ctx,
			Label:     fmt.Sprintf("Ralph round %d", round),
			Prompt:    prompt,
			OutSchema: ReportSchema,
		})
		if out.Structured == nil {
			// null child → round-failed（脚本 :112-114）。
			fail := out.FailReason
			if fail == "" {
				fail = "Ralph child returned no structured round report"
			}
			return &RunOutcome{Status: "round-failed", RoundsDone: round,
				LastReport: previous, AgentsStart: round, FailReason: fail}, nil
		}
		var rep Report
		if err := json.Unmarshal(out.Structured, &rep); err != nil {
			return nil, fmt.Errorf("Ralph child returned no structured round report")
		}
		if err := validateReport(&rep, cfg.MaxHandoffChars); err != nil {
			return nil, err
		}
		if rep.Status == "complete" {
			return &RunOutcome{Status: "complete", RoundsDone: round,
				Report: &rep, AgentsStart: round}, nil
		}
		if rep.Status == "blocked" {
			return &RunOutcome{Status: "blocked", RoundsDone: round,
				Report: &rep, AgentsStart: round}, nil
		}
		previous = &rep
	}
	return &RunOutcome{Status: "budget-limited", RoundsDone: maxRounds,
		Report: previous, AgentsStart: maxRounds}, nil
}

// boundResult 渲染工具输出文本（maxResultChars 截断，尾部换行 +
// "… [truncated]"）。
func BoundResult(v any, maxChars int) string {
	b, err := json.Marshal(v)
	s := string(b)
	if err != nil {
		s = fmt.Sprintf("%v", v)
	}
	const suffix = "\n… [truncated]"
	if len(s) <= maxChars {
		return s
	}
	if maxChars <= len(suffix) {
		return s[:maxChars]
	}
	return s[:maxChars-len(suffix)] + suffix
}

// BoundText 是 boundResult 的字符串面。DSH 按 JS 字符串长度（UTF-16 码元）
// 计，Go 侧按 rune 近似（notice = "\n… [truncated]"，14 rune）。
func BoundText(text string, maxChars int) string {
	const notice = "\n… [truncated]"
	const noticeLen = 14
	r := []rune(text)
	if len(r) <= maxChars {
		return text
	}
	if maxChars <= noticeLen {
		return string([]rune(notice)[:maxChars])
	}
	return string(r[:maxChars-noticeLen]) + notice
}

// indentJSON 是 JSON.stringify(x, null, 2) 的等价物。
func indentJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "null"
	}
	return string(b)
}

func roundsPhrase(n int) string {
	if n == 1 {
		return "1 round"
	}
	return fmt.Sprintf("%d rounds", n)
}

// RenderResult 渲染固定终局文本（renderResult 逐字；complete/blocked/
// budget-limited 面）。
func RenderResult(o *RunOutcome, maxChars int) string {
	rounds := roundsPhrase(o.RoundsDone)
	var text string
	switch o.Status {
	case "complete":
		text = fmt.Sprintf("Ralph worker reported completion after %s.\nFinal report:\n%s", rounds, indentJSON(o.Report))
	case "blocked":
		text = fmt.Sprintf("Ralph worker reported a blocker after %s.\nFinal report:\n%s", rounds, indentJSON(o.Report))
	case "budget-limited":
		text = fmt.Sprintf("Ralph reached its %s limit; the worker reported work remaining.\nFinal report:\n%s", rounds, indentJSON(o.Report))
	default:
		return ""
	}
	return BoundText(text, maxChars)
}

// RenderRoundFailure 渲染子失败文本（renderRoundFailure 逐字）。
func RenderRoundFailure(o *RunOutcome, maxChars int) string {
	header := fmt.Sprintf("Ralph round %d child failed before producing a structured report.", o.RoundsDone)
	if o.LastReport == nil {
		return BoundText(header+"\nNo previous handoff was available.", maxChars)
	}
	return BoundText(fmt.Sprintf("%s\nLast successful handoff:\n%s", header, indentJSON(o.LastReport)), maxChars)
}
