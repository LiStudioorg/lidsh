// web 入口：启动 HTTP 服务器（M1b WS mux + 一元 RPC；前端静态资源 M1c 接）。
//
// 配置来源与 headless 相同（LIDSH_API_KEY 等环境变量），另加：
//
//	LIDSH_PORT       监听端口，默认 3000
//	LIDSH_HOST       绑定地址，默认 127.0.0.1
package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"lidsh/internal/llm"
	"lidsh/internal/server"
)

// RunWeb 启动 web profile 的 HTTP 服务器（阻塞直到 ctx 取消或监听失败）。
func RunWeb(ctx context.Context, home, workdir string, args []string) error {
	apiKey := envOr("LIDSH_API_KEY", "")
	if apiKey == "" {
		return fmt.Errorf("web: LIDSH_API_KEY is required (set it or pass in .env)")
	}
	baseURL := envOr("LIDSH_BASE_URL", "https://api.deepseek.com")
	model := envOr("LIDSH_MODEL", "deepseek-chat")
	provider := envOr("LIDSH_PROVIDER", "deepseek-official")
	host := envOr("LIDSH_HOST", "127.0.0.1")
	port := envOr("LIDSH_PORT", "3000")

	adapter := llm.NewOpenAI(llm.OpenAIConfig{
		Provider:         provider,
		BaseURL:          baseURL,
		APIKey:           apiKey,
		SessionIDHeader:  "x-deepseek-harness-session-id",
		SupportsThinking: true, // DeepSeek 形态
	})

	srv := server.New(server.Options{
		Home:     home,
		Workdir:  workdir,
		Provider: provider,
		Model:    model,
		Reason:   llm.EffortHigh,
		System:   webSystemPrompt(workdir),
		Adapter:  adapter,
	})

	addr := net.JoinHostPort(host, port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	// 启动后在 ctx 取消时优雅关闭。
	go func() {
		<-ctx.Done()
		_ = httpSrv.Close()
	}()

	fmt.Fprintf(os.Stderr, "lidsh: web server listening on http://%s (provider=%s model=%s)\n",
		addr, provider, model)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web: listen: %w", err)
	}
	return nil
}

func webSystemPrompt(workdir string) string {
	return fmt.Sprintf("You are lidsh, a coding agent. Work in directory %s. "+
		"You have shell and file tools; use them, then give a final answer. Be concise.", workdir)
}
