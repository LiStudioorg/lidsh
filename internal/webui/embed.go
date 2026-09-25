// Package webui 嵌入前端构建产物（web/dist → internal/webui/dist）并以单二进制
// 静态服务。M1c：Vue3 前端经 vite build 产出到 internal/webui/dist，
// 这里用 //go:embed 打进可执行文件；Handler 提供静态文件 + SPA fallback。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// FS 是嵌入的前端文件系统（子路径不含 dist 前缀）。
var FS fs.FS

func init() {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // 说明没跑 vite build；发行前必须构建。
	}
	FS = sub
}

// Handler 返回前端静态服务根 handler。对未知路径回退到 index.html（SPA 路由）。
// API 路径（/api/*）由上层 mux 先接管，不会落到这里。
func Handler() http.Handler {
	fileServer := http.FileServer(http.FS(FS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || p == "index.html" {
			serveIndex(w)
			return
		}
		// 有对应静态文件则直接服务；否则 SPA fallback。
		if _, err := fs.Stat(FS, p); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w)
	})
}

func serveIndex(w http.ResponseWriter) {
	data, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		http.Error(w, "frontend not embedded (run: cd web && pnpm build)", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
