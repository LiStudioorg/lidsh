// Package protocol 复刻 typert 的远程流协议（WS mux + HTTP 一元 RPC 信封）。
//
// 契约来源：docs/_parts/ws-protocol.md §5（实测 @deepseek-ai/dsh@0.1.5-rc.3）。
//
// 物理层（§5.3）：
//   - 路由 /api/remote.mux，只收文本帧
//   - 非法 JSON / 帧型不合法 → close(1008)
//   - Host 每 2s 发协议层 Ping，连续 2 次无 Pong → terminate
//
// 浏览器→Host 仅 2 种帧：open（发起流）、cancel（取消流）。
// Host→浏览器 4 种：ready、item、end、error，外加 emit/waterfall（$events 流内）。
package protocol

import "encoding/json"

// ---------- 浏览器 → Host ----------

// ClientOpen 发起一个远程流（浏览器→Host，§5.4）。
type ClientOpen struct {
	Type     string          `json:"type"` // "open"
	StreamID string          `json:"streamId"`
	Endpoint string          `json:"endpoint"` // "session/follow" | "$events" | ...
	Payload  json.RawMessage `json:"payload"`
}

// OpenPayload 是 open 帧的 payload（args 是端点参数）。
type OpenPayload struct {
	Args json.RawMessage `json:"args"`
}

// ClientCancel 取消一个流（浏览器→Host，§5.4）。
type ClientCancel struct {
	Type     string `json:"type"` // "cancel"
	StreamID string `json:"streamId"`
}

// IsOpen 判断 browser 帧类型。
func IsClientOpen(t string) bool          { return t == "open" }
func IsClientCancel(t string) bool        { return t == "cancel" }

// ---------- Host → 浏览器 ----------

// ServerReady 是 $events 每代流第一帧。
type ServerReady struct {
	Type     string     `json:"type"` // "ready"
	ClientID string     `json:"clientId"`
	Host     ServerHost `json:"host"`
}

type ServerHost struct {
	Home string `json:"home"`
}

// ServerItem 是一个业务产出值帧。
type ServerItem struct {
	Type     string          `json:"type"` // "item"
	StreamID string          `json:"streamId"`
	Value    json.RawMessage `json:"value,omitempty"` // 可省略
}

// ServerEnd 正常结束流。
type ServerEnd struct {
	Type     string `json:"type"` // "end"
	StreamID string `json:"streamId"`
}

// RemoteError 是失败帧的 error 体（§5.4）。
type RemoteError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

// ServerError 是失败帧。
type ServerError struct {
	Type     string      `json:"type"` // "error"
	StreamID string      `json:"streamId"`
	Error    RemoteError `json:"error"`
}

// ServerEmit 是 $events 流的广播事件帧（§5.5.1）。
type ServerEmit struct {
	Type  string          `json:"type"` // "emit"
	Event string          `json:"event"`
	Args  json.RawMessage `json:"args"` // 事件参数数组（lossless JSON）
}

// ServerWaterfall 是 $events 流的瀑布请求帧（§5.5.2）。
type ServerWaterfall struct {
	Type     string          `json:"type"` // "waterfall"
	Event    string          `json:"event"`
	EventID  string          `json:"eventId"`
	AgentID  string          `json:"agentId"`
	Request  json.RawMessage `json:"request"`
}

// ServerStreamCancel 是 $events 流的瀑布取消帧（§5.5.3）。
type ServerStreamCancel struct {
	Type    string `json:"type"` // "cancel"
	EventID string `json:"eventId"`
}

// ---------- HTTP 一元 RPC 信封（§5.2） ----------

// RPCRequest 是 HTTP 一元 RPC 请求信封。
type RPCRequest struct {
	Type    string          `json:"type"` // "client-request"
	RPCID   string          `json:"rpcId,omitempty"`
	Method  string          `json:"method"` // "$events/result" | "session/prompt" | ...
	Payload json.RawMessage `json:"payload"`
}

// RPCPayload 是请求的 args 包装。
type RPCPayload struct {
	Args json.RawMessage `json:"args"`
}

// RPCResponse 是 HTTP 一元 RPC 响应信封（错误永不 throw，只 ok:false，§5.2）。
type RPCResponse struct {
	Ok    *bool           `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error *RPCError       `json:"error,omitempty"`
}

// RPCError 是 RPC 响应的 error 体。
type RPCError struct {
	Name    string          `json:"name"`
	Message string          `json:"message"`
	Code    string          `json:"code,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

// OK 构造成功响应。
func OK(value any) RPCResponse {
	b, _ := json.Marshal(value)
	t := true
	return RPCResponse{Ok: &t, Value: json.RawMessage(b)}
}

// OKVoid 构造无值成功响应（value:undefined）。
func OKVoid() RPCResponse {
	t := true
	return RPCResponse{Ok: &t}
}

// Fail 构造失败响应。
func Fail(code, message string) RPCResponse {
	f := false
	return RPCResponse{Ok: &f, Error: &RPCError{Name: "Error", Message: message, Code: code}}
}
