// Package server 复刻 dsh-api-gateway + dsh-api-session-controller/remotes：
// 一条 WS 承载全部远程流（/api/remote.mux），HTTP 承载一元 RPC（/api/*）。
//
// M1b 范围：会话建、prompt、follow 流、$events 事件流的最小闭环。会话当前为
// 内存态（M1a 的 JSONL+zstd 持久化在后续里程碑接回写路径）。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"lidsh/internal/agent"
	"lidsh/internal/compaction"
	"lidsh/internal/goal"
	"lidsh/internal/llm"
	"lidsh/internal/sandbox"
	"lidsh/internal/session"
	"lidsh/internal/tools"

	"github.com/gorilla/websocket"
)

func newUUID() string { return session.NewUUID() }

// Options 是 New 的输入。
type Options struct {
	Home     string
	Workdir  string
	Provider string
	Model    string
	Reason   llm.ReasoningEffort
	System   string
	Adapter  llm.Adapter
	// ContextWindow 是模型容量（compaction 压力换算；0=未知 → 自动压缩
	// 放行，DSH warn-once 面）。
	ContextWindow int
	// Compaction 是压缩配置（nil=不挂压缩引擎）。
	Compaction *compaction.Config
	// Sandbox 是沙箱 standing 配置（M2）；nil 或 danger-full-access=未挂载
	// confinement executor（不 advertise 提权字段，bash 直跑）。
	Sandbox *tools.SandboxOptions
}

// Server 持有一个 host 的全部会话与连接。
type Server struct {
	Opts    Options
	Tools   *tools.Registry
	upgrade websocket.Upgrader

	mu       sync.Mutex
	sessions map[string]*entry

	// 事件→follow 订阅分发。
	subMu sync.Mutex
	subs  map[string]map[*conn]struct{} // sessionID → conn 集

	hub *hub // $events 广播（活跃连接）

	// 在途瀑布表：eventId → waterfall（§5.5 回环）。
	wfMu       sync.Mutex
	waterfalls map[string]*waterfall

	// 已暂存文件收据：sessionID → receiptId → stagedFile（上传→prompt 解析）。
	stageMu sync.Mutex
	staged  map[string]map[string]stagedFile
}

type entry struct {
	sess  *session.Session
	agent *agent.Agent
	// agentOpts 是惰性构造参数（runPrompt/RunRound 共用；M2 goal 续轮可能
	// 先于首个人工 prompt 需要 agent）。
	agentOpts agent.Options
	// 持久化写路径：会话目录、写锁、增量日志（M1b 接回 M1a 的 JSONL+zstd）。
	dir string
	lw  *session.LogWriter

	// goal 装配（M2-2）。
	goalSvc *goal.Service
	goalDrv *goal.Driver

	// busyMu 护 entry 本地状态：busy = 有在途 turn（driver IdleAndLive 谓词）。
	busyMu   sync.Mutex
	busy     bool
	goalTurn bool
	queued   []string // busy 时排队的人工输入（competing input 优先于续轮）

	// compactionCfg 是引擎配置（nil=不挂压缩）。
	compactionCfg *compaction.Config
}

// ensureAgent 惰性构造共享 agent（跨 turn 复用；surface 已含历史）。
func (e *entry) ensureAgent() *agent.Agent {
	e.busyMu.Lock()
	defer e.busyMu.Unlock()
	if e.agent == nil {
		e.agent = agent.New(e.agentOpts)
		if e.agent.Compaction == nil && e.compactionCfg != nil {
			// 引擎在 agent 之后挂（Host=agent 自身）；配置错误 → 不挂载
			// （DSH TargetPressureConfigError 在自动路径 warn-once 的等价面）。
			if eng, err := compaction.NewEngine(e.agent, *e.compactionCfg); err == nil {
				e.agent.Compaction = eng
			}
		}
		if e.goalDrv != nil {
			gd := e.goalDrv
			e.agent.GoalGate = func(msg *llm.Message) bool {
				return !gd.GateCheck(msg.Source.GoalID, msg.Source.Revision, msg.Source.Round)
			}
		}
	}
	return e.agent
}

// StartHuman 提交一次人工 prompt（direct-human 权威）。busy 时排队
// （competing input：人工输入优先于 goal 续轮）。返回是否立即开始。
func (e *entry) StartHuman(text string) bool {
	// /compact：手动压缩命令（dsh-command-compact；args 非空 → USAGE）。
	if trimmed := strings.TrimSpace(text); trimmed == "/compact" || strings.HasPrefix(trimmed, "/compact ") {
		go func() {
			var resultText string
			if strings.TrimSpace(trimmed) != "/compact" {
				resultText = "Usage: /compact (no arguments)"
			} else {
				resultText = e.runCompact()
			}
			// 命令输出作为 plugin notice 落面（command/run|done 的收缩面）。
			_, _ = e.sess.Append(session.EventUserMessage, llm.Message{
				ID: session.NewMessageID(), Role: llm.RoleUser,
				Content: []llm.ContentBlock{{Type: "text", Text: resultText}},
				Source: llm.MessageSource{Kind: "plugin", Plugin: "command-compact",
					Form: "notice", Summary: "compact"},
			}, session.AppendOp(), nil)
			e.driveAfterCommand()
		}()
		return true
	}
	e.busyMu.Lock()
	if e.busy {
		e.queued = append(e.queued, text)
		e.busyMu.Unlock()
		return false
	}
	e.busy = true
	e.busyMu.Unlock()
	go func() {
		res, err := e.ensureAgent().Prompt(text, "followup")
		e.finishTurn(turnKind(res, err))
	}()
	return true
}

// runCompact 执行一次手动压缩并把 command-compact 的逐字文案返回。
func (e *entry) runCompact() string {
	a := e.ensureAgent()
	res, err := a.CompactNow(a.Signal, "cmd-compact")
	if err == nil && res == nil {
		return "No compactable history yet."
	}
	if err == nil {
		return fmt.Sprintf("Compacted %d history items (~%d tokens).",
			len(res.ShadowedSeqs), res.ShadowedTokenCount)
	}
	var mc *compaction.ManualCompactionError
	if errors.As(err, &mc) {
		return compactManualText(mc.Code)
	}
	return err.Error()
}

// compactManualText 是 expectedFailure 的逐字映射。
func compactManualText(code string) string {
	switch code {
	case "busy":
		return "Compaction is unavailable because this process has an active compaction, or the agent is not idle."
	case "cancelled":
		return "Compaction cancelled."
	case "changed":
		return "The history selected for compaction changed before it could be replaced. The conversation is unchanged; the attempt is recorded in the session log."
	case "summary":
		return "Compaction could not produce a useful summary. The conversation is unchanged; the attempt is recorded in the session log."
	case "commit":
		return "Compaction did not finish cleanly; some session history may have changed. Inspect the current session state before retrying."
	case "persistence":
		return "Compaction finished, but the session could not be saved."
	default:
		return "Compaction could not produce a useful summary. The conversation is unchanged; the attempt is recorded in the session log."
	}
}

// driveAfterCommand 让压缩后的空闲面重新驱动 goal 续轮（若有）。
func (e *entry) driveAfterCommand() {
	if d := e.goalDrv; d != nil {
		d.RequestDrive()
	}
}

// Cancel 取消在途 turn（session/cancel 对应物）。
func (e *entry) Cancel() {
	e.busyMu.Lock()
	a := e.agent
	e.busyMu.Unlock()
	if a != nil {
		a.Cancel()
	}
}

// finishTurn 清 busy、回收排队输入，并把 idle 信号交给 goal driver
// （agent/status=idle + turn/end 的合并面）。
func (e *entry) finishTurn(kind string) {
	e.busyMu.Lock()
	goalTurn := e.goalTurn
	e.goalTurn = false
	e.busy = false
	var human string
	if len(e.queued) > 0 {
		human = e.queued[0]
		e.queued = e.queued[1:]
		if human != "" {
			e.busy = true // 排队人工输入立即占位（先于续轮评估）
		}
	}
	d := e.goalDrv
	a := e.agent
	e.busyMu.Unlock()

	if d != nil {
		d.OnTurnEnd(goalTurn, kind)
	}
	if human != "" && a != nil {
		go func() {
			res, err := a.Prompt(human, "followup")
			e.finishTurn(turnKind(res, err))
		}()
	}
}

// errAgentNotReady 触发 driver 的 queue-failed block 路径。
var errAgentNotReady = &agentNotReadyError{}

type agentNotReadyError struct{}

func (*agentNotReadyError) Error() string { return "agent is busy or not constructible" }

// turnKind 归一收束 kind（max-tokens 等驱动 fence 需要）。
func turnKind(res *agent.Result, err error) string {
	if res != nil && res.Kind != "" {
		return res.Kind
	}
	if err != nil {
		return "error"
	}
	return "completed"
}

// ---- goal.RoundHost ----

// IdleAndLive：agent 可构造且当前无在途 turn。
func (e *entry) IdleAndLive() bool {
	e.busyMu.Lock()
	defer e.busyMu.Unlock()
	return !e.busy && e.sess != nil
}

// RunRound 启动一个 goal 续轮 turn（异步；完成经 finishTurn 回灌驱动）。
func (e *entry) RunRound(content string, src *tools.GoalRoundSource) error {
	e.busyMu.Lock()
	if e.busy || e.sess == nil {
		e.busyMu.Unlock()
		return errAgentNotReady
	}
	e.busy = true
	e.goalTurn = true
	e.busyMu.Unlock()

	a := e.ensureAgent()
	go func() {
		res, err := a.GoalRoundPrompt(content, src)
		e.finishTurn(turnKind(res, err))
	}()
	return nil
}

// New 构造服务器。
func New(opts Options) *Server {
	reg := tools.NewRegistry()
	// 沙箱挂载判定（§1.2/§6.1）：挂载 confinement executor 才 advertise
	// sandbox_permissions/justification；danger-full-access/未配置 → 普通直跑。
	if opts.Sandbox != nil && opts.Sandbox.Mode != sandbox.ModeDangerFullAccess {
		tools.RegisterBashSandbox(reg)
	} else {
		tools.RegisterBash(reg)
	}
	tools.RegisterFSTools(reg)
	s := &Server{
		Opts:       opts,
		Tools:      reg,
		upgrade:    websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		sessions:   map[string]*entry{},
		subs:       map[string]map[*conn]struct{}{},
		hub:        newHub(),
		waterfalls: map[string]*waterfall{},
	}
	// M2-2：goal 三工具（provider 按会话解析）+ 固定 ralph 循环工具。
	goal.RegisterGoalTools(reg, s.goalProvider)
	s.registerRalph(reg)
	// 沙箱挂载时绑定瀑布审批通道（浏览器经 $events/result 回环应答，§6.5）。
	if s.Opts.Sandbox != nil && s.Opts.Sandbox.Approver == nil {
		s.Opts.Sandbox.Approver = &waterfallApprover{s}
	}
	return s
}

// Handler 返回根 http.Handler（挂 /api/remote.mux 与 /api/* 一元 RPC）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/remote.mux", s.handleMux)
	mux.HandleFunc("/api/", s.handleAPI) // /api/{namespace}/{method}
	s.registerUpload(mux)                // /api/session/uploadFileBinary
	return mux
}

// ---------- /api/remote.mux ----------

// handleMux 升级为 WS 并进入连接循环（§5.3）。
func (s *Server) handleMux(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := newConn(s, conn)
	s.hub.add(c)
	c.run() // 阻塞：读 open/cancel + 写帧
	s.hub.drop(c)
}

// ---------- /api/{namespace}/{method} 一元 RPC ----------

// handleAPI 派发 POST /api/... 一元 RPC（§5.2）。
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := readAllCapped(r.Body, 1<<20)
	if err != nil {
		writeRPCFail(w, "", "gateway/bad-request", "invalid body")
		return
	}
	var req protocolRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCFail(w, "", "gateway/bad-request", "invalid JSON: "+err.Error())
		return
	}
	// 端点 = URL path 去掉 /api/ 前缀；method 必须一致。
	endpoint := r.URL.Path[len("/api/"):]
	if req.Method != "" && req.Method != endpoint {
		writeRPCFail(w, req.RPCID, "gateway/bad-request", fmt.Sprintf("method %q does not match endpoint %q", req.Method, endpoint))
		return
	}
	res := s.dispatchRPC(endpoint, req)
	writeRPC(w, req.RPCID, res)
}

type protocolRPCRequest struct {
	Type    string          `json:"type"`
	RPCID   string          `json:"rpcId"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}
