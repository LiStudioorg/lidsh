// Package server 复刻 dsh-api-gateway + dsh-api-session-controller/remotes：
// 一条 WS 承载全部远程流（/api/remote.mux），HTTP 承载一元 RPC（/api/*）。
//
// M1b 范围：会话建、prompt、follow 流、$events 事件流的最小闭环。会话当前为
// 内存态（M1a 的 JSONL+zstd 持久化在后续里程碑接回写路径）。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"lidsh/internal/agent"
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
	// 持久化写路径：会话目录、写锁、增量日志（M1b 接回 M1a 的 JSONL+zstd）。
	dir string
	lw  *session.LogWriter
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
