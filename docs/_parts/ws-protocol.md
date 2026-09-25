# WS 远程协议 `/api/remote.mux` + `/api` HTTP RPC（第 5 节）

BASE = `/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`。
核心文件：`dsh-api-gateway/lib/index.js`（Host 侧）、`dsh-api-gateway/lib/client.js`（浏览器侧，同一协议的双面）、`dsh-client-connection/lib/index.js` + `lib/client.js`（RPC 信封与鉴权）、`dsh-api-session-controller/lib/*`（session 业务 Remote）、`dsh-api-remotes/lib/index.js`（转发事件白名单）。

## 5.0 总体结构

- **一元调用**走 HTTP：`POST /api/<namespace>/<method>`，JSON 信封 `client-request`/`server-response`（不是 WS）。
- **流式调用**（Typert `@Remote({mode:'stream'})` 方法）与 **`$events` 事件流**走 WebSocket：唯一路由 `WS /api/remote.mux`，一条物理连接上多路复用逻辑流（mux），每帧一个 JSON 文本消息。
- 端点格式恒为 `<namespace>/<method>` 两段；wire payload 恒为 `{args: {...}}`（恰好一个 `args` 键）：gateway 校验 `remoteRequest()`（gateway/index.js:925-936）。
- `stream` 模式方法**不能**走一元 RPC，一元方法**不能**走流载体，否则 `gateway/signature-invalid`（gateway/index.js:540,555）。

## 5.1 鉴权（两条通道共用）

`dsh-client-connection/lib/index.js`：
- 进程启动 token：`GET /?token=<launchToken>` 校验通过后 `Set-Cookie` 并 307 到干净 `/`（:389-405）。查询参数名 `token`（:222）。
- Cookie 名：`dsh-auth-` + base64url(sha256(authority))（`COOKIE_PREFIX` :223、`cookieName` :280-282）；值：`v1.<base64url(JSON payload)>.<base64url(HMAC-SHA256(secret,body))>`（:298-300），签名密钥 32 字节存 credential 记录 `client-connection/browser-session`（:219-225）。属性：`HttpOnly; SameSite=Strict; Path=/`（:292-293），默认 30 天（:740）。
- 每个 `/api` 请求（含 WS upgrade）先过 `connection.requestRejection(req)`（browser-trust fence：Host 头必须是 loopback 或 `trustedHosts`；api-request-trust.d.ts:1-37），失败 gateway 直接回 `401 Unauthorized` / `403 Forbidden` 纯文本（gateway/index.js:377-388, 463-467）。
- 结论（Go 复刻要点）：浏览器先用一次性 token 换 cookie，之后 HTTP 与 WS 都靠该 cookie；WS 升级请求与普通请求同一鉴权函数。

## 5.2 HTTP 一元 RPC 信封

请求（浏览器→Host）：`POST /api/{namespace}/{method}`，头 `content-type: application/json`，体（`createWebConnectionRpc`，client-connection/lib/client.js:6194-6215）：

```json
{
  "type": "client-request",
  "rpcId": "8e4a…uuid…",
  "method": "session/prompt",
  "payload": { "args": { "request": { "...": "wire 参数按名放置" } } }
}
```

- `rpcId` 客户端生成（randomUuid），响应必须回显一致（client.js:6214）。
- Host 侧 zod 校验信封（client-connection/lib/index.js:480-511）；`method` 与 URL endpoint 不一致直接错误（:651）。
- 响应（恒 HTTP 200，除非 transport 层失败）：

```json
{
  "type": "server-response",
  "rpcId": "8e4a…uuid…",
  "result": { "ok": true, "value": { "accepted": true } }
}
```
或业务失败（RemoteError 原样编码，gateway/index.js:968-989）：
```json
{
  "type": "server-response",
  "rpcId": "…",
  "result": { "ok": false, "error": { "code": "session/agent-busy", "message": "…", "details": { "reason": "…" } } }
}
```
未知异常折叠为 `code:"gateway/internal", details:{}`（:978-985）。取消：`gateway/cancelled`（:964-967）。

`wire args` 的规则：每个 Remote 方法的参数按**声明顺序**映射为同名 JSON 字段（descriptor.parameters[].wire，typert.host.js 逐方法注册；SCOPE 方法额外带 `agentId` lookup 字段，如 `fileReferences/list` 的 `args:{agentId, query}`，typert.remote-client.js:692-731）。参数为 undefined 时省略该键（gateway client.js:1661-1663）。

### 主要 session 一元端点（namespace `session`，除注明外）

来源：`dsh-api-session-controller/lib/typert.remote-client.js` descriptors（692-1117）与 `lib/types/types.d.ts`。

| endpoint | args | result |
|---|---|---|
| `session/list` | `{}`（`_request` 可含 `cursor?`） | `{items: SessionSummary[]}` |
| `session/search` | `{query}` | `{items:[{sessionId,snippet}],hasMore}`（上限 20，types.d.ts:161） |
| `session/create` | `{workspaceId?,cwd?,sessionId?,agentPreset?}` | `{sessionId,agentPreset?}` |
| `session/selectModel` | `{sessionId,provider,model,reasoningEffort?}` | `{selected}` |
| `session/modelCatalog` | 无参 | `{default,routableProviders[],groups[],failures[]}`（types.d.ts:127-133） |
| `session/prompt` | `{request:{requestId,sessionId,mode:'queue'|'steer',content:PromptContentPart[],clientTimeZone?}}` | `{accepted:true}` |
| `session/cancel` | `{request:{sessionId}}` | `{accepted:true}` |
| `session/page` | `{request:{address,throughSeq,beforeSeq?,maxMessages?}}` | `{records:[{type:'event',event}],hasMore}` |
| `session/rename` | `{request:{sessionId,title}}` | `{title,seq}` |
| `session/fork` | `{request:{sessionId,atSeq?}}` | `{sessionId}` |
| `session/attachment` | `{request:{sessionId,attachmentId}}` | `{attachment,data /*base64*/}` |
| `session/updateQueue` | `{request:{sessionId,itemId,action}}` | `{accepted:true}` |
| `session/canOpenWorkspacePath` | 无参 | `boolean` |
| `session/openWorkspacePath` | `{request:{action?:'reveal',path}}` | `{opened:true}` |
| `skills/list`（namespace `skills`） | `{sessionId}` | `{skills:[{name,description,whenToUse?,modelInvocable}]}` |
| `fileReferences/list`（namespace `fileReferences`，scope agent） | `{agentId,query}` | `[{path,kind:'file'|'directory'}]` |
| `$events/result` | `{args:{clientId,eventId,outcome}}` | `{ok:true,value:null}`（见 5.5） |

PromptContentPart（types.d.ts:64-75）：`{type:'text',text}` | `{type:'image',mediaType:'image/png'|'image/jpeg'|'image/webp'|'image/gif',data /*base64*/,name?}` | `{type:'file',receiptId}`（receiptId 来自先行的文件上传，见 dsh-client-file-upload：`POST /api/session/uploadFileBinary`，exact Fetch 路由，dsh-client-file-upload/lib/index.js:73）。

## 5.3 WS mux 物理层

- 路由：`/api/remote.mux`（gateway/index.js:11）。URL 由页面 origin 换成 ws/wss（gateway client.js:541-547）。
- **只接受文本帧**；二进制帧 → `close(1003,"text messages required")`；JSON 非法或帧型不合法 → `close(1008,"invalid Remote stream request")`（gateway/index.js:287-296, 293-295）。客户端收到非法服务帧 → `close(4002,"invalid Remote stream frame")`（client.js:486）。主动重连 close code 4000（client.js:323）。
- **心跳**：Host 侧 `setInterval` 发 **WebSocket 协议层 Ping 控制帧**（不是 JSON 帧），默认间隔 `websocketHeartbeatIntervalMs=2000`（gateway/index.js:398,433,253-267）；连续 `MAX_MISSED_HEARTBEATS=2` 次无 Pong → `socket.terminate()`（:197,256-261）。浏览器原生自动回 Pong，Vue 端无需处理（Go 服务端同样用 `ws` 的 ping/pong 即可）。

## 5.4 WS mux 逻辑流帧（确切 JSON）

校验器：`parseRemoteStreamClientMessage`（gateway/index.js:122-133，**键集必须精确匹配**）、`parseRemoteStreamServerMessage`（gateway client.js:111-130）。`streamId` 为非空字符串（浏览器端用 `randomUUID()`，client.js:341）。

### 浏览器 → Host（仅 2 种）

发起流（调用 `session/follow` 为例）：
```json
{
  "type": "open",
  "streamId": "0f1c2a3b-…-uuid",
  "endpoint": "session/follow",
  "payload": {
    "args": {
      "request": {
        "address": { "kind": "session", "sessionId": "ses_…" },
        "maxMessages": 50,
        "assistantStream": true
      }
    }
  }
}
```
`follow` 的 request schema（typert.remote-client.js:226-238）：`address = {kind:'session',sessionId} | {kind:'subagent',parentSessionId,childSessionId,mode:'one-shot'|'continuable'}`；`maxMessages?: number`；`assistantStream?: true`。

打开事件流：
```json
{ "type": "open", "streamId": "…uuid…", "endpoint": "$events", "payload": { "args": {} } }
```

取消（消费者提前退出或 signal abort 时客户端自动发）：
```json
{ "type": "cancel", "streamId": "0f1c2a3b-…-uuid" }
```
（Host 只 abort 对应 AbortController；重复 streamId 视为协议错误关连接，gateway/index.js:309。）

### Host → 浏览器（3 种）

每个业务产出值一帧（`session/follow` 的 value 即 SessionFollowFrame，见 5.6；`$events` 的 value 见 5.5）：
```json
{ "type": "item", "streamId": "…", "value": { "type": "event", "event": { "type": "user/message", "seq": 12, "time": 1735100000000, "data": { "…": "…" } } } }
```
（`value` 可省略——`item` 允许只有 `{type,streamId}`，client.js:113-117。）

正常结束（abort 后不发 end，只静默清理）：
```json
{ "type": "end", "streamId": "…" }
```

失败（RemoteError 编码）：
```json
{ "type": "error", "streamId": "…", "error": { "code": "session/not-found", "message": "…", "details": {} } }
```
（`details` 必为对象，client.js:119-127 精确校验三键。）

发送串行化：每连接一个写队列 promise 链，顺序 = 产出顺序（gateway/index.js:346-365）。

## 5.5 `$events` 事件流与 `$events/result` 回环

来源：gateway/index.js:480-727 + gateway client.js:549-757 + dsh-api-remotes/lib/index.js。

打开 `$events` 后**第一帧必是 ready**（gateway/index.js:599-604；clientId=randomUUID，host.home=Host 的 homedir()）：
```json
{ "type": "ready", "clientId": "9b7…uuid…", "host": { "home": "/root" } }
```

此后每帧四种之一：

1. **广播事件**（Cordis emit 模式转发；args 是事件参数数组，必须 lossless JSON）：
```json
{ "type": "emit", "event": "api-session/status", "args": ["ses_…", true] }
```
2. **waterfall 请求**（Host 把需要浏览器裁决的瀑布事件下发；`request` = 原请求对象去掉 `agent`/`signal` 键；`agentId` 为 Agent=Session id）：
```json
{
  "type": "waterfall",
  "event": "approval/request",
  "eventId": "d3a…uuid…",
  "agentId": "ses_…",
  "request": { "…": "宿主事件请求体，如审批的 {toolName, args, sandboxMode…}" }
}
```
3. **waterfall 取消**（Host 侧事件被取消/超时/Agent 释放；浏览器应中止对应 UI）：
```json
{ "type": "cancel", "eventId": "d3a…uuid…" }
```

（第四种即新的 ready，重连后每一代流重新 ready。）

**转发事件白名单**（`API_REMOTE_FORWARDED_EVENTS`，dsh-api-remotes/lib/index.js:17-94；mode 决定 emit 还是 waterfall）：
`agent-preset/selected(emit)`、`approval/request(waterfall)`、`api-session/activity|added|error|removed|status(emit)`、`commands/change`、`credentials/reference-updated`、`goal/activation-changed`、`cordis/request-run|request-run-resolved|dynamic-package|dynamic-retract|inspect-query|inspect-query-resolved`、`llm/adapters-updated`、`settings/document-updated`、`user-questions/request(waterfall)`。

**waterfall 应答走 HTTP 回环**：浏览器处理完后调用一元 RPC `$events/result`（gateway client.js:654-680），即：

```
POST /api/$events/result
{
  "type": "client-request",
  "rpcId": "…",
  "method": "$events/result",
  "payload": { "args": {
      "clientId": "9b7…uuid…",
      "eventId": "d3a…uuid…",
      "outcome": { "kind": "result", "value": "allowed-once" }
  }}
}
```
outcome 三种（gateway/index.js:21-50 精确键校验）：
- `{ "kind": "next" }` —— 本客户端不处理，让 Host 走 next（所有已投递客户端都回 next 才 settle）；
- `{ "kind": "result" }` 或 `{ "kind": "result", "value": <JSON 值> }` —— 第一个 result 决定瀑布返回值（value 可省略）；
- `{ "kind": "rejected", "error": { "name": "Error", "message": "…", "code": "…", "details": {…} } }`（`code`/`details` 可选，name/message 必填字符串）→ Host 侧该瀑布以重建的 Error 失败。
多浏览器同时在线时事件投递给**每个** client，谁先回 result 谁定胜负；`clientId` 不认识时该 result 被丢弃（gateway client 端返回 `{ok:true,value:undefined}`，gateway/index.js:567-578）。

## 5.6 `session/follow` 流的价值帧（Vue 前端核心数据面）

`follow` 产出 `SessionFollowFrame`（`dsh-api-session-controller/lib/types/types.d.ts:475-486`）：

```jsonc
// 1) 打开快照（每代流恰好一帧，先于一切 event）
{ "type": "snapshot",
  "header": { "version": 1, "id": "ses_…", "createdAt": 1735099000000, "cwd": "/repo",
              "parentSession": "ses_…", "isSeeded": false, "origin": "subagent",
              "delegationDepth": 1, "agentPreset": "standard" },          // :372-383
  "cursor": 41,                       // 已含 records 的最后一 seq
  "records": [ { "type": "event", "event": { …SessionWireEvent… } } ],    // 对齐消息的历史前缀
  "hasMore": true,
  "projections": { "asOfSeq": 41, "values": { "sessionListMetadata": {…}, "modelSelection": {…}, "imageLimits": {…} } }, // :52-56
  "assistantStream": {                // 仅当请求 assistantStream:true 且当前有活动 attempt
    "revision": 7,
    "activeAttempt": { "attemptId": "…", "startedAfterSeq": 40, "turn": 3, "step": 2,
                       "nextIndex": 128, "stream": [ …AssistantStreamRecord… ] } }
}
```

```jsonc
// 2) 增量 durable 事件（seq 连续无洞）
{ "type": "event",
  "event": { "type": "assistant/message", "seq": 42, "time": 1735100005000,
             "data": { "turn": 3, "step": 2,
                       "message": { "id": "…", "role": "assistant",
                                    "content": [ {"type":"text","text":"…"},
                                                 {"type":"reasoning","text":"…"},
                                                 {"type":"tool-call","id":"call_…","name":"bash","arguments":"{…}"} ],
                                    "source": { "kind": "model", "provider": "deepseek-official", "model": "deepseek-v4-flash", "replayState": {…} } },
                       "stream": [ …compact records… ],
                       "usage": { "inputTokens": 812, "outputTokens": 155, "cacheReadTokens": 768 },
                       "interrupted": true },                             // 可选
             "surfaceOp": "append", "sourceEventSeqs": [ … ] } }          // surface 事件才有
```

```jsonc
// 3) 过程内 assistant 呈现帧（assistantStream:true 才有；未落盘的实时 token 流）
{ "type": "assistant-stream", "frame":
  { "type": "start", "attemptId": "att_…", "revision": 8, "startedAfterSeq": 41, "turn": 4, "step": 1 } }
{ "type": "assistant-stream", "frame":
  { "type": "chunk", "attemptId": "att_…", "revision": 8, "index": 0, "time": 1735100006000,
    "chunk": { "type": "text-delta", "index": 1, "text": "你好" } } }      // chunk = StreamChunk 原样（第 1 节）
{ "type": "assistant-stream", "frame":
  { "type": "end", "attemptId": "att_…", "revision": 8, "index": 57,
    "outcome": { "kind": "committed", "eventType": "assistant/message", "seq": 60 } } }
    // 或 { "kind": "abandoned" }
```
（`SessionAssistantStreamFrame` 定义 types.d.ts:441-468。）

事件类型全集 `SessionEventMap`（dsh-session/lib/types/types.d.ts:242-404）：`turn/start{turn}`、`turn/end{turn,reason}`、`step/start{turn,step}`、`step/end{turn,step}`、`user/message`(UserMessage)、`system/message{turn,step,message}`、`assistant/message{turn,step,message,stream,usage?,interrupted?}`、`assistant/attempt{turn,step,stream}`、`tool/call{turn,step,callId,name,arguments}`、`tool/result{turn,step,message,error?,meta?}`、`request/header{header,reason,startsSeries?}`、`request/context`、`session/end-seed{inherited?}`；另有 `model/selection`（api-session-controller types.d.ts:29-36）。

**流式分片的持久格式** `AssistantStreamRecord`（dsh-llm/lib/types/assistant-stream.d.ts:16-40；紧凑化算法 client-connection/lib/client.js:1442-1541）——同 index 的连续 delta 打包成时间差分记录：
```jsonc
{ "type": "text-chunks",      "time0": 1735100000000, "index": 1, "dt": [17, 23], "texts": ["你", "好"] }
{ "type": "reasoning-chunks", "time0": …, "index": 0, "dt": [], "texts": ["…"] }
{ "type": "tool-call-chunks", "time0": …, "index": 2, "dt": [], "id": "call_…", "name": "bash", "args": ["{\"comm", "and\":\"ls\"}"] }
{ "type": "chunk", "time": …, "chunk": { "type": "usage", "usage": {…} } }   // block-start/block-end/usage/finish 原样存
```

## 5.7 `session/control` 流（Host 级控制面，第二条常驻流）

`session/control`（stream，args `{}`）产出 `SessionControlFrame`（api-session-controller types.d.ts:523-536）：每代第一帧
```json
{ "type": "baseline", "value": { "queues": {"ses_…": [SessionQueuedItem…]}, "jobs": {"ses_…": [SessionJob…]}, "projections": {"ses_…": {…}} } }
```
后续替换帧：`{type:'queue',sessionId,items}` | `{type:'jobs',sessionId,jobs}` | `{type:'projection',sessionId,key,value,seq}`。
`SessionQueuedItem`={id,placement:'queued'|'steering'|'context',rpcId?,message:{id,content}}；`SessionJob`={id,kind,label,status:'running'|'stopping'|'completed'|'killed'|'failed',detail?,startedAt,finishedAt?}（:488-508）。

## 5.8 RemoteError 码样本（Vue 端错误路由用）

声明于 `RemoteErrorDetailsMap`（api-session-controller types.d.ts:164-214）：`session/model-unavailable{provider,model}`、`session/conflict{sessionId,requestedCwd,existingCwd?}`、`session/agent-busy{reason}`、`session/invalid-time-zone{value}`、`session/workspace-attach-failed{sessionId,workspaceId}`、`agent-preset/conflict{…}`、`session/attachment-invalid{reason}`、`session/queue-item-not-found{itemId}`、`session/steer-unavailable{itemId}`、`session/title-invalid{sessionId}`、`session/fork-unavailable{sessionId}`、`subagent/not-found{parentSessionId,childSessionId}`、`subagent/catalog-diagnostic{…,reason:'corrupt'|'unsupported'|'unavailable'}`、`llm/model-discovery-rejected{settingsNs,baseURL?}`（dsh-llm types.d.ts:252-259）。网关自身：`gateway/signature-invalid|service-unavailable|definition-unavailable|invocation-unavailable|ambiguous-endpoint|context-unavailable|context-failed|context-not-found|lookup-unavailable|lookup-failed|provider-mismatch|method-unavailable|binding-invalid|result-invalid|arguments-invalid|cancelled|internal`（gateway/index.js:536-996）。

## 5.9 复刻要点小结

1. 前端只需一条 WS（`/api/remote.mux`）+ 普通 fetch POST（`/api/<ns>/<method>`）。
2. 打开流 = `open` 帧；停止消费 = `cancel` 帧；断线重连后 `session/follow` 用 `cursor`（throughSeq）重开即可无缝续传（seq 连续，`page` 接口补历史）。
3. `$events` 的 waterfall 应答**不走 WS**，走 `POST /api/$events/result`；UI 未决时对 `cancel` 帧清 UI。
4. 服务端要按帧精确键集校验（多键即拒），并对 item/end/error 保持每流顺序。
