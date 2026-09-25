// 审批编排（bash-sandbox.md §6）：sandbox_permissions + justification → 严格更宽
// 检查 → 审批请求 → outcome 映射。获批 mode 仅 stamp 本次调用。
//
// 时序契约（§6.3）：
//
//	a. 严格变宽检查（WIDER_MODES），非严格更宽直接抛（不提示人）；
//	b. 无 approval 服务 / 无 agent → 抛（fail-closed）；
//	c. 请求审批 reason = "escalate sandbox to ${mode}: ${justification}"；
//	d. outcome 映射：allowed-once → 获批 mode；rejected/cancelled/unavailable → 抛对应文案。
package sandbox

import (
	"context"
	"errors"
	"fmt"
)

// Outcome 是审批的闭集结果（§6.4：allowed-once|rejected|cancelled|unavailable）。
type Outcome string

const (
	OutcomeAllowedOnce Outcome = "allowed-once"
	OutcomeRejected    Outcome = "rejected"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeUnavailable Outcome = "unavailable"
)

// ApprovalPolicy 是审批策略闭集（§6.4：ask|never）；never 直接 rejected（fail-closed 不弹窗）。
type ApprovalPolicy string

const (
	ApprovalAsk   ApprovalPolicy = "ask"
	ApprovalNever ApprovalPolicy = "never"
)

// Request 是一次审批请求（对应 §6.3 的 approver.request 载荷）。
type Request struct {
	ToolName      string // "bash" | "read" | ... 发起审批的工具名
	CallID        string // 对应工具调用 id
	SessionID     string // 发起 agent（=Session id）
	Mode          Mode   // 请求升到的模式
	Justification string // 模型给的一句话理由
	Policy        ApprovalPolicy
}

// Reason 派生审批理由（§6.3c 模板："escalate sandbox to ${mode}: ${justification}"）。
func (r Request) Reason() string {
	return fmt.Sprintf("escalate sandbox to %s: %s", r.Mode, r.Justification)
}

// Approver 是宿主侧审批通道（由 server 的 waterfall 回环实现；§6.4-6.5）。
// Request 必须阻塞直到用户裁决或 ctx 取消；ctx 取消 → OutcomeCancelled。
type Approver interface {
	Request(ctx context.Context, req Request) Outcome
}

// EscalationInput 是模型可见的提权参数对（validateEscalationArgs，§6.2）。
type EscalationInput struct {
	Permissions   string // sandbox_permissions
	Justification string
}

// ValidateEscalationArgs 复刻参数配对校验（§6.2）：两字段必须同现、justification 非空。
func ValidateEscalationArgs(perms, justification string) error {
	if perms == "" && justification == "" {
		return nil
	}
	if perms == "" {
		return errors.New("invalid escalation: justification is provided without sandbox_permissions")
	}
	if justification == "" {
		return errors.New("invalid escalation: sandbox_permissions requires a justification")
	}
	switch Mode(perms) {
	case ModeWorkspaceWrite, ModeDangerFullAccess:
	default:
		return fmt.Errorf("invalid escalation: unknown sandbox_permissions %q (want workspace-write or danger-full-access)", perms)
	}
	return nil
}

// ApproveEscalation 复刻 approveEscalation 编排（§6.3）。current 是本调用当前
// 生效模式；返回获批 mode（仅本次调用 stamp，§6.6 落点）。
func ApproveEscalation(ctx context.Context, ap Approver, current Mode, req Request) (Mode, error) {
	// a. 严格变宽检查（非更宽 → 直接抛，不提示人类）。
	if !IsStrictlyWider(current, req.Mode) {
		return current, fmt.Errorf(
			"sandbox escalation to %q is not strictly wider than this call's current %q mode", req.Mode, current)
	}
	// b. 无 approval 服务 → fail-closed。
	if ap == nil {
		return current, errors.New("cannot escalate sandbox: no approval channel is available")
	}
	// never 策略：直接 rejected（fail-closed，不弹窗，§6.4）。
	if req.Policy == ApprovalNever {
		return current, fmt.Errorf("the user rejected escalating this %s to %q", req.ToolName, req.Mode)
	}
	// c. 审批请求（阻塞直到裁决/取消）。
	oc := ap.Request(ctx, req)
	// d. outcome 映射。
	switch oc {
	case OutcomeAllowedOnce:
		return req.Mode, nil
	case OutcomeRejected:
		return current, fmt.Errorf("the user rejected escalating this %s to %q", req.ToolName, req.Mode)
	case OutcomeCancelled:
		return current, fmt.Errorf("approval for escalating to %q was cancelled", req.Mode)
	case OutcomeUnavailable:
		return current, fmt.Errorf("cannot escalate sandbox to %q: no approval channel is available", req.Mode)
	default:
		return current, fmt.Errorf("unknown approval outcome %q", oc)
	}
}
