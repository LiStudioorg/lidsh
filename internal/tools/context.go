// 工具执行期的调用方上下文与服务挂载（M2 goal/ralph）。
//
// DSH 侧语义（core-control.md §7.3）：goal 工具的执行前置是"calling agent 是
// live 精确实例且为当前 initiator"，权限面 requireDirectHuman = agent ∈
// roots 且当前 turn 内存在 source.kind==='user' 的 user/message；goal 续轮
// 的 turn 携带 GoalMessageSource（kind='goal'）。lidsh 把权威快照与每会话
// 服务以接口挂载进 ExecContext（tools 包不反向依赖 goal/agent，避免环）。
package tools

import (
	"context"
	"encoding/json"
)

// GoalRoundSource 是当前 turn 的 goal 续轮归因（GoalMessageSource 子集）。
type GoalRoundSource struct {
	GoalID   string
	Revision int
	Round    int
}

// ToolContext 是一次 turn 的调用方权威快照（agent 在每次 Prompt 前写入）。
type ToolContext struct {
	// SessionID 是发起 turn 的会话 id（goal 服务等每会话组件的解析键）。
	SessionID string
	// DirectHuman = 本 turn 由真人输入驱动（requireDirectHuman 权限面）。
	DirectHuman bool
	// GoalRound 非空 = 本 turn 是 goal 续轮（模型可 complete/blocked）。
	GoalRound *GoalRoundSource
}

// GoalView 是 goal 投影的跨包扁平视图（对齐 GoalView，types.d.ts:74-83）。
type GoalView struct {
	ID          string
	Revision    int
	Objective   string
	Phase       string // active|paused|blocked|complete
	BlockedCode string // 仅 phase=blocked
	BlockedMsg  string
	MaxRounds   int
	RoundsDone  int
	CreatedAt   int64
	UpdatedAt   int64
	Activation  string // armed|disarmed（进程本地，永不持久化）
}

// GoalService 是 goal 服务面向工具的窄接口（goal.Manager 实现；每会话一个）。
// 返回的 error 由 goal 包给出精确文本，工具层原样进 isError 通道。
type GoalService interface {
	View() *GoalView
	Create(objective string, maxRounds int) (*GoalView, error)
	Edit(id string, revision int, objective *string, maxRounds *int) (*GoalView, error)
	Pause(id string, revision int) (*GoalView, error)
	Resume(id string, revision int) (*GoalView, error)
	Complete(id string, revision int) (*GoalView, error)
	Block(id string, revision int, code, message string) (*GoalView, error)
	// BlockedAfter 是模型报阻塞所需的最少连续轮数（默认 3）。
	BlockedAfter() int
}

// RalphSpawnRequest 请求拉起一个全新的一次性子代理（fresh provider：
// 不继承父会话、不继承前一轮子会话；ralph §5.1 requireFreshProvider）。
// outputSchema 能力 = 挂载 structured_output 工具并把 structured 输入回传。
type RalphSpawnRequest struct {
	Signal    context.Context
	Label     string
	Prompt    string
	OutSchema map[string]any
}

// RalphSpawnOutcome：Structured=nil 表示子失败或未按约束产出。
type RalphSpawnOutcome struct {
	Structured json.RawMessage
	FailReason string
}

// RalphLauncher 由装配层实现（ralph.SpawnFresh 绑定父 agent 的实现）。
type RalphLauncher interface {
	SpawnFresh(req RalphSpawnRequest) RalphSpawnOutcome
}
