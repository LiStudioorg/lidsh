// ralph 循环测试：报告 schema 校验、prompt 渲染、循环出口、结果渲染。
package ralph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lidsh/internal/tools"
)

type fakeLauncher struct {
	reports []json.RawMessage // 每轮返回（null = 子失败）
	calls   []tools.RalphSpawnRequest
}

func (f *fakeLauncher) SpawnFresh(req tools.RalphSpawnRequest) tools.RalphSpawnOutcome {
	i := len(f.calls)
	f.calls = append(f.calls, req)
	if i >= len(f.reports) {
		return tools.RalphSpawnOutcome{FailReason: "no more scripted reports"}
	}
	if f.reports[i] == nil {
		return tools.RalphSpawnOutcome{FailReason: "child died"}
	}
	return tools.RalphSpawnOutcome{Structured: f.reports[i]}
}

func reportJSON(t *testing.T, r Report) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var contReport = Report{Status: "continue", Summary: "did work",
	Evidence: []string{"e1"}, NextSteps: []string{"next"}, Blocker: ""}
var doneReport = Report{Status: "complete", Summary: "all done",
	Evidence: []string{"commit abc"}, NextSteps: []string{}, Blocker: ""}
var blockedReport = Report{Status: "blocked", Summary: "stuck",
	Evidence: []string{}, NextSteps: []string{}, Blocker: "need API key"}

func TestRunCompleteAfterContinue(t *testing.T) {
	l := &fakeLauncher{reports: []json.RawMessage{
		reportJSON(t, contReport), reportJSON(t, doneReport)}}
	out, err := Run(context.Background(), "obj", 0, Config{}, l)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "complete" || out.RoundsDone != 2 || out.Report.Summary != "all done" {
		t.Fatalf("outcome %+v", out)
	}
	if len(l.calls) != 2 || l.calls[0].Label != "Ralph round 1" {
		t.Fatalf("labels %+v", l.calls)
	}
	// 第一轮 handoff = (none)；第二轮 = 上轮报告 JSON。
	if !strings.Contains(l.calls[0].Prompt, "Previous structured handoff:\n(none — this is the first round)") {
		t.Fatalf("first prompt: %s", l.calls[0].Prompt)
	}
	if !strings.Contains(l.calls[1].Prompt, "Previous structured handoff:\n"+string(reportJSON(t, contReport))) {
		t.Fatalf("second prompt: %s", l.calls[1].Prompt)
	}
	if !strings.Contains(l.calls[0].Prompt, "Ralph round: 1 of 256.") {
		t.Fatal("default ceiling 256")
	}
}

func TestRunBlockedAndBudgetLimited(t *testing.T) {
	l := &fakeLauncher{reports: []json.RawMessage{reportJSON(t, blockedReport)}}
	out, _ := Run(context.Background(), "obj", 3, Config{}, l)
	if out.Status != "blocked" || out.RoundsDone != 1 || out.Report.Blocker != "need API key" {
		t.Fatalf("blocked outcome %+v", out)
	}

	l2 := &fakeLauncher{reports: []json.RawMessage{
		reportJSON(t, contReport), reportJSON(t, contReport)}}
	out2, _ := Run(context.Background(), "obj", 2, Config{}, l2)
	if out2.Status != "budget-limited" || out2.RoundsDone != 2 || out2.Report == nil {
		t.Fatalf("budget outcome %+v", out2)
	}
}

func TestRunRoundFailed(t *testing.T) {
	l := &fakeLauncher{reports: []json.RawMessage{reportJSON(t, contReport), nil}}
	out, err := Run(context.Background(), "obj", 5, Config{}, l)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "round-failed" || out.RoundsDone != 2 || out.LastReport == nil {
		t.Fatalf("round-failed outcome %+v", out)
	}
	txt := RenderRoundFailure(out, DefaultMaxResultChars)
	if !strings.HasPrefix(txt, "Ralph round 2 child failed before producing a structured report.") ||
		!strings.Contains(txt, "Last successful handoff:") {
		t.Fatalf("failure render: %s", txt)
	}
}

func TestValidateReportRules(t *testing.T) {
	cases := []struct {
		name string
		r    Report
		msg  string
	}{
		{"empty summary", Report{Status: "continue", Summary: "", Evidence: []string{}, NextSteps: []string{"a"}},
			"summary must be non-empty"},
		{"continue without nextSteps", Report{Status: "continue", Summary: "s", Evidence: []string{}, NextSteps: []string{}},
			"needs nextSteps"},
		{"continue with blocker", Report{Status: "continue", Summary: "s", Evidence: []string{}, NextSteps: []string{"a"}, Blocker: "x"},
			"needs nextSteps"},
		{"complete without evidence", Report{Status: "complete", Summary: "s", Evidence: []string{}, NextSteps: []string{}},
			"needs evidence"},
		{"complete with nextSteps", Report{Status: "complete", Summary: "s", Evidence: []string{"e"}, NextSteps: []string{"n"}},
			"needs evidence"},
		{"blocked without blocker", Report{Status: "blocked", Summary: "s", Evidence: []string{}, NextSteps: []string{}},
			"concrete blocker"},
		{"bad status", Report{Status: "done", Summary: "s", Evidence: []string{"e"}, NextSteps: []string{}},
			"status is invalid"},
		{"untrimmed blocker", Report{Status: "blocked", Summary: "s", Evidence: []string{}, NextSteps: []string{}, Blocker: " x "},
			"blocker must be a normalized string"},
	}
	for _, c := range cases {
		err := validateReport(&c.r, 16384)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("%s: want %q got %v", c.name, c.msg, err)
		}
	}
	// maxHandoffChars 上界。
	long := doneReport
	long.Evidence = []string{strings.Repeat("x", 200)}
	err := validateReport(&long, 50)
	if err == nil || !strings.Contains(err.Error(), "exceeds maxHandoffChars") {
		t.Fatalf("oversize: %v", err)
	}
}

func TestResolveMaxRoundsCeiling(t *testing.T) {
	if v, err := resolveMaxRounds(0, 256); err != nil || v != 256 {
		t.Fatalf("default: %d %v", v, err)
	}
	if _, err := resolveMaxRounds(257, 256); err == nil ||
		!strings.Contains(err.Error(), "exceeds the deployment ceiling 256") {
		t.Fatalf("ceiling: %v", err)
	}
	if _, err := resolveMaxRounds(0, 0); err == nil {
		t.Fatal("zero ceiling invalid")
	}
}

func TestRenderResultTexts(t *testing.T) {
	c := &RunOutcome{Status: "complete", RoundsDone: 1, Report: &doneReport}
	if s := RenderResult(c, 16384); !strings.HasPrefix(s,
		"Ralph worker reported completion after 1 round.\nFinal report:\n") {
		t.Fatalf("complete render: %q", s)
	}
	b := &RunOutcome{Status: "blocked", RoundsDone: 3, Report: &blockedReport}
	if s := RenderResult(b, 16384); !strings.Contains(s, "Ralph worker reported a blocker after 3 rounds.") {
		t.Fatalf("blocked render: %q", s)
	}
	// 截断标记（按 rune 长度，对齐 JS 字符串长度语义）。
	tiny := RenderResult(c, 30)
	if !strings.HasSuffix(tiny, "… [truncated]") || len([]rune(tiny)) != 30 {
		t.Fatalf("truncation: %q len=%d", tiny, len([]rune(tiny)))
	}
}

func TestRoundFailedFirstRound(t *testing.T) {
	l := &fakeLauncher{reports: []json.RawMessage{nil}}
	out, _ := Run(context.Background(), "obj", 3, Config{}, l)
	if out.Status != "round-failed" || out.RoundsDone != 1 || out.LastReport != nil {
		t.Fatalf("first-round failure %+v", out)
	}
	if s := RenderRoundFailure(out, 16384); !strings.Contains(s, "No previous handoff was available.") {
		t.Fatalf("first failure render: %q", s)
	}
}

func TestSchemaShape(t *testing.T) {
	// reportSchema 键集逐字。
	m := ReportSchema
	if m["additionalProperties"] != false {
		t.Fatal("additionalProperties false")
	}
	req := fmt.Sprint(m["required"])
	if !strings.Contains(req, "status") || !(strings.Contains(req, "evidence") &&
		strings.Contains(req, "nextSteps") && strings.Contains(req, "summary") &&
		strings.Contains(req, "blocker")) {
		t.Fatalf("required: %s", req)
	}
}
