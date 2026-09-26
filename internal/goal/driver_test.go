// goal 续轮驱动集成测试：create → armed → idle → 自动续轮 → complete
// 全链路（fake adapter 驱动 agent，RoundHost 用最小假宿主）。
package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"lidsh/internal/agent"
	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// fakeAdapter：脚本化流。
type scriptedAdapter struct {
	mu      sync.Mutex
	streams [][]llm.Chunk
}

func (f *scriptedAdapter) Stream(ctx context.Context, opts llm.GenerateOptions) (<-chan llm.Chunk, error) {
	f.mu.Lock()
	var batch []llm.Chunk
	if len(f.streams) > 0 {
		batch = f.streams[0]
		f.streams = f.streams[1:]
	} else {
		// 脚本耗尽：持续"继续干活"文本，直到测试主动收束 goal。
		batch = textChunks("still working")
	}
	f.mu.Unlock()
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		for _, c := range batch {
			select {
			case <-ctx.Done():
				return
			case ch <- c:
			}
		}
	}()
	return ch, nil
}

func (f *scriptedAdapter) Info() llm.ProviderInfo                 { return llm.ProviderInfo{Provider: "fake"} }
func (f *scriptedAdapter) ResolveModel(id string) (string, error) { return id, nil }

func textChunks(text string) []llm.Chunk {
	return []llm.Chunk{
		{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockText},
		{Type: llm.ChunkTextDelta, BlockIndex: 0, Delta: text},
		{Type: llm.ChunkBlockEnd, BlockIndex: 0},
		{Type: llm.ChunkFinish, Finish: llm.FinishStop},
	}
}

func toolCallChunks(id, name, args string) []llm.Chunk {
	return []llm.Chunk{
		{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockToolCall, ToolCallID: id, ToolName: name},
		{Type: llm.ChunkToolCallDelta, BlockIndex: 0, ToolCallID: id, ToolName: name, ArgumentsDelta: args},
		{Type: llm.ChunkBlockEnd, BlockIndex: 0},
		{Type: llm.ChunkFinish, Finish: llm.FinishToolCalls},
	}
}

// testHost 是最小 RoundHost：起 goal turn 并在结束时回灌 driver。
type testHost struct {
	mu       sync.Mutex
	a        *agent.Agent
	drv      *Driver
	busy     bool
	goalTurn bool
	rounds   int
	done     chan struct{}
}

func (h *testHost) IdleAndLive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.a != nil && !h.busy
}

func (h *testHost) RunRound(content string, src *tools.GoalRoundSource) error {
	h.mu.Lock()
	if h.busy || h.a == nil {
		h.mu.Unlock()
		return context.DeadlineExceeded
	}
	h.busy = true
	h.goalTurn = true
	h.rounds++
	a := h.a
	h.mu.Unlock()
	go func() {
		res, err := a.GoalRoundPrompt(content, src)
		kind := "completed"
		if res != nil && res.Kind != "" {
			kind = res.Kind
		} else if err != nil {
			kind = "error"
		}
		h.mu.Lock()
		h.busy = false
		gt := h.goalTurn
		h.goalTurn = false
		h.mu.Unlock()
		h.drv.OnTurnEnd(gt, kind)
		if h.done != nil {
			select {
			case h.done <- struct{}{}:
			default:
			}
		}
	}()
	return nil
}

func (h *testHost) startHuman(a *agent.Agent, text string) string {
	h.mu.Lock()
	h.busy = true // 人工 turn 期间 agent 非 idle（DSH status=running 面）
	h.mu.Unlock()
	res, err := a.Prompt(text, "followup")
	kind := "completed"
	if res != nil && res.Kind != "" {
		kind = res.Kind
	} else if err != nil {
		kind = "error"
	}
	h.mu.Lock()
	h.busy = false
	h.mu.Unlock()
	h.drv.OnTurnEnd(false, kind)
	return kind
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", msg)
}

func TestDriverAutoContinuesToComplete(t *testing.T) {
	hdr := session.NewHeader(session.NewID(), t.TempDir(), session.Meta{})
	sess := session.New(hdr)
	svc, err := NewService(sess, DefaultBlockedAfter, 5)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	RegisterGoalTools(reg, func(*tools.ExecContext) *Service { return svc })

	// 轮脚本：human step1 → create_goal；step2 → 文本收尾。
	fa := &scriptedAdapter{streams: [][]llm.Chunk{
		toolCallChunks("c1", "create_goal", `{"objective":"ship it","max_goal_rounds":5}`),
		textChunks("goal created"),
		// driver round 1：goal-round 权威下走 direct 面 Complete（goal id
		// 脚本期未知），文本收尾。
		textChunks("round 1 closing"),
	}}
	a := agent.New(agent.Options{
		Sess:     sess,
		Resolver: agent.NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools:    reg, Provider: "fake", Model: "m", CWD: "/tmp",
	})
	host := &testHost{a: a, done: make(chan struct{}, 16)}
	drv := NewDriver(svc, host)
	host.drv = drv
	defer drv.Stop()
	a.GoalGate = func(msg *llm.Message) bool {
		return !drv.GateCheck(msg.Source.GoalID, msg.Source.Revision, msg.Source.Round)
	}
	sess.OnEvent(func(e *session.Event) {
		if e.Type != session.EventUserMessage {
			return
		}
		var m llm.Message
		if json.Unmarshal(e.Data, &m) != nil || m.Source.Kind != "goal" {
			return
		}
		svc.AdmitRound(m.Source.GoalID, m.Source.Revision, m.Source.Round)
	})

	// 人工 turn：agent 调 create_goal。
	if kind := host.startHuman(a, "please build it"); kind != "completed" {
		t.Fatalf("human turn kind %s", kind)
	}
	v := svc.View()
	if v == nil || v.Phase != PhaseActive || v.Activation != Armed {
		t.Fatalf("after create: %+v act=%s", v, svc.Activation())
	}

	// 驱动自动续轮（round 1）。goal prompt 逐字含 <goal_round>。
	waitFor(t, func() bool { return svc.View().RoundsDone >= 1 }, "round 1 admitted")

	// goal turn 里模型调 update_goal complete：需要 goal-round 权威 →
	// 真实 goal id 在脚本里未知，改用工具注册表里的通用替换：goal 工具
	// 对未知 id 报 GOAL_STALE_REVISION，这里等 goal turn 自然结束（text）。
	// → 手动 complete（direct human）验证收束。
	waitFor(t, func() bool { return host.rounds >= 1 }, "driver ran a round")
	v = svc.View()
	if _, err := svc.Complete(v.ID, v.Revision); err != nil {
		t.Fatalf("human complete: %v", err)
	}
	waitFor(t, func() bool {
		h := host
		h.mu.Lock()
		b := h.busy
		h.mu.Unlock()
		return !b && drv.Quiet()
	}, "driver quiet after complete")
	if v := svc.View(); v.Phase != PhaseComplete {
		t.Fatalf("final phase %+v", v)
	}
	// goal prompt 落盘逐字（invariant 面：落盘即渲染文本）。
	sawRound := map[int]bool{}
	for _, e := range sess.Log() {
		if e.Type != session.EventUserMessage {
			continue
		}
		var m llm.Message
		if json.Unmarshal(e.Data, &m) == nil && m.Source.Kind == "goal" {
			sawRound[m.Source.Round] = true
			txt := ""
			for _, b := range m.Content {
				txt += b.Text
			}
			want := fmt.Sprintf("<goal_round>\nObjective: \"ship it\"\nRound: %d/5\n\n", m.Source.Round)
			if !strings.HasPrefix(txt, want) || !strings.HasSuffix(txt, "\n</goal_round>") {
				t.Fatalf("goal prompt round %d: %q", m.Source.Round, txt)
			}
			if m.Source.Revision != 1 || m.Source.GoalID != v.ID {
				t.Fatalf("goal source %+v", m.Source)
			}
		}
	}
	if !sawRound[1] {
		t.Fatal("no goal-sourced user/message for round 1 in log")
	}
}

func TestDriverGateRejectDisarms(t *testing.T) {
	hdr := session.NewHeader(session.NewID(), t.TempDir(), session.Meta{})
	sess := session.New(hdr)
	svc, _ := NewService(sess, DefaultBlockedAfter, 5)
	fa := &scriptedAdapter{streams: [][]llm.Chunk{textChunks("x")}}
	a := agent.New(agent.Options{
		Sess:     sess,
		Resolver: agent.NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools:    tools.NewRegistry(), Provider: "fake", Model: "m", CWD: "/tmp",
	})
	host := &testHost{a: a}
	drv := NewDriver(svc, host)
	host.drv = drv
	defer drv.Stop()
	a.GoalGate = func(msg *llm.Message) bool {
		return !drv.GateCheck(msg.Source.GoalID, msg.Source.Revision, msg.Source.Round)
	}

	if _, err := svc.Create("obj", 5); err != nil {
		t.Fatal(err)
	}
	// 未预约直接塞一条伪造 goal 消息 → gate reject → turn blocked + disarm。
	if svc.Activation() != Armed {
		t.Fatalf("create should arm; act=%s", svc.Activation())
	}
	res, err := a.GoalRoundPrompt(PromptText("obj", 1, 5),
		&tools.GoalRoundSource{GoalID: svc.View().ID, Revision: 1, Round: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "blocked" {
		t.Fatalf("unreserved round must block turn, got %s", res.Kind)
	}
	if svc.Activation() != Disarmed {
		t.Fatalf("gate reject must fail-closed disarm, act=%s", svc.Activation())
	}
	// 未落盘：日志里没有 goal-sourced user/message。
	for _, e := range sess.Log() {
		if e.Type == session.EventUserMessage {
			var m llm.Message
			if json.Unmarshal(e.Data, &m) == nil && m.Source.Kind == "goal" {
				t.Fatal("rejected goal round must not persist its message")
			}
		}
	}
}

func TestDriverDisabledUntilArmed(t *testing.T) {
	hdr := session.NewHeader(session.NewID(), t.TempDir(), session.Meta{})
	sess := session.New(hdr)
	// fold 出 active goal（直接写事件）。
	s1, _ := NewService(sess, DefaultBlockedAfter, 5)
	if _, err := s1.Create("obj", 5); err != nil {
		t.Fatal(err)
	}
	fa := &scriptedAdapter{streams: [][]llm.Chunk{textChunks("idle")}}
	a := agent.New(agent.Options{
		Sess:     sess,
		Resolver: agent.NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools:    tools.NewRegistry(), Provider: "fake", Model: "m", CWD: "/tmp",
	})
	host := &testHost{a: a}
	// 重建（服务启动即 disarmed——进程重启面）。
	svc2, err := NewService(sess, DefaultBlockedAfter, 5)
	if err != nil {
		t.Fatal(err)
	}
	drv := NewDriver(svc2, host)
	host.drv = drv
	defer drv.Stop()

	drv.RequestDrive()
	time.Sleep(50 * time.Millisecond)
	if host.roundCount() != 0 {
		t.Fatalf("disarmed goal must not continue: rounds=%d", host.roundCount())
	}
	if v := svc2.View(); v.Phase != PhaseActive {
		t.Fatalf("goal stays active: %+v", v)
	}
}

func (h *testHost) roundCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rounds
}
