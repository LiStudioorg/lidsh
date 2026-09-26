// headless 入口：无 GUI 跑一次 agent 对话（接受一个用户输入 → 逐步执行
// 工具 → 输出最终回答，事件流落盘到 $LIDSH_HOME）。
//
// 配置来源（M1a，环境变量；settings/credentials 层后续里程碑接入）：
//
//	LIDSH_API_KEY   必需，提供方 API key
//	LIDSH_BASE_URL  默认 https://api.deepseek.com
//	LIDSH_MODEL     默认 deepseek-chat
//	LIDSH_PROVIDER  默认 deepseek-official
//
// 输入：stdin 全文或第一个非 flag 参数；输出：最终 assistant 文本到 stdout；
// 事件与进度到 stderr。
package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lidsh/internal/agent"
	"lidsh/internal/goal"
	"lidsh/internal/llm"
	"lidsh/internal/sandbox"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// RunHeadless 执行一次 headless 对话。
func RunHeadless(ctx context.Context, workdir, home string, args []string) error {
	prompt, err := parseHeadlessPrompt(args)
	if err != nil {
		return err
	}

	apiKey := envOr("LIDSH_API_KEY", "")
	if apiKey == "" {
		return fmt.Errorf("headless: LIDSH_API_KEY is required (set it or pass in .env)")
	}
	baseURL := envOr("LIDSH_BASE_URL", "https://api.deepseek.com")
	model := envOr("LIDSH_MODEL", "deepseek-chat")
	provider := envOr("LIDSH_PROVIDER", "deepseek-official")

	adapter := llm.NewOpenAI(llm.OpenAIConfig{
		Provider:         provider,
		BaseURL:          baseURL,
		APIKey:           apiKey,
		SessionIDHeader:  "x-deepseek-harness-session-id",
		SupportsThinking: true, // DeepSeek 形态
	})

	return runHeadlessCore(ctx, home, workdir, provider, model, prompt, adapter)
}

// runHeadlessCore 是 headless 的核心执行（可注入 adapter 以测试）：
// 建会话 → 事件流 zstd 落盘 → 跑 agent → 最终回答写到 out。
func runHeadlessCore(ctx context.Context, home, workdir, provider, model, prompt string, adapter llm.Adapter) error {
	// 会话目录 & 持久化。
	id := session.NewID()
	dir := session.SessionDir(home, workdir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("headless: mkdir %s: %w", dir, err)
	}
	lease, err := session.AcquireLease(dir)
	if err != nil {
		return fmt.Errorf("headless: %w", err)
	}
	defer lease.Release()

	hdr := session.NewHeader(id, workdir, session.Meta{AgentPreset: "standard"})
	sess := session.New(hdr)

	// 写 header 帧，事件追加落盘。
	logPath := filepath.Join(dir, session.LogFileName(session.FormatVersion, "zstd"))
	lw, err := session.OpenLogWriter(logPath, "zstd", 0)
	if err != nil {
		return fmt.Errorf("headless: %w", err)
	}
	defer lw.Close()
	hb, err := json.Marshal(hdr)
	if err != nil {
		return err
	}
	if err := lw.Append([][]byte{hb}); err != nil {
		return fmt.Errorf("headless: write header: %w", err)
	}
	sess.OnEvent(func(e *session.Event) {
		_ = lw.AppendEvent(e)
	})

	// 工具：bash + 文件工具（挂沙箱时 advertise 提权字段，§1.2/§6.1）。
	reg := tools.NewRegistry()
	sb := buildSandboxConfig(workdir, true)
	if sb != nil && sb.Mode != sandbox.ModeDangerFullAccess {
		tools.RegisterBashSandbox(reg)
	} else {
		tools.RegisterBash(reg)
	}
	tools.RegisterFSTools(reg)

	// goal：服务 + 三工具 + 续轮驱动（headless 宿主：busy 标志 + idle 钩子）。
	goalSvc, err := goal.NewService(sess, goal.DefaultBlockedAfter, goal.DefaultMaxGoalRounds)
	if err != nil {
		return fmt.Errorf("headless: goal fold: %w", err)
	}
	goal.RegisterGoalTools(reg, func(ec *tools.ExecContext) *goal.Service {
		if ec == nil || ec.Ctx == nil || ec.Ctx.SessionID != string(sess.Header.ID) {
			return nil
		}
		return goalSvc
	})

	a := agent.New(agent.Options{
		Sess:     sess,
		Resolver: agent.NewStaticResolver(map[string]llm.Adapter{provider: adapter}),
		Tools:    reg, Provider: provider, Model: model,
		Reason:  llm.EffortHigh,
		System:  goalGuidanceHeadless(headlessSystemPrompt(workdir), goal.DefaultBlockedAfter),
		CWD:     workdir,
		Sandbox: sb,
	})
	host := &headlessHost{a: a}
	drv := goal.NewDriver(goalSvc, host)
	host.drv = drv
	defer drv.Stop()
	a.GoalGate = func(msg *llm.Message) bool {
		return !drv.GateCheck(msg.Source.GoalID, msg.Source.Revision, msg.Source.Round)
	}
	// durable admitted 面：goal 消息落盘 → 镜像推进。
	sess.OnEvent(func(e *session.Event) {
		if e.Type != session.EventUserMessage {
			return
		}
		var m llm.Message
		if json.Unmarshal(e.Data, &m) != nil || m.Source.Kind != "goal" {
			return
		}
		goalSvc.AdmitRound(m.Source.GoalID, m.Source.Revision, m.Source.Round)
	})

	// 人工 turn（direct human 权威）。
	res, err := a.Prompt(prompt, "followup")
	host.turnDone(res, err)
	if err != nil {
		return fmt.Errorf("headless: %v", err)
	}
	// goal 续轮：goal/change 与 idle 都经 drv.RequestDrive（svc 已接线）。
	// 等驱动排空（无在途 turn、无预约、非 running）后继续收尾。
	host.drain(drv)
	if res.Kind == "aborted" {
		return fmt.Errorf("headless: aborted: %v", res.Err)
	}
	if res.Kind == "error" && res.Err != nil {
		return fmt.Errorf("headless: %v", res.Err)
	}

	// 输出最终 assistant 文本（surface 最后一条 assistant 消息）。
	text := lastAssistantText(sess.DeriveMessages())
	if text != "" {
		fmt.Println(text)
	}
	if res.Kind == "max-tokens" {
		fmt.Fprintln(os.Stderr, "\n(reached max tokens)")
	}
	return nil
}

// lastAssistantText 取 surface 里最后一条 assistant 文本块拼接。
func lastAssistantText(msgs []llm.Message) string {
	var last string
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			var sb strings.Builder
			for _, blk := range m.Content {
				if blk.Type == "text" {
					sb.WriteString(blk.Text)
				}
			}
			last = sb.String()
		}
	}
	return last
}

// headlessSystemPrompt 是一条最小系统提示，指明工作目录与工具能力。
func headlessSystemPrompt(workdir string) string {
	return fmt.Sprintf("You are lidsh, a coding agent. Work in directory %s. "+
		"You have shell and file tools; use them, then give a final answer. "+
		"Be concise and show relevant output.", workdir)
}

// goalGuidanceHeadless 并入 goal policy section（systemPrompt.section 对应物）。
func goalGuidanceHeadless(base string, blockedAfter int) string {
	return base + "\n\n" + goal.Guidance(blockedAfter)
}

// headlessHost 是 goal driver 的单 agent 宿主（busy 标志 + idle 回灌）。
type headlessHost struct {
	a        *agent.Agent
	drv      *goal.Driver
	mu       sync.Mutex
	busy     bool
	goalTurn bool
}

func (h *headlessHost) IdleAndLive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.busy
}

func (h *headlessHost) RunRound(content string, src *tools.GoalRoundSource) error {
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		return fmt.Errorf("headless host is busy")
	}
	h.busy = true
	h.goalTurn = true
	h.mu.Unlock()
	go func() {
		res, err := h.a.GoalRoundPrompt(content, src)
		h.turnDone(res, err)
	}()
	return nil
}

// turnDone 是 idle 钩子（agent/status=idle + turn/end 合并面）。
func (h *headlessHost) turnDone(res *agent.Result, err error) {
	kind := "completed"
	if res != nil && res.Kind != "" {
		kind = res.Kind
	} else if err != nil {
		kind = "error"
	}
	h.mu.Lock()
	h.busy = false
	goalTurn := h.goalTurn
	h.goalTurn = false
	h.mu.Unlock()
	if h.drv != nil {
		h.drv.OnTurnEnd(goalTurn, kind)
	}
}

// drain 等 goal 续轮排空：宿主空闲且驱动静默（无请求/无在飞/无预约）。
func (h *headlessHost) drain(drv *goal.Driver) {
	for {
		h.mu.Lock()
		busy := h.busy
		h.mu.Unlock()
		if !busy && drv.Quiet() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// parseHeadlessPrompt：args 里第一个非 flag token 是提示词；无则读 stdin 全文。
func parseHeadlessPrompt(args []string) (string, error) {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a, nil
		}
	}
	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return "", fmt.Errorf("headless: read stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
