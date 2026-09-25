// 瀑布审批通道（§6.4-6.5）：approval/request 经 $events 瀑布下发浏览器，
// 应答走 HTTP $events/result 回环。outcome 闭集映射：
// "allowed-once" → 获批；"rejected" → 拒绝；无客户端应答（超时）→ cancelled。
package server

import (
	"context"
	"fmt"

	"lidsh/internal/sandbox"
)

// waterfallApprover 把沙箱提权审批桥到 $events 瀑布回环。
type waterfallApprover struct{ s *Server }

func (wa *waterfallApprover) Request(ctx context.Context, req sandbox.Request) sandbox.Outcome {
	payload := map[string]any{
		"toolName":  req.ToolName,
		"callId":    req.CallID,
		"reason":    req.Reason(),
		"mode":      string(req.Mode),
		"sessionId": req.SessionID,
	}
	res := wa.s.dispatchWaterfall("approval/request", req.SessionID, mustRaw(payload))
	switch {
	case !res.settled:
		// 超时/无客户端应答：dispatchWaterfall 已广播 cancel 帧清 UI。
		return sandbox.OutcomeCancelled
	case res.rejected:
		return sandbox.OutcomeRejected
	}
	switch fmt.Sprint(res.value) {
	case "allowed-once", `"allowed-once"`:
		return sandbox.OutcomeAllowedOnce
	case "rejected", `"rejected"`:
		return sandbox.OutcomeRejected
	default:
		// 未知应答按拒绝处理（fail-closed，闭集外不放行）。
		return sandbox.OutcomeRejected
	}
}
