// goal 续轮驱动（dsh-goal-round-driver 语义复刻，core-control.md §7.4）。
//
// 事件驱动 + 单飞（不轮询）：goal/changed、activation 变化、agent idle 都
// 只是"请求驱动"；真正的预约在 drive()：ready（idle+live+armed+active+
// 预算有余）→ 预约 round=roundsStarted+1 → 渲染固定 prompt →
// host.RunRound 起 goal turn。竞态闸门（GateCheck）在 turn claim 后、
// 落盘前复核预约精确性（fence 3），reject → 消息退回、turn blocked。
//
// lidsh 简化（文档未禁止的实现收缩）：
//   - competing 输入检测面只有"人工 prompt 占用 agent"（IdleAndLive=false）；
//   - 取消语义收缩为 goal turn 以 aborted 结束时 pause（DSH attempt.cancelled
//     → idle pause），max-tokens 结束 → disarm（fence 5 逐条）。
package goal

import (
	"fmt"
	"sync"

	"lidsh/internal/tools"
)

// RoundHost 是驱动对执行体的窄接口（server entry 实现）。
type RoundHost interface {
	// IdleAndLive：agent 已构造且当前无在途 turn。
	IdleAndLive() bool
	// RunRound 启动一个 goal 续轮 turn；返回启动期错误（不等待完成）。
	RunRound(content string, src *tools.GoalRoundSource) error
}

type attemptState struct {
	goalID   string
	revision int
	round    int
	stale    bool
}

// Driver 是每会话 goal 续轮驱动器。
type Driver struct {
	svc  *Service
	host RoundHost

	mu        sync.Mutex
	stopping  bool
	requested bool // 已置位但 worker 尚未消费
	running   bool // worker 协程在飞
	attempt   *attemptState

	request  chan struct{} // 合并信号（容量 1）
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewDriver 构造驱动器并把 svc.DriveRequest 接到自身。
func NewDriver(svc *Service, host RoundHost) *Driver {
	d := &Driver{svc: svc, host: host,
		request: make(chan struct{}, 1),
		stop:    make(chan struct{})}
	svc.DriveRequest = d.RequestDrive
	return d
}

// Stop 停止驱动（会话 dispose）并等 worker 收摊。
func (d *Driver) Stop() {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.stopping = true
		d.mu.Unlock()
		close(d.stop)
	})
	d.wg.Wait()
}

// Quiet 报告驱动是否静默：无待处理请求、无在飞 worker、无预约
// （headless 排空判定用）。
func (d *Driver) Quiet() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.requested && !d.running && d.attempt == nil
}

// RequestDrive 合并触发一次驱动（requestDrive：置位 + 单飞 worker）。
func (d *Driver) RequestDrive() {
	d.mu.Lock()
	d.requested = true
	launch := !d.running && !d.stopping
	if launch {
		d.running = true
	}
	d.mu.Unlock()
	if !launch {
		// worker 在飞：它每轮都会重新检查 requested；保险再发一次信号。
		select {
		case d.request <- struct{}{}:
		default:
		}
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.worker()
	}()
}

// worker 是单飞串行面：requested 未清就持续 drive()，与 DSH 的
// while(state.requested) 环一致。
func (d *Driver) worker() {
	for {
		d.mu.Lock()
		d.requested = false
		stopping := d.stopping
		d.mu.Unlock()

		if !stopping {
			d.drive()
		}

		d.mu.Lock()
		if !d.requested || d.stopping {
			d.running = false
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
		// 等下一个合并信号或 stop（requested 已置位时不等待）。
		d.mu.Lock()
		pending := d.requested
		d.mu.Unlock()
		if !pending {
			select {
			case <-d.request:
			case <-d.stop:
			}
		}
	}
}

// OnTurnEnd 是 idle 钩子（agent/status=idle + turn/end 事件的合并面）：
// 清预约并按结果执行 fence。
func (d *Driver) OnTurnEnd(goalTurn bool, reasonKind string) {
	if goalTurn {
		switch reasonKind {
		case "max-tokens":
			// fence 5：max-tokens → disarm（逐条）。
			d.svc.Disarm()
		case "aborted":
			// attempt cancelled 面：goal 仍 active 则 pause（DSH 取消 →
			// idle 处理里 pause 被取消的 goal 轮）。
			if v := d.svc.View(); v != nil && v.Phase == PhaseActive {
				_, _ = d.svc.Pause(v.ID, v.Revision)
			}
		}
	}
	d.mu.Lock()
	d.attempt = nil
	d.mu.Unlock()
	d.RequestDrive()
}

// GateCheck 是 agent/pre-step 闸门的 goal 分支：预约仍然精确才放行。
// 通过时保留 attempt（goal 消息落盘由 OnEvent 推进镜像、finishTurn 清理）；
// 拒绝 = 驱动状态与投影不一致 → 清预约 + fail-closed disarm（DSH driver
// 各 fence 的公共姿态），杜绝同轮重预约循环。
func (d *Driver) GateCheck(goalID string, revision, round int) bool {
	d.mu.Lock()
	a := d.attempt
	ok := a != nil && !a.stale && a.goalID == goalID && a.revision == revision &&
		a.round == round
	if !ok {
		d.attempt = nil
	}
	d.mu.Unlock()
	if ok {
		ok = d.svc.GateCheck(goalID, revision, round)
	}
	if !ok {
		d.svc.Disarm()
	}
	return ok
}

// drive 处理静默期的准入工作，然后至多预约一个下一轮。
func (d *Driver) drive() {
	if !d.host.IdleAndLive() {
		return
	}
	d.mu.Lock()
	if d.attempt != nil {
		// 预约尚未被消费（队列被人工输入抢先或已 stale）：回收后重评。
		a := d.attempt
		d.attempt = nil
		if !a.stale {
			// 仍 queued 却 idle——说明消息没进 inbox 或被丢弃；不动 goal。
			d.mu.Unlock()
			return
		}
	}
	d.mu.Unlock()

	round, view, ok := d.svc.ReserveNextRound()
	if !ok {
		return
	}
	src := &tools.GoalRoundSource{GoalID: view.ID, Revision: view.Revision, Round: round}
	content := PromptText(view.Objective, round, view.MaxRounds)

	d.mu.Lock()
	d.attempt = &attemptState{goalID: view.ID, revision: view.Revision, round: round}
	d.mu.Unlock()

	if err := d.host.RunRound(content, src); err != nil {
		d.mu.Lock()
		if d.attempt != nil && d.attempt.goalID == view.ID && d.attempt.round == round {
			d.attempt = nil
		}
		d.mu.Unlock()
		// queue-failed：goal 仍是同 revision 的 active+armed 时 block（§7.4）。
		if cur := d.svc.View(); cur != nil && cur.ID == view.ID && cur.Revision == view.Revision &&
			cur.Phase == PhaseActive && cur.Activation == Armed {
			_, _ = d.svc.Block(cur.ID, cur.Revision, "queue-failed",
				fmt.Sprintf("Could not queue goal round %d: %v", round, err))
		}
	}
}
