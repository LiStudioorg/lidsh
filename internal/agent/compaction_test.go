// agent 侧 compaction 集成：step 前压力压缩、context-overflow 恢复重试。
package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"lidsh/internal/compaction"
	"lidsh/internal/llm"
	"lidsh/internal/session"
	"lidsh/internal/tools"
)

func failStream(code llm.ErrorCode, msg string) []llm.Chunk {
	return []llm.Chunk{
		{Type: llm.ChunkFinish, Finish: llm.FinishError,
			Fail: &llm.Failure{Message: msg, Code: code}},
	}
}

func TestPressureCompactionRunsBeforeStep(t *testing.T) {
	s := session.New(session.NewHeader(session.NewID(), "/tmp", session.Meta{}))
	// 预置超阈值历史（system 之外的大消息对 + 尾问）。
	big := strings.Repeat("abcdefgh ", 100)
	mustAppend(t, s, session.EventSystemMessage, session.SystemMessageData{
		Turn: 0, Message: systemMessage("sys prompt")}, session.AppendOp())
	mustAppend(t, s, session.EventUserMessage, *userMsgText(big), session.AppendOp())
	mustAppend(t, s, session.EventAssistantMessage, session.AssistantMessageData{
		Turn: 1, Message: frameAssistantMessage("fake", "m",
			[]llm.ContentBlock{{Type: "text", Text: big}}),
	}, session.AppendOp())

	fa := &fakeAdapter{provider: "fake", model: "m", streams: [][]llm.Chunk{
		textStream("tiny summary"), // 摘要流
		textStream("final answer"), // turn 首步
	}}
	a := New(Options{
		Sess: s, Resolver: NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools: tools.NewRegistry(), Provider: "fake", Model: "m", CWD: "/tmp",
		ContextWindow: 500, // threshold=400 < 当前历史
	})
	eng, err := compaction.NewEngine(a, compaction.Config{})
	if err != nil {
		t.Fatal(err)
	}
	a.Compaction = eng

	res, err := a.Prompt("latest question", "followup")
	if err != nil || res.Kind != "completed" {
		t.Fatalf("turn: %+v %v", res, err)
	}
	// 日志里有完整压缩事务。
	haveCompaction := false
	for _, e := range s.Log() {
		if e.Type == session.EventCompactionSummary {
			haveCompaction = true
		}
	}
	if !haveCompaction {
		t.Fatal("pressure compaction did not land")
	}
}

func TestOverflowRecoveryRetriesStep(t *testing.T) {
	s := session.New(session.NewHeader(session.NewID(), "/tmp", session.Meta{}))
	big := strings.Repeat("abcdefgh ", 60)
	mustAppend(t, s, session.EventSystemMessage, session.SystemMessageData{
		Turn: 0, Message: systemMessage("sys")}, session.AppendOp())
	mustAppend(t, s, session.EventUserMessage, *userMsgText(big), session.AppendOp())
	mustAppend(t, s, session.EventAssistantMessage, session.AssistantMessageData{
		Turn: 1, Message: frameAssistantMessage("fake", "m",
			[]llm.ContentBlock{{Type: "text", Text: big}}),
	}, session.AppendOp())

	fa := &fakeAdapter{provider: "fake", model: "m", streams: [][]llm.Chunk{
		failStream(llm.ErrContextWindowExceeded, "context length exceeded"), // step1
		textStream("checkpoint summary"),                                    // 摘要
		textStream("recovered answer"),                                      // retry 的 step
	}}
	a := New(Options{
		Sess: s, Resolver: NewStaticResolver(map[string]llm.Adapter{"fake": fa}),
		Tools: tools.NewRegistry(), Provider: "fake", Model: "m", CWD: "/tmp",
		ContextWindow: 1000,
	})
	// 挂 engine（overflow 分支跳过阈值，只要求容量可解析 → 阈值校验仍跑）。
	eng, err := compaction.NewEngine(a, compaction.Config{ThresholdRatio: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	a.Compaction = eng

	res, err := a.Prompt("ask", "followup")
	if err != nil || res.Kind != "completed" {
		t.Fatalf("turn should recover: %+v %v", res, err)
	}
	// 恢复面：一次 overflow 压缩 + retry 后拿到最终回答。
	n := 0
	for _, e := range s.Log() {
		if e.Type == session.EventCompactionSummary {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 overflow compaction, got %d", n)
	}
}

func mustAppend(t *testing.T, s *session.Session, typ string, data any, op json.RawMessage) {
	t.Helper()
	if _, err := s.Append(typ, data, op, nil); err != nil {
		t.Fatalf("append %s: %v", typ, err)
	}
}

func userMsgText(text string) *llm.Message {
	return &llm.Message{ID: session.NewMessageID(), Role: llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: text}},
		Source:  llm.MessageSource{Kind: "user"}}
}
