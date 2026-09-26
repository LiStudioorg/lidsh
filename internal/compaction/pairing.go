// tool-pairing 边界判定（dsh-compaction/lib/index.js:20-93 逐行复刻）。
//
// assistant/message 按其 tool-call block 数 +delta、tool/result delta -1；
// 累计为 0 的切点才 balanced；orphan tool/result → corrupt surface 抛错。
// lidsh 收缩：不做 replaceGeneration 缓存（每次线性折，表面量级可接受）。
package compaction

import (
	"encoding/json"
	"fmt"

	"lidsh/internal/session"
)

// eventDelta：一个 surface 事件对在途 tool-call 计数的改变。
func eventDelta(e *session.Event) (int, error) {
	switch e.Type {
	case session.EventAssistantMessage:
		var d session.AssistantMessageData
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return 0, err
		}
		n := 0
		if d.Message != nil {
			for _, b := range d.Message.Content {
				if b.Type == "tool-call" {
					n++
				}
			}
		}
		return n, nil
	case session.EventToolResult:
		return -1, nil
	}
	return 0, nil
}

// balanceState 是当前 surface 的切点平衡表：cutBalanced[i] = "位置 i 之前"
// 的切点平衡（i ∈ [0, len]）。
type balanceState struct {
	cutBalanced []bool
}

// balanceOf 折全 surface（每次重折，无缓存）。
func balanceOf(sess *session.Session) (*balanceState, error) {
	nodes := sess.Surface()
	st := &balanceState{cutBalanced: make([]bool, 0, len(nodes)+1)}
	st.cutBalanced = append(st.cutBalanced, true) // 面首切点恒平衡
	inProgress := 0
	for _, n := range nodes {
		e := sess.EventAt(n.EventSeq)
		if e == nil || e.Seq != n.EventSeq {
			return nil, fmt.Errorf("tool-pairing balance: surface seq %d has no matching session event (corrupt surface)", n.EventSeq)
		}
		d, err := eventDelta(e)
		if err != nil {
			return nil, err
		}
		inProgress += d
		if inProgress < 0 {
			return nil, fmt.Errorf("tool-pairing balance: tool/result at surface seq %d has no matching tool-call (corrupt surface)", n.EventSeq)
		}
		st.cutBalanced = append(st.cutBalanced, inProgress == 0)
	}
	return st, nil
}

// balancedBefore：surface 位置上第 idx 个节点之前的切点是否平衡。
func (b *balanceState) balancedBefore(idx int) bool { return b.cutBalanced[idx] }

// balancedAfter：第 idx 个节点之后的切点是否平衡。
func (b *balanceState) balancedAfter(idx int) bool { return b.cutBalanced[idx+1] }
