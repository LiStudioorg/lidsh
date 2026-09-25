# LLM 抽象层与 DeepSeek Provider（第 1、2 节）

BASE = `/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`（下文相对路径均以此为前缀）。

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
