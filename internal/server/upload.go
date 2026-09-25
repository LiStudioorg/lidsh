// 附件文件上传接收（attachment-token.md §A1）：专用 HTTP 原始字节路由
// POST /api/session/uploadFileBinary?sessionId=&name=，body 即文件原始字节
// （无 multipart、非 WS 二进制帧）。响应恒 HTTP 200：
//
//	{ok:true, value:{receiptId, file:{attachmentId,name,bytes}}}   // FileUploadValue
//	{ok:false, error:{code,message,details}}
//
// 约束：仅 POST 否则 405；content-type application/octet-stream 否则 415；
// sessionId 缺失/空否则 400；cache-control: no-store。
//
// 存储：内容寻址不可变对象 <Home>/attachments/v1/file-objects/<sha[:2]>/<sha>，
// attachmentId = "sha256:"+hex(sha256(bytes))；每成功一次铸造随机 receiptId，
// 暂存到会话作用域（stagedFiles），供后续 prompt 的 {type:'file',receiptId} 解析。
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lidsh/internal/session"
)

const uploadMaxBytes = 64 << 20 // 64 MiB 上限

// registerUpload 在根 mux 上挂文件上传路由。
func (s *Server) registerUpload(mux *http.ServeMux) {
	mux.HandleFunc("/api/session/uploadFileBinary", s.handleFileUpload)
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeUploadFail(w, "gateway/bad-request", "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
		writeUploadFail(w, "gateway/bad-request", "content-type must be application/octet-stream", http.StatusUnsupportedMediaType)
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		writeUploadFail(w, "gateway/bad-request", "sessionId query required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	_, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		writeUploadFail(w, "session/not-found", "session not found", http.StatusOK)
		return
	}

	body, err := readAllCapped(r.Body, uploadMaxBytes)
	if err != nil {
		writeUploadFail(w, "gateway/bad-request", "body too large or unreadable", http.StatusOK)
		return
	}

	// 内容寻址存储（不可变对象布局，§A2：file-objects/<sha[0:2]>/<sha>，2 位 hex）。
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	id := "sha256:" + sha
	dir := filepath.Join(s.Opts.Home, "attachments", "v1", "file-objects", sha[:2])
	obj := filepath.Join(dir, sha)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeUploadFail(w, "gateway/internal", "storage unavailable", http.StatusOK)
		return
	}
	// EEXIST 幂等（内容寻址去重）；否则原子写就位。
	if _, err := os.Stat(obj); os.IsNotExist(err) {
		tmp := obj + ".tmp"
		if err := os.WriteFile(tmp, body, 0o600); err != nil {
			writeUploadFail(w, "gateway/internal", "storage unavailable", http.StatusOK)
			return
		}
		if err := os.Rename(tmp, obj); err != nil && !os.IsExist(err) {
			_ = os.Remove(tmp)
			writeUploadFail(w, "gateway/internal", "storage unavailable", http.StatusOK)
			return
		}
	}

	name := fileLeafName(r.URL.Query().Get("name"))
	receiptID := session.NewUUID()

	s.stageMu.Lock()
	if s.staged == nil {
		s.staged = map[string]map[string]stagedFile{}
	}
	if s.staged[sessionID] == nil {
		s.staged[sessionID] = map[string]stagedFile{}
	}
	s.staged[sessionID][receiptID] = stagedFile{attachmentID: id, name: name, bytes: int64(len(body))}
	s.stageMu.Unlock()

	writeUploadOK(w, map[string]any{
		"receiptId": receiptID,
		"file":      map[string]any{"attachmentId": id, "name": name, "bytes": len(body)},
	})
}

// stagedFile 是一次已暂存、待 prompt {type:'file',receiptId} 引用的文件收据。
type stagedFile struct {
	attachmentID string
	name         string
	bytes        int64
}

// resolveReceipt 按会话解析文件收据（M1b：给 prompt 的文件部件解析用）。
func (s *Server) resolveReceipt(sessionID, receiptID string) (stagedFile, bool) {
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	if s.staged == nil {
		return stagedFile{}, false
	}
	f, ok := s.staged[sessionID][receiptID]
	return f, ok
}

// retireReceipt 在 user/message 落地后按 rpcId 退役该会话收据（节省内存）。
func (s *Server) retireReceipt(sessionID, receiptID string) {
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	if s.staged == nil {
		return
	}
	if m, ok := s.staged[sessionID]; ok {
		delete(m, receiptID)
		if len(m) == 0 {
			delete(s.staged, sessionID)
		}
	}
}

// fileLeafName 复刻净化规则（§A2：剥离分隔符、删控制字符、Windows 非法字符→'_'、
// UTF-8 ≤255 字节、空/./.. → "file"）。
func fileLeafName(s string) string {
	if s == "" {
		return "file"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\':
			// 剥分隔符
			continue
		case r < 0x20 || r == 0x7f:
			continue
		case strings.ContainsRune("<>:\"|?*", r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		out = "file"
	}
	// UTF-8 前缀截断 ≤ 255 字节。
	res := []byte(out)
	if len(res) > 255 {
		res = res[:255]
	}
	return string(res)
}

func writeUploadOK(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = writeJSON(w, map[string]any{"ok": true, "value": value})
}

func writeUploadFail(w http.ResponseWriter, code, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = writeJSON(w, map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message, "details": map[string]any{}}})
}

func writeJSON(w http.ResponseWriter, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}
