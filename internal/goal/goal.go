// Package goal 复刻 dsh-goal 数据模型/状态机 + dsh-goal-round-driver 语义
// （core-control.md §7.2-§7.4）。错误文本逐字取自本机安装包
// @deepseek-ai/dsh-goal/lib/index.js。
//
// 轮次模型（fold 精确语义，dsh-goal lib/index.js:258-279）：
//   - 持久事实 = goal/change（快照形/墓碑形）。create 的 change.roundsStarted
//     恒 0；其余操作保留当前计数（不守恒 → corrupt）。
//   - goal-sourced user/message 折入校验：phase=active、goalId/revision
//     精确、source.round === roundsStarted+1、round ≤ maxGoalRounds；
//     通过后 roundsStarted = source.round（即视图轮数 = 最后被准入的轮号，
//     轮内与轮后同值——tool 层 isMatchingGoalRound 据此比对）。
//   - driver：roundsStarted >= max → block('round-limit')；否则预约
//     round = roundsStarted+1。
//   - activation（armed|disarmed）进程本地、永不持久化；启动即 disarmed。
//   - 默认 maxGoalRounds=256，blockedAfterConsecutiveRounds=3。
package goal

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

// 服务默认值（dsh-goal :588 / tool-goal Config）。
const (
	DefaultMaxGoalRounds = 256
	DefaultBlockedAfter  = 3
)

// Phase 枚举。
const (
	PhaseActive   = "active"
	PhasePaused   = "paused"
	PhaseBlocked  = "blocked"
	PhaseComplete = "complete"
)

// Activation 枚举（进程本地）。
const (
	Armed    = "armed"
	Disarmed = "disarmed"
)

// Goal 错误码（GoalError.code）。
const (
	CodeAlreadyExists      = "GOAL_ALREADY_EXISTS"
	CodeInvalidEdit        = "GOAL_INVALID_EDIT"
	CodeInvalidMaxRounds   = "GOAL_INVALID_MAX_ROUNDS"
	CodeInvalidObjective   = "GOAL_INVALID_OBJECTIVE"
	CodeInvalidTransition  = "GOAL_INVALID_TRANSITION"
	CodeInvalidBlockReason = "GOAL_INVALID_BLOCK_REASON"
	CodeNotFound           = "GOAL_NOT_FOUND"
	CodeStaleRevision      = "GOAL_STALE_REVISION"
)

// kebabCodeRe：blockedReason.code 的 lower-kebab-case 校验。
var kebabCodeRe = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// BlockReason：code lower-kebab，message 非空且 normalize。
type BlockReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Snapshot：blockedReason 仅 phase=blocked 时存在。
type Snapshot struct {
	ID            string       `json:"id"`
	Revision      int          `json:"revision"`
	Objective     string       `json:"objective"`
	Phase         string       `json:"phase"`
	BlockedReason *BlockReason `json:"blockedReason,omitempty"`
	MaxGoalRounds int          `json:"maxGoalRounds"`
}

// Change 是持久事件 goal/change 的 data。
type Change struct {
	Kind      string `json:"kind"` // 恒 "goal/change"
	Version   int    `json:"version"`
	Operation string `json:"operation"` // create|edit|pause|resume|complete|block|clear

	Goal          *Snapshot `json:"goal,omitempty"`
	RoundsStarted int       `json:"roundsStarted"`
	CreatedAt     int64     `json:"createdAt"`
	UpdatedAt     int64     `json:"updatedAt"`

	Cleared   *Ref  `json:"cleared,omitempty"`
	ClearedAt int64 `json:"clearedAt,omitempty"`
}

// Ref 是 CAS 引用；亦作 clear 墓碑载体（键集恰为 id,revision）。
type Ref struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

// FoldState 是投影（GoalState）：RoundsStarted = 最后被准入的轮号
// （无 goal 或被 clear 归零）。
type FoldState struct {
	Goal          *Snapshot
	RoundsStarted int
	CreatedAt     int64
	UpdatedAt     int64
}

// ---------- 校验器 ----------

type goalError struct {
	Code string
	Msg  string
}

func (e *goalError) Error() string { return e.Msg }

func goalErrf(code, format string, a ...any) error {
	return &goalError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// ErrCode 提取 goal 错误码（无码返回空串）。
func ErrCode(err error) string {
	if ge, ok := err.(*goalError); ok {
		return ge.Code
	}
	return ""
}

func resolveObjective(v string) error {
	if strings.TrimSpace(v) == "" {
		return goalErrf(CodeInvalidObjective, "goal objective must be a non-empty string")
	}
	return nil
}

func resolveMaxGoalRounds(v int) error {
	if v < 1 {
		return goalErrf(CodeInvalidMaxRounds, "maxGoalRounds must be a positive safe integer")
	}
	return nil
}

func resolveBlockReason(code, message string) (*BlockReason, error) {
	if !kebabCodeRe.MatchString(code) || strings.TrimSpace(message) == "" {
		return nil, goalErrf(CodeInvalidBlockReason,
			"goal block reason requires a lower-kebab-case code and a non-empty message")
	}
	return &BlockReason{Code: code, Message: message}, nil
}

// ---------- fold（投影重建 + 状态机校验） ----------

// applyChange 把一条 goal/change 折进状态；clear 返回 nil 投影。
func applyChange(st *FoldState, ch *Change) (*FoldState, error) {
	if ch.Version != 1 {
		return nil, fmt.Errorf("goal change version must be 1")
	}
	if ch.Operation == "clear" {
		if st == nil || st.Goal == nil {
			return nil, fmt.Errorf("goal clear requires a current goal")
		}
		if ch.Cleared == nil || ch.Cleared.ID != st.Goal.ID || ch.Cleared.Revision != st.Goal.Revision+1 {
			return nil, fmt.Errorf("goal clear tombstone must have exactly id and revision fields one past the current goal")
		}
		if ch.ClearedAt < st.UpdatedAt {
			return nil, fmt.Errorf("goal clear timestamp cannot precede the current goal update")
		}
		return nil, nil
	}
	if ch.Goal == nil {
		return nil, fmt.Errorf("goal snapshot change requires a goal")
	}
	g := ch.Goal
	if err := resolveObjective(g.Objective); err != nil {
		return nil, err
	}
	if err := resolveMaxGoalRounds(g.MaxGoalRounds); err != nil {
		return nil, err
	}
	if ch.RoundsStarted < 0 {
		return nil, fmt.Errorf("goal change roundsStarted must be a non-negative safe integer")
	}
	if ch.UpdatedAt < ch.CreatedAt {
		return nil, fmt.Errorf("goal change updatedAt cannot precede createdAt")
	}

	if ch.Operation == "create" {
		// :247 —— fresh active revision-one goal with zero rounds；
		// 已有非 complete 当前 goal 时拒绝。
		if ch.RoundsStarted != 0 || g.Revision != 1 || g.Phase != PhaseActive {
			return nil, fmt.Errorf("goal create requires a fresh active revision-one goal with zero rounds")
		}
		if st != nil && st.Goal != nil && st.Goal.Phase != PhaseComplete {
			return nil, fmt.Errorf("goal create requires a fresh active revision-one goal with zero rounds")
		}
		if g.BlockedReason != nil {
			return nil, fmt.Errorf("goal create must not carry a blocked reason")
		}
		return &FoldState{Goal: g, RoundsStarted: 0,
			CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt}, nil
	}

	if st == nil || st.Goal == nil {
		return nil, fmt.Errorf("goal %s requires a current goal", ch.Operation)
	}
	if g.ID != st.Goal.ID || g.Revision != st.Goal.Revision+1 {
		return nil, fmt.Errorf("goal %s must advance the current goal by one revision", ch.Operation)
	}
	// :182 —— 计数器/时间守恒。
	if ch.CreatedAt != st.CreatedAt || ch.UpdatedAt < st.UpdatedAt || ch.RoundsStarted != st.RoundsStarted {
		return nil, fmt.Errorf("goal %s does not preserve the current counters and timestamps", ch.Operation)
	}
	if err := checkTransition(st.Goal, ch.Operation, g, st.RoundsStarted); err != nil {
		return nil, err
	}
	return &FoldState{Goal: g, RoundsStarted: st.RoundsStarted,
		CreatedAt: st.CreatedAt, UpdatedAt: ch.UpdatedAt}, nil
}

// checkTransition 是 validateCurrentChange 的逐条复刻。
func checkTransition(current *Snapshot, op string, next *Snapshot, roundsStarted int) error {
	switch op {
	case "edit":
		if next.Phase != current.Phase || !sameBlock(next.BlockedReason, current.BlockedReason) {
			return fmt.Errorf("goal edit cannot change phase or blocked reason")
		}
	case "pause":
		if current.Phase != PhaseActive {
			return transitionErr(current, "pause", []string{PhaseActive})
		}
		if next.Phase != PhasePaused || next.BlockedReason != nil {
			return fmt.Errorf("goal pause must land on paused without a blocked reason")
		}
	case "resume":
		// :197 —— 逐字错误文本。
		if !(current.Phase == PhaseActive || current.Phase == PhasePaused || current.Phase == PhaseBlocked) ||
			next.Phase != PhaseActive || roundsStarted >= next.MaxGoalRounds {
			return fmt.Errorf("goal resume has an invalid phase transition or exhausted round budget")
		}
		if next.BlockedReason != nil {
			return fmt.Errorf("goal resume must not carry a blocked reason")
		}
	case "complete":
		if !contains([]string{PhaseActive, PhasePaused, PhaseBlocked}, current.Phase) {
			return transitionErr(current, "complete", []string{PhaseActive, PhasePaused, PhaseBlocked})
		}
		if next.Phase != PhaseComplete || next.BlockedReason != nil {
			return fmt.Errorf("goal complete must land on complete without a blocked reason")
		}
	case "block":
		if current.Phase != PhaseActive {
			return transitionErr(current, "block", []string{PhaseActive})
		}
		if next.Phase != PhaseBlocked {
			return fmt.Errorf("goal block must land on blocked phase")
		}
		if next.BlockedReason == nil {
			return fmt.Errorf("goal block requires a blocked reason")
		}
		if _, err := resolveBlockReason(next.BlockedReason.Code, next.BlockedReason.Message); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown goal operation %q", op)
	}
	return nil
}

func transitionErr(current *Snapshot, op string, allowed []string) error {
	return goalErrf(CodeInvalidTransition, "cannot %s goal \"%s\" from phase \"%s\"; expected %s",
		op, current.ID, current.Phase, strings.Join(allowed, " or "))
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func sameBlock(a, b *BlockReason) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Fold 从事件日志重建投影，并校验 goal-sourced user/message 归因
// （applyGoalEvent :270-279 逐字错误文本）。
func Fold(events []*session.Event) (*FoldState, error) {
	var st *FoldState
	for _, e := range events {
		switch e.Type {
		case session.EventGoalChange:
			var ch Change
			if err := json.Unmarshal(e.Data, &ch); err != nil {
				return nil, fmt.Errorf("goal change at session event %d has an invalid kind", e.Seq)
			}
			if ch.Kind != "goal/change" {
				return nil, fmt.Errorf("goal change at session event %d has an invalid kind", e.Seq)
			}
			np, err := applyChange(st, &ch)
			if err != nil {
				return nil, fmt.Errorf("goal: at seq %d: %w", e.Seq, err)
			}
			st = np
		case session.EventUserMessage:
			var m llm.Message
			if err := json.Unmarshal(e.Data, &m); err != nil {
				continue
			}
			if m.Source.Kind != "goal" {
				continue
			}
			bad := st == nil || st.Goal == nil ||
				st.Goal.Phase != PhaseActive ||
				m.Source.GoalID != st.Goal.ID ||
				m.Source.Revision != st.Goal.Revision ||
				m.Source.Round != st.RoundsStarted+1 ||
				m.Source.Round > st.Goal.MaxGoalRounds
			if bad {
				return nil, fmt.Errorf("goal round at session event %d is not the next admitted round of the active goal", e.Seq)
			}
			st.RoundsStarted = m.Source.Round
		}
	}
	return st, nil
}

// ---------- Service ----------

// View 是 tools.GoalView 别名（跨包扁平视图）。
type View = tools.GoalView

// Service 是单会话 goal 管理器：持久投影（fold）+ 进程本地 activation。
// 公开方法可并发调用；内部 *Locked 方法假定已持 s.mu。
type Service struct {
	mu       sync.Mutex
	sess     *session.Session
	state    *FoldState
	act      string
	listener []func(*View)

	blockedAfter int
	maxRounds    int

	// DriveRequest 由 round driver 注入：投影/activation 变化后请求驱动
	// （事件驱动 + 单飞，§7.4；不轮询）。
	DriveRequest func()
}

// NewService 从事件日志 fold 出投影并构造服务；activation 一律 disarmed
// （进程/会话启动即 disarmed，§7.2）。
func NewService(sess *session.Session, blockedAfter, defaultMaxRounds int) (*Service, error) {
	if blockedAfter <= 0 {
		blockedAfter = DefaultBlockedAfter
	}
	if defaultMaxRounds <= 0 {
		defaultMaxRounds = DefaultMaxGoalRounds
	}
	st, err := Fold(sess.Log())
	if err != nil {
		return nil, err
	}
	return &Service{sess: sess, state: st, act: Disarmed,
		blockedAfter: blockedAfter, maxRounds: defaultMaxRounds}, nil
}

// OnChange 订阅视图变化（goal/changed 与 activation-changed 的源头）。
func (s *Service) OnChange(fn func(*View)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener = append(s.listener, fn)
}

// BlockedAfter 是配置的最少连续轮数。
func (s *Service) BlockedAfter() int { return s.blockedAfter }

func newGoalID() string { return "goal-" + session.NewUUID() }

func nowMillis() int64 { return time.Now().UnixMilli() }

// nextMutationTime：墙钟回拨钳制。
func nextMutationTime(prev int64) int64 {
	now := nowMillis()
	if now < prev {
		return prev
	}
	return now
}

// commitLocked（持锁）：写 goal/change → fold 推进 → activation 迁移 →
// 通知 → 请求驱动。
func (s *Service) commitLocked(ch *Change, activation string) (*View, error) {
	if _, err := s.sess.Append(session.EventGoalChange, ch, nil, nil); err != nil {
		return nil, err
	}
	np, err := applyChange(s.state, ch)
	if err != nil {
		return nil, err
	}
	s.state = np
	s.act = activation
	view := s.viewLocked()
	s.notifyLocked(view)
	s.requestDriveLocked()
	return view, nil
}

func (s *Service) requestDriveLocked() {
	if s.DriveRequest != nil {
		fn := s.DriveRequest
		go fn()
	}
}

func (s *Service) notifyLocked(view *View) {
	for _, fn := range s.listener {
		if view != nil {
			v := *view
			go fn(&v)
		}
	}
}

// expectCurrentLocked：缺 goal → GOAL_NOT_FOUND；ref 不符 →
// GOAL_STALE_REVISION（逐字）。
func (s *Service) expectCurrentLocked(id string, revision int) error {
	if s.state == nil || s.state.Goal == nil {
		return goalErrf(CodeNotFound, "no current goal")
	}
	cur := s.state.Goal
	if id != cur.ID || revision != cur.Revision {
		return goalErrf(CodeStaleRevision, "stale goal ref \"%s\" revision %d; current is \"%s\" revision %d",
			id, revision, cur.ID, cur.Revision)
	}
	return nil
}

func (s *Service) currentGoalLocked() *Snapshot {
	if s.state == nil {
		return nil
	}
	return s.state.Goal
}

// Create 创建并 arm 一个 goal；complete 可被替换（GOAL_ALREADY_EXISTS）。
func (s *Service) Create(objective string, maxRounds int) (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := resolveObjective(objective); err != nil {
		return nil, err
	}
	if maxRounds == 0 {
		maxRounds = s.maxRounds
	}
	if err := resolveMaxGoalRounds(maxRounds); err != nil {
		return nil, err
	}
	if cur := s.currentGoalLocked(); cur != nil && cur.Phase != PhaseComplete {
		return nil, goalErrf(CodeAlreadyExists, "goal \"%s\" already exists with phase \"%s\"", cur.ID, cur.Phase)
	}
	now := nowMillis()
	g := &Snapshot{ID: newGoalID(), Revision: 1, Objective: objective,
		Phase: PhaseActive, MaxGoalRounds: maxRounds}
	return s.commitLocked(&Change{Kind: "goal/change", Version: 1, Operation: "create",
		Goal: g, RoundsStarted: 0, CreatedAt: now, UpdatedAt: now}, Armed)
}

// Edit 改 objective 和/或 round cap（不改 phase）。
func (s *Service) Edit(id string, revision int, objective *string, maxRounds *int) (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.expectCurrentLocked(id, revision); err != nil {
		return nil, err
	}
	if objective == nil && maxRounds == nil {
		return nil, goalErrf(CodeInvalidEdit, "goal edit requires objective and/or maxGoalRounds")
	}
	if objective != nil {
		if err := resolveObjective(*objective); err != nil {
			return nil, err
		}
	}
	if maxRounds != nil {
		if err := resolveMaxGoalRounds(*maxRounds); err != nil {
			return nil, err
		}
	}
	cur := s.currentGoalLocked()
	g := *cur
	g.Revision++
	if objective != nil {
		g.Objective = *objective
	}
	if maxRounds != nil {
		g.MaxGoalRounds = *maxRounds
	}
	return s.commitLocked(&Change{Kind: "goal/change", Version: 1, Operation: "edit",
		Goal: &g, RoundsStarted: s.state.RoundsStarted,
		CreatedAt: s.state.CreatedAt, UpdatedAt: nextMutationTime(s.state.UpdatedAt)}, s.act)
}

// transitionLocked 是 pause/complete 的共用路径。
func (s *Service) transitionLocked(id, op string, revision int, allowed []string, phase, activation string) (*View, error) {
	if err := s.expectCurrentLocked(id, revision); err != nil {
		return nil, err
	}
	cur := s.currentGoalLocked()
	if !contains(allowed, cur.Phase) {
		return nil, transitionErr(cur, op, allowed)
	}
	g := *cur
	g.Revision++
	g.Phase = phase
	g.BlockedReason = nil
	return s.commitLocked(&Change{Kind: "goal/change", Version: 1, Operation: op,
		Goal: &g, RoundsStarted: s.state.RoundsStarted,
		CreatedAt: s.state.CreatedAt, UpdatedAt: nextMutationTime(s.state.UpdatedAt)}, activation)
}

// Pause：active→paused + disarm。
func (s *Service) Pause(id string, revision int) (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(id, "pause", revision, []string{PhaseActive}, PhasePaused, Disarmed)
}

// Resume：active|paused|blocked→active + arm；预算耗尽/已 active+armed 拒绝
// （:696-697 逐字）。
func (s *Service) Resume(id string, revision int) (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.expectCurrentLocked(id, revision); err != nil {
		return nil, err
	}
	cur := s.currentGoalLocked()
	resumable := []string{PhaseActive, PhasePaused, PhaseBlocked}
	if !contains(resumable, cur.Phase) {
		return nil, transitionErr(cur, "resume", resumable)
	}
	if cur.Phase == PhaseActive && s.act == Armed {
		return nil, goalErrf(CodeInvalidTransition, "goal \"%s\" is already active and armed", cur.ID)
	}
	if s.state.RoundsStarted >= cur.MaxGoalRounds {
		return nil, goalErrf(CodeInvalidTransition,
			"goal \"%s\" exhausted %d goal rounds; increase maxGoalRounds before resuming", cur.ID, cur.MaxGoalRounds)
	}
	g := *cur
	g.Revision++
	g.Phase = PhaseActive
	g.BlockedReason = nil
	return s.commitLocked(&Change{Kind: "goal/change", Version: 1, Operation: "resume",
		Goal: &g, RoundsStarted: s.state.RoundsStarted,
		CreatedAt: s.state.CreatedAt, UpdatedAt: nextMutationTime(s.state.UpdatedAt)}, Armed)
}

// Complete：非 complete→complete + disarm。
func (s *Service) Complete(id string, revision int) (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(id, "complete", revision,
		[]string{PhaseActive, PhasePaused, PhaseBlocked}, PhaseComplete, Disarmed)
}

// Block：仅 active→blocked + disarm。
func (s *Service) Block(id string, revision int, code, message string) (*View, error) {
	reason, err := resolveBlockReason(code, message)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blockLocked(id, revision, reason)
}

func (s *Service) blockLocked(id string, revision int, reason *BlockReason) (*View, error) {
	if err := s.expectCurrentLocked(id, revision); err != nil {
		return nil, err
	}
	cur := s.currentGoalLocked()
	if cur.Phase != PhaseActive {
		return nil, transitionErr(cur, "block", []string{PhaseActive})
	}
	g := *cur
	g.Revision++
	g.Phase = PhaseBlocked
	g.BlockedReason = reason
	return s.commitLocked(&Change{Kind: "goal/change", Version: 1, Operation: "block",
		Goal: &g, RoundsStarted: s.state.RoundsStarted,
		CreatedAt: s.state.CreatedAt, UpdatedAt: nextMutationTime(s.state.UpdatedAt)}, Disarmed)
}

// Clear：写墓碑（tombstone revision = 快照 revision+1），投影清空。
func (s *Service) Clear(id string, revision int) (*Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.expectCurrentLocked(id, revision); err != nil {
		return nil, err
	}
	cur := s.currentGoalLocked()
	tomb := &Ref{ID: cur.ID, Revision: cur.Revision + 1}
	if _, err := s.commitLocked(&Change{Kind: "goal/change", Version: 1, Operation: "clear",
		Cleared: tomb, ClearedAt: nextMutationTime(s.state.UpdatedAt)}, Disarmed); err != nil {
		return nil, err
	}
	return tomb, nil
}

// ---------- activation（进程本地，永不持久化） ----------

// Activation 返回当前 activation。
func (s *Service) Activation() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.act
}

// Disarm 撤消进程本地续轮权限（不改持久 phase/revision）。
func (s *Service) Disarm() {
	s.mu.Lock()
	if s.act == Disarmed {
		s.mu.Unlock()
		return
	}
	s.act = Disarmed
	s.notifyLocked(s.viewLocked())
	s.requestDriveLocked()
	s.mu.Unlock()
}

// Arm 改进程本地续轮权限（GUI 激活开关对应物；不改持久投影）。
// 只有 active 且预算有余的 goal 可 arm；变化后请求驱动。
func (s *Service) Arm() {
	s.mu.Lock()
	cur := s.currentGoalLocked()
	if cur == nil || cur.Phase != PhaseActive || s.state.RoundsStarted >= cur.MaxGoalRounds || s.act == Armed {
		s.mu.Unlock()
		return
	}
	s.act = Armed
	s.notifyLocked(s.viewLocked())
	s.requestDriveLocked()
	s.mu.Unlock()
}

// GateCheck 是 pre-step 竞态闸门对 goal 消息的纯校验（§7.4 driver fence）：
// claim 后、turn 前复核预约精确性。镜像推进在消息落盘成功后由
// AdmitRound 完成（落盘失败不消耗轮次，durable fold 与进程态不分叉）。
func (s *Service) GateCheck(goalID string, revision, round int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || s.state.Goal == nil {
		return false
	}
	cur := s.currentGoalLocked()
	return cur.ID == goalID && cur.Revision == revision &&
		cur.Phase == PhaseActive && s.state.RoundsStarted == round-1 &&
		round <= cur.MaxGoalRounds
}

// AdmitRound 在 goal 消息落盘（durable）后推进进程镜像（fold 语义镜像：
// roundsStarted = source.round）。
func (s *Service) AdmitRound(goalID string, revision, round int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || s.state.Goal == nil {
		return
	}
	cur := s.currentGoalLocked()
	if cur.ID == goalID && cur.Revision == revision && s.state.RoundsStarted == round-1 {
		s.state.RoundsStarted = round
	}
}

// ReserveNextRound 预约下一轮（driver drive() 语义）：
// roundsStarted >= max → block('round-limit')（driver :125-129 逐字 message）
// 并返回不可续；否则 round = roundsStarted+1。
func (s *Service) ReserveNextRound() (round int, view *View, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || s.state.Goal == nil {
		return 0, s.viewLocked(), false
	}
	cur := s.currentGoalLocked()
	if cur.Phase != PhaseActive || s.act != Armed {
		return 0, s.viewLocked(), false
	}
	if s.state.RoundsStarted >= cur.MaxGoalRounds {
		_, _ = s.blockLocked(cur.ID, cur.Revision, &BlockReason{
			Code:    "round-limit",
			Message: fmt.Sprintf("Goal reached its configured limit of %d rounds.", cur.MaxGoalRounds),
		})
		return 0, s.viewLocked(), false
	}
	return s.state.RoundsStarted + 1, s.viewLocked(), true
}

// GoalRoundSource 描述当前正在进行的 goal 轮（tool 层 isMatchingGoalRound
// 比对基准：view.roundsStarted 轮内即当前轮号）。
func (s *Service) GoalRoundSource() *tools.GoalRoundSource {
	v := s.View()
	if v == nil {
		return nil
	}
	return &tools.GoalRoundSource{GoalID: v.ID, Revision: v.Revision, Round: v.RoundsDone}
}

// ---------- 视图 ----------

// viewLocked 组装对外视图：RoundsDone = fold roundsStarted（最后被准入
// 轮号 = goalValue/goalView.roundsStarted 语义）。
func (s *Service) viewLocked() *View {
	if s.state == nil || s.state.Goal == nil {
		return nil
	}
	g := s.state.Goal
	v := &View{
		ID: g.ID, Revision: g.Revision, Objective: g.Objective, Phase: g.Phase,
		MaxRounds: g.MaxGoalRounds, RoundsDone: s.state.RoundsStarted,
		CreatedAt: s.state.CreatedAt, UpdatedAt: s.state.UpdatedAt,
		Activation: s.act,
	}
	if g.BlockedReason != nil {
		v.BlockedCode = g.BlockedReason.Code
		v.BlockedMsg = g.BlockedReason.Message
	}
	return v
}

// View 返回当前视图（无 goal 返回 nil）。
func (s *Service) View() *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewLocked()
}

// WrapNotice 生成 goal-round 收尾 notice 正文（renderWrapupContext 逐字；
// blockedReason 空 = complete 形）。
func WrapNotice(objective, blockedReason string) string {
	heading := "Objective: " + jsonQuote(objective) + "\n"
	if blockedReason == "" {
		return "<goal_complete>\n" + heading + "The goal is marked complete and this autonomous run is ending. Write the closing message to the user now: state the outcome, summarize what was done and how it was verified, and point to the concrete results (files, commits, or other artifacts). Report only what earlier rounds and tool results in this session actually establish; when a detail is not in the session, say so instead of inventing it. Note anything the user should review or do next. Address the user directly. Do not call any more tools in this run; further work waits for the user's next instruction.\n</goal_complete>"
	}
	return "<goal_blocked>\n" + heading + "Blocked: " + jsonQuote(blockedReason) + "\nThe goal is marked blocked and this autonomous run is ending. Write the closing message to the user now: state what has been completed so far, describe the concrete blocking condition and what you tried, and say exactly what you need from the user to continue. Report only what earlier rounds and tool results in this session actually establish; when a detail is not in the session, say so instead of inventing it. Address the user directly. Do not call any more tools in this run; further work waits for the user's next instruction.\n</goal_blocked>"
}

func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// PromptText 渲染 goal 续轮 prompt（renderGoalRoundPrompt 逐字）。
func PromptText(objective string, round, maxRounds int) string {
	return "<goal_round>\nObjective: " + jsonQuote(objective) + "\n" +
		fmt.Sprintf("Round: %d/%d", round, maxRounds) +
		"\n\nContinue working toward the objective in this same session. Treat the current workspace, tool results, and durable session state as authoritative; inspect them instead of assuming earlier narration is still current. Make concrete progress and verify the result. Before claiming completion, gather evidence that the whole objective is achieved, read the current goal, and mark it complete. If work remains, leave the goal active for the next round. Follow the configured goal-tool policy before reporting a blocker.\n</goal_round>"
}

// Guidance 返回 system prompt 的 tool:goal section 文本（guidance 逐字）。
func Guidance(blockedAfter int) string {
	return fmt.Sprintf("Use goal tools for one long-running completion objective in the current session. create_goal may infer goal intent from a direct human request in any language; do not create a goal for routine single-turn work. Call get_goal before update_goal and copy its exact goal_id and revision. After session resume or fork, an active goal is disarmed: when a human asks to continue or resume in any wording or language, use update_goal action resume to rearm it. Mark complete only when the objective is actually achieved. Mark blocked only after the same blocking condition persists for at least %d consecutive goal rounds, and report that concrete condition in blocked_reason; difficulty, uncertainty, or useful remaining work is not blocked.", blockedAfter)
}

var _ tools.GoalService = (*Service)(nil)
