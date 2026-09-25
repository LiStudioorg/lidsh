# DSH 外部集成层逆向规格说明（Go + Vue3 复刻参考）

> 调研对象：`BASE=/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`（已安装编译产物 JS + .d.ts，全程只读）。
> 全部断言附 `文件:行号`（相对 BASE）；找不到的项在文中标「未找到」。本文档由多个只读调研通道汇总，行号经过交叉复核。
>
> 目录：
> 1. LLM 抽象层接口 —— §1
> 2. DeepSeek provider 具体实现（含 web 工具附录）—— §2
> 3. 模型目录与配置、API key、DSH_* 环境变量 —— §3
> 4. token 计量 —— §4
> 5. `/api/remote.mux` WS 远程协议完整帧格式 —— §5
> 6. MCP 接入 —— §6
> 7. Skills 机制 —— §7
> 8. 附件/上传与图片下采样 —— §8
> 9. 子代理与工作流 —— §9
> 10. bash 沙箱 —— §10

---

# §1–§2 LLM 抽象层接口 与 DeepSeek provider 实现

## 1. LLM 抽象层接口（dsh-llm）

### 1.1 Provider 适配器必须实现的接口

`LlmAdapter` 抽象类（`dsh-llm/lib/types/index.d.ts:126-183`）。**唯一必须实现的方法是 `stream()`**，其余都有默认实现：

| 方法 | 签名 | 说明 | 出处 |
|---|---|---|---|
| `stream` (abstract) | `(options: GenerateOptions) => AsyncIterable<StreamChunk>` | 唯一必需方法，必须尊重 `options.signal` | index.d.ts:182 |
| `providerInfo` | `(provider: string) => LlmProviderInfo` | `{id, name}`，id 必须等于入参 | index.d.ts:132 |
| `providerRetryPolicy` | `(provider: string) => ResolvedRetryPolicy \| undefined` | undefined 走默认 | index.d.ts:138 |
| `imageRequestPricing` | `(provider, model) => LlmImageRequestPricing \| undefined` | 同步、禁 I/O，供 token meter 调用 | index.d.ts:148 |
| `listModels` | `(provider) => Promise<readonly LlmModelInfo[]>` | 目录是 advisory，不校验请求 | index.d.ts:156 |
| `resolveModel` | `(provider, model, signal?) => Promise<LlmResolvedModelInfo>` | 精确模型元数据 | index.d.ts:166 |
| `prepareCall` | `(provider, model, signal?) => Promise<PreparedAdapterCall>` | 把模型元数据与一次 dispatch 绑定到同一 adapter 世代 | index.d.ts:176 |

注册方式：`ctx.llm.registerAdapter(providers: string[], adapter): AdapterRegistrationHandle`（`index.d.ts:248`），冲突抛 `LlmError` code=`DUPLICATE_ADAPTER`（`dsh-llm/lib/index.js:1810`）；handle 带原子 `replace(providers: string[])`（`index.d.ts:205`）。适配器变更事件：Cordis 事件 `llm/adapters-updated`（`dsh-llm/lib/types/types.d.ts:21`）。

`LlmRuntime`（服务名 `llm`，`index.d.ts:231-408`）关键 API：
- `stream(options)`：最外层流 API；适配器选择/dispatch/迭代失败被归一为终态 `error`/`aborted` finish chunk（`index.d.ts:394-406`）
- `prepareCall(config: LlmCallConfig)` → `PreparedLlmCall`（config/retryPolicy/context/modalities + 一次性 `stream()`；复用或 config 变化抛 `INVALID_PREPARED_CALL`，`index.js:2165-2166`）
- `resolveModelInfo / resolveCallConfig / listModels / listProviders / providerRetryPolicy / imageRequestPricing / fileRequestText`
- `registerConfigurableProviders(entries)` / `listConfigurableProviders()`（可配置 provider 目录，`index.d.ts:276-281`）
- `registerModelDiscovery(settingsNs, discover)` / `discoverModels(settingsNs, request, signal)`（草稿端点探测，`index.d.ts:292-303`）；`LlmModelDiscoveryRequest = {provider?, baseURL?, api?, apiKey?}`（types.d.ts:229-246）
- waterfall 事件：`llm/stream(options, next)`（index.d.ts:45）

`LlmCallConfig`（`call-config.d.ts:170-177`）：`provider, model, reasoningEffort?, temperature?, maxTokens?, stop?`——与 `GenerateOptions` 同名字段 1:1，是会话头部状态。

### 1.2 统一请求类型 GenerateOptions

`dsh-llm/lib/types/types.d.ts:404-444`：

| 字段 | 类型 | 说明 | 行号 |
|---|---|---|---|
| `provider` | `string` | 选择 adapter 的路由键 | 406 |
| `model` | `string` | 模型 id | 407 |
| `reasoningEffort` | `ReasoningEffortId?` | 精确模型上选推理档位 | 409 |
| `messages` | `Message[]` | 模型所见顺序消息（loop 请求首条 system 携带系统提示） | 416 |
| `system` | `string?` | 一次性调用的系统提示（loop 请求留 undefined） | 421 |
| `tools` | `ToolSchema[]?` | `{name, description, parameters: Record<string,unknown>}`（JSON Schema） | 423, 397-402 |
| `temperature` | `number?` | 424 |
| `maxTokens` | `number?` | 425 |
| `stop` | `string[]?` | 停止序列，映射到 provider stop 字段 | 431 |
| `signal` | `AbortSignal?` | 432 |
| `sessionId` | `Branded<'SessionId'>?` | 请求路由/回放游标隔离 | 437 |
| `purpose` | `'compaction' \| 'session-title'?` | 辅助调用分类 | 443 |

### 1.3 消息与内容块

`Message`（`message.d.ts:120-129`）：`{id: MessageId, role: 'system'|'user'|'assistant', content: ContentBlock[], source: MessageSource}`。
- `MessageSourceMap`（message.d.ts:94-104）：`user {kind:'user'}` / `plugin {kind:'plugin', plugin, ...ContextFormed}` / `model ModelMessageSource {kind:'model', provider, model, replayState?}` / `tool ToolMessageSource {kind:'tool', callId}`。
- `ContextForm`（message.d.ts:42-54）：`'instructions' | 'catalog' | 'snapshot' | 'notice' | 'relay' | 'recall'`；notice 的 summary 上限 120 字符（message.d.ts:110）。
- 专用消息：`ToolResultMessage`（role 为 user，content 是单元素 `[ToolResultBlock]`，source `{kind:'tool',callId}`，message.d.ts:149-153）；`SystemMessage`（message.d.ts:144-147）。

内容块 `ContentBlockMap`（types.d.ts:91-98，按 `type` 判别可合并扩展）：

| type | 字段 | 行号 |
|---|---|---|
| `text` | `{type:'text', text: string}` | 39-42 |
| `reasoning` | `{type:'reasoning', text: string}`（思考内容，与可见文本分离） | 44-47 |
| `image` | `{type:'image', attachment: ImageAttachmentRef}`（只有 user 消息可携带） | 54-58 |
| `file` | `{type:'file', attachment: FileAttachmentRef}`（永不原样到 provider，请求组装时投影为确定性 handle 文本） | 66-70 |
| `tool-call` | `{type:'tool-call', id: ToolCallId, name: string, arguments: string /* 原始 JSON 串 */}` | 72-79 |
| `tool-result` | `{type:'tool-result', toolCallId, content: ContentBlock[], isError?}` | 81-86 |

### 1.4 流式协议 StreamChunk（全部 7 种事件）

`types.d.ts:359-389`（契约注释 351-358：block index 关联交错 delta；`block-end` 带完整块；usage 必须在终态 finish 之前、之后无 chunk；工具参数保持原始 JSON 字符串）：

```ts
type StreamChunk =
  | { type: 'block-start';   index: number; blockType: ContentBlockType }          // :360-362
  | { type: 'text-delta';    index: number; text: string }                          // :363-366
  | { type: 'reasoning-delta'; index: number; text: string }                        // :367-370
  | { type: 'tool-call-delta'; index: number; id: ToolCallId; name?: string; argumentsDelta: string } // :371-376
  | { type: 'block-end';     index: number; block: ContentBlock }                   // :377-380
  | { type: 'usage';         usage: TokenUsage }                                    // :381-383
  | { type: 'finish';        reason: FinishReason; replayState?: ReplayEnvelope }   // :384-389
```

组装器 `BlockAssembler`（`assembler.d.ts:87-139`）：`push(chunk)` / `blocks()`（max-tokens 截断时丢掉不能安全执行的工具调用）/ `interruptedBlocks()`（中断流只保留 text/reasoning 非空块）/ `usage` / `finish`（无 finish chunk 结束时视为 `{kind:'stop'}`）/ `replayState` / `message(source?)`。

### 1.5 Stop reason 取值

`FinishReasonMap`（types.d.ts:107-125）：
- `stop {kind:'stop'}`
- `tool-calls {kind:'tool-calls'}`
- `max-tokens {kind:'max-tokens'}`
- `aborted {kind:'aborted', failure: LlmFailure}`
- `error {kind:'error', failure: LlmFailure}`

### 1.6 TokenUsage（不相交计数约定）

types.d.ts:136-150：`inputTokens`（仅未缓存输入）、`outputTokens`、`totalTokens?`、`cacheReadTokens?`、`cacheWriteTokens?`、`reasoningTokens?`。缓存字段单列；把 cache 命中折进 prompt 总量的 provider（DeepSeek 的 `prompt_tokens`）适配器需自行减出（注释 :130-134）。

### 1.7 错误分类

- `LlmFailure`（types.d.ts:26-37）：`{message, code, status?, providerRetryAfterMs?, requestId?}` —— 可序列化事实，策略决定是否可重试。
- `HarnessError`（error.d.ts:12-16）：`code` 是机器路由码；`LlmError extends HarnessError`（index.d.ts:61-70），附加 `failure: LlmFailure`。
- 规范码（error.d.ts）：`CONTEXT_WINDOW_EXCEEDED`（:18）、`QUOTA`（:20）、`EMPTY_RESPONSE`（:30）、`INVALID_CREDENTIAL`（:38）。
- 运行时观察到的其它码：`NO_ADAPTER`（index.js:2179）、`DUPLICATE_ADAPTER`（index.js:1810）、`REGISTRATION_DISPOSED`（:1795,1896）、`INVALID_PREPARED_CALL`（:2165-2166）、`MISSING_CREDENTIAL`（adapter.d.ts 注释 index.d.ts:98；dsh-llm-deepseek/lib/index.js:1838 区域）、`ABORTED`/`TIMEOUT`/`TRANSPORT`/`AUTH`/`RATE_LIMIT`/`SERVER`/`INVALID_REQUEST`/`HTTP_<status>`/`STREAM_CLOSED`/`MALFORMED_RESPONSE`/`UNSUPPORTED_CONTENT`/`UNSUPPORTED_REASONING_EFFORT`/`REQUEST_EXTENSION`（DeepSeek 适配器，见下）。
- 默认重试策略（`dsh-llm/lib/index.js:232-242`）：`DEFAULT_MAX_RETRIES=5`、`initialDelayMs=500`、`maxDelayMs=10000`、`jitterRatio=0.1`；`DEFAULT_RETRYABLE_CODES = [EMPTY_RESPONSE, RATE_LIMIT, SERVER, TIMEOUT, TRANSPORT]`。策略类型 `NormalRetryPolicyConfig {mode:'normal', maxRetries?, retryableCodes?, backoff?}` / `AlwaysRetryPolicyConfig {mode:'always', backoff?}`（retry-policy.d.ts:20-38）。
- API key 合法性：`normalizeApiKey(raw)` → trim 后判 `empty`/`illegalCharacters`（api-key.d.ts:134-154）。

### 1.8 模型元数据

- `LlmModelInfo`（types.d.ts:277-288）：`provider, id, name, description?, inputModalities?: ('text'|'image')[]`。
- `LlmResolvedModelInfo extends LlmModelInfo`（types.d.ts:321-330）：`context?: {contextWindow}`, `defaultMaxTokens?`, `reasoning?: {efforts: {id,name,description?}[], defaultEffort?}`, `systemPromptUpdate?: 'in-history'`。
- `LlmDiscoveredModel`（types.d.ts:266-275）：`{id, name?, contextWindow?, maxTokens?}`。
- `LlmConfigurableProvider`（types.d.ts:199-222）：`{provider, displayName, settingsNs, settingsPath, declared?, error?}`。
- `ReplayEnvelope`（types.d.ts:338-350）：`{response: unknown, blocks?: unknown[]}`，adapter 私有回放状态，随 `finish` chunk 携带。

## 2. DeepSeek provider 具体实现（dsh-llm-deepseek）

### 2.1 协议与端点

**OpenAI 兼容 chat-completions**（不是 Anthropic）。包注释即写明 "fetch + SSE against a DeepSeek (OpenAI-compatible) chat-completions endpoint"（`dsh-llm-deepseek/lib/types/adapter.d.ts:2`）。
- provider 路由名：`deepseek-official`（`dsh-llm-deepseek/lib/index.js:1840`），插件名 `llm-deepseek`（types/index.d.ts:32）。
- 请求 URL：`POST {baseURL}/chat/completions`（index.js:1770）。
- baseURL 默认：`PUBLIC_BASE_URL = "https://api.deepseek.com"`（index.js:1911；types/index.d.ts:84），环境变量 `DEEPSEEK_BASE_URL` 仅从可信环境层覆盖（index.js:1913）。
- 凭证引用默认：`apiKeyEnv = "DEEPSEEK_API_KEY"`（index.js:1838；types/index.d.ts:43），是 credential ref（环境变量名），不是明文。

### 2.2 鉴权与请求头

`dsh-llm-deepseek/lib/index.js:1660-1668`：

```
authorization: Bearer <apiKey>
content-type: application/json
accept: text/event-stream
user-agent: <attributionHeaders() 生成的 product/version (+url)>   // dsh-llm attribution.d.ts:171-185
x-deepseek-harness-user-id: <匿名 id>
x-deepseek-harness-session-id: <sessionId>            // 可选
x-deepseek-harness-compact: "1"                        // purpose==='compaction' 时
```

requestId 取响应头 `x-request-id` 或 `x-deepseek-request-id`（index.js:1516-1519）；`retry-after` 支持秒数与 HTTP-date（index.js:1507-1515）。

### 2.3 请求体字段（WireRequest）

`dsh-llm-deepseek/lib/types/types.d.ts:12-33`（`lib/types/types.d.ts` 为该文件行号）：

```jsonc
{
  "model": "deepseek-v4-flash",
  "messages": [...],
  "stream": true,
  "stream_options": { "include_usage": true },
  "thinking": { "type": "enabled" | "disabled" },   // 顶层字段，非 extra_body
  "reasoning_effort": "low" | "high" | "max",
  "tools": [ { "type": "function", "function": { "name", "description", "parameters": {...} } } ],
  "temperature": 0.7,
  "max_tokens": 256000,
  "stop": ["..."]
}
```

序列化代码 `requestWithMessages`（index.js:227-249）：可选字段缺省时整个键省略；tools 空数组不发。thinking 解析 `resolveThinking`（index.js:31-41）：`purpose==='session-title'` 强制 disabled；effort `off`→`thinking:{type:'disabled'}`（不发 reasoning_effort）；`low|high|max`→`thinking:{type:'enabled'}` + `reasoning_effort`；未指定时按 profile 默认（默认 `high`，types/index.d.ts:50）。

### 2.4 消息序列化

`serializeMessages`（index.js:134-162）与 `serializeMessagesWithImages`（index.js:171-225）：
- system → `{role:'system', content: string}`。
- assistant → `{role:'assistant', content: string /* 拼接 text 块 */, reasoning_content?: string /* 拼接 reasoning 块回放 */, tool_calls?: [{id, type:'function', function:{name, arguments}}]}`（serializeAssistant index.js:107-125；`reasoning_content` 回放在 thinking 模式的 tool-call 轮是必需的，types.d.ts:78-88 注释）。
- user 文本 → `{role:'user', content: string}`；纯文本 parts 会折叠回 string（`userContent` index.js:99-106）。
- tool-result → 独立 `{role:'tool', tool_call_id, content: text || "(no output)"}`（index.js:155-159）。
- 多模态 user content parts：`{type:'text'|'file'|'image_url'}`；file 部分 `{type:'file', file_id}`（Files API 引用，types.d.ts:45-48），image_url 为 `data:<mediaType>;base64,<...>`（index.js:68-71）。tool-result 里的图片在各自 tool 消息后合并进一条 `{role:'user', content:[{type:'text', text:"Attached image(s) from tool result:"}, ...images]}`（index.js:24,175-185）。

### 2.5 SSE 流解析

`parseSse`（index.js:1107-1114）：`response.body → TextDecoderStream → eventsource-parser 的 EventSourceParserStream`（依赖 `eventsource-parser/stream`，index.js:14；framing/UTF-8/CRLF/BOM/多 data 行合并由该库负责）。逐条 yield `data` 载荷；字面量 `[DONE]` 原样透传给调用方；**流在 `[DONE]` 前 EOF 抛 `LlmError('SSE stream ended without [DONE]', 'STREAM_CLOSED')`**（index.js:1113）。SSE 注释帧通过 onComment 回调仅作 transport 活动信号（喂 idle watchdog）。

`translate(payloads)`（index.js:1208-1319）把 wire chunk 翻译为 StreamChunk：
- 每个 `data:` JSON.parse 成 `WireChunk`（`choices[].delta` + 可选 `usage`，types.d.ts:110-131）；解析失败抛 `MALFORMED_RESPONSE`（index.js:1253）。
- `delta.reasoning_content`：非空字符串才开 reasoning 块（**首帧空串不开块**，types.d.ts:127-130 注释）→ `block-start{blockType:'reasoning'}` + 后续每帧 `reasoning-delta`。
- `delta.content` 非空 → text 块，同上产出 `text-delta`。
- `delta.tool_calls[]`：按 wire `call.index` 复用/新建 harness block（自己的 `nextIndex` 递增分配 index）；`id`/`name` 只在首个 delta 出现，续帧重复的 `''`/`null` 视为"不变"（`acceptIdentity`，index.js:1178-1180）；`function.arguments` 分片直接拼进块并即时产出 `tool-call-delta {index, id, name?, argumentsDelta}`。
- `finish_reason`：`stop→{kind:'stop'}`、`tool_calls→{kind:'tool-calls'}`、`length→{kind:'max-tokens'}`、其它（如 content_filter）→`{kind:'error', failure:{message:"model stopped: <r>", code:<r>.toUpperCase()}}`（`mapFinishReason` index.js:1131-1144）。finish 与 usage 都**推迟到 `[DONE]`** 才发（先按流顺序发所有 `block-end`，再 `usage`，最后 `finish`；index.js:1225-1247）。stop/缺省 finish 且没有任何块 → 替换为 `{kind:'error', failure:{code:'EMPTY_RESPONSE'}}`（index.js:1236-1246）。
- usage 映射 `mapUsage`（index.js:1154-1166）：`cacheRead = prompt_tokens_details.cached_tokens ?? prompt_cache_hit_tokens`；`inputTokens = prompt_tokens - cacheRead`（DeepSeek 的 prompt_tokens 含缓存命中，harness 约定不相交计数）；`totalTokens = prompt+completion`（仅当与 wire total_tokens 一致时给）；`reasoningTokens = completion_tokens_details.reasoning_tokens`。

wire usage 字段（types.d.ts:153-170）：`prompt_tokens, completion_tokens, total_tokens?, prompt_cache_hit_tokens?, prompt_cache_miss_tokens?, prompt_tokens_details.cached_tokens?, completion_tokens_details.reasoning_tokens?`。

### 2.6 错误映射与运行保护

- HTTP → code：`httpErrorCode`（index.js:1526-1542）：401/403→`AUTH`；413→`INVALID_REQUEST`；quota 文案（code/type/message 拼接判定）→`QUOTA`；429→`RATE_LIMIT`；400 且上下文溢出文案→`CONTEXT_WINDOW_EXCEEDED` 否则 `INVALID_REQUEST`；>=500→`SERVER`；其余 `HTTP_<status>`。错误体 `{error:{message,type,code}}`（types.d.ts:171-177），message 优先取 provider 的。
- 流闲置 watchdog：每次 read 最长 `streamIdleTimeoutMs`（默认 300000，index.js:1390），超时 `TIMEOUT`（码 `LLM_STREAM_IDLE_TIMEOUT`，index.js:1411,1642）；调用方 abort → `ABORTED`；其余 fetch 失败 → `TRANSPORT`（index.js:1641-1645）。
- Files API 失败回退：file 引用被拒→invalidate 索引重试一次；Files API 上传整体失败→整个请求降级为 base64 表示（index.js:1747-1751）；被拒 file id 的检测/规范化图片诊断（index.js:1793-1800）。

### 2.7 默认模型目录与关键常量

`DEFAULT_MODELS`（index.js:1841-1871）：`deepseek-flash`（V41-Flash，text+image，pixelBudget/maxBytes，`systemPromptUpdate:'in-history'`）、`deepseek-v4-flash`、`deepseek-v4-pro`、`deepseek-v4-flash-vision-exp`（text+image）；均 contextWindow=DEFAULT_CONTEXT_WINDOW。
catalog 条目字段（types/adapter.d.ts:21-44 + schema index.js:1873-1883）：`id*, name?, description?, contextWindow?, maxTokens?, inputModalities?, imagePixelBudget?: number|'low', imageMaxBytes?, systemPromptUpdate?: 'in-history'`。

常量（index.js）：`DEFAULT_CONTEXT_WINDOW=1_000_000`（:1392）、`DEFAULT_MAX_TOKENS=256_000`（:1394）、`DEFAULT_MAX_REQUEST_FILES_BYTES=128MiB`（:446）、`DEFAULT_MAX_IMAGES_PER_REQUEST=600`（:448）、`DEFAULT_REQUEST_IMAGE_PIXEL_BUDGET=640_000`（:450）、`DEFAULT_LOW_DETAIL_IMAGE_PIXEL_BUDGET=512*512`（:452）、`DEFAULT_REQUEST_IMAGE_MAX_BYTES=1MiB`（:454）、`DEFAULT_MAX_INLINE_REQUEST_IMAGE_BYTES=20MiB`（:1396）、`DEFAULT_IMAGE_OFFLOAD_BYTE_QUANTUM=64MiB`（:1398）、`DEFAULT_INLINE_IMAGE_OFFLOAD_BYTE_QUANTUM=10MiB`（:1400）、countQuantum=20（:1402/1903）、`DEFAULT_FILE_EXPIRY_SECONDS=10080*60`（7 天，:1404）、`DEFAULT_FILE_REFRESH_MARGIN_SECONDS=3600`（:1406）、quota 清理批量 100（:1907）、`MAX_FILE_UPLOAD_BYTES=128MiB`（:545）、`MAX_STORED_FILE_COUNT=10000`、`MAX_STORED_FILE_BYTES=25GiB`（:549）、`MAX_CHAT_IMAGE_BYTES=32MiB`（:872）。Files API 索引文件：`DSH_HOME/llm-deepseek/files-v3.json`（upload-index 注释，types/upload-index.d.ts:887）。

### 2.8 DeepSeek 视觉 token 公式

`deepSeekImageTokens(width,height)`（index.js:422-432，导出；注释 301-310：官方 calculator 逐字移植）：patch=14px（:312）、每轴 3:1 降采样（:314）、单图上限 384 token（:316）、pad-to-4 取最坏 +3（:318）、宽/高比钳 8（:320）、像素下限 384×384（:322）。网格 token：`gridTokens(h,w) = h*(w+1)+2`，奇数行加 `w+1`，再加奇数行对补偿（:326-331）；`resizeOnce` 做 clamp→scale→pad→project，迭代到不动点（≤10 次，:424-428）。

### 2.9 pi-ai 通用 provider（dsh-llm-pi-ai，对照实现）

多协议适配器：支持 `openai-completions` / `openai-responses` / `anthropic-messages` 三种 wire 协议（index.js:755-757 的 api 注册表；discovery 默认 `openai-completions`，index.js:2283）。鉴权：非 Anthropic 用 bearer（`GET {baseURL}/models` 探测），Anthropic 用 `x-api-key` + `anthropic-version` 头（index.js:2119-2120, 2293-2295）。profile 结构 `PiAiProviderProfile`（types/config.d.ts:53-144）：`apiKeyEnv, displayName, api, baseURL, models[], modelOverrides{}, compat{}, defaultContextWindow(262144), defaultMaxTokens(32768), defaultInput[text], headers{}, reasoning, thinkingBudgets, cacheRetention, transport, timeoutMs, streamIdleTimeoutMs(300000), maxRequestImageBytes(20MiB), requestImagePixelBudget, requestImageMaxBytes, retryPolicy`；顶层 `Config = { providers?: Record<string, PiAiProviderProfile> }`（config.d.ts:180-187），**settings 里 providers 的 dict key 即 provider 路由**。模型条目 `PiAiModelProfile`（catalog.d.ts:257-293）：`id, name?, contextWindow?, maxTokens?, input?, reasoningEfforts?: false | Partial<Record<level, string|null>>, compat?`。模型目录（含 cost）由上游包 `@earendil-works/pi-ai` 的 catalog 提供，DSH 侧只做逐字段覆盖（catalog.d.ts:1-11,345-353）；DSH 包内**未找到** cost 字段定义（cost 在 pi-ai 上游 Model 类型里，本安装中未见其字段行号）。

## 附录：web 工具（dsh-tool-web / dsh-web-fetch-http / dsh-web-search-deepseek）

- `web_search`（dsh-tool-web/lib/index.js）：入参 `queries: string[]`（1..maxQueries，非空、去重，:29-42），结果 markdown `- [label](url)` 行（:67-72），失败搜索中止整体（:177-212）。
- DeepSeek 搜索走 **Anthropic 兼容 Messages API + 原生 server tool**（`dsh-web-search-deepseek/lib/index.js:7-11`）：`POST {baseURL}/messages`，baseURL 默认 `https://api.deepseek.com/anthropic/v1`（:21，注意与 chat 的 baseURL 不同、不共享 `$DEEPSEEK_BASE_URL`）；头 `x-api-key` + `authorization: Bearer` + `anthropic-version: 2023-06-01`（:25,136-143）；body `{model:'deepseek-v4-flash', max_tokens:4096, messages:[{role:'user',content:[{type:'text',text:`Perform a web search for the query: ${query}`}]}], tools:[{type:'web_search_20250305', name:'web_search', max_uses:5}]}`（:108-124）；结果从 `web_search_tool_result` 块提取并按 text 块 citations 的 `cited_text` 拼 snippet、按 url 去重（:33-76）。
- `web_fetch`（dsh-web-fetch-http/lib/index.js）：配置默认 `maxResponseBytes=5e6, maxBodyChars=1e5, timeoutMs=3e4, maxRedirects=5, userAgent="deepseek-harness/0.0.1 (+https://github.com/deepseek-ai)"`（:644-649）；URL 长度上限 2048（:264）；只自动跟随**同源**重定向，跨源抛 `WEB_REDIRECT_BLOCKED`（:466-479）；请求头 `accept: text/html,application/xhtml+xml,text/*;q=0.9,application/json;q=0.8`（:498）；超限按字节截断（truncatedByBytes）与按字符截断（:525-540）。

---

# §3 模型目录与配置、API key 存放、DSH_* 环境变量约定

---

## 3A. settings 模型

### A1. 根结构：namespace 机制与逐层解析

`ctx.settings` 是「user-settings capability seam」：**provider 存一份原始文档（raw document），
每个 namespace 对应文档里的一个 section**；插件注册 namespace schema，读到的是三层合成的值：
**schema 默认值 → 注册者的 composition `base` → 用户文档 section**，按此顺序叠加。
（`dsh-settings/lib/types/index.d.ts:1-7`、`85`、`295-296`）

核心类型（均在 `dsh-settings/lib/types/index.d.ts`）：

| 类型 | 行号 | 说明 |
|---|---|---|
| `SettingsApplies` | :21 | `'live' | 'restart'`，owner 声明生效时机，默认 `live` |
| `SettingsRegisterOptions<T>` | :23-48 | 字段：`base?: Partial<T>`（组合层）、`applies?`、`validate?: (value: T) => void`（跨字段校验，写失败即拒绝写入） |
| `SettingsDescriptor` | :50-73 | `ns`、`schema`（`schema.toJSON()`）、`value`（当前解析值）、`revision`（用户 section 单调修订号，写时回传防冲突）、`base?`、`user?`（原始用户 section，字段出现即代表被用户覆盖）、`applies`、`secrets?`（仅 `redactSecrets` 时） |
| `SettingsDescribeOptions` | :75-82 | `redactSecrets?: boolean`，**所有过线（wire）读取必须置 true** |
| `SettingsScope<T>` | :84-110 | owner 句柄：`get()`、`watch(cb)`、`update(patch)`（合并补丁）、`replace(section)`（整体替换，缺省键回落到 base/默认，`replace({})` 即重置） |
| `SettingsConflictError` | :121-134 | `code = "SETTINGS_CONFLICT"`，`expected`/`actual` revision |
| `SettingsPathOp` | :143-150 | `{op:'set',path[],value}` 或 `{op:'unset',path[]}`；路径写是给只持有脱敏视图的调用方用的，防止整段 replace 误删没见过的 secret |
| `SettingsProvider`（抽象类） | :157-313 | `writable`、`documentPath`、`prepareDocument()`、抽象 `load()`/`persist(ns,section)`、`register(ns,schema,options)`、`installSection(owner,ns,schema,entry,hooks)`、`describe(options)`、`get/update/replace/mutate(ns,…,expectedRevision?)`、`publish(doc,source?)` |
| `SettingsSectionHooks<T>` | :315-334 | `setSource(current)`、`onChange()`、`validate?` |

Namespace 语法：小写字母开头，仅 `[a-z0-9-]`；类型层面 `SettingsNamespaceInput` 强制，运行
时 `register` 对非法名抛 `TypeError`（`index.d.ts:15-19`、`214`）。`SettingsNamespace` 是
`Branded<'SettingsNamespace'>` 名义类型（`dsh-settings/lib/types/types.d.ts:52`）。

事件（`dsh-settings/lib/types/types.d.ts`）：
- `settings/updated(ns, next, prev, source)` — 解析值变化后发（深相等不发）；source ∈ `'update' | 'provider'`（:54、:128）。
- `settings/document-updated(ns, revision)` — 原始用户 section 变化即发，供配置界面发现 revision 过期（:140）。

Wire 视图 `SettingsNamespaceView`（永远在 redactSecrets 下）：`ns`、`schema`、`value`、`base?`、`user?`、`applies`、`secrets[]`、`revision`（`types.d.ts:67-88`）；
`SettingsDescribeValue = { writable, hasDocument, namespaces[] }`（:103-110）。

#### 已注册的 namespace 清单（模型相关优先）

| namespace | 属主文件:行 | 相关度 |
|---|---|---|
| `llm-pi-ai` | `dsh-llm-pi-ai/lib/index.js:2533`（NS 常量），`installSection` :2661 | 模型/provider 主配置 |
| `llm-deepseek` | `dsh-llm-deepseek/lib/index.js:1837`，`installSection` :2080 | DeepSeek 官方 chat 适配器 |
| `agent-default-model` | `dsh-agent-default-model/lib/index.js:11`，schema :13-17 | 默认模型 |
| `subagent-model-selection` | `dsh-tool-subagent/lib/model-selection-settings.js:42`，schema :44-47 | 子代理可选模型 |
| `web-search-deepseek` | `dsh-web-search-deepseek/lib/index.js:261`，schema :245-253 | 搜索模型/key |
| `agent-loop` | `dsh-agent-loop/lib/index.js:1463-1465` | `maxParallelToolCalls`（默认 10），非模型 |
| `shell` | `dsh-shell/lib/index.js:64`；bash 侧 schema `dsh-bash-local/lib/index.js:128-134`：`cwd`、`timeoutMs`(120000)、`maxTimeoutMs`(600000)、`maxOutputBytes`(64000)、`maxSpillBytes`、`graceMs`(3000) | 非模型 |
| `agent-presets` | `dsh-agent-presets/lib/index.js:1145`，schema :1151：`{ default: string }` | 默认预设 |
| `permission` | `dsh-permission-presets/lib/types/index.js:23` | 非模型 |
| `locale` / `ui-chat` / `ui-conversation` / `ui-theme` / `ui-onboarding` | `dsh-client-locale/lib/index.js:5`、`dsh-client-ui-chat/lib/index.js:5`、`dsh-client-ui-conversation/lib/index.js:5`、`dsh-client-ui-theme/lib/index.js:11`、`dsh-client-ui-settings-general/lib/index.js:5` | UI |

#### `agent-default-model` section（逐字段）

`dsh-agent-default-model/lib/index.js:13-17`：
- `provider: string`（required）— provider 路由键；
- `model: string`（required）— 模型 id；
- `reasoningEffort: string`（可选）— 品牌化为 `ReasoningEffortId`（:23）。
写路径 `saveSelection` 走 `settings.replace(NS, {provider, model, reasoningEffort?})`（:65-72）。

`subagent-model-selection` section：`enabled: boolean=false`、`allowedModels: [{provider, model}]`
（`dsh-tool-subagent/lib/model-selection-settings.js:44-47`）；`enabled=true` 时必须至少一条路由（:88）。

### A2. 模型如何配置（真实字段名逐字段）

#### A2.1 `llm-pi-ai` 路由 profile（settings section = `{ providers: { <路由键>: profile } }`）

Section 顶层只有 `providers` 一个 dict，**dict 的键就是 provider 路由键**（`Config = z.object({ providers: z.dict(profile).default({}) })`，
`dsh-llm-pi-ai/lib/index.js:1017`；类型 `dsh-llm-pi-ai/lib/types/config.d.ts:180-187`）。

`PiAiProviderProfile` 逐字段（`dsh-llm-pi-ai/lib/types/config.d.ts:53-144`，schema `lib/index.js:983-1016`）：

| 字段 | 类型/默认 | 说明 |
|---|---|---|
| `apiKeyEnv` | string，schema 标 `role("credential-ref")`（`lib/index.js:984`） | **凭据引用（环境变量名），不是 key 本身**，每请求经 `ctx.credentials` 解析 |
| `displayName` | string，默认=路由键 | 配置界面显示名 |
| `api` | string | 线协议；省略则保留目录内每个模型自带协议；目录没有的路由必须给 |
| `baseURL` | string | 端点，默认取已安装目录的端点 |
| `models` | `PiAiModelProfile[]` | 显式模型目录，省略=直接用已安装 pi-ai 目录 |
| `modelOverrides` | `Record<modelId, PiAiModelOverride>` | 只改目录中某几个模型（与 `models` 互斥语义） |
| `compat` | `PiAiCompatProfile` | 路由级线兼容开关，逐字段被模型级覆盖 |
| `defaultContextWindow` | 正整数，默认 **262144**（`config.d.ts:37`；schema `lib/index.js:994`） | 目录与条目都没给时的兜底 |
| `defaultMaxTokens` | 正整数，默认 **32768**（`config.d.ts:39`；schema :995） | 同上（输出能力兜底） |
| `defaultInput` | 模态数组，默认 `["text"]`（`config.d.ts:50`；schema :996） | 模态兜底，不得为空 |
| `headers` | `Record<string,string>` | 请求头，按 Fetch 校验 |
| `reasoning` | pi-ai `ModelThinkingLevel` | provider 中性推理级别 |
| `thinkingBudgets` | `{minimal,low,medium,high}: number`（schema `lib/index.js:907-912`） | 推理 token 预算 |
| `cacheRetention` | `'none'|'short'|'long'`（schema :1000-1004） | prompt-cache 保留偏好 |
| `transport` | `'sse'|'websocket'|'websocket-cached'|'auto'`（schema :1005-1010） | 流传输偏好 |
| `timeoutMs` | natural | HTTP/SDK 超时 |
| `websocketConnectTimeoutMs` | natural | WS 连接超时 |
| `streamIdleTimeoutMs` | 默认 **300000**（`config.d.ts:21`；schema :1011） | 流空闲上限 |
| `maxRequestImageBytes` | 默认 20MiB（`config.d.ts:31`；schema :1012） | 每请求 base64 图片总上限（超限把最旧图片换成文本占位） |
| `requestImagePixelBudget` | 默认 2048×2048（`config.d.ts:33`；schema :1013） | 请求图片像素预算 |
| `requestImageMaxBytes` | 默认 1MiB（`config.d.ts:35`；schema :1014） | 请求图片编码字节目标 |
| `retryPolicy` | `RetryPolicyConfig`（来自 `dsh-llm`；schema :1015） | 重试策略，改动会触发重注册 |

`PiAiModelProfile`（一个模型条目，`catalog.d.ts:257-293`；schema `lib/index.js:969-981`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string required | 发给 provider 的模型 id（=选择器里 `provider/model` 的 model 半） |
| `name` | string? | 显示名；默认目录 name，再默认 id |
| `contextWindow` | 正整数? | 上下文窗（请求+响应合计 token） |
| `maxTokens` | 正整数? | 最大输出 token；**显式配置**的会额外进入 `configuredMaxTokens` 成为请求默认上限，目录继承值不会（`catalog.d.ts:264-270`、`327-343`） |
| `input` | `PiAiModality[]`（`"text" | "image"`，`MODALITIES` 实现见 `lib/index.js:279-282`） | 输入模态=能力声明；空数组等于没声明，回落目录再回落路由 `defaultInput` |
| `reasoningEfforts` | `false | Record<级别, 线上拼写|null>`（schema `lib/index.js:967`） | 推理能力；`false`=声明非推理模型；dict 键=可选级别（off/minimal/low/medium/high/xhigh/max，`THINKING_LEVELS`），值=线上 spelling，仅 `off` 允许空值 |
| `compat` | `PiAiCompatProfile` | 模型级线兼容开关，逐字段压过路由级 |

`PiAiModelOverride` = `Omit<PiAiModelProfile,'id'>`，id 在 `modelOverrides` 的 dict 键里（`catalog.d.ts:301`）。

`PiAiCompatProfile` 全部 26 字段（`catalog.d.ts:156-228`；schema `lib/index.js:929-960`）：
`supportsStore`、`supportsDeveloperRole`、`supportsReasoningEffort`、`supportsUsageInStreaming`、`supportsFinishReason`、
`maxTokensField`、`requiresToolResultName`、`requiresAssistantAfterToolResult`、`requiresThinkingAsText`、
`requiresReasoningContentOnAssistantMessages`、`thinkingFormat`、`chatTemplateKwargs`、`chatTemplateArgs`、
`supportsThinkingTokenBudget`、`thinkingTokenBudgetField`、`vllmPriority`、`supportsMaxOutputTokens`、`supportsStrictMode`、
`cacheControlFormat`、`supportsLongCacheRetention`、`supportsEagerToolInputStreaming`、`supportsCacheControlOnTools`、
`supportsTemperature`、`forceAdaptiveThinking`、`allowEmptySignature`、`supportsStrictTools`。

物化结果 `RouteCatalog = { models, modelErrors, configuredMaxTokens }`（`catalog.d.ts:327-343`）；
物化函数 `resolveRouteModels(request,'strict'|'deferred')`（:353）；条目缺省逐字段取目录条目→路由兜底
（实现 `lib/index.js:670-688`）。文档示例（YAML）在 `lib/index.js:2485-2527`。

**别名/成本/capability 的边界**：settings 层模型条目**没有** cost、工具能力字段；能力=
`input` 模态 + `reasoningEfforts`（+compat 开关），provider 归属=providers dict 键，别名机制=
路由键+`name` 显示名。cost 只存在于下层 pi-ai 目录（见下），且 harness 明确不读。

#### A2.2 pi-ai 上游目录条目（catalog entry，逐字段）

`@earendil-works/pi-ai` 的 `Model<Api>`（`$BASE/../@earendil-works/pi-ai/dist/types.d.ts:716-737`，相对 BASE 即
`/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@earendil-works/pi-ai/dist/types.d.ts`）：

| 字段 | 行 | 说明 |
|---|---|---|
| `id: string` | :717 | 模型 id |
| `name: string` | :718 | 显示名 |
| `api: TApi` | :719 | 协议（`openai-completions`/`openai-responses`/`anthropic-messages`/`bedrock-converse-stream` 等） |
| `provider: ProviderId` | :720 | provider 归属 |
| `baseUrl: string` | :721 | 端点 |
| `reasoning: boolean` | :722 | 是否推理模型 |
| `thinkingLevelMap?: ThinkingLevelMap` | :727 | pi 级别→供应商级别映射；缺键用默认，`null` 标记不支持 |
| `input: ("text"|"image")[]` | :728 | 输入模态 |
| `cost: ModelCost` | :729 | 单价（见下） |
| `contextWindow: number` | :730 | 上下文窗口 token |
| `maxTokens: number` | :731 | 最大输出 token |
| `samplingParams?: Record<string, unknown>` | :733 | 默认采样参数 |
| `headers?: Record<string, string>` | :734 | 附加头 |
| `compat?: OpenAICompletionsCompat | OpenAIResponsesCompat | AnthropicMessagesCompat | BedrockCompat` | :736 | 按协议分支的兼容对象 |

`ModelCost`/`ModelCostRates`/`ModelCostTier`（同文件 :702-715）：
- `input: number`、`output: number`、`cacheRead: number`、`cacheWrite: number`；
- `tiers?: ModelCostTier[]`，`ModelCostTier = { inputTokensAbove: number, …四个费率 }`——按请求总输入 token 匹配最高档（:708-714）。

**cost 单位：美元 / 每百万 token（USD per 1M tokens）**。证据：
1. `calculateCost` 用 `rates.input / 1_000_000 * usage.input` 等计算（`pi-ai/dist/models.js:530-548`，其中 `:543-546` 除 1000000；`:541` 注释 Anthropic 1h 缓存写按 2×基础输入价）。
2. OpenRouter 路由对象注释直接写 "Maximum price per million tokens (USD)"（`types.d.ts:655-667`）。
3. 真实目录数据：`pi-ai/dist/providers/data/deepseek.json` 中 `deepseek-v4-pro` 为 `{ input: 0.435, output: 0.87, cacheRead: 0.003625, cacheWrite: 0 }`，与 USD/1M 语义一致。

但注意：**harness 从不读 pi-ai 的 cost 元数据** —— "The harness never reads pi-ai's cost metadata —
`replay.ts` zeroes it and no consumer reports spend"，目录外模型统一给 `NO_COST`（全 0）
（`dsh-llm-pi-ai/lib/index.js:267-278`；物化处 `cost: base?.cost ?? NO_COST` :683）。

#### A2.3 `llm-deepseek` section（官方 DeepSeek 适配器，逐字段）

schema `dsh-llm-deepseek/lib/index.js:1884-1908`：
`apiKeyEnv`（role credential-ref，默认 `"DEEPSEEK_API_KEY"` :1838、:1885）、`baseURL`、
`thinking: 'enabled'|'disabled'`、`reasoningEffort: 'off'|'low'|'high'|'max'`、
`maxTokens`（默认 **256000**，`DEFAULT_MAX_TOKENS=256e3` :1394）、`defaultContextWindow`（默认 **1000000**，`DEFAULT_CONTEXT_WINDOW=1e6` :1392）、
`models: catalogModel[]`（默认 `DEFAULT_MODELS` :1841-1871：deepseek-flash / deepseek-v4-flash / deepseek-v4-pro / deepseek-v4-flash-vision-exp）、
`streamIdleTimeoutMs`（默认 300000 :1390）、`maxRequestFilesBytes`（128MiB :446）、
`maxInlineRequestImageBytes`（20MiB :1396）、`maxImagesPerRequest`（600）、`imageOffloadByteQuantum`、
`inlineImageOffloadByteQuantum`、`imageOffloadCountQuantum`、`filesApiTimeoutMs`、`fileExpiresAfterSeconds`(3600..2592000)、
`fileRefreshMarginSeconds`、`fileQuotaCleanupBatch`、`retryPolicy`。
唯一路由 `PROVIDER = "deepseek-official"`（:1840）。
`catalogModel` 逐字段（:1873-1883）：`id`(required)、`name`、`description`、`contextWindow`、`maxTokens`、
`inputModalities: ("text"|"image")[]`（默认 `["text"]`）、`imagePixelBudget: number|"low"`、`imageMaxBytes`、
`systemPromptUpdate: "in-history"`。
端点解析：`config.baseURL ?? env(DEEPSEEK_BASE_URL) ?? "https://api.deepseek.com"`（:1911-1913、:1993）。

`web-search-deepseek` section（`dsh-web-search-deepseek/lib/index.js:245-253`）：
`apiKey: role("secret")`（**明文写进 settings 的例外字段** :245）、
`apiKeyEnv: role("credential-ref")` 默认 `DEEPSEEK_API_KEY`（:243-246）、`baseURL`（回落
`DEEPSEEK_SEARCH_BASE_URL` → `https://api.deepseek.com/anthropic/v1` :259、:284）、
`model`（默认 `deepseek-v4-flash`）、`apiVersion`、`maxTokens`(4096)、`maxUses`(5)。

### A3. API key 存在哪里

- **settings/组合文件只存“引用”，不存明文**：credential seam 的文档明说 "Settings and composition
  files carry *references* to secrets — environment-variable names — while providers own the actual
  values"（`dsh-credentials/lib/types/index.d.ts:1-9`）。引用即 schema 上 `role("credential-ref")`
  的 `apiKeyEnv` 字段（`dsh-llm-pi-ai/lib/index.js:984`；`dsh-llm-deepseek/lib/index.js:1885`）。
  例外：`web-search-deepseek.apiKey` 是 `role("secret")` 明文槽位（`dsh-web-search-deepseek/lib/index.js:245`，过 wire 时会被 redact 掉）。
- **引用名格式不是 `"deepseek:apiKey"`**，而是 POSIX 环境变量名：`CredentialRef` 语法
  `^[A-Za-z_][A-Za-z0-9_]*$`（`dsh-credentials/lib/index.js:13`、`credentialRef` :21-24）。典型值就是
  `DEEPSEEK_API_KEY`、`OPENAI_API_KEY`、`ANTHROPIC_API_KEY`（示例 `dsh-llm-pi-ai/lib/index.js:2493-2506`）。
- **第二个键空间 CredentialKey**：存储记录地址 `<scope>/<id>`，scope=属主插件注册名（如 `llm-pi-ai`），
  id=该插件自己的寻址单元（provider 路由键）；段语法 `^[a-z][a-z0-9-]*$`
  （`dsh-credentials/lib/types/types.d.ts:14-26`；`dsh-credentials/lib/index.js:15` `KEY_SEGMENT_PATTERN`）。
  即形如 `llm-pi-ai/openai`（`dsh-llm-pi-ai/lib/types/auth.d.ts:16-27` `RECORD_SCOPE="llm-pi-ai"`、`recordKeyFor(providerId)`）。
  记录类型：`ApiKeyRecord = { kind:'api-key', key?: string, env?: Record<string,string> }`、
  `GrantRecord = { kind:'grant', payload: unknown }`（`dsh-credentials/lib/types/types.d.ts:33-54`）。
- **介质与路径**：`dsh-credentials-local` 提供文件后端，路径 **`$DSH_HOME/.credentials.yaml`**
  （默认 `~/.dsh/.credentials.yaml`；`dsh-credentials-local/lib/types/index.d.ts:2`、`CREDENTIALS_FILENAME` :42，
  运行时 `dsh-credentials-local/lib/index.js:49`）。文件权限：目录 0o700（mode 448）、文件 0o600（mode 384）
  （写入处 `dsh-credentials-local/lib/index.js:547`、`574`、`581`；settings-file 同模式 `lib/index.js:163-176`）。
- **解析优先级**（`dsh-credentials-local/lib/types/index.d.ts:5-10`）：
  `继承的进程环境`（只读、最高）> `$DSH_HOME/.credentials.yaml`（provider 管理、可写）>
  `<启动 cwd>/.env`（只读）> `$DSH_HOME/.env`（只读）。source 层 id 为 `env`/`file`/`project-env`/`user-env`
  （`dsh-credentials/lib/types/index.d.ts:74`）。写会被继承环境遮蔽时直接拒绝（`assertUnshadowed`，local d.ts:152-157）。
- **文档格式**：YAML，`DOCUMENT_VERSION = 1`，顶层 `{version, refs: {REF: value}, records: {key: record}}`；
  旧版扁平布局（顶层直接 ref→value）会被识别并原地升级为 `version: 1 / refs:`（`dsh-credentials-local/lib/index.js:147-155`、迁移 :189；d.ts:68-101）。
- **每请求解析**：`llm-pi-ai` 每次请求 `credentials.resolve(profile.apiKeyEnv)`，未设置则报
  `MISSING_CREDENTIAL` 并提示"到 Models 页写入或 export"（`dsh-llm-pi-ai/lib/index.js:2594-2601`）；
  `llm-deepseek` 同构（`dsh-llm-deepseek/lib/index.js:2038-2049`）。凭据每次操作重新解析、禁止跨操作缓存
  （`dsh-credentials/lib/types/index.d.ts:120-129`）。
- **redact 机制**（`dsh-settings/lib/types/redact.d.ts` + `redact.js`）：`redactSecrets(schema, value)`
  沿 `object/dict/array` 容器遍历，凡 schema 节点 `meta.role === 'secret'` 即从值中删除，并在 sidecar
  记一条 `RedactedSecret = { path: string[], set: boolean }`（redact.js:16-20；d.ts:11-16）。对象属性槽位
  即使未赋值也枚举（表单可画 write-only 输入框）；union/transform 里埋的 secret 不覆盖（TODO fail-closed
  注释 redact.js:55-58）。远程面（`ctx.remote.settings`）强制 `redactSecrets: true`
  （`dsh-api-settings-controller/lib/index.js:292`）。注意 `credential-ref` 角色**不**被 redact——
  它本来就只是变量名。

### A4. 配置文件磁盘位置与格式

- **settings 文档**：单个文件，默认 **`$DSH_HOME/settings.yaml`**（`dsh-settings-file/lib/index.js:31-33`
  `resolveSpec`：`config.path ?? join(resolveDshHome(), "settings.yaml")`）。格式按扩展名：
  `.yaml/.yml → yaml`，`.json → json`，其他扩展直接报错；**不是 JSONC**（`dsh-settings-file/lib/index.js:20-25`；
  d.ts:23-24）。YAML 写入是“保留注释的叶子级 diff”（d.ts:93-100），JSON 写入是整 key 替换（d.ts:101-102）。
  默认 watch 热加载，debounce 100ms（`lib/index.js:35-36`；d.ts:18-21）。文件锁+原子写（`lib/index.js:289-297`）。
- **namespace→文件命名规则：没有“每 namespace 一文件”的规则**。全部 namespace 都是同一文档的顶层 key，
  key 即 namespace 名（小写-连字符标识符）（`dsh-settings/lib/types/index.d.ts:2-4`；`SettingsNamespaceView.ns`
  示例注释 `llm-deepseek`、`llm-pi-ai`：`types.d.ts:68-69`）。
- **harness home**：`$DSH_HOME`，默认 `~/.dsh`；显式配置 > `$DSH_HOME` > `~/.dsh`；空白值视同未设
  （`dsh-home-paths/lib/index.js:11`、`15`、`66-76`）。
- **其他相关磁盘文件**：profile 目录 `$DSH_HOME/profiles/<name>`（`dsh-app-boot/lib/index.js:291`）；
  启动 env 文件 `<cwd>/.env` 与 `$DSH_HOME/.env`（`dsh-app-boot/lib/index.js:1074-1082`，经
  `process.loadEnvFile` :940-946 加载，只补 unset 的 key）；凭据文件见 A3。

---

## 3B. 环境变量

### B1. `DSH_*` 环境变量（全部真实读取点）

前缀常量 `DSH_ENV_PREFIX = "DSH_"`（`dsh-subprocess/lib/index.js:13`；d.ts `types.d.ts:11`）。

| 变量 | 用途 | 读取点（文件:行） |
|---|---|---|
| `DSH_HOME` | harness 单根目录；解析优先级：显式配置 > `$DSH_HOME` > `~/.dsh`，空白视同未设 | 名称常量 `dsh-home-paths/lib/index.js:15`；读取 `resolveDshHome` `dsh-home-paths/lib/index.js:73-76`（settings-file/credentials-local/shell-env 均经此函数间接读） |
| `DSH_TELEMETRY_DISABLED` | 关闭 session-telemetry 行的开关 | 外层 CLI `OUTER/lib/profile-boot-Dk-7KqJc.js:249`（`process.env.DSH_TELEMETRY_DISABLED`；说明注释 :121、:180） |
| `DSH_AGENTS_HOME` | AGENTS.md/技能用户根目录，缺省 `~/.agents` | `dsh-skill-filesystem/lib/index.js:78` |
| `DSH_BUNDLED_SKILL_DIR` | 内置技能目录 | `dsh-skill-filesystem/lib/index.js:84` |
| `DSH_WEB_SEARCH_PROVIDER` | web search provider 选择（config 优先） | `dsh-web/lib/index.js:57` |
| `DSH_WEB_FETCH_PROVIDER` | web fetch provider 选择 | `dsh-web/lib/index.js:58` |
| `DSH_SNAPSHOT` | bin 侧快照回放：值为 `'replay'` 时把 `cordis.yml` 换成同目录 `cordis.snapshot.yml` | 读取在发行 bin 内（本构建产物中**未找到**直接 `process.env` 读取点）；接口文档 `dsh-app-boot/lib/index.js:923-931`（`resolveConfigPath(configPath, snapshotMode)` :928-931） |

补充事实：
- `DSH_*` 是 bootstrap 保护前缀：任何被发现的 `.env` 文件都**不允许**设置 `DSH_*`（连同 `XDG_`、
  `DYLD_`、`BASH_FUNC_`）——`BOOTSTRAP_PREFIXES = ["DSH_","XDG_","DYLD_","BASH_FUNC_"]`，
  `isBootstrapOnly()`（`dsh-app-boot/lib/index.js:993-999`、:1021-1024）。
- `dsh-cmdline`、`dsh-base` 包内**未找到**任何 `process.env` 读取（grep 无命中）。
- 全库字符串枚举中还有 `__DSH_BOOT__`、`__DSH_TRANSPORT__`、`__DSH_FILE_UPLOAD__`、
  `__DSH_CONNECTION_RECOVERY__`、`__DSH_BOOT_READY__`、`__DSH_PERSISTENT_PWSH_PROMPT__` 等
  ——这些是 `globalThis.__DSH_*__` 浏览器注入符号（如 `dsh-client-connection/lib/client.js:6307`），**不是环境变量**。
- 误报排除：`LLM_STREAM_IDLE_TIMEOUT`、`BASH_TIMEOUT`、`TOOL_TIMEOUT`、`PERSISTENT_BASH_TIMEOUT`、
  `DEEPSEEK_FILES_API_TIMEOUT` 等是超时/错误 machine code 字符串（如 `dsh-llm-deepseek/lib/index.js:1411-1412`），不是环境变量。

### B2. 注入给 bash/pwsh 子进程的变量

注册表 `ctx.shellEnv`（`dsh-shell-env/lib/index.js`）：每次模型 shell 调用重建命名空间；
执行器先丢弃继承的 `DSH_*`，再注入注册表快照（:28-33 注释、scrub 见下）。

内置变量（`collect()` 实现 `dsh-shell-env/lib/index.js:79-86`）：

| 变量 | 值 | 位置 |
|---|---|---|
| `DSH_HOME` | 解析后的 harness home | :82 |
| `DSH_SHELL=1` | 标记"这是 harness 托管 shell" | 键 :19，值 :83 |
| `DSH_SESSION_ID` | 当前 session id（`execution.agent.session.header.id`） | 键 :20，值 :85 |

三个键为保留键，插件贡献者不得占用（:21-25、:61）；贡献者键必须 `DSH_` 前缀 +
`^[A-Z][A-Z0-9_]*$`（:26、:60）。已有贡献者：`web-runtime` 提供 **`DSH_WEB_URL`**
（本会话 Web GUI 的本地 URL，`dsh-web-app/lib/index.js:40`、:187-191）。
`DSH_WORKSPACE` / `DSH_CWD`：**未找到**（workspace 通过 bash 工具 `workdir` 参数解析
`resolveWorkdir(...)`，`dsh-tool-bash/lib/index.js:394`，不走环境变量）。

注入链：bash 工具调 `ctx.shellEnv.collect(exec)` 得 `dshEnv` 放进执行请求
（`dsh-tool-bash/lib/index.js:395`、:400）；本地执行器最终环境 =
`{ ...ENV_OVERRIDES, ...spec.env, ...spec.dshEnv }`，`ENV_OVERRIDES = { NO_COLOR:"1", TERM:"dumb",
PAGER:"cat", GIT_PAGER:"cat" }`（`dsh-bash-local/lib/index.js:80-85`、:196-199）。
持久 PTY 终端另注入 `DSH_SHELL`、`DSH_SESSION_ID`、**`DSH_PTY_SESSION_ID`**（`dsh-terminal-bash/lib/index.js:874-882`）。

子进程基环境清洗 `scrubbedParentEnv()`（`dsh-subprocess/lib/index.js:44-56`）：从 `process.env`
剔除所有 credential 形态名（`SENSITIVE_ENV_PATTERN = /KEY|PASSWORD|SECRET|TOKEN/i` :31）与全部
`DSH_*`（大小写不敏感），再叠加解析后的代理变量——所以 `PATH/HOME/locale/proxy` 传给子进程，
`DEEPSEEK_API_KEY` 等默认**不会**泄入 bash 工具（刻意显式传的 env 层在 scrub 之后合并，:25-30 注释）。

### B3. 其他重要环境变量

**DeepSeek 相关（经 launch-environment 快照读取，非直接 process.env）：**

| 变量 | 用途 | 读取点 |
|---|---|---|
| `DEEPSEEK_API_KEY` | 三个组件的默认 credential-ref 名：llm-deepseek `DEFAULT_API_KEY_ENV`（`dsh-llm-deepseek/lib/index.js:1838`，:1885 default、:1992）；web-search-deepseek（`dsh-web-search-deepseek/lib/index.js:243`、:246）；UI 默认引用 `dsh-client-ui-settings-plugins/lib/client.js:1447`。实际取值经 credentials 层（B 见 A3） | 见左 |
| `DEEPSEEK_BASE_URL` | llm-deepseek chat 端点覆盖（config.baseURL > env > `https://api.deepseek.com`）；经 `launchEnvironmentOf(ctx).get(BASE_URL_ENV)` | `dsh-llm-deepseek/lib/index.js:1913`、:1993；快照构建 `dsh-app-boot/lib/index.js:1074-1098`（层：process > `./项目.env` > `~/.dsh/.env`）；同时列入 `BOOTSTRAP_NAMES`（`.env` 不得设置它，`dsh-app-boot/lib/index.js:987`） |
| `DEEPSEEK_SEARCH_BASE_URL` | DeepSeek 搜索（Anthropic 兼容 Messages API）端点，默认 `https://api.deepseek.com/anthropic/v1` | `dsh-web-search-deepseek/lib/index.js:259`、:284；`BOOTSTRAP_NAMES` `dsh-app-boot/lib/index.js:988` |

**代理与 TLS（`dsh-http-proxy`）**：策略变量
`POLICY_ENV_NAMES = { httpProxy:["http_proxy","HTTP_PROXY"], httpsProxy:["https_proxy","HTTPS_PROXY"], noProxy:["no_proxy","NO_PROXY"] }`
（`dsh-http-proxy/lib/index.js:29-33`；读快照 `snapshotProxyEnv` :353-356，
`PROXY_ENV_NAMES` 额外含 `all_proxy/ALL_PROXY` :38-43）。启动侧：`HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY`
列在 `BOOTSTRAP_NAMES`（项目 `.env` 一律拒绝），但**仅 `~/.dsh/.env`（home 层）可设代理**
（`HOME_LAYER_PROXY_NAMES`，`dsh-app-boot/lib/index.js:991-1006`）。

**bootstrap 保护名单**（只许继承环境提供，`.env` 拒设；`dsh-app-boot/lib/index.js:949-997`）：
`PATH、HOME、USERPROFILE、SHELL、NODE_OPTIONS、NODE_PATH、NODE_EXTRA_CA_CERTS、LD_PRELOAD、
LD_LIBRARY_PATH、LD_AUDIT、BASH_ENV、ENV、SHELLOPTS、BASHOPTS、PERL5OPT、PERL5LIB、PYTHONSTARTUP、
PYTHONPATH、RUBYOPT、RUBYLIB、JAVA_TOOL_OPTIONS、_JAVA_OPTIONS、JDK_JAVA_OPTIONS、PYTHONHOME、
GIT_SSH、GIT_SSH_COMMAND、GIT_EXTERNAL_DIFF、GIT_PAGER、GIT_EDITOR、GIT_ASKPASS、SSH_ASKPASS、
GIT_CONFIG_GLOBAL、GIT_CONFIG_SYSTEM、GIT_CONFIG_COUNT、EDITOR、VISUAL、PAGER、BROWSER、
DEEPSEEK_BASE_URL、DEEPSEEK_SEARCH_BASE_URL、SSL_CERT_FILE、SSL_CERT_DIR、HTTP_PROXY、HTTPS_PROXY、
ALL_PROXY、NO_PROXY、REQUESTS_CA_BUNDLE、CURL_CA_BUNDLE、NODE_TLS_REJECT_UNAUTHORIZED`。

**其他零散读取点：**

| 变量 | 用途 | 读取点 |
|---|---|---|
| `XDG_DATA_HOME` / `XDG_DATA_DIRS` | Linux 桌面 entry 查找 | `dsh-host-open-in-app/lib/index.js:619` |
| `PATH` | 目录选择器探测可执行文件 | `dsh-host-directory-picker-auto/lib/index.js:123` |
| `TSX_TSCONFIG_PATH` | workflow worker 线程透传 | `dsh-workflow-worker-thread/lib/index.js:247` |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` | 不是硬编码读取；只作为 `apiKeyEnv` 引用名示例出现（`dsh-llm-pi-ai/lib/index.js:2493-2499`），取值仍走 credentials 层 | 见左 |

**credentials 的 `.env` 回退**：`<启动 cwd>/.env` 与 `$DSH_HOME/.env` 也作为凭据只读层被解析
（`dsh-credentials-local/lib/types/index.d.ts:8-9`；`dotenvFallback` 实现 `dsh-credentials-local/lib/index.js:437`）。

---

## 未找到项汇总

- `process.env.DSH_SNAPSHOT` 的直接读取点（读取发生在发行 bin，本目录只见文档/参数传递）——B1。
- `DSH_WORKSPACE`、`DSH_SESSION_ID` 之类的 `DSH_SESSION_ID` 存在，但 `DSH_WORKSPACE` 不存在——B2。
- `dsh-cmdline`、`dsh-base` 包中的 `process.env` 读取——未找到。
- settings 模型条目层的 cost/单价配置字段——未找到（cost 仅在 pi-ai 目录层，且 harness 不读，见 A2.2）。
- 每 namespace 独立文件的命名规则——不存在（单文档多 key，见 A4）。

---

# §4 token 计量（tokenizer 策略、context window 占用与上报）

### B1. tokenizer：无真实分词器，纯启发式定密度估算

- **无 tiktoken/js-tiktoken/gpt-tokenizer 依赖**：`dsh-token-meter/package.json:42-46` 运行时依赖仅 `zod`、`@deepseek-ai/schemastery`、`@deepseek-ai/dsh-util-values`；全包 grep 无 tokenizer 相关。
- 核心常数（`dsh-token-meter/lib/types/estimate.js:9-13`；`estimate.d.ts:11`）：`CHARS_PER_TOKEN = 4`、`BLOCK_OVERHEAD = 4`、`ROLE_OVERHEAD = 4`。
- 计价公式（`estimate.js:21-92`；`lib/index.js:16-84` 为同一实现的 bundle 副本）：
  - `text`/`reasoning` 块：`ceil(text.length/4) + 4`（`:33-36`）
  - `tool-call`：`ceil(name.length/4) + ceil(arguments.length/4) + 4`（`:37-41`）
  - `tool-result`：递归 `estimateContent(content) + 4`（`:42-44`）
  - 其它/未知块（image/file 引用、merge 扩展块）：`4 + ceil(JSON.stringify(block).length / 4)`（`estimateStructuralBlock`，`:21-23`；`:45-49` 注明图片请求价由路由接管）
  - 系统消息：`ceil(总字符/4) + 4`（无块开销；空内容 = 0）（`:62-70`）
  - 普通消息：`estimateContent(content) + 4`（`:77-81`）
  - 工具 schema：`ceil(JSON.stringify(tools).length/4) + 4`；无则 0（`:88-92`）
- 配置：`TokenMeterConfig = Record<string, never>` —— 估计器无设置项（`types/types.d.ts:10`；未知键直接抛错，`lib/index.js:603-606`）。
- 图片"真实"价来自路由接口：`LlmImageRequestPricing.priceImages(images) → readonly LlmImageRequestPrice[]`，`LlmImageRequestPrice = {visualTokens: number, text: string}`（`dsh-llm/lib/types/types.d.ts:159-178`）；DeepSeek 实现见 A3（`dsh-llm-deepseek/lib/index.js:494-517`）。

### B2. context window 占用计算与 breakdown 投影逐字段

**表面（surface）模型**：session 模型可见消息按 seq 位置维护节点表；每节点双价：`tokens`（路由价 = 图片视觉 token + handle 文本价）与 `heuristicTokens`（固定启发式价）——`TokenSurfaceNode = {seq, tokens, heuristicTokens}`（`dsh-token-meter/lib/types/types.d.ts:39-55`）。

路由重定价 `priceSurface`（`dsh-token-meter/lib/types/route-pricing.js:17-55`）：
`tokens(node) = heuristicTokens − imageStructuralTokens − fileStructuralTokens + Σ(visualTokens + estimateContent([{type:'text',text:price.text}])) + Σ estimateContent([{type:'text',text:fileText(file)}])`
——把图片/文件的结构占位价替换为"视觉 token + 模型可见文本"与"file handle 文本"。价格数与图片数不齐即抛错（`:29-31`）。

**TokenMeasurement**（`types/types.d.ts:24-37`）逐字段：
- `logRevision: SessionLogOffset` — 已消费耐久事件数（`:25-26`）
- `baseline: TokenMeasurementBaseline = {kind:'none',tokens:0} | {kind:'estimated',tokens} | {kind:'usage',tokens,usage:Readonly<TokenUsage>}`（`:12-22`）
- `surfaceDeltaTokens` — 相对锚点的带符号重定价（`:28-29`）
- `totalTokens = max(0, baseline.tokens + surfaceDeltaTokens)`（`:30-31`）
- `surfaceTokens` — 当前表面路由价总和（`:32-33`）
- `nodes: readonly TokenSurfaceNode[]` — 头到尾位置序（`:34-35`）

**measure() 锚定逻辑**（`lib/index.js:643-686`）：取最近 `assistant/message` 锚点（header/surface/usage 快照，`:751-767`）；canonical request header 相等且 `usageTokens(usage) = input + cacheRead + cacheWrite + output`（`:594-597`）≥ 全路由重定价锚点 `estimateToolsTokens(header) + anchorSurfaceTokens` 时 baseline=usage，否则 baseline=estimated；delta = 当前表面价 − 锚点表面价。**输入组成 = 工具 schema（envelope 唯一计价字段）+ 系统提示（surface 节点）+ 消息（surface 节点）+ 图片（路由 visualTokens+文本）。**

**三个对前端的投影**（`lib/index.js:615-617` 注册进 `ctx.sessionProjections`；键声明 `dsh-token-meter/lib/types/projection.d.ts:64-73`）：

1. `tokenUsage` → **TokenUsageProjection**（`projection.d.ts:12-17`）：`uncachedInputTokens`、`outputTokens`、`cacheReadTokens`、`cacheWriteTokens`；四桶互斥，reasoning 已含在 outputTokens 不重复累计（`:8-11` 注释）。状态 `{totals, last:{turn,step,buckets}|null}`（`usage-projection.d.ts:12-30`）。
2. `contextPressure` → **ContextPressureProjection**（`projection.d.ts:28-46`；wire `usage-projection.js:48-56,178-187`）：
   - `pressureTokens?` = 最近请求 prompt 侧 = `inputTokens + cacheReadTokens + cacheWriteTokens`（`pressureFrom`，`usage-projection.js:58`），不含输出；
   - `projectedTokens? = max(0, pressureTokens + surfaceTokens − sampledSurfaceTokens)`（`usage-projection.js:183-185`）——锚点 + 采样后表面带符号漂移；压缩可见性靠 shadow-price 协议：`compaction/summary|prune` 事件先 arm `claim {start,end,tokens}`，紧随其后的 surface replace 消费它，无 claim 的 replace 按 0 delta 折叠（`surface-projection.d.ts:24-57`；`lib/index.js:304-336`）；
   - `contextWindow?` = 最近 `request/context` 事件的 `event.data.contextWindow`（`RequestContext.contextWindow`，`dsh-session/lib/types/types.d.ts:216-224`；折叠 `usage-projection.js:149-160`）。三值各自 last-wins、刻意非原子（`projection.d.ts:19-26` 注释）。
3. `contextBreakdown` → **ContextBreakdownProjection**（`projection.d.ts:56-63`；fold `lib/index.js:211-267`）：
   - `systemTokens`：表面序上最后一个非空存活 system/message 节点价（`findLast`，`:252`）
   - `toolsTokens`：最新 `request/header` 的 `estimateToolsTokens(canonicalHeader(...))`（`:231-240`）
   - `messageTokens`：其余全部可见节点（含被取代的旧系统提示）= `旧system + 旧message + delta − 新system`（`:253`）
   - checkpoint 状态：`nodes: [{seq, heuristicTokens, system}] + breakdown {systemTokens, toolsTokens, messageTokens}`（`breakdown-projection.d.ts:87-98`）
   - 明示三数用固定启发式、系统性低估 CJK/JSON，"只作构成近似、绝不作总量"（`projection.d.ts:47-55`）。

### B3. usage 如何上报前端

**没有独立 usage 事件名；是投影（projection）而非事件。** 三条路：

1. **会话投影块随快照/基线下发**：Host 端 `projectionBaseline`/`projectionBlock` 产出 `{asOfSeq, values: SessionProjectionValues}`（`dsh-api-session-controller/lib/index.js:1067,1070,1559-1564`）。传输：client connection 控制基线（`dsh-client-connection/lib/client.js:5577-5595`）与会话快照响应 `projections` 字段（`:5733-5736`）。前端以 `useProjection('tokenUsage'|'contextPressure'|'contextBreakdown')` 读取：
   - `dsh-client-ui-chat/lib/client.js:4081`（StatsPills 累计用量）
   - `dsh-client-ui-conversation/lib/client.js:15414-15415`（ContextMeter 环；占用率 `min(100, round(usedTokens/contextWindow*100))`，`usedTokens = projectedTokens ?? pressureTokens`，`:15328-15334`）。
2. **逐轮用量走 turn-usage**：`deriveTurnTokenUsage(events)` 纯函数在浏览器端折叠 Turn 生命周期事件（`turn-usage.d.ts:23-32`；`turn-usage.js:115+`；UI 调用 `dsh-client-ui-chat/lib/client.js:7203-7212`）。`TurnTokenUsage` 逐字段（`turn-usage.d.ts:8-22`）：`uncachedInputTokens`、`outputTokens`、`totalTokens`、`cacheReadTokens?`（仅当每个 attempt 都报该桶）、`cacheWriteTokens?`（同）、`reasoningTokens?`、`routes?: readonly {provider, model}[]`（去重键 `provider\0model`，`turn-usage.js:86-92`）。渲染 `TurnUsagePanel`：缓存命中率 = `cacheReadTokens / (totalTokens − outputTokens)`（`dsh-client-ui-chat/lib/client.js:3490-3492`）；i18n 键 `message.turnUsage.*`（`:2702-2711` 中文：本轮用量/未缓存输入/缓存读取/缓存写入/输出）。
3. **累计折叠的取数**（usage-projection）：来源事件 `assistant/message`（顶层 `data.usage` 优先，否则流内最后一个 usage chunk `lastAssistantStreamChunk(stream,'usage')`）与 `assistant/attempt`（`usage-projection.js:60-66`）；同 `(turn,step)` 的最后样本以 `addReplacing` 替换旧桶再计入 totals（`:24-29,100-117`）；`llm/retry-started` 关闭替换槽使重试 attempt 重新累加（`:92-96`）。

### B4. 缓存 token（cacheRead/cacheWrite）折算

- 约定：`TokenUsage` 各桶**互斥**——`inputTokens` 只含未缓存输入，计费输入 = input + cacheRead + cacheWrite；"适配器把把缓存命中折进总 prompt 计数的 provider（DeepSeek 的 prompt_tokens）自行减出"（`dsh-llm/lib/types/types.d.ts:131-150`）。
- **DeepSeek 适配器折算**（`dsh-llm-deepseek/lib/index.js:1145-1166`）：
  `cacheRead = usage.prompt_tokens_details?.cached_tokens ?? usage.prompt_cache_hit_tokens`；
  `inputTokens = prompt_tokens − (cacheRead ?? 0)`；`outputTokens = completion_tokens`；
  `totalTokens = prompt_tokens + completion_tokens`（仅当安全整数、非负且与 wire `total_tokens` 一致时给出）；`reasoningTokens = completion_tokens_details?.reasoning_tokens`；DeepSeek 无 cacheWrite 桶。
- **pi-ai 适配器**：`usage.cacheRead > 0` 才发 `cacheReadTokens`、`usage.cacheWrite > 0` 才发 `cacheWriteTokens`（pi-ai 报 0 而非缺省）（`dsh-llm-pi-ai/lib/index.js:1357-1365`）。
- **投影折叠**：`uncachedInputTokens = usage.inputTokens`、`cacheReadTokens = usage.cacheReadTokens ?? 0`、`cacheWriteTokens = usage.cacheWriteTokens ?? 0`（`usage-projection.js:14-19`）。
- **一致性校验**（turn-usage `normalizeUsage`，`turn-usage.js:21-58`）：`exactPrompt = totalTokens − outputTokens` 必须 ≥ `knownPrompt = input + cacheRead? + cacheWrite?`；当 cacheRead 与 cacheWrite 都存在时必须严格相等，否则整轮披露不可用；无 `totalTokens` 时两个 cache 桶必须齐全，`total = knownPrompt + output`；`reasoningTokens ≤ outputTokens`。

---

# §5 WS 远程协议 /api/remote.mux 与 /api HTTP RPC（Vue 前端对接面）


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

---

# §6 MCP 接入（dsh-mcp-client）



包定位：*"MCP client bridge: connects to MCP servers and registers their tools on ctx.tools"*（`dsh-mcp-client/package.json:3`）。每个插件实例连接 **一个** MCP server，多 server = 多条 cordis 配置行（`lib/index.js:710-713` 模块注释）。插件名 `mcp-client`，注入服务 `["tools"]`（`lib/index.js:724,726`）。依赖 `@modelcontextprotocol/sdk ^1.12.0`（实际安装 1.30.1，`/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@modelcontextprotocol/sdk/package.json`）。

### A1. 支持的传输方式

**只支持两种：`stdio` 与 `streamable-http`。独立的 SSE transport 未接入。**

真实实现证据 —— transport 工厂是 `switch (config.transport)` 的穷举分支，无 default：

```js
// $BASE/dsh-mcp-client/lib/index.js:40-50（原文）
function createTransport(config) {
	switch (config.transport) {
		case "stdio": return new StdioClientTransport({
			command: config.command,
			args: config.args,
			env: buildChildEnv(config.env),
			cwd: config.cwd
		});
		case "streamable-http": return new StreamableHTTPClientTransport(new URL(config.url), { requestInit: { headers: config.headers } });
	}
}
```

`StdioClientTransport` 从 `@modelcontextprotocol/sdk/client/stdio.js` 导入，`StreamableHTTPClientTransport` 从 `.../streamableHttp.js` 导入（`lib/index.js:6-7`）。

- SDK 内虽有 `sse.js` transport（`@modelcontextprotocol/sdk/dist/cjs/client/sse.js` 存在），但 dsh-mcp-client **没有任何 import/分支使用它** —— grep `SSEClientTransport|sse` 于 `lib/index.js` 无匹配。
- `.d.ts` 注释把 streamable-http 描述为 *"Streamable HTTP (SSE)"*（`lib/types/index.d.ts:49`），即 SSE 仅作为 Streamable HTTP 内部的 server→client 流式通道，不作为独立配置传输。
- README 已知限制确认：Streamable HTTP 的断连恢复是「SDK per-request 重试」，不由 supervisor 负责（`README.md:193-195`）。

stdio 子进程环境做了凭据清洗：`buildChildEnv` = `scrubbedParentEnv()` + 配置 `env` 覆盖（`lib/index.js:28-33`）；`scrubbedParentEnv` 来自 `dsh-subprocess`，丢弃匹配 `/KEY|PASSWORD|SECRET|TOKEN/i` 及 `DSH_*` 的环境变量（`$BASE/dsh-subprocess/lib/index.js:32,50`；README `dsh-mcp-client/README.md:134`）。

### A2. 配置格式与配置文件路径

配置不是放在 `settings.yaml` 里，而是作为 **Cordis 插件行的 `config`**（settings 域 grep "mcp" 无匹配：`dsh-settings/lib/index.js`、`dsh-settings-file/lib/index.js` 均 0 处 mcp）。

**配置入口 = cordis 组合文件**，插件行形如（从 `dsh-mcp-client/README.md:34-52` 的官方样例原样摘录）：

```yaml
- id: mcp-github
  name: '@deepseek-ai/dsh-mcp-client'
  config:
    serverName: github
    transport: stdio
    command: npx
    args: ['-y', '@modelcontextprotocol/server-github']
    env:
      GITHUB_TOKEN: !!js process.env.GITHUB_TOKEN

- id: mcp-web
  name: '@deepseek-ai/dsh-mcp-client'
  config:
    serverName: web
    transport: streamable-http
    url: http://localhost:3000/mcp
    headers:
      Authorization: !!js '`Bearer ${process.env.MCP_TOKEN}`'
```

文件路径体系（cordis 树组成）：
- 每个 profile 目录：`$DSH_HOME/profiles/<name>/`，内含 `package.json`（`dsh.profile.bundles` 有序 bundle 列表）与用户层 **`cordis.patch.yml`**（`$BASE/dsh-app-boot/lib/index.js:291-296,312-314,323-327`）。
- `DSH_HOME` 解析优先级：显式配置 > `$DSH_HOME` 环境变量 > `~/.dsh`（`$BASE/dsh-home-paths/lib/index.js:65-74`）。
- bundle（如 `@deepseek-ai/dsh-base`）自带 `cordis.patch.yml` 层；用户 patch 覆盖在 bundle 层之后（`dsh-base/cordis.patch.yml:1-12` 头部注释）。mcp-client 无默认行 —— README：*"no server is enabled by default"*（`README.md:12`），grep 全部 `*.yml` 无 dsh-mcp-client 默认挂载。
- Agent preset 组合文件 `agent.cordis.yml`（`$BASE/dsh-agent-presets/lib/types/discovery.js:35`），mcp-client 亦可挂载为 Agent 作用域（见 A4 的 serverName 作用域规则）。

**Config schema（真实代码，`lib/index.js:737-761`，schemastery/zod）**：

```js
const Reconnect = z.object({
  enabled: z.boolean().default(true),                      // RECONNECT_DEFAULTS.enabled
  initialDelayMs: z.number().min(1).max(MAX_TIMER_DELAY_MS).default(500),
  maxDelayMs: z.number().min(1).max(MAX_TIMER_DELAY_MS).default(30000),
  maxAttempts: z.number().step(1).min(1).max(Number.MAX_SAFE_INTEGER).default(10)
});
const Config = z.union([z.object({
  transport: z.const("stdio"),
  serverName: z.string().required().pattern(/^[A-Za-z0-9_-]{1,32}$/),  // SERVER_NAME_PATTERN, :730
  command: z.string().required(),
  args: z.array(String).default([]),
  env: z.dict(String).default({}),
  cwd: z.string().default(""),
  toolCallTimeoutMs: z.number().default(60000),            // DEFAULT_TOOL_CALL_TIMEOUT_MS, :728
  failOnStartupError: z.boolean().default(false),
  reconnect: Reconnect
}), z.object({
  transport: z.const("streamable-http"),
  serverName: z.string().required().pattern(SERVER_NAME_PATTERN),
  url: z.string().required(),
  headers: z.dict(String).default({}),
  toolCallTimeoutMs: z.number().default(60000),
  failOnStartupError: z.boolean().default(false),
  reconnect: Reconnect
})]);
```

对应 JSON 形态（由上述 schema 直接推出，等价于 `lib/types/index.d.ts:26-84` 的 `StdioConfig | StreamableHttpConfig`）：

```json
{
  "transport": "stdio",
  "serverName": "github",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-github"],
  "env": { "GITHUB_TOKEN": "<token>" },
  "cwd": "",
  "toolCallTimeoutMs": 60000,
  "failOnStartupError": false,
  "reconnect": { "enabled": true, "initialDelayMs": 500, "maxDelayMs": 30000, "maxAttempts": 10 }
}
```

另一入口：**ACP 会话的 `mcpServers` 声明**被翻译成 Agent 作用域的 mcp-client 实例：`mountAcpMcpServers` / `resolveMcpConfigs`（`$BASE/dsh-acp/lib/index.js:216-258`），只接受无 `type`（stdio，要求 `command` 绝对路径，`:228`）和 `type:"http"`（映射为 `transport:"streamable-http"`，`:244-253`），其余 transport 抛 `AcpMcpConfigError`（`:257`）；均强制 `failOnStartupError: true`，`cwd` 取 session 工作区。

### A3. MCP 工具 → 内部工具的映射

**命名规则**（`lib/index.js:120-126`，`publicToolName`）：

```js
const joined = `mcp__${serverName}__${rawName}`;
const normalized = joined.replace(/[^A-Za-z0-9_-]/g, "_");
if (normalized === joined && normalized.length <= 64) return normalized;
// 有损归一化/超长时：截断到 51 字符 + "_" + SHA-256(serverName + "\0" + rawName) 的前 12 位 hex
const hash = createHash("sha256").update(`${serverName}\0${rawName}`).digest("hex").slice(0, 12);
return `${normalized.slice(0, 64 - 12 - 1)}_${hash}`;
```

- 前缀 `mcp__<serverName>__`；上限 64 字符（DeepSeek function-name 契约，`lib/index.js:70`）；非法字符集替换为 `_`（`:72`）；有损时追加 12-hex hash 防碰撞（`:74,124`）。
- **raw name 只出现在 wire 上**：`tools/call` 永远发送 MCP 原始名，公共名从不回解析（`lib/index.js:57-62` 注释、`createExecutor` 闭包持有 rawName，`:255-258`）。

**参数 schema**：原样透传 MCP `inputSchema` 作为工具 `parameters`（`lib/index.js:160` → `createDefinition` 的 `parameters` 字段 `:203-208`），不做转换；`description` 直接取 server 提供值（缺省 `""`）。

**输出 schema**：注册工具带规范化 result schema `{ content: array, structuredContent?: <advertised outputSchema> }`（`createOutput`，`lib/index.js:223-244`）；server 广告了不支持的 `outputSchema` 词汇时降级为无约束 `{}`（`supportedOutputSchema`，`:181-189`，借助 `dsh-tools` 的 `assertSupportedJsonSchema`）。

**结果映射**（`projectContent`，`lib/index.js:407-452`）：text 块按 `\n` 拼接；image 块经解码/能力校验后落 attachment store 成 durable 图片（`:349-390`，仅 PNG/JPEG/WebP/GIF，`:78-83`）；`resource_link` → 文本 `Resource link: ${name} (${uri})`（`:436`）；audio / embedded resource / 未知块 → 固定诊断占位文本（`:439-444`）。`isError: true` 抛错走 ToolRuntime 失败路径（`:257,273`）。`taskSupport: "required"` 的工具在调用时抛错（`:160,257`）。

### A4. 生命周期

- **启动**：`apply` 即 await 初次连接 + 首轮 `tools/list`（`lib/index.js:770-789`，`connection.ready` 在 activation 前 settle）；失败默认只记 error 不阻塞 harness，`failOnStartupError: true` 时抛错拒绝 activation（`:788`）。`serverName` 在注册作用域内用 WeakMap 排他预留（`:736,772-782`；同 Agent 重复即报错，不同 Agent 作用域可复用同名）。
- **注册换代**：`syncTools` 两阶段——先取全量分页（`listToolsUncached` 处理 `nextCursor`，重复 cursor 判非法，`:87-92,154-167`），成功后才 dispose 旧一代并注册新一代；注册冲突整体回滚（`:168-178`）。
- **tools/list 刷新**：监听 `notifications/tools/list_changed`（`ToolListChangedNotificationSchema`），收到即排队 re-sync（`:632-640`）；所有 sync（初始/通知/重连后）经单条 `syncChain` 串行化（`:544-558`）。
- **断线重连**（`startConnection`/`scheduleReconnect`，`:517-706`）：指数退避 `min(maxDelayMs, initialDelayMs * 2^(n-1))`（`:597`）；一次 outage 共享 `maxAttempts` 预算，连接存活超过 `maxDelayMs` 则预算重置（`:586`）；用尽后**注销该 server 全部工具并停止**，只能 reload/HMR 恢复（`:589-595`）；失败 generation 在 5s（`GENERATION_CLOSE_TIMEOUT_MS`，`:478`）内未关闭则停止重连以防进程重叠（`:658-662`）。HMR 热替换 = dispose 旧实例 + 新建（`:715-719` 注释）。

### A5. MCP resources / prompts

**未接入。** 源码不含任何 `resources/*`、`prompts/*`、`listResources`、`getPrompt` 调用（grep 无匹配；client capabilities 传空 `{}`，`lib/index.js:620`）。结果内容里的 `resource`/`resource_link` 块仅作文本投影。README 明确：*"Tools are the only bridged MCP capability — Resources and Prompts have no harness consumer mechanism and are deferred"*（`README.md:191`；Dev Note `README.md:209` 记录未来方向）。

---


---

# §7 Skills 机制


四个包分工：`dsh-skill` = 注册表 Service（provider 合并/优先级/渲染）；`dsh-skill-filesystem` = 本地文件 provider（发现+frontmatter 解析+watch）；`dsh-tool-skill` = 模型可见的 `skill` 工具 + 目录注入；`dsh-skill-badge` = 内置示例 provider。宿主默认挂载见 `dsh-base/cordis.patch.yml:273-284`（`skill`、`skill-filesystem`、`skill-badge`（**disabled: true**）、`tool-skill`）；agent preset 层再挂 `skill-filesystem` + `tool-skill`（`dsh-agent-presets/presets/standard/agent.cordis.yml:84-88`，ptc/cordis preset 同）。

### B1. skill 目录布局与查找优先级

两种形态（`dsh-skill-filesystem/lib/index.js:586-592`）：目录 bundle `<root>/<name>/SKILL.md`，或顶层扁平文件 `<root>/<name>.md`；**不递归发现嵌套 `**/SKILL.md`**（README `dsh-skill-filesystem/README.md:36`；发现代码只扫根目录直接条目 `discoverRoot`，`:581-614`）。

Root 与 rank（低者胜；`dsh-skill-filesystem/lib/index.js:21-25,150-188`；表载 `README.md:46-52`）：

| Rank | source | 路径 | 说明 |
|---|---|---|---|
| 100 | `project-dsh` | `<projectRoot>/.dsh/skills` | projectRoot = 最近含 `.git` 的祖先，否则 cwd（`findProjectRoot`，`:799-807`） |
| 200 | `project-agents` | `<projectRoot>/.agents/skills` | |
| 250 | `runtime` | 代码注册（无目录） | `ctx.skills.register()`，`dsh-skill/lib/index.js:20-21,437-451` |
| 300 | `custom` | `Config.customSkillDirs` 各项 | `:36,166-170` |
| 400 | `user-dsh` | `<dshHome>/skills`（`$DSH_HOME` 或 `~/.dsh`） | 跳过其 `.system` 子目录（`:175,585`） |
| 500 | `user-agents` | `<agentsHome>/skills`（`$DSH_AGENTS_HOME` 或 `~/.agents`） | `:78,176-180` |
| 600 | `bundled` | `Config.bundledSkillDir`；缺省读 `$DSH_BUNDLED_SKILL_DIR`（仅 includeDefaultRoots 时） | `:84-85,181-186`；`BUNDLED_SKILL_RANK=600` `dsh-skill/lib/index.js:23` |

合并规则：同层内按 `rank → provider 注册序 → 条目本地序` 排序，先到先得，同名后来者 warn 丢弃（`dsh-skill/lib/index.js:312-330,518-520`）；跨 scope 层时「最近的层整体获胜」（`:108-118` 类注释）。插件目录（provider）本身也是一种 skill 来源：`dsh-skill-badge` 直接在代码内注册单条 skill，body 在 `assets/dsh-badge.md`（`dsh-skill-badge/lib/index.js:10-15`，注意该 md 文件**无 frontmatter**，是纯 body，`assets/dsh-badge.md:1`）。

### B2. SKILL.md frontmatter 字段与 YAML 库

解析链：`parseSkillFile`（`dsh-skill-filesystem/lib/index.js:664-704`）→ `parseFrontmatter`（手写行扫描找首尾 `---`，`:772-798`）→ YAML 解析用 **`yaml` 包（`import { parse } from "yaml"`，`:7`；依赖 `"yaml": "^2.4.2"`，`package.json`）——不是 js-yaml/gray-matter**。

逐字段（全部出处 `:679-703,833-875`）：

| frontmatter 键 | 必填 | 处理代码 | 说明 |
|---|---|---|---|
| `name` | 是 | `stringField(data,"name")` `:679,833-836` | 必须 kebab-case：`/^[a-z0-9]+(?:-[a-z0-9]+)*$/`（`dsh-skill/lib/index.js:17`；校验 `:685-688`） |
| `description` | 是 | `:680` | 非空字符串，否则整文件忽略（`:681-684`） |
| `whenToUse` | 否 | `optionalString(parsed.data,"whenToUse")` `:699,837-840` | **camelCase 键名** |
| `metadata` | 否 | `optionalMetadata` `:701,871-875` | 仅接受非数组 object，原样保留 |
| `disable-model-invocation` | 否 | `parseInvocationPolicy` `:841-851` | `true` ⇒ `invocation.modelInvocable=false` |
| `user-invocable` | 否 | 同上 | `false` ⇒ `invocation.userInvocable=false` |

布尔语法（`frontmatterBoolean`，`:855-870`）：boolean 外接受 `1/0`、大小写不敏感 `true/false/yes/no/on/off`；非法值整文件 warn 丢弃。旧键 `disableModelInvocation`/`modelInvocable`/`userInvocable` 一律抛错并提示改用 kebab-case 规范键（`rejectLegacyInvocationKey`，`:842-844,852-854`）。frontmatter 缺失/坏 YAML → warn 跳过（`:671-678`）。

真实样例（字段名逐一对应上表）：

```markdown
---
name: my-cool-skill
description: One sentence routing summary.
whenToUse: Use when the task involves X.
disable-model-invocation: false
user-invocable: true
metadata:
  owner: team-a
---

正文 Markdown（去掉 frontmatter 后 trim 即为 content）。
```

### B3. catalog 注入格式（原文模板）

注入**不在 system prompt**，而是 `agent/pre-step` 钩子向本步 messages 追加/替换一条 **user 角色消息**（`source.kind="skill-catalog"`），仅当该 agent 解析到本插件注册的 `skill` 工具时（`dsh-tool-skill/lib/index.js:203-236`；grep `available_skills` 在 dsh-agent-instructions/dsh-agent-loop 均无 —— system prompt 域无 skill 内容）。

首次目录模板（`renderCatalogMessage`，`:238-261`，逐行原文）：

```
<system-reminder>
A skill is a reusable set of task-specific instructions. The following skills are available in this session:

<available_skills>
- `<name>`: <description>
</available_skills>

If the user names a skill, or the task clearly matches a skill's description, call the `skill` tool with the exact skill name before taking task actions. Load all applicable skills, then follow their full instructions. This catalog contains summaries only; do not infer or follow a skill's instructions until it has been loaded.
A user may also invoke a skill directly; its <skill_content> block then appears in this conversation. Follow it, and do not call the `skill` tool again for that skill.
</system-reminder>
```

条目行：`` `- \`${entry.name}\`: ${escapeText(entry.description)}` ``（`renderCatalogEntries`，`:293-295`）。description 折叠空白并截断到 `catalogDescriptionMaxLength`（默认 **500**，`:40,359-362`；Config 可改，`:49`）。

目录变化时的替换模板（`renderCatalogUpdate`，`:262-286`）：首行改为 `The available skill catalog changed. This complete catalog replaces every earlier available-skills list in this session:`，空目录时尾部改为 `No skills are currently available through the \`skill\` tool. Do not use names from earlier skill catalogs.`。去重按条目 SHA-256 摘要（`digestCatalogEntries`，`:301-304`），未变则不重发。

用户显式 `/name` 手势触发的整文注入：`SKILL_GESTURE = /(^|\s)\/([a-z0-9]+(?:-[a-z0-9]+)*)(?=\s|$)/g` 扫描用户消息（`:373,381-394`），在另一 `agent/pre-step` 钩子把 skill 全文以 `source.kind="skill-invocation"` user 消息注入（`:168-202`）。

### B4. `skill` 工具的输入 schema 与返回

工具定义（`dsh-tool-skill/lib/index.js:59-166`，`defineTool`）：

```js
name: "skill",
description: "Load the full instructions for an available skill. Call this with the exact skill name from the session skill catalog before acting on a task that names or clearly matches that skill.",
parameters: { name: {
    type: "string", required: true,
    description: "The exact skill name from the available skills list." } }
```
（与本会话 `skill` 工具声明一致。）

**返回对象**（canonical value，`:151-156`）：`{ name, provider, resourceBase?, content }` —— `content` 为 **frontmatter 之后的完整正文**；**不含文件路径**（`SkillDefinition.path` 存在于注册表内部（`dsh-skill/lib/types/index.d.ts:72-79`），但工具返回值未投影，schema 亦未列出该字段，`:67-132`）。`resourceBase` 对 filesystem 目录 bundle skill 为 `{kind:"directory", path:<SKILL.md 所在目录>}`，扁平 `.md` skill 则为所属 root 目录（locator 构造 `dsh-skill-filesystem/lib/index.js:116-133,586-592`）。输出 schema：`{name:string, provider:string, resourceBase: oneOf(directory|url|opaque), content:string}`，`additionalProperties:false`（`:67-132`）。

**模型实际看到的文本**由 `render()` → `renderSkillContent` 生成（`dsh-skill/lib/index.js:57-70`），与用户显式注入共用同一形状（原文模板）：

```
<skill_content name="my-cool-skill">
<skill_resources>
Base directory for this skill: /abs/path/to/skill-dir
Resolve relative paths mentioned by this skill against the base directory before using them. Load referenced resources only as needed.
</skill_resources>

<skill_instructions>
（skill 正文，逐字嵌入）
</skill_instructions>
</skill_content>
```

即：路径不直接给，而是以 `resourceBase` 派生的 "Base directory" 提示形式出现（`renderResourceHint`，`:71-81`）。执行前校验 `isModelInvocable`，未知/不可调用均抛错（`:139-150`）。

### B5. 字段↔行号总表

| 断言 | 出处 |
|---|---|
| name kebab-case 正则 | `$BASE/dsh-skill/lib/index.js:17` |
| `renderSkillContent` `<skill_content>` 模板 | `$BASE/dsh-skill/lib/index.js:57-70` |
| `renderResourceHint` 三种 base 文案 | `$BASE/dsh-skill/lib/index.js:71-81` |
| 合并排序 rank→providerOrder→localOrder | `$BASE/dsh-skill/lib/index.js:518-520`；后来者丢弃 `:319-321` |
| runtime rank 250 / bundled rank 600 | `$BASE/dsh-skill/lib/index.js:20-23` |
| 六类 root 构造与 rank 常量 | `$BASE/dsh-skill-filesystem/lib/index.js:21-25,150-188` |
| SKILL.md / 扁平 .md locator | `$BASE/dsh-skill-filesystem/lib/index.js:586-592` |
| frontmatter 手写扫描 + `yaml` 库 parse | `$BASE/dsh-skill-filesystem/lib/index.js:7,772-798` |
| name/description 必填、whenToUse/metadata 可选 | `$BASE/dsh-skill-filesystem/lib/index.js:679-703` |
| `disable-model-invocation` / `user-invocable` 解析 | `$BASE/dsh-skill-filesystem/lib/index.js:841-851` |
| 旧键拒绝（camelCase modelInvocable 等） | `$BASE/dsh-skill-filesystem/lib/index.js:842-844,852-854` |
| chokidar depth-1 watch、SKILL.md 二级事件 | `$BASE/dsh-skill-filesystem/lib/index.js:371-430,541-551` |
| `skill` 工具 name/description/parameters | `$BASE/dsh-tool-skill/lib/index.js:59-66` |
| 工具返回 {name,provider,resourceBase,content} | `$BASE/dsh-tool-skill/lib/index.js:151-156` |
| catalog 首次模板 | `$BASE/dsh-tool-skill/lib/index.js:238-261` |
| catalog 更新模板 | `$BASE/dsh-tool-skill/lib/index.js:262-286` |
| 条目行与 500 字符截断 | `$BASE/dsh-tool-skill/lib/index.js:293-295,40,359-362` |
| `/name` 手势正则与注入 | `$BASE/dsh-tool-skill/lib/index.js:373,168-202` |
| 宿主挂载行（skill-badge disabled） | `$BASE/dsh-base/cordis.patch.yml:273-284` |
| badge 单 skill 代码注册 | `$BASE/dsh-skill-badge/lib/index.js:10-43` |
| MCP 传输 switch | `$BASE/dsh-mcp-client/lib/index.js:40-50` |
| `mcp__<server>__<raw>` 命名+hash 兜底 | `$BASE/dsh-mcp-client/lib/index.js:120-126` |
| Config schema（stdio/http union） | `$BASE/dsh-mcp-client/lib/index.js:743-761` |
| Reconnect 默认值 | `$BASE/dsh-mcp-client/lib/index.js:472-477,737-742` |
| tools/list_changed 重同步 | `$BASE/dsh-mcp-client/lib/index.js:632-640` |
| 重连退避/预算/放弃 | `$BASE/dsh-mcp-client/lib/index.js:579-605` |
| resources/prompts 未接入 | grep 无匹配 + `$BASE/dsh-mcp-client/README.md:191` |
| ACP mcpServers 翻译 | `$BASE/dsh-acp/lib/index.js:216-258` |

### 未找到项（§6/§7 共用）

- `settings.yaml`（`~/.dsh/settings.yaml`，`$BASE/dsh-settings-file/lib/index.js:27,32`）中 **无** MCP/skill 相关字段 —— MCP/skill 配置均走 cordis 组合文件，「settings 中的 mcp 字段」不存在。
- 旧式独立 **SSE transport** 配置：未找到（仅 SDK 内有 `sse.js`，未被引用）。
- MCP **resources / prompts** 桥接：未找到（明确 deferred）。
- `dsh-skill` 的 `SkillLayer` 跨层「nearest layer wins」仅注释级描述，运行时细节以 `ScopedLayers`（`dsh-scope`）为准，未逐行展开。

---

# §8 附件/上传与图片下采样



### A1. 上传通道（真实路由 / 方法名）

**没有 multipart 上传；也不是 WS 二进制帧。** 两条独立通道：

1. **通用文件（file）→ 专用 HTTP 原始字节路由**
   - 路由常量：`FILE_UPLOAD_PATH = "/api/session/uploadFileBinary"`
     （`dsh-client-file-upload/lib/types/protocol.d.ts:2`；`dsh-client-file-upload/lib/index.js:73`）。
   - Host 端注册：`FileUploads` 服务构造器内
     `ctx.connection.fetch.register({ path: FILE_UPLOAD_PATH, methods: ["POST"], requestBody: "streaming", fetch: (request) => handleFileUploadHttp(this, request) })`
     （`dsh-client-file-upload/lib/index.js:170-175`）。fetch 路由挂在共享 `/api` 通道（`ConnectionFetchRoute.path` = "Absolute path below /api"，`dsh-client-connection/lib/types/rpc.d.ts:84-93`；`API_PATH = "/api"`，`dsh-client-connection/lib/types/api-path.d.ts:6`）。
   - 请求约束（`handleFileUploadHttp`，`dsh-client-file-upload/lib/index.js:13-56`）：
     - 仅 `POST`，否则 405（`:14-17`）；
     - `content-type` 必须 `application/octet-stream`，否则 415（`:18`）；
     - query：`sessionId` 必填（缺失/空 → 400），`name` 可选（`:19-22`）；
     - 请求体以流方式喂 `service.uploadStream(...)`（`requestBodyChunks`，`:57-69`）；
     - 响应恒 HTTP 200 + JSON `{ok:true, value:FileUploadValue}` 或 `{ok:false, error:{code,message,details}}`，`cache-control: no-store`（`:24-55`）。
   - **body 即文件原始字节**，无 multipart。
   - Host 服务 `FileUploads`（Typert Remote 服务，命名空间 `fileUploads`，`dsh-client-file-upload/lib/index.js:131,167`）：
     - Remote 方法 `upload`（`Remote("upload")`，`:138`）：
       `'fileUploads/upload': (agentId, request: EncodedFileUploadRequest, signal?) => Promise<RemoteResult<FileUploadValue>>`，作用域别名 `'agent:fileUploads/upload'`（`dsh-client-file-upload/lib/typert.remote-client.d.ts:10-20`）。请求 `{data: canonical base64, name?}`（`dsh-client-file-upload/lib/types/types.d.ts:16-21`）→ `ctx.attachments.admitEncodedFile`（`lib/index.js:202-208`）。
     - `uploadStream`（HTTP 路由内部调用，非 Remote）→ `ctx.attachments.saveFileStream`（`:214-221`）。
     - 每次成功上传铸造 `receiptId = randomUUID()`，存 `stagedFiles: WeakMap<Session, Map<receiptId,{file, requestId?}>>`（`:266-287`；randomUUID 在 `:281`）。收据仅在接收 Agent 作用域内解析（`resolve`，`:228-231`）；subagent 会话拒绝文件上传：`RemoteError("subagent/attachment-invalid", ..., {reason:"SUBAGENT_FILE_UNSUPPORTED"})`（`:298-301`）；未知收据 → `FILE_NOT_STAGED`（`:314-316`）；`user/message` 事件按 `rpcId` 退役收据（`observeSessionEvent`/`retire`，`:302-311`）。
   - 浏览器端 `FileUploadRuntime.upload`（`dsh-client-file-upload/lib/client.js:181-201`）：
     - `Blob`/`ReadableStream` → Web Worker 后台载体（Blob 用 XHR 拿 `upload.onprogress` 进度；流用 fetch `duplex:'half'`）POST `` `${FILE_UPLOAD_PATH}?${query}` ``，query 含 `sessionId`、可选 `name`，头 `content-type: application/octet-stream`（`:183-191`；worker 体 `:71-151`）。
     - `Uint8Array`（fixture/精确字节）→ Remote fallback `ctx.remote.fileUploads.upload(sessionId, {data: bytesToBase64(bytes), name})`（`:196-200`）。
     - 返回 `FileUploadValue = { receiptId: FileUploadReceiptId; file: FileAttachmentRef }`（`types.d.ts:23-29`）。
   - UI 调用点：`dsh-client-ui-conversation/lib/client.js:3036` `this.ctx.fileUpload.upload(sessionId, attachment.file, name, signal, onProgress)`。

2. **图片 → 随 prompt 远程调用以 base64 内联（不走 upload 路由）**
   - Prompt Remote 方法：`Remote("prompt")` → 映射 `'session/prompt'`（`dsh-api-session-controller/lib/index.js:2508`；`lib/typert.remote-client.d.ts:26,48`）。传输为 HTTP POST `/api/<endpoint>` JSON RPC 信封 `{type:"client-request", rpcId, method, payload}`（`dsh-client-connection/lib/client.js:6194-6216`；`/api` 共享 Channel：`rpc.d.ts:113-119`）。通用 RPC 请求体上限 300 MiB（`DEFAULT_MAX_REQUEST_BODY_BYTES = 300*1024*1024`，`dsh-client-connection/lib/index.js:24`）。
   - 线格式 `PromptContentPart`（`dsh-api-session-controller/lib/types/types.d.ts:64-75`）：
     - `{ type:'text', text: string }`
     - `{ type:'image', mediaType: ImageMediaType, data: string(base64), name?: string }`
     - `{ type:'file', receiptId: Branded<'file-upload-receipt-id'> }` —— 文件部件只引用先前上传收据，不重复传字节。
   - 浏览器编码：图片 `FileReader.readAsDataURL` 取 base64（`dsh-client-ui-conversation/lib/client.js:2811-2823`）；`encodeImage` 产出 `{mediaType, data, name?}`（`:3212-3219`）；文件部件产出 `{type:'file', receiptId}`（`serializeDraftAttachments`，`:3111-3127`）。图片媒体类型白名单在前端同样校验（`:3221-3228`）。
   - Host 准入（`dsh-api-session-controller/lib/index.js:752-790`）：先验当前模型 `inputModalities` 含 `image`（否则 `MODEL_DOES_NOT_SUPPORT_IMAGES`，`:761-765`）；`resolvePromptFileReceipts` 把 `receiptId` 换成 `FileAttachmentRef`（`:924-939`）；`ctx.attachments.admitPromptContent(...)` 将内联图片字节提升为持久 `ImageAttachmentRef`（`:768`）；`fileUploads.bindPrompt` 绑定收据到 requestId（`:772`）。"wire 调用方永远无法引用自己没上传过的附件"（`dsh-attachment/lib/types/types.d.ts:83-88` 注释）。

### A2. attachmentId 生成与存储布局

**均为内容寻址 sha256，不是 uuid。**

- 图片：`attachmentId = "sha256:" + hex(sha256(归一化后的字节))` —— 摘要对象是确定性归一化字节，不是原始提交字节（`dsh-attachment-local/lib/index.js:324,330`）。校验模式 `/^sha256:([a-f0-9]{64})$/`（`:267`）。
- 文件：逐字节 verbatim 存储，`attachmentId = "sha256:" + hex(sha256(提交字节))`（`dsh-attachment-local/lib/index.js:674-676` 内存路径；`:693-695` 流式路径；语义注释 `dsh-attachment/lib/types/types.d.ts:29-33`）。
- `AttachmentId`/`ImageVariantId` 为品牌字符串（`dsh-attachment/lib/types/brand.d.ts:4,12`；实现是恒等函数，`dsh-attachment/lib/index.js:126-136`）。

**存储根**：`<DSH_HOME>/attachments/v1`（`dsh-attachment-local/lib/index.js:986`）；`resolveDshHome`：`config.dshHome ?? 环境变量 DSH_HOME ?? ~/.dsh`（`dsh-home-paths/lib/index.js:11-15,73-76`）。

本地目录布局：

| 内容 | 路径 | 出处 |
|---|---|---|
| 归一化图片对象 | `attachments/v1/objects/<sha[0:2]>/<sha>` | `dsh-attachment-local/lib/index.js:288-291` |
| 请求版本图片缓存 | `attachments/v1/request-images/<variantHash[0:2]>/<variantHash>` | `:803-805,862` |
| 文件对象（真身） | `attachments/v1/file-objects/<sha[0:2]>/<sha>` | `:664-665` |
| 文件别名（带文件名） | `attachments/v1/files/<sha[0:2]>/<sha>/<sanitized name>` | `:659-661` |

发布为不可变对象：暂存写 → fsync → 硬链接就位 → EEXIST 按摘要去重 → 只读模式（目录 0o440 / 文件 0o600，即 mode 448/384）→ 逐级目录 fsync（`publishImmutableObject` `:425-436`；目录持久化 `:349-400`；文档 `dsh-attachment-local/lib/types/store.d.ts:88-98`）。

文件名净化 `fileLeafName`（`dsh-attachment-local/lib/index.js:638-644`）：手工剥离 `/` 与 `\` 两种分隔符、控制字符删除、Windows 非法字符 `<>:"|?*` → `_`、保留设备名加 `_` 前缀、UTF-8 前缀截断 ≤255 字节、空/`.`/`..` → `"file"`。图片显示名 `displayName`（`:272-276`）：basename、去控制字符、trim、截 255。

**ImageAttachmentRef 逐字段**（`dsh-attachment/lib/types/types.d.ts:7-28`）：
- `attachmentId: AttachmentId` — 不透明存储 id，绝不携带路径/URL（`:8-9`）
- `mediaType: ImageMediaType` — 由存储字节验证；`ImageMediaType = 'image/png'|'image/jpeg'|'image/webp'|'image/gif'`（`:5,10-11`）
- `bytes: number` — 精确编码字节长（`:12-13`）
- `width: number` / `height: number` — 编码固有宽高 px（`:14-17`）
- `name?: string` — 去路径显示名（`:18-19`）
- `originalDimensions?: { width: number; height: number }` — 仅当归一化缩小过才出现；EXIF 方向应用后、归一化缩放前的输入尺寸（`:20-27`）

**FileAttachmentRef 逐字段**（`dsh-attachment/lib/types/types.d.ts:34-41`）：
- `attachmentId: AttachmentId`（内容寻址）
- `name: string`（净化显示名，同时是存储对象叶子名）
- `bytes: number`（精确字节长）

相关类型：`EncodedImageAttachment {mediaType, data(base64), name?}`（`:75-82`）、`EncodedFileAttachment {data(base64), name?}`（`:43-48`）、`ImageAttachmentLimits {maxImageBytes, maxImagesPerMessage, maxMessageImageBytes, maxImagePixels, maxImageDimension, mediaTypes}`（`:65-73`）、`ImageRequestPolicy {maxPixels, maxBytes}`（`:128-133`）、`RequestImageAttachment {variantId, attachment, data, mediaType, bytes, width, height, depth:'uchar', space:'srgb', hasAlpha}`（`:135-152`）、`FileUploadValue {receiptId, file}`（`dsh-client-file-upload/lib/types/types.d.ts:23-27`）。

### A3. 图片验证、下采样与视觉 token 公式

**支持 MIME**：`image/png | image/jpeg | image/webp | image/gif`（`dsh-attachment/lib/types/types.d.ts:5`；sharp format 映射 `dsh-attachment-local/lib/index.js:129-134`）。

**校验链**（全在宿主端；浏览器端不缩放）：
1. Canonical base64：`decoded.toString('base64') !== data` → `INVALID_IMAGE_BASE64`（文件通道空串报 `INVALID_FILE_BASE64`；图片拒绝空 payload）（`dsh-attachment/lib/index.js:73-77`）。
2. 批次限制 `validateImageBatch`（`dsh-attachment/lib/index.js:195-200`）：数量 > `maxImagesPerMessage` → `TOO_MANY_IMAGES`；总字节 > `maxMessageImageBytes` → `IMAGES_TOO_LARGE`；类型不在白名单 → `UNSUPPORTED_IMAGE_TYPE`。
3. 单图准入（`prepareImageFile`，`dsh-attachment-local/lib/index.js:320-342`）：`byteLength > maxImageBytes` → `IMAGE_TOO_LARGE`（`:321`）；空字节 → `INVALID_IMAGE`（`:293`）；sharp 全解码（`failOn:'error', limitInputPixels:false`）+ `image.raw().toBuffer()`，`w*h > maxImagePixels` → `IMAGE_TOO_MANY_PIXELS`、`max(w,h) > maxImageDimension` → `IMAGE_DIMENSION_TOO_LARGE`（`:179-194`）；声明类型 ≠ 解码类型 → `IMAGE_TYPE_MISMATCH`（`:298`）。EXIF orientation ≥5 宽高互换（`:142-146`）。
4. 默认限制（`dsh-attachment-local/lib/index.js:888-912`）：
   - `DEFAULT_MAX_IMAGE_BYTES = 20*1024*1024`（20 MiB，超限**拒绝而非缩小**）
   - `DEFAULT_MAX_IMAGES_PER_MESSAGE = 20`
   - `DEFAULT_MAX_MESSAGE_IMAGE_BYTES = 200*1024*1024`（200 MiB）
   - `DEFAULT_MAX_IMAGE_PIXELS = 64e6`；`DEFAULT_MAX_IMAGE_DIMENSION = 8192`
   - 归一化：`DEFAULT_NORMALIZED_IMAGE_MAX_PIXELS = 2048*2048`；`DEFAULT_NORMALIZED_IMAGE_MAX_DIMENSION = 8192`；`DEFAULT_NORMALIZED_IMAGE_MAX_BYTES = 4*1024*1024`
   - 压缩并发默认 2、上限 8（`:910-912`）。

**下采样库：sharp**（`"sharp": "^0.35.3"`，`dsh-attachment-local/package.json:35`），非自研。

**持久归一化（provider-independent）**（`normalizeImage`，`dsh-attachment-local/lib/index.js:248-263`）：
- 直通条件 `canPassThroughNormalization`（`:205-207`）：非 GIF、非动图、无残留元数据（exif/xmp/iptc/icc/profile/photoshop/comments/orientation，`:135-137`）、`depth==='uchar'`、`space==='srgb'`、字节 ≤ maxBytes、像素 ≤ maxPixels、长边 ≤ maxDimension。
- 尺寸：`requestImageDimensions(w,h,maxPixels)` 保纵横比缩入总像素预算，再套长边帽：`scale = maxDimension/longEdge`，`w,h = max(1, floor(w*scale))`（`:227-236`）。核心公式 `scale = min(1, sqrt(maxPixels/(w*h)))`，随后逐像素回退循环保证整数积 ≤ 预算（`dsh-attachment/lib/index.js:150-178`）。
- 管道：`sharp(data,{failOn:'error',limitInputPixels:false}).rotate().toColourspace('srgb').resize({width,height,fit:'inside',withoutEnlargement:true})`（`:215-225`）。
- **质量阶梯**：`IMAGE_ENCODING_QUALITIES = [85, 75, 60]`（`:60-64`）；编码器：源有 alpha → WebP `{quality, effort:0}`，否则 JPEG `{quality}`（`:65-87`；`encoding.d.ts:143-145` 注明 WebP effort 固定 0：更深搜索多花 3-4 倍编码时间只换约 5% 体积）。`encodeFirstWithinLimit` 取首个 ≤ maxBytes 候选；全超则保留最小者（`:94-105`）。
- 输出必须单帧 8-bit sRGB 且 alpha 兼容（`:209-213,126-128`）。

**请求版本图片（route-owned）**：`readRequestImageFile`（`:857-883`）。变体身份：
`REQUEST_IMAGE_TRANSFORM_VERSION = "request-image-v5"`（`:742`）；
`variantId = "sha256:" + sha256(JSON.stringify({transformVersion, attachmentId, routePixelBudget:policy.maxPixels, encodedByteBudget:policy.maxBytes, encoding:{webpQualities:[85,75,60], webpEffort:0, jpegQualities:[85,75,60], order:["alpha:webp","opaque:jpeg"], colourspace:"srgb"}}))`（descriptor `:754-768`；`requestImageVariantId :775-777`）。

**DeepSeek 路由请求图片预算**（`dsh-llm-deepseek/lib/index.js:446-466`）：
- `DEFAULT_MAX_REQUEST_FILES_BYTES = 128*1024*1024`；`DEFAULT_MAX_IMAGES_PER_REQUEST = 600`；
- 正常视觉像素预算 `DEFAULT_REQUEST_IMAGE_PIXEL_BUDGET = 64e4`（640,000 px）；low-detail `512*512`；
- 单请求图字节目标 `DEFAULT_REQUEST_IMAGE_MAX_BYTES = 1024*1024`（1 MiB）；
- 溢出卸载量子：字节 64 MiB（`:1398`）、张数默认 20（`:1903`）；
- 超 32 MiB 的 chat 图强制走 Files API `file_id`（`MAX_CHAT_IMAGE_BYTES = 32*1024*1024`，`:872`）；Files API 索引持久化于 `DSH_HOME/llm-deepseek/files-v3.json`（`dsh-llm-deepseek/lib/types/upload-index.d.ts:33`）。

**DeepSeek VL（v4）视觉 token 公式**（`deepSeekImageTokens`，`dsh-llm-deepseek/lib/index.js:422-432`；声明 `lib/types/image-tokens.d.ts:18`；官方计算器逐字移植，`:301-310`）：
- 常量（`:311-322`）：`PATCH_SIZE=14`、`DOWNSAMPLE_RATIO=3`、`MAX_IMAGE_TOKENS=384`、`COMPRESS_PAD_TO=4`、`MAX_WIDTH_HEIGHT_RATIO=8`、`MIN_PIXELS=384*384`。
- 网格 token（含行分隔与首尾帧，`gridTokens` `:326-331`）：
  `tokens = gridHeight*(gridWidth+1) + 2`；若 `gridHeight` 奇数再 `+ (gridWidth+1)`；再 `+ ceilDiv(gridHeight,2)*(gridWidth+1) % 2 * 2`。
- 单轮 `resizeOnce`（`:398-411`）：宽钳到 ≤8×高；总像素 < 384×384 时放大 `scale=sqrt(MIN_PIXELS/pixels)`（trunc）；宽高各 pad 到 14 的倍数；`grid = ceilDiv(floor(padded/14), 3)`；预算 = `384 − (4−1) = 381`，超预算用闭式解 `solveResizeRatio`（`idealGridWidth = sqrt((budget−2)/aspect + 0.25) − 0.5`，`:333-372`）重投影；返回 `numTokens + 3`（pad-to-4 对齐按 3-token 上界计价，`:374-396`）。
- `deepSeekImageTokens(w,h)` 迭代 `resizeOnce` 至不动点（≤10 轮，`:422-432`）。**每图封顶 384 token。**
- 计费集成 `deepSeekImageRequestPricing`（`:494-517`）：文本路由每图 → `{visualTokens:0, text:textOnlyImageText(ref)}`；图像路由按 `offloadedImagePrefixCount`（`dsh-llm/lib/types/content.js:243-264`：超额张数/字节各按 quantum 向上取整，从最旧开始移除前缀）确定卸载数，保留图 `visualTokens = deepSeekImageTokens(requestImageDimensions(w,h,像素预算))`，`text = requestImageHandleText(...)`。

**未见客户端下采样**：浏览器原图 base64 直传（`dsh-client-ui-conversation/lib/client.js:2811-2823`）；`dsh-tool-fs`/`dsh-web` 中未发现另一下采样实现（read_image 只报告归一化降尺倍率：`(downscaled from WxH px; multiply coordinates by X)`，`dsh-tool-fs/lib/index.js:1003-1009`）。

### A4. 文件附件 → 文本 handle 模板（原文）

`fileHandleText`（`dsh-llm/lib/types/content.js:115-122`）；`digest = attachmentId.slice(7,15)`（sha256 前 8 位十六进制，`:116`）；`quoted = JSON.stringify`（`:20-22`）。

`identity = \`File ${quoted(ref.name)} (${ref.bytes} bytes, sha256:${digest})\``（`:117`）

有可读路径时（`:121`，逐字模板）：
```
[${identity}: verbatim read-only copy saved at ${quoted(readonlyPath)}. Read that path with your file tools when its contents are needed; copy it to a writable location before modifying it. When delegating file work, include this saved path in the delegation prompt; only subagents sharing this execution environment can read it.]
```
无可读路径时（`:119`，逐字）：
```
[${identity} was uploaded, but the current execution environment cannot access a readable path. Report that limitation if its contents are needed; do not claim to have read it.]
```
投影 `projectFilesToText` 对所有模型路由无条件执行（含嵌套 tool-result 递归替换为 text 块）；这是 provider 收到的文件唯一表示（`:123-159`；`content.d.ts:68-85`）。计量侧同一函数：`llm.fileRequestText(ref) = fileHandleText(ref, fileReadPath(ref))`（`dsh-llm/lib/index.js:2005-2007`）。

图片相关 handle（`dsh-llm/lib/types/content.js`）：
- `requestImageHandleText`（`:61-66`）：`Image ${identity}; request preview ${w}x${h}px.` +（有路径时）`normalizedAccessText`：` Normalized copy (read-only; may be resized or re-encoded): ${quoted(readonlyPath)} (${ref.width}x${ref.height}px, ${ref.mediaType}). Source dimensions, format, and byte size may differ. Copy to a writable path ending in ${ext} before editing.`（`:37-41`）；无路径时追加 `It may be resized or re-encoded; source dimensions, format, and byte size may differ.`（`:63-64`）。
- `offloadedImageText`（`:73-79`）：`[image omitted to fit request image limits; ${identity}. No local normalized image path is available; ask the user to attach it again if needed.]`
- `textOnlyImageText`（`:47-50`）：`[image omitted because this model accepts text only; attachment sha256:${digest}]`
- DeepSeek 工具结果图片前导文本：`"Attached image(s) from tool result:"`（`dsh-llm-deepseek/lib/index.js:24`）。

---


### 未找到项（§4/§8 共用）

- 图片 multipart / WS 帧上传通道：**未找到**（文件走 `application/octet-stream` HTTP POST 流式路由；图片随 `session/prompt` JSON base64 内联）。
- 客户端（浏览器）侧图片下采样：**未找到**——浏览器原样 base64 上传，缩放全部在宿主端 sharp。
- 除 `session/prompt`、`fileUploads/upload`、`POST /api/session/uploadFileBinary` 之外的第三条上传路由：**未找到**。
- `dsh-token-meter` 中任何真实 tokenizer（tiktoken/gpt-tokenizer 等）：**未找到**——纯 `len/4+4` 启发式 + 路由视觉 token 价。
- 独立 usage 推送事件名（如 `usage/update`）：**未找到**——usage 经 session projection 块（`{asOfSeq, values}`）随快照/基线下发；逐轮面板由前端 `deriveTurnTokenUsage` 现算。

---

# §9 子代理与工作流

> 本节缩写（均相对 BASE）：**ST** = `dsh-tool-subagent/lib/index.js`；**SC** = `dsh-tool-subagent-control/lib/index.js`；**LA** = `dsh-tool-subagent-control/lib/types/list-agents.js`；**SA** = `dsh-subagent/lib/index.js`；**DRV** = `dsh-subagent-in-process-driver/lib/index.js`；**SP** = `dsh-subagent-spawn-in-process/lib/index.js`；**FK** = `dsh-subagent-fork-in-process/lib/index.js`；**TW** = `dsh-tool-workflow/lib/index.js`；**WF** = `dsh-workflow/lib/index.js`；**WW** = `dsh-workflow-worker-thread/lib/index.js`（宿主侧）；**WK** = `dsh-workflow-worker-thread/lib/worker.cjs`（worker 侧）；**RL** = `dsh-tool-ralph/lib/index.js`；**TJ** = `dsh-tool-jobs/lib/index.js`；**TJo** = `dsh-jobs-local/lib/index.js`。

---

## 1. `subagent` 工具：输入 schema、返回、id 生成

### 1.1 插件与配置

- 插件名 `tool-subagent`，注入 `tools/subagents/systemPrompt/sessionProjections`（ST:245-251）。
- 配置 `Config`（ST:252-270）：
  - `provider: string`（必填，`ctx.subagents` 注册名，如 spawn/fork）
  - `toolName: string`，默认 `"subagent"`
  - `modelSelectionSettings: boolean`，默认 false
  - `enableRunInBackground: boolean`，默认 true
  - `backgroundMode: "one-shot" | "continuable"`，默认 `"one-shot"`
  - `agentOptions?: { provider, model, reasoningEffort, maxTokens }`
  - `persona?: string`；`toolFilter?: { allow?, deny? }`
  - `maxDepth: natural | "provider-managed"`，默认 `3`
- 类型逐字段见 `dsh-tool-subagent/lib/types/index.d.ts:17-72`（`Config` 接口）。

### 1.2 模型可见参数 schema（逐字段）

工具注册于 `mount()` 内 `defineTool({ name: toolName, ... })`（ST:398-562）：

| 参数 | 类型 | 必填 | 出现条件 | 说明出处 |
|---|---|---|---|---|
| `description` | string | 是 | 恒有 | 「3-5 词短描述」，作为 child label（ST:402-406） |
| `prompt` | string | 是 | 恒有 | 描述文案随 provider `inheritsParentContext` 变化（ST:407-411; wording 在 ST:344-353） |
| `provider` | string | 否 | 仅当 model-selection 策略启用（ST:412-425） | 与 `model` 必须成对提供（ST:68） |
| `model` | string | 否 | 同上 | provider 解释的精确 model id |
| `reasoning_effort` | string | 否 | 同上 | 适配器拥有的 reasoning effort（ST:79 `ReasoningEffortId(...)`） |
| `run_in_background` | boolean | 否 | 仅当 `enableRunInBackground !== false`（ST:426-429） | `continuable` 模式默认 true（`request.run_in_background ?? options.continuable`，ST:360）；`one-shot` 模式默认 false；关闭后台时显式传 true 会抛错（ST:357） |

- 工具描述主体分三种文案（后台+continuable / 后台+one-shot / 纯前台），ST:400。
- 配套发现工具 `list_subagent_models`（仅 model-selection 启用时注册）：参数 `provider?`、`model?`，返回字符串（ST:172-197）。
- continuable 后台模式下注册系统提示节（`tool:${toolName}`，order=`TOOL_SUBAGENT`=2800，order 常量见 `dsh-system-prompt/lib/index.js:34`），文案模板（ST:579）：
  > `Use ${toolName} in the background by default. Start independent delegations together in one assistant message ... the runtime sends you a notice containing its outcome and any final assistant message.`

### 1.3 返回 schema 与渲染

`output.schema` 是 three-way `oneOf`（ST:431-483）：

1. `{ kind: "background", jobId: string }` — one-shot 后台，走 `ctx.jobs.start({ kind: "subagent", ... })`（ST:534-555）；
2. `{ kind: "continuable", subagentId: string }` — `ctx.subagents.startContinuable(...)` 返回的 `childId`（ST:525-533）；
3. `{ kind: "foreground", runId: string, output: json[] }` — 前台 `settleForegroundRun`（ST:314-331, 557-560）。

渲染文本（ST:484-487）：
- background：`` `started background subagent job ${value.jobId}` ``
- continuable：`` `started subagent ${value.subagentId}` ``
- foreground：拼接 output 里的 text block（`outputValueText`，ST:272-274）。

### 1.4 agent id 生成格式

- **child/agent id = 裸 UUID 字符串，无 `"agent-"` 前缀**：
  - continuable：`const childId = spec.childId ?? brandString(randomUUID())`（SA:1649）；
  - one-shot 进程内驱动：`const childId = brandString(randomUUID())`（DRV:166）；
  - `brandString` 是纯编译期 brand，运行时恒等（`dsh-brand/lib/index.js:20-22`）。
  - 在本次调研的全部包中 grep `"agent-"` 前缀字面量：**未找到**（用户示例前缀 `agent-` 并非真实格式，实际就是 UUID）。
- 生命周期 `runId`：`SubagentRunId(randomUUID())`（SA:295, SA:328）。
- **one-shot 后台 job id** 才有可读前缀：`` JobId(`${spec.kind}-${count}`) ``（`dsh-jobs-local/lib/index.js:141`），kind 为 `"subagent"`（ST:539），即形如 `subagent-1`、`subagent-2`。
- workflow run id：`` WorkflowRunId(randomUUID()) ``（WW:877）。

---

## 2. 子代理生命周期 与 控制工具

### 2.1 两种形态

- **one-shot**（`SubagentRun`，`dsh-subagent/lib/types/types.d.ts:292-318`）：一个 turn 一个结果，`result: Promise<SubagentResult>`（子级失败不 reject，以 `stopReason` 表达），必须 `dispose()`。
- **continuable**：一个持久 Session + 至多一个进程内 Activation，Agent inbox 是唯一 turn 队列（SA:1549-1562 模块注释；`Activation` 结构 `dsh-subagent/lib/types/continuation-activation.d.ts:26-64`）。

### 2.2 创建 → 运行

- `SubagentRuntime.start(name, request)`（SA:3145-3173）：capability 校验 → 生成 one-shot descriptor → `provider.start()` → 向父 Session 追加 `subagent/catalog` 事件（`establishCatalogChild`，SA:1509-1523）→ `observeRun` 发布 `subagent/start`/`subagent/end` 事件对（SA:293-314）。
- `startContinuable(spec)`（SA:1643-1723）流程：`assertAdmitting` → 需要 sessionPersistence（SA:1958-1963）→ 分配 childId（UUID）→ `resolveChildDepth`/`resolveChildAgentOptions` → snapshot descriptor（version 3）→ `provider.prepareContinuable` 取 seed → `materialize`（`agents.create({ sessionId: childId, parentAgent, meta: childSessionMeta(...), seed, ... })`，SA:1060-1118）→ 若作用域内 `send_message` 是标准工具则给初始 prompt 追加「回传指引」（SA:1710-1714）→ `submitMaterialized` 以 `delivery:"queue"` 投递首条 prompt → 向父 Session 追加 catalog 事件。inbox 接受即 resolve，返回 `{ childId, messageId }`。
- 运行：child Agent 自身 loop 消费 inbox（`deliver`: `steer`→`agent.steer(message)`，`queue`→`agent.followup(message)`，SA:719-723）。
- 子组合（父子共同点）：`applyChildComposition` = join 父 preset + 注册固定委派声明 `SUBAGENT_DELEGATION_CONTEXT`（"You are a delegated subagent: ..."，SA:519, 542-555）+ persona 遮蔽 + `tools.restrict(toolFilter)`；委派策略以 `sandbox/mode`、`approval/policy`（`source:"delegation"`，审批固定 `"never"`）写入子日志（SA:566-590）。

### 2.3 idle → 自然结算 → 通知父

- `watchSettlement`（SA:1147-1191）：await `agent.whenIdle()` → `settlementState`（inbox 有 pending 或仍有 ownedChildren 则 wait）→ `flushFinalState` → 在 `runMaintenance` 中 `dispose(activation, true)`。
- `finishDisposal`（SA:1200-1243）：capture 终态 → dispose handle → `notifySettlement`（向父发送结算 notice）→ `observer.settle` 发 `subagent/end` 事件。
- 停因映射 `epochStopReason`：max-tokens / aborted(aborted|interrupted) / error / refusal(blocked) / completed（SA:376-391）。

### 2.4 `send_message`（续聊）schema 与语义（SC:22-60）

- 参数：`agent_id: string`（必填）、`message: string`（必填）。
- 输出：`{ messageId: string }`；渲染 `` `message delivered to agent ${args.agent_id}` ``（SC:46-49）。
- 执行：`ctx.subagents.sendMessage(sender, brandString(agent_id), [{type:'text',text}], { signal })`（SC:51-59）。
- 路由语义（`SubagentContinuationManager.sendMessage`，SA:1734-1747）：
  1. sender 是 resident continuable child 且 target == 其 `parentSession` → `sendToParent`（父不可用报 `PARENT_UNAVAILABLE`）；
  2. sender 的 header.parentSession == target 但不是 resident child → `UNAUTHORIZED`（SA:1742）；
  3. 其余按 direct child 投递，`delivery:"steer"`（运行中在最近的 step 边界插入；idle 则开启新 turn；不在内存则冷恢复 `coldResume`，SA:1781-1817, 1870-1919）。
- 冷恢复要求：child 日志含 version-3 `subagent/descriptor` 且 `mode:"continuable"`，否则 `NOT_RESUMABLE`（SA:1888-1889）。

### 2.5 `interrupt_agent` schema 与语义（SC:61-92）

- 参数：`agent_id: string`（必填）。输出：`{ accepted: boolean }`（恒 true）；渲染 `` `interrupt requested for agent ${args.agent_id}` ``。
- 执行：`ctx.subagents.interrupt(brandString(agent_id), { kind:"ancestor", agent: caller })`（SC:83-91）。
- 语义（registry.interrupt，SA:853-866）：ancestor 必须是精确 live agent 且不能打断自己；目标必须是 caller 的 live 后代（`activation.ancestry` WeakSet 判定）；`cancel({kind:'parent'}, { keepInbox: true })` —— 只停当前 turn，已排队 inbox 保留、后代继续运行、目标保留可续聊（public JSDoc SA:2916-2930）。目标不存在 = 接受的 no-op。
- 面向浏览器的 `interruptByParent`（authority `{kind:'user', parentSessionId}`，校验 header.parentSession）：SA:3082-3098。

### 2.6 `list_agents` schema 与语义（LA）

- 插件名 `tool-subagent-list-agents`（LA:11），注册工具 `list_agents`（LA:52-153）。
- 参数：`scope?: "children" | "descendants"`，默认 children（LA:67-73, 14-16）。
- 输出：`oneOf` 两种 entry（LA:74-104）：
  - `{ kind:'child', id, label, status: 'running'|'idle'|'ready', parent?, depth? }`
  - `{ kind:'diagnostic', id, reason: 'corrupt'|'unsupported'|'unavailable', parent?, depth? }`
- status 判定（LA:24-29）：live agent 且 `status==='running'` → `running`；resident 但不在跑 → `idle`；registry 无 live agent（仅存在于存储）→ `ready`（可恢复态，不是终态）。
- one-shot child 被过滤掉不呈现给模型（LA:38-40）；render：每行 `${id} [${status}]${at} — ${label}`，descendants 附加 ` parent=... depth=...`，空列表 `(no subagents)`（LA:105-123）。
- 底层 `ctx.subagents.listChildren/listDescendants`（SA:2981-3001 → listChildren SA:2071-2074；冷读并发常量 `COLD_READ_CONCURRENCY = 4`，SA:2054）。

### 2.7 生命周期事件名（父侧可观察）

- Cordis 事件：`subagent/provider-added`、`subagent/provider-removed`、`subagent/start`、`subagent/end`（`dsh-subagent/lib/types/index.d.ts:62-95`；payload `SubagentRunInfo { runId, provider, id, local }` / `SubagentRunEndInfo { ...stopReason, lastAssistantMessage? }`，`types.d.ts:74-110`）。
- Session 事件：`subagent/descriptor`（子日志，version 3，log-only 不进模型历史；`dsh-subagent/lib/types/descriptor.d.ts:26-44`）、`subagent/catalog`（父日志追加子发现事实，SA:1509-1523）。
- workflow 事件：`workflow/start|phase|log|agent-start|agent-end|end`（`dsh-workflow/lib/types/index.d.ts:17-72`）。

---

## 3. 父子消息回流（通知文本模板原文）

### 3.1 子 → 父：`send_message` relay 消息

`createAgentMessage`（SA:612-620）：构造 user message，内容首块为模板原文：

```
Agent ${sender.id} sent a message: 
```
（注意冒号后有一个尾随空格，SA:616），随后拼接 sender 的 content blocks。

source 归属（`AgentMessageSource`，`dsh-subagent/lib/types/continuation-messages.d.ts:12-18`）：`{ kind: "agent-message", form: "relay", senderSessionId }`。

投递：`sendWaking(parent, message, "steer")`——父若 resident 走其 Activation inbox，否则直接 `parent.steer/followup`（SA:873-885, 1829-1846）。

### 3.2 子 → 父：运行结束 settlement notice

`settlementSummary(childId, stopReason)` 开头行模板（SA:641-654），subject = `` `Background subagent ${childId}` ``：

- completed：`` `${subject} finished and will do no further work unless you send it more.` ``
- aborted：`` `${subject} was stopped before it finished.` ``
- max-tokens：`` `${subject} ran out of room before it finished.` ``
- refusal：`` `${subject} declined the task.` ``
- error：`` `${subject} failed before it finished.` ``
- 其它：`` `${subject} ended abnormally (${String(stopReason)}) before it finished.` ``

`createSettlementMessage(childId, terminal)`（SA:661-681）content = [summary 行] + 二选一：

- 无输出：`"It left no closing message."`（SA:669）
- 有输出：`"Its closing message:"`（SA:672）+ `terminal.output` blocks

source：`{ kind: "subagent-settled", form: "notice", summary: boundContextSummary(summary), senderSessionId: childId }`（SA:674-679；类型 `continuation-messages.d.ts:26-34`）。

发送时机：`notifySettlement`（SA:1245-1259）——父正 teardown 时 `parent.inject(message)`（不唤醒）；否则 `sendWaking(parent, message, parent.status === "idle" ? "queue" : "steer")`。只有曾被投递过消息（`activation.announced`）的子才发通知（SA:1246）。

### 3.3 后台 one-shot（jobs 路径）notice 模板

- 完成通知文本 `fitCompletionNotice`（TJ:116-121）：
  ```
  background job ${snapshot.id} (${snapshot.kind}: ${snapshot.label}) finished ${statusLine(snapshot)}. Read its output with job_output.
  ```
  其中 `statusLine` = `` `[status: ${status}]` `` 或 `` `[status: ${status}, ${detail}]` ``（TJ:80-82）；kind=`subagent`、id=`subagent-N`。
- source：`{ kind: "plugin", plugin: "tool-jobs", form: "notice", summary: "${kind} ${label} ${statusLine}" }`（TJ:206-218, 113-115）。
- 投递：owner idle 且未超 `maxConsecutiveWakes`（默认 3）时 `followup`（唤醒），否则 `inject`（TJ:220-226）。
- one-shot 结果 → job outcome 映射 `runOutcome`：completed→`{status:'completed', output: 最终文本}`；无 diagnostic 的 aborted→`killed`；其余→`failed` + `stopReason; diagnostic: ...`（SA:2673-2694）。

### 3.4 父 → 子的初始「回传指引」（注入到 continuable 首条 prompt）

`withContinuableReturnGuidance(parentId, prompt)` 追加的 text block 模板原文（SA:627-633，`encodedParentId = JSON.stringify(parentId)`）：

```
Your parent agent id is ${encodedParentId}. Before you finish, send your result to that agent with send_message({ agent_id: ${encodedParentId}, message: "<self-contained result>" }). The parent shares your workspace but does not automatically receive your transcript, tool output, or reasoning. Send earlier messages as well when a finding changes what the parent should do next; sending a message does not end your turn.
```

### 3.5 独立 session 与父子 link 字段

子代理是**独立 Session**（childId 即 sessionId）；关联通过父 Session header（`childSessionMeta`，SA:502-513）：

```
{ cwd, agentPreset, parentSession: parentHeader.id, isSeeded, origin: "subagent", delegationDepth: childDepth }
```

- 遍历/列举仅认 `header.origin === "subagent" && header.parentSession === parentSessionId`（SA:2073, 2129-2130）。
- 深度：`delegationDepthOf(agent) = max(header.delegationDepth ?? 0, options.subagentDepth ?? 0)`（SA:144-148）；子深度 = 父深度+1，超过 `maxDepth` 抛 `SubagentDepthError`（`subagent depth ${attemptedDepth} exceeds maxDepth ${maxDepth}`，SA:412-437）。
- fork seed：截至父日志最后一个 `turn/end` 的平衡前缀（FK:23-28）；`inheritedEventCount` 标记继承边界；`subagent/catalog` 投影 fold 时 `event.seq < state.inheritedEventCount` 的继承事实被排除（SA:1490-1496）。
- 子最终输出选取规则（`subagent/end.lastAssistantMessage` 与 `SubagentResult.output` 同规则）：最后一条非空 assistant message，否则累计流式文本（`AssistantOutputFold`，SA:175-221）。

---

## 4. Workflow：脚本执行机制、参数校验、并发上限

### 4.1 分层

- 工具层 `dsh-tool-workflow`（TW）：只管模型可见 schema + run 生命周期 + 持久记录；
- 服务层 `dsh-workflow`（WF）：抽象 `WorkflowEngine` seam + `WorkflowError`/`isFatalWorkflowError`（WF:35-51）；
- 引擎层 `dsh-workflow-worker-thread`（WW/WK）：实际执行。

### 4.2 执行机制：worker thread + node:vm（不是 new Function）

- `WorkerThreadWorkflowEngine.start()`（WW:872-919）：`validateMeta` → `assertBodyParses` → 解析 provider/maxTotalAgents → `new WorkerRun(...)` → 发 `workflow/start`，result 结算时发 `workflow/end`（WW:910-917）。
- 每个 run 起一个**全新 Node `Worker` 线程**（WW:310-311，`resolveWorkerSpawn` WW:223-251：built 形态加载 `worker.cjs`；未构建形态用 data: URL bootstrap 内联 tsx 注册）。worker 环境被 scrub：无凭证、无 loader flags、无代理策略（`workerSpawnEnv` WW:205-214）。
- 脚本编译：`new vm.Script("(async () => {\n${body}\n})()", { filename: "workflow:${meta.name}", lineOffset: -1 })`——宿主侧同一 wrapper 先 parse 一次（WW:816-826），worker 侧编译并 `runInContext(context, { timeout: limits.syncTimeoutMs })`（WK:263-268, 331）。上下文为 `vm.createContext({}, ...)`（WK:270）。
- 启动握手：worker 发 `ready`，宿主回 `go` 才执行脚本体（WW:415-417；WK:729-731, 769-772）。取消 `cancel` 令所有 hook 在下一个边界抛 `CANCELLED`（WK:305-317）。

### 4.3 hooks 注入与实现位置

globals 在 worker 侧注入并 freeze（WK:271-281）：`agent`、`parallel`、`pipeline`、`phase`、`log`、`args`。

- `agent(prompt, opts)`：WK:404-495。校验 prompt 非空字符串；`readAgentOptions`（WK:497-536）只允许 `{label, phase, schema, provider, model}`（SUPPORTED_AGENT_OPTIONS，WK:216-222），`effort/isolation/agentType` 明确拒绝为 deferred（WK:224-228）；schema 过 `assertObjectJsonSchema`（WK:525-532）。总配额检查 `started >= maxTotalAgents` → `AGENT_CAP`（WK:408）。子经 RPC（`child-start`）在宿主上以 `subagents.start(provider, {...})` 真起（WW:482-560）；有 schema 时返回 `result.structured`，无 schema 返回 text 拼接；子失败/非 completed 返回 `null`。
- `parallel(thunks)`：WK:539-555 —— 数组项必须函数；每项 catch → `null`，`isFatalWorkflowError` 则 rethrow；`Promise.all` 是 barrier。
- `pipeline(items, ...stages)`：WK:557-576 —— 每个 item 独立串 stages（`(prev, item, index)`），无跨 stage barrier；stage 普通抛错 → 该 item 变 `null`。
- `phase(title)`：WK:581-586（记录 currentPhase + 通知 observer）；`log(message)`：WK:588-592。
- 并发槽：FIFO `acquireSlot/releaseSlot`（WK:383-401），上限 `limits.maxConcurrentAgents`。
- 错误纪律：所有 hook 误用抛 `WorkflowError`（fatal）杀死脚本；只有子失败与 stage 内普通错误降级为 per-item `null`（`dsh-workflow/lib/types/index.d.ts:81` 列出全部 `WorkflowErrorCode`：`SCRIPT_PARSE | META_INVALID | INVALID_ARGUMENT | UNSUPPORTED_OPTION | UNSUPPORTED_SCHEMA | AGENT_CAP | ITEM_CAP | AGENT_START | AGENT_RESULT | RESULT_UNSERIALIZABLE | CANCELLED`）。
- 返回值须为 plain JSON（`materializeFromRealm`，WW:73-130；违规 → `RESULT_UNSERIALIZABLE`，WK:352-358）。

### 4.4 meta / script 参数校验

- `meta` 是工具参数里的 JSON object（TW:152-200）：必填 `name`、`description`；可选 `whenToUse`、`phases[{title(必填), detail?, provider?, model?}]`。
- 引擎 `validateMeta`（WW:792-796）：未知字段逐个报错（`meta.${key} is not a recognized field (name/description/whenToUse/phases)`，WW:743）；不合规抛 `WorkflowError(META_INVALID)`；返回 normalized 拷贝。
- script：以正则 `/^\s*export\s+const\s+meta\b/` 拒绝带 meta 语句的 body（WW:807, 817，`SCRIPT_PARSE`）；再用与 worker 相同 wrapper 做宿主侧 parse 检查（WW:816-826）。
- `subagentProvider` 覆盖需非空、无首尾空白、已注册（WW:828-833）；`maxTotalAgents` 请求值必须是正 safe integer 且 ≤ 引擎上限（WW:835-840）。

### 4.5 并发/上限常量（引擎 `static Config`，WW:849-856）

| 配置 | 默认 | 说明 |
|---|---|---|
| `provider` | `"spawn"` | 子运行所在 subagent provider |
| `maxConcurrentAgents` | `0` | 0 ⇒ 自动 `min(16, max(1, availableParallelism() - 2))`（WW:883） |
| `maxTotalAgents` | `1000` | 单 run agent() 总量上限（runaway backstop） |
| `maxItemsPerCall` | `4096` | 单次 `parallel()`/`pipeline()` 条目上限（`ITEM_CAP`，WK:577-579） |
| `syncTimeoutMs` | `5000` | vm 同步段超时 |
| `disposeGraceMs` | `5000` | 取消后宽限，超时强制 settle `cancelled` 并 `worker.terminate()`（WW:346-358） |

工具层上限：`maxResultChars` 默认 50000 字符，渲染结果 JSON 超长截断（TW:23, 130-134）。

### 4.6 host⇄worker 协议与工具侧记录

- 协议 tag（WW:142-178 / `dsh-workflow-worker-thread/lib/types/protocol.d.ts`）：worker→host `ready|phase|log|agent-start|agent-end|child-start|child-dispose|result`；host→worker `go|cancel|child-started|child-start-error|child-settled|child-failed|child-disposed`。
- 结算三态来源互斥认领：首个 worker `result`、worker 死亡/exit、grace 超时（WW:623-658, 706-715）；未配对 agent 会被合成 `outcome:'cancelled'` 的 agent-end（WW:671-688）。
- 工具层向父 Session 写持久事件 `tool-workflow/run-start|agent-start|agent-end|run-end`（TW:49-88；类型 `dsh-tool-workflow/lib/types/types.d.ts:9-56`），仅顶层调用（`exec.parent === undefined`）记录（TW:241）。
- 工具输出：`{ runId, agentsStarted, result }`；非 completed 转工具错误，模板 ``workflow run was cancelled(...)`` / ``workflow run failed: ${error}``（TW:119-128）；成功渲染 `workflow "${name}" completed (${n} agent(s)).\nReturn value:\n${clipped}`（TW:130-134）。

---

## 5. Ralph：轮次机制与 worker 报告回传

### 5.1 结构

`dsh-tool-ralph` 是**部署方持有的固定 workflow 脚本**上的前台循环：模型只提供 `objective`（必填）与可选 `maxRounds`（RL:303-313）；循环体、schema、provider 路由均不可被模型改写（RL:32-35 注释）。

- 插件配置（RL:18-23）：`subagentProvider` 默认 `"spawn"`；`maxRounds` 默认 256；`maxHandoffChars` 默认 16384；`maxResultChars` 默认 16384。
- `requireFreshProvider`：provider 必须已注册、支持 `outputSchema`、且 `inheritsParentContext === false`（RL:150-156）。
- 启动：`ctx.workflowEngine.start({ script: RALPH_SCRIPT, meta: RALPH_META, args: { objective, maxRounds, maxHandoffChars }, subagentProvider, maxTotalAgents: maxRounds, parent, signal })`（RL:332-344）——即每轮一个子，总子数被 `maxRounds` 封顶；meta 固定 `name:"ralph-loop"`，phase 固定 `"Fresh-agent rounds"`（RL:24-31）。

### 5.2 轮次机制（固定脚本 RALPH_SCRIPT，RL:36-123）

- `for (let round = 1; round <= args.maxRounds; round += 1)`（RL:99）：每轮 `await agent(prompt, { label: 'Ralph round ' + round, phase: 'Fresh-agent rounds', schema: reportSchema })`（RL:109-113）——新子无父会话、无前一轮子会话；跨轮只传「有界结构化 handoff」（`previous` = 上一份 report，RL:100, 120）。
- `reportSchema`（RL:37-48）：`{ status: 'continue'|'complete'|'blocked', summary: string, evidence: string[], nextSteps: string[], blocker: string }`，required 全字段、`additionalProperties: false`。
- 脚本内 `validateReport`（RL:58-95）继续做语义校验：continue 需 nextSteps 且 blocker 为空；complete 需 evidence、无 nextSteps、blocker 空；blocked 需非空 blocker；序列化长度 ≤ `args.maxHandoffChars`。
- 退出：worker 报 `complete` → 返回 `{status:'complete', roundsStarted, report}`；`blocked` 同理；`agent()` 返回 null（子失败）→ `{status:'round-failed', roundsStarted, lastReport}`（RL:114-116）；轮数耗尽 → `{status:'budget-limited', roundsStarted, report}`（RL:122）。
- 每轮 prompt 关键帧（RL:102-108）：`'You are one fresh worker in a foreground Ralph loop...'`、`'Immutable objective:\n' + args.objective`、`'Ralph round: ' + round + ' of ' + args.maxRounds + '.'`、workspace 即长期记忆、`'Previous structured handoff:\n' + prior`。

### 5.3 worker 报告如何回传

报告 = workflow 的 return value，经 worker→host `result` 消息回传（WK:772）→ 工具侧 `readRunResult`/`readReport` 防御式二次解码（RL:166-227；键集合逐字节比对 `"blocker,evidence,nextSteps,status,summary"`，RL:168）→ `round-failed` 抛工具错误（RL:355），其余作为工具输出 `{ runId, agentsStarted, result }`。

父可见渲染模板（RL:246-261）：

- `` `Ralph worker reported completion after ${rounds}.\nFinal report:\n${JSON.stringify(report, null, 2)}` ``
- `` `Ralph worker reported a blocker after ${rounds}.\nFinal report:\n...` ``
- `` `Ralph reached its ${rounds} limit; the worker reported work remaining.\nFinal report:\n...` ``
- 轮失败：`` `Ralph round ${n} child failed before producing a structured report.` `` + `No previous handoff was available.` 或 `Last successful handoff:\n${...}`（RL:278-281）。
- 总长度经 `boundResult` 截断，尾标 `"\n… [truncated]"`（RL:238-244）。

---

## 6. 各包 .d.ts 类型逐字段清单

### 6.1 子代理状态/模式枚举汇总

- 子模式：`'one-shot' | 'continuable'`（`dsh-subagent/lib/types/descriptor.d.ts:49-81`）。
- 列表活动态：`activity: 'running' | 'inactive'`（`control-types.d.ts:42`）；诊断原因：`'corrupt' | 'unsupported' | 'unavailable'`（`control-types.d.ts:69`）。
- `list_agents` 状态：`'running' | 'idle' | 'ready'`（LA:86）。
- 浏览器投递：`delivery: 'queue' | 'steer'`（`control-types.d.ts:94`）。
- 停止原因 `SubagentStopReason = 'completed' | 'aborted' | 'error' | 'max-tokens' | 'refusal'`（merge-extensible map；`types.d.ts:239-252`）。
- workflow：`WorkflowStopReason = 'completed' | 'cancelled' | 'error'`；`WorkflowAgentOutcome = 'completed' | 'failed' | 'cancelled'`（`dsh-workflow/lib/types/types.d.ts:55, 98`）。

### 6.2 dsh-subagent（核心，逐字段）

`types.d.ts`：
- `ContinuableStartSpec`（:26-44）：`provider, label, childId?, request(Omit<SubagentStartRequest,'label'|'signal'|'outputSchema'>), signal`。
- `ContinuableStart`（:46-51）：`childId, messageId`。
- `SubagentInterruptAuthority`（:57-63）：`{kind:'user', parentSessionId} | {kind:'ancestor', agent}`。
- `SubagentRunInfo`（:74-88）：`runId, provider, id, local`；`SubagentRunEndInfo`（:93-110）：+ `stopReason, lastAssistantMessage?`。
- `SubagentCapabilities`（:122-128）：`agentOptions, outputSchema, depthLimit, toolFilter, persona`（全 boolean）。
- `SubagentStartRequest`（:136-192）：`label?, prompt: ContentBlock[], parent, signal, agentOptions?, outputSchema?, maxDepth?, toolFilter?, persona?`。
- `SubagentResult`（:256-282）：`output, structured?, diagnostic?, stopReason`；`SubagentRun`（:292-318）：`id, localAgent, result, dispose()`。
- `SubagentProvider`（:327-376）：`name, capabilities, inheritsParentContext, agentRouteDefaults?, start(), prepareContinuable?()`。

`control-types.d.ts`：`SubagentListEntry`（:30-70）= child 分支（`kind/id/activity/hasChildren` + mode 判别 `{mode:'one-shot',label?}` / `{mode:'continuable',label}`）或 diagnostic 分支（`kind/id/reason`）；`SubagentCatalog {entries, parentAvailable}`（:72-75）；`SubagentPromptRequest`（:86-103）：`requestId, parentSessionId, childSessionId, mode:'continuable', delivery, content, clientTimeZone?`；`SubagentPromptReceipt {messageId}`（:105-107）；`SubagentInterruptReceipt {accepted:true}`（:109-111）；RemoteError details（:116-145）。

`continuation-messages.d.ts`：`AgentMessageSource {kind:'agent-message', form:'relay', senderSessionId}`（:12-18）；`SubagentSettledMessageSource {kind:'subagent-settled', form:'notice', summary, senderSessionId}`（:26-34）。

`continuation-activation.d.ts`：`Activation`（:26-64）：`childId, parentSession, provider, handle, inbox, ancestry(WeakSet), ownedChildren(Set), observer, announced, poke`；`MaterializeInputs`（:66-91）：`childId, provider, parent, create?{seed,meta,inheritedEventCount,delegatedPolicies,descriptor}, agentOptions, composition{persona?,toolFilter?}, signal`。

`lifecycle.d.ts`：`ActivationTerminal {stopReason, output?}`（:25-30）；`ActivationObserver {start,capture,terminal,settle}`（:37-67）。

`descriptor.d.ts`：`SUBAGENT_DESCRIPTOR_VERSION = 3`（:44）；one-shot 字段 `version/mode/provider/label?`；continuable 增 `label/agentProvider?/agentModel?/agentReasoningEffort?/persona?/toolFilter?`（:46-81）。

`projection-types.d.ts`：`SubagentCatalogEntry`（:8-17）、`SubagentTimingProjection {settledMs, active?{since,through}}`（:19-29）、`SubagentIdentityProjection`（mode/label/seq，:36-55）。

`inbox.d.ts`：`SubagentDelivery = 'queue'|'steer'`（:10）；`SubagentInbox`：`closing`, `hasPending`, `deliver`, `close`（:12-42）。

`error.d.ts`：`SubagentError extends HarnessError(message, code, options?)`。代码内出现的 code（从 JS 提取）：`CANCELLED, DUPLICATE_CHILD, DUPLICATE_PROVIDER, NO_PROVIDER, UNSUPPORTED_CAPABILITY, PERSISTENCE_UNAVAILABLE, CONTINUATION_UNAVAILABLE, SUBAGENT_CONTROL_PROJECTIONS_UNAVAILABLE, SUBAGENT_CONTROL_SESSION_STORE_UNAVAILABLE, SUBAGENT_CONTROL_QUERY_UNAVAILABLE, MODEL_DOES_NOT_SUPPORT_IMAGES, NOT_RESUMABLE, UNAUTHORIZED, DRAINING, ACTIVATION_CLOSING, PARENT_UNAVAILABLE, ACTIVATION_TEARDOWN_FAILED`。

`depth.d.ts`：`AgentOptions.subagentDepth?: number` 模块增强（:9-14）。

### 6.3 其余包 d.ts（要点）

- `dsh-tool-subagent-control/lib/types/index.d.ts` / `list-agents.d.ts`：仅插件元数据（`name/inject/apply`），schema 在 JS 中（见 §2）。
- `dsh-subagent-spawn-in-process`/`-fork-in-process`：`Config { providerName }`（默认 `spawn`/`fork`）；provider capabilities 全 true（SP:23-29 / FK:37-43），`inheritsParentContext` = false / true（SP:30 / FK:44）。
- `dsh-subagent-in-process-driver`：`InProcessRunOptions {seed?}`；`STRUCTURED_OUTPUT_TOOL = "structured_output"`（`structured.d.ts:15`），指令文本 `STRUCTURED_OUTPUT_INSTRUCTION`（DRV:27）。
- `dsh-workflow/lib/types/types.d.ts`：`WorkflowMeta {name, description, whenToUse?, phases?}`（:39-48）；`WorkflowPhase {title, detail?, provider?, model?}`（:22-31）；`WorkflowResult {value, stopReason, error?, agentsStarted}`（:63-78）；`WorkflowAgentInfo {seq,label,phase?,childId}`（:87-96）。
- `dsh-workflow/lib/types/runtime-types.d.ts`：`WorkflowStartRequest {script, meta, args?, subagentProvider?, maxTotalAgents?, parent, signal?}`（:15-30）；`WorkflowRun {id, meta, result, cancel, dispose}`（:35-44）。
- `dsh-workflow-worker-thread/lib/types/index.d.ts`：`Config` 六项（:16-33，见 §4.5）。
- `dsh-workflow-worker-thread/lib/types/types.d.ts`：`WorkerLimits`（:14-23）、`WorkerInit {meta, body, args?, limits}`（:25-34）、`ChildStartRequest {prompt, schema?, provider?, model?}`（:36-45）、`ChildResult {output, structured?, stopReason:string}`（:51-58）、`ChildHandle/ChildPort`（:63-87）。
- `dsh-tool-workflow/lib/types/index.d.ts`：`Config {toolName?, maxResultChars?}`（:17-22）。
- `dsh-tool-ralph/lib/types/index.d.ts`：`Config {subagentProvider?, maxRounds?, maxHandoffChars?, maxResultChars?}`（:12-21）。

---

## 7. 「未找到」项

- `"agent-"` 前缀的 agent id：**未找到**（child id 为裸 UUID，见 §1.4；仅后台 one-shot 的 job id 为 `subagent-N`）。
- workflow hook `agent()` 的 `effort/isolation/agentType` 选项：显式 **deferred、未实现**（WK:224-228, 512）。
- `dsh-subagent` 对 `subagent/descriptor` 之外的子代理事件类型：未定义更多 Session 事件类型（仅 `subagent/descriptor`、`subagent/catalog`）。

---

# §10 bash 工具与沙箱机制


## 1. bash 工具输入 schema 与输出格式

### 1.1 插件与注入

工具插件 `tool-bash` 注入 `["tools","shell","systemPrompt","shellEnv"]`（`dsh-tool-bash/lib/index.js:110-116`）。运行时配置仅一个字段：`enableRunInBackground: boolean`，默认 `true`（`dsh-tool-bash/lib/index.js:118`）。

### 1.2 参数 schema（逐字段）

注册于 `defineTool({ name: "bash", parameters: {...} })`，`dsh-tool-bash/lib/index.js:259-296`：

| 字段 | 类型 | 必填 | 描述（schema 原文摘要） | 位置 |
|---|---|---|---|---|
| `command` | string | ✔ | "The bash command to execute." | `:263-267` |
| `description` | string | ✔ | "Clear, concise description of what this command does in active voice, 5-10 words (shown in the UI)." | `:268-272` |
| `timeoutMs` | number | ✘ | "Timeout in milliseconds. The executor applies its configured default and cap, and kills the command on expiry." | `:273-276` |
| `workdir` | string | ✘ | "Working directory for this command. Defaults to the session workspace; a relative path is resolved against it." | `:277-280` |
| `run_in_background` | boolean | ✘ | 仅当 `enableRunInBackground` 为 true 才出现在 schema："Run in the background and return a job id immediately (collect with job_output, stop with job_kill). No timeout applies." | `:281-284` |
| `sandbox_permissions` | string enum | ✘ | 仅当组合挂载了 confinement executor（`ESCALATION_TARGETS` 非空）才出现；enum 为 `["workspace-write","danger-full-access"]`："The wider sandbox mode this command needs. Only valid as a one-shot retry of a command the sandbox just denied; requires justification and user approval." | `:285-290` |
| `justification` | string | ✘ | 与 `sandbox_permissions` 成对出现："Required with sandbox_permissions: one sentence for the user explaining why this exact command needs the wider access." | `:291-294` |

参数校验 `validateBashArgs`（`dsh-tool-bash/lib/index.js:119-124`）：
- `command`/`description` 非空，否则 `invalid command: expected a non-empty string` / `invalid description: ...`；
- `timeoutMs` 若给出必须为正有限数；
- 升级参数配对校验委托 `validateEscalationArgs`（见 §6）。

执行流程（`execute`，`dsh-tool-bash/lib/index.js:386-442`）：先解析 standing policy（`ctx.sandboxPolicy.resolve`），若带 `sandbox_permissions`+`justification` 则先走审批（`approveBashEscalation`，`:238-253`）拿到获批 mode 并只 stamp 到本次调用（`:389-393`）；`workdir` 相对路径按 session workspace 解析（`resolveWorkdir`，`:177-183`）；然后 `ctx.shell.run`（前台）或 `ctx.shell.start` + `ctx.jobs.start`（后台，`:403-428`）。

### 1.3 前台输出渲染：stdout/stderr 合流、截断、exit 标记

渲染函数 `renderResult`（`dsh-tool-bash/lib/index.js:54-74`）：

1. stdout 文本在前；
2. stderr 非空时合流为独立标记段——在 body 末尾补换行后追加 `[stderr]\n<err文本>`（`:58-61`）。即 stdout/stderr 不合排，stderr 有显式 `[stderr]` 段头；
3. 全空则 body 为 `(no output)`（`:62`）；
4. 依次追加 markers（各占一行）：
   - 沙箱拒绝：`sandboxDenialMarker(mode)`，随后若组合支持升级再追加 `escalationHintMarker("command")`（`:64-67`）；
   - 超时：`[timed out after ${result.timeoutMs}ms]`（`:68`）；
   - 信号：`[killed by signal: ${result.signal}]`（`:69`）；
   - 否则非零退出：`` `[exit code: ${result.exitCode}]` ``（`:70`）——**模板原文即 `[exit code: N]`**。

非零退出不算 isError，仅基础设施失败（spawn 错误、abort）才是错误结果（`:43-47` 注释；`:433-437` abort 抛 `TOOL_ABORTED`）。

UI 呈现端用 `parseExitStatus` 反向解析标记：正则 `/\n\[killed by signal: ([^\]\n]+)\]$/` 与 `/\n\[exit code: (\d+)\]$/`（`dsh-shell/lib/index.js:31-44`），把 exit/signal 从正文剥离成 terminal pill；超时/沙箱标记保留在正文。

### 1.4 截断策略

每个 executor 配置（`LocalBashExecutor.Config`，`dsh-bash-local/lib/index.js:128-135`）：
- `timeoutMs` 默认 120000ms；`maxTimeoutMs` 默认 600000ms（请求值经 `clampTimeout` 夹紧，`:164-165`）；
- `maxOutputBytes` 默认 64000 字节（stdout 用请求值或该默认；stderr 固定用该默认，`spawnSpec` `:181-202`）；
- `maxSpillBytes` 默认 64MiB（`:89`）；`graceMs` 默认 3000ms 的 SIGTERM→SIGKILL 宽限（`:87,194`）。

收集器 `OutputCollector`（`dsh-subprocess-local/lib/runner-launch-COYGu0Dl.js:702-780`）：**tail-keep**——内存只保留尾部，超限从头部丢整块或截头（`:744-756`）；首次溢出时懒创建 spill 文件并把此前所有 chunk 一并写入（`:758-770`），文件名 `dsh-subprocess-<pid>-<n>-<hex>-<label>.log`，位于 `os.tmpdir()` 下私有 0700 目录 `dsh-subprocess-*`（`mkdtempSync`，`:675-686`）；总字节超过 spill 上限则删除 spill 文件不再承诺完整（`discardSpill` `:773-786`）。
截断提示模板（模型可见）：`[output truncated; full output: ${spillPath ?? "(unavailable)"}]`（`dsh-tool-bash/lib/index.js:39-42`）。

后台（`job_output` 增量读）：`readOutput` 的 delta 为 stdout 段 + `[stderr]\n...` 段（`dsh-bash-local/lib/index.js:302-317`）；有丢失时追加 `[some output was dropped from memory; full output: <stdout spill, stderr spill>]`（`dsh-tool-bash/lib/index.js:85-90`）。

### 1.5 结构化输出 schema

`output.schema` 是 `oneOf`（`dsh-tool-bash/lib/index.js:297-380`）：
- 背景分支 `{kind:"background", jobId}`（`:208-218`），文本渲染 `started background job <jobId>`（`:381-384`）；
- 前台分支 `{kind:"foreground", exitCode, signal, timedOut, aborted, timeoutMs, stdout:{text,truncated,spillPath?}, stderr:{...}, sandbox?:{mode,denied,enforcement?,runnerFailed?}}`（`:303-379`）。

### 1.6 persistent bash 变体（dsh-tool-bash-persistent）

不同工具，走 owner 级 PTY seam（`ctx.terminals`）。参数只有 `command`（`dsh-tool-bash-persistent/lib/index.js:332-336`）；无沙箱字段（不经 ctx.shell/sandbox）。输出为纯字符串：以 nonce 标记 `__DSH_PERSISTENT_BASH_START_<uuid>__` / `__DSH_PERSISTENT_BASH_END_<uuid>:` 包裹 `eval` 截取输出（`:80-92`）；exit 标记为 `[Command finished with exit code N]`（`:156`），超时 `[Command timed out or OOM]`（`:71`），shell 退出 `[shell killed by signal: X]` / `[shell exited: code N]`（`:162-164`）。截断用 `<response clipped>` 前缀消息与 `maxOutputChars`（默认 16000，`:68-79,365`）。

---

## 2. 沙箱模式枚举、默认值与升级

### 2.1 真实枚举取值

```ts
export type SandboxMode = 'read-only' | 'workspace-write' | 'danger-full-access';
```
（`dsh-sandbox/lib/types/index.d.ts:19`）。运行时数组 `SANDBOX_MODES = ["read-only","workspace-write","danger-full-access"]`（`dsh-sandbox-policy/lib/index.js:27-31`）。语义：`read-only` 只允许 `/dev/null` 等必需 sink；`workspace-write` 加 workspace + temp；`danger-full-access` 完全绕过 confinement（`dsh-sandbox/lib/types/index.d.ts:13-19`；`dsh-bash-sandbox/lib/index.js:147-153` 直接透传不 wrap）。

### 2.2 默认值与按会话覆盖

- 部署默认：`SandboxPolicyService.Config.mode` 的 schema 默认是 **`"read-only"`**（`dsh-sandbox-policy/lib/index.js:97-104`）；本会话快照显示实际部署以 `danger-full-access` 运行（由配置注入覆盖）。
- 会话覆盖：追加一条 `sandbox/mode` 事件到 session log（`setSandboxMode`，`dsh-sandbox-policy/lib/index.js:41-43`；事件定义 `dsh-sandbox-policy/lib/types/session-mode.d.ts:29-35`），经 `sessionProjections` 折叠（`index.js:114-120`）。
- 优先级：`resolve()` = 显式获批 mode > session 覆盖 > 部署默认；`workspaceRoot` = `session.header.cwd`（canonical 化）否则配置根（`dsh-sandbox-policy/lib/index.js:141-148`）。
- 策略文本注入 system prompt 的 runtime-context：`renderPolicyContext`（`dsh-sandbox-policy/lib/index.js:72-83`）。

### 2.3 升级阶梯（按命令）

`WIDER_MODES`（严格更宽表，执行期检查）：

```js
{ "read-only": ["workspace-write","danger-full-access"],
  "workspace-write": ["danger-full-access"] }
```
（`dsh-sandbox/lib/index.js:30-33`）。schema enum 为闭合的 `ESCALATION_TARGETS = ["workspace-write","danger-full-access"]`（`:42`），advertise 条件 = 挂载的 executor 有 `sandboxMode`（`dsh-tool-bash/lib/index.js:221-222`）。升级只在执行时验证严格变宽，不匹配直接抛 `sandbox escalation to "..." is not strictly wider than this call's current "..." mode`（`dsh-sandbox/lib/index.js:95`）。获批 mode 仅作用于发起的那一次调用（`dsh-sandbox/lib/types/escalation.d.ts:120-133` 注释；`dsh-tool-bash/lib/index.js:389-393`）。

---

## 3. Linux 隔离机制（及 mac/Windows 简述）

**结论：Linux 是真实内核级隔离，双 runner 链：首选 bubblewrap（bwrap），回退自研 Landlock launcher。不是纯 JS 路径检查，也不使用 seccomp/chroot/pivot_root。**

### 3.1 provider 与 runner 链选择

`LocalSandboxProvider`（注册为 `ctx.sandbox`）的链表：

```js
const PLATFORM_CHAINS = { linux: ["bwrap","landlock"], darwin: ["seatbelt"], win32: ["windows-acl"] };
```
（`dsh-sandbox-local/lib/index.js:172-176`）。Linux 两个候选按序功能探测（各探测一次并缓存）：`defaultProbeBwrap` 实际执行 `bwrap <read-only profile> -- true` 看 exit 0（`:99-112`）；landlock 走 `probe()`（`:504`）。全不可用 → `chainVerdict()="unavailable"` → 抛 `SandboxUnavailableError` fail-closed（`:477-499`；错误文案要求 "Install bubblewrap or run a Landlock-enforcing kernel (Linux)..."，`dsh-sandbox/lib/index.js:183-188`）。操作员可用 `runnerCommand` 配置直接断言外部 runner，跳过探测（`dsh-sandbox-local/lib/index.js:298-309`）。

### 3.2 bwrap profile（Linux 首选）

`bwrapProfileArgs`（`dsh-sandbox-local/lib/index.js:22-39`）：

```
bwrap --ro-bind / / --dev /dev --unshare-pid --proc /proc --die-with-parent
      [--tmpfs /tmp --bind <workspaceRoot> <workspaceRoot>]   # 仅 workspace-write
```
即：根只读 bind、独立 PID namespace（`--unshare-pid`）、新 /proc、tmpfs 覆盖 /tmp、workspace bind 为可写。**没有** `--unshare-user/net`、`--pivot-root` 等更多项。拒绝方言 = stderr 含 `read-only file system`（EROFS，`DENIAL_SIGNATURES.bwrap`，`:206`）；runner 失败签名 `bwrap: `（`:230`）。

### 3.3 Landlock launcher（Linux 回退，真实 Landlock syscall）

`landlockProfileArgs`（`dsh-sandbox-local/lib/index.js:45-52`）生成 `--ro / --rw /dev/null [--rw /tmp --rw <workspaceRoot>]` 授权，交给原生 launcher 二进制 `landlock-run`（`grantArgs`：`node-addon-system/lib/index.js:68-73`）。launcher 路径由 `launcherPath()` 从平台包 `@deepseek-ai/node-addon-system-linux-{x64,arm64}` 的 `bin/landlock-run` 解析（`node-addon-system/lib/index.js:45-56`）；仓库内实际文件：`node-addon-system-linux-x64/bin/landlock-run`（`prebuilds.json` 标注 `static-musl`，同目录另有 `bin/glibc/system.node`、`bin/musl/system.node` 是 **flock** Node-API addon 的加载点，`node-addon-system/lib/flock.js:16-24`，与 Landlock 无关——Landlock 走独立静态二进制而非 .node）。

launcher C 源码 `node-addon-system/src/main.c`（298 行，自述 "self-restrict-then-exec Landlock launcher"）：
- 直接 raw syscall：`__NR_landlock_create_ruleset 444 / __NR_landlock_add_rule 445 / __NR_landlock_restrict_self 446`（`:101-104`），UAPI 结构体本地定义（`:58-`）；
- `prctl(PR_SET_NO_NEW_PRIVS,1,...)`（`:254`）；
- 失败即非零退出不 exec（fail-closed）；旧 ABI 上报 `partial enforcement`；`--probe` 构建最大 ruleset 验证内核真实强制（`:1-35` 头注释；JS 侧探测解析 `/partially enforced/` → `partial`，`node-addon-system/lib/index.js:89-97`）。
- launcher 级失败退出码 125（`LAUNCHER_FAILURE_EXIT`，`node-addon-system/lib/index.js:29`），签名 `landlock-run: `（`dsh-sandbox-local/lib/index.js:229-235`）。

**未找到 seccomp / chroot / pivot_root 的任何实现**（对 BASE 全量 grep `seccomp|pivot_root|chroot` 仅命中 web-frontend 语法高亮资源）。Landlock 不需要它们；bwrap 用 mount namespace + `--unshare-pid`。（另注：`dsh-subprocess-local` 的 linux-scope 用 systemd-run scope 做**进程树管理/回收**，与文件沙箱无关，`dsh-subprocess-local/lib/types/linux-scope.d.ts:1-53`。）

### 3.4 封装与拒绝分类

执行侧 `SandboxBashExecutor extends LocalBashExecutor`（注册为 `ctx.shell`）：`confine()` 把 `["bash","-c",command]` 交给 `ctx.sandbox.confine`（`dsh-bash-sandbox/lib/index.js:229-235`）；run 后按 provider 返回的 `denialSignatures` 把非零退出+stderr 匹配分类为 `denied`（`classifyDenial` `:53-55,172`），按 `runnerFailureRules` 分类 runner 失败并抛 `SANDBOX_UNAVAILABLE`（`:166-167`）。

### 3.5 macOS / Windows（略）

- macOS：`sandbox-exec -p <SBPL>`（Seatbelt），profile `(allow default)(deny file-write*)` + 允许 `/dev/null` 与 writableRoots 的 subpath（`dsh-sandbox-local/lib/index.js:65-75`）；拒绝方言 `operation not permitted`。
- Windows：`dsh-sandbox-windows-acl` 受限令牌 runner + NTFS ACL（workspace 常驻 write SID、每 session 随机私有 temp SID），enforcement=`partial`（`dsh-sandbox-local/lib/index.js:78-97,186-191,344-369`）。

---

## 4. `[sandbox: file access denied under <mode> mode]` 的产生点

**唯一产生函数**（bash 与 fs 两个工具族共用）：

```js
function sandboxDenialMarker(mode) {
	return `[sandbox: file access denied under ${mode} mode]`;
}
```
— `dsh-sandbox/lib/index.js:64-66`（`sandboxDenialMarker`）。

调用点：
- bash 前台渲染：`dsh-tool-bash/lib/index.js:65`（`renderResult` 内 `result.sandbox.denied` 为真时 push）；后台读：`:93`。
- fs 工具错误映射：`dsh-tool-fs/lib/index.js:1228`（`mapError` 把 `FS_SANDBOX_DENIED` 的文本替换为 `` `${sandboxDenialMarker(mode)}\n${escalationHintMarker("operation")}` ``，保留 code）；`dsh-tool-str-replace-editor/lib/index.js:64` 同理只带 marker。
- fs 底层抛出的原始 FsError 文案（不含标记）：`dsh-fs-sandbox/lib/index.js:157` `cannot write "<path>": file access denied under read-only mode` 与 `:164` `...denied under workspace-write mode`，code=`FS_SANDBOX_DENIED`。
- 工具描述中教育模型该标记的原文在 `dsh-tool-bash/lib/index.js:127`；pwsh 同款 `dsh-tool-pwsh/lib/index.js:142`。

配套标记：
- 升级提示（`dsh-sandbox/lib/index.js:76-78`）：`[sandbox: escalation available — retry this exact ${subject} once with sandbox_permissions (the narrowest wider mode that suffices) + justification; the approval prompt asks the user]`；
- runner 自身失败（区别于拒绝）：`[sandbox: the sandbox runner itself failed under ${mode} mode — the command did not run; this is a sandbox problem, not a command failure]`（`dsh-tool-bash/lib/index.js:91`）。

注意：该标记**不是**内核或 bwrap 输出，而是 harness 在命令结算后按各 runner 的 `DENIAL_SIGNATURES`（bwrap=`read-only file system`、landlock=`permission denied`、seatbelt=`operation not permitted`、windows-acl=`access is denied` 等，`dsh-sandbox-local/lib/index.js:205-215`）对 stderr 做大小写不敏感子串匹配后合成的统一词汇（`dsh-bash-sandbox/lib/index.js:87-91,172`）。

---

## 5. workspace-write 的允许写路径与判定算法

### 5.1 允许列表

`writableRoots(policy)`（`dsh-sandbox/lib/index.js:155-162`）：`read-only` → `[]`；`workspace-write` → 去重后的 **canonical( [policy.workspaceRoot, "/tmp", os.tmpdir()] )**（注释明言 /tmp 与 per-user temp 都必须给，`dsh-sandbox/lib/index.js:146-154`）。Linux bwrap 方言用 `--tmpfs /tmp` + `--bind workspaceRoot` 表达等价授权（launcher 方言 `--rw /tmp --rw <root>`）；注释说明各方言保留自己的 grant 拼法、以测试钉住 parity（`dsh-sandbox/lib/index.js:115-127`）。

### 5.2 denied path 列表

**未找到**任何 denied/excluded path 黑名单：对 `dsh-sandbox*/`、`dsh-fs-sandbox/`、`dsh-tool-fs/`、`dsh-bash-sandbox/` 及 `dsh-fs*/` grep `denied path|denyPath|\.git|\.env` 等，不存在针对 `.git`、`.env` 的排除逻辑（workspace 内的 `.git`/`.env` 在 workspace-write 下**可写**）。模式判定纯粹是「allow-list containment」，没有例外项。

### 5.3 判定算法（canonicalize-then-contain）

- **canonical 化**：`canonicalPath()` = `fs.realpathSync.native(path)`，失败回退原拼写（`dsh-sandbox/lib/index.js:139-145`；注释：Seatbelt filter 与 fs fence 都比对 resolved path，darwin 上 `/tmp` IS `/private/tmp`）。
- **fs 工具 fence**（JS 层，`SandboxedFileSystem.checkedTarget`，`dsh-fs-sandbox/lib/index.js:153-166`）：`danger-full-access` 直放；`read-only` 抛 `FS_SANDBOX_DENIED`；`workspace-write` **写前即时重新 canonicalize** 目标（`this.resolve`），再对每个 writable root 做 `isPathUnder`。
- **isPathUnder**（`dsh-fs-sandbox/lib/index.js:53-65`）：先 lexical 前缀匹配（root+`sep` 前缀、win32 大小写不敏感，`:21-27`）；不等则**回退文件系统身份**：stat(root) 后逐级上溯 target 祖先比对 `dev/ino`（识别 Windows 8.3 别名/casing，`:38-64`）。
- 角色定位：该 fence 是「trusted code 对 model 可控路径的 containment，不是 kernel 边界」，残余 TOCTOU 已声明接受；内核级隔离是 bash sandbox 的职责（`dsh-fs-sandbox/lib/index.js:76-85` 注释原文）。
- bash 侧无 JS 判定——授权直接由内核 runner（bwrap mounts / Landlock ruleset）执行。
- workdir 与 policy root 用同一 canonical 身份：`resolveWorkdir` 优先取 `policyWorkspaceRoot`（`dsh-tool-bash/lib/index.js:171-183`）。

---

## 6. approval 流程（sandbox_permissions → 用户审批）

时序（bash；fs 同构）：

1. **入口**：`execute` 见 `args.sandbox_permissions && args.justification` → `approveBashEscalation`（`dsh-tool-bash/lib/index.js:389`）。组合守卫：无 sandboxing executor 时字段未 advertise，仍到达 execute 则抛 `sandbox_permissions is not available in this composition ...`（`:238-239`）。
2. **参数配对校验** `validateEscalationArgs`（`dsh-sandbox/lib/index.js:51-55`）：两字段必须同现，justification 非空；错误原文 `invalid escalation: sandbox_permissions requires a justification` 等。
3. **共享编排 `approveEscalation`**（`dsh-sandbox/lib/index.js:93-112`，先执行后审批是禁止的——一切执行前完成）：
   a. 严格变宽检查（`WIDER_MODES`，非宽直接抛，不提示人类）；
   b. 无 approval 服务 / 无 agent → 抛（fail-closed）；
   c. `approval.approver.request({agent, toolName:"bash", callId, reason: "escalate sandbox to ${mode}: ${justification}", signal})`；
   d. outcome 映射：`allowed-once`→返回获批 mode；`rejected`→抛 `the user rejected escalating this ${subject} to "${mode}"`；`cancelled`→抛 `approval for escalating to "${mode}" was cancelled`；`unavailable`→抛 `...no approval channel is available`。
4. **审批服务 `ctx.approval`**（`dsh-user-approval/lib/index.js`）：`request()` 要求在 open turn 内（`:131-133`）；向 session log 追加审计对 `approval/asked`（含 id/toolName/callId/reason，`:135-140`）→ `decide()` → `approval/decided`（`:142-145`）。`decide`（`:175-192`）：signal aborted→`cancelled`；会话有效 policy 为 `never`→**直接 `rejected`（fail-closed，不弹窗）**；否则派发 cordis waterfall **action `"approval/request"`**（`:179`），闭集 outcomes=`allowed-once|rejected|cancelled|unavailable`（`:30-35`），policy 闭集 `ask|never`（`:37`）；`never` 的模型文案 `NEVER_SENTENCE`（`:39`）。
5. **传输到 UI**：`approval/request` 作为 **waterfall 型 Remote Event** 原名转发（`dsh-api-remotes/lib/types/remote-events.js:14`：`{ event: 'approval/request', mode: 'waterfall' }`）；client 侧 `ctx.remote.$on("approval/request", ...)`（`dsh-client-ui-approval/lib/client.js:282`）创建 `PendingApproval` 面板（拒绝/允许一次按钮，answer 值 `"rejected"`/`"allowed-once"`，`client.js:86-93`），答案 resolve 宿主 waterfall。ACP 另有 `ctx.on("approval/request",...)` 桥（`dsh-acp/lib/index.js:1115`）。
6. **落点**：获批 mode 只 stamp 本次 `sandboxPolicy`（`dsh-tool-bash/lib/index.js:390-393`），不改变 session standing mode；改长期模式走另一条 `sandbox/mode` 事件路径（`setSandboxMode`，§2.2）。

---

## 7. 未找到项汇总

- 沙箱语义中的 seccomp / chroot / pivot_root：未找到（Linux 仅 bwrap mount-ns + Landlock；PR_SET_NO_NEW_PRIVS 存在于 landlock-run）。
- denied path 黑名单（`.git`、`.env` 等排除）：未找到，判定为纯 allow-list containment。
- bash 工具对 network / 进程可见性的沙箱约束：未找到（模式词汇明确排除，`dsh-sandbox/lib/types/index.d.ts:16-18`）。
- 独立命名的 approval "Remote 方法名"（RPC method）：未找到；审批以 **waterfall remote event `approval/request`** 承载，答复是同事件 waterfall 的返回值而非单独 RPC。
