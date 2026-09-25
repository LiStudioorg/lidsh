package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lidsh/internal/llm"
	"lidsh/internal/session"
)

// headlessFakeAdapter 返回一个固定文本流的 adapter。
type headlessFakeAdapter struct{}

func (headlessFakeAdapter) Stream(ctx context.Context, opts llm.GenerateOptions) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		ch <- llm.Chunk{Type: llm.ChunkBlockStart, BlockIndex: 0, BlockType: llm.BlockText}
		ch <- llm.Chunk{Type: llm.ChunkTextDelta, BlockIndex: 0, Delta: "hello from headless"}
		ch <- llm.Chunk{Type: llm.ChunkBlockEnd, BlockIndex: 0}
		ch <- llm.Chunk{Type: llm.ChunkUsage, Usage: &llm.TokenUsage{InputTokens: 4, OutputTokens: 3}}
		ch <- llm.Chunk{Type: llm.ChunkFinish, Finish: llm.FinishStop}
	}()
	return ch, nil
}

func (headlessFakeAdapter) Info() llm.ProviderInfo { return llm.ProviderInfo{Provider: "fake"} }
func (headlessFakeAdapter) ResolveModel(id string) (string, error) {
	return id, nil
}

// TestHeadlessCore 验证完整管线：事件 zstd 落盘 + 最终回答输出。
func TestHeadlessCore(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	_ = os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("hi"), 0o644)

	// 捕获 stdout。
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	prompt := "ping"
	err := runHeadlessCore(context.Background(), home, workdir, "fake", "m", prompt, headlessFakeAdapter{})
	w.Close()
	os.Stdout = old

	var out bytes.Buffer
	_, _ = out.ReadFrom(r)

	if err != nil {
		t.Fatalf("runHeadlessCore: %v", err)
	}
	if !strings.Contains(out.String(), "hello from headless") {
		t.Errorf("stdout = %q, want final assistant text", out.String())
	}

	// 验证事件落盘：home 下应有 session 目录 + zstd 日志，且可 LoadLog 回读。
	dirs, err := os.ReadDir(home)
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no session dir created under home (%v)", err)
	}
	// 项目子目录 + 会话目录 = 两层。
	var logPath string
	_ = filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".zstd") {
			logPath = p
		}
		return nil
	})
	if logPath == "" {
		t.Fatal("no .zstd session log found under home")
	}
	hdr, events, _, err := session.LoadLog(logPath, "zstd")
	if err != nil {
		t.Fatalf("LoadLog: %v", err)
	}
	if hdr.CWD != workdir {
		t.Errorf("header cwd = %q, want %q", hdr.CWD, workdir)
	}
	// 事件应含 turn/start..turn/end 及 user/assistant message。
	types := map[string]bool{}
	for _, e := range events {
		types[e.Type] = true
	}
	for _, want := range []string{
		session.EventUserMessage, session.EventAssistantMessage,
		session.EventTurnStart, session.EventTurnEnd,
	} {
		if !types[want] {
			t.Errorf("log missing event %q (got %v)", want, types)
		}
	}
}
