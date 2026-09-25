# 附件/上传 与 Token 计量（第 3、4 节相关部分）

BASE = `/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`。
注：另一子代理负责本主题的独立文档但未能落盘；本文件由主线补写，覆盖任务 A（附件/上传）与任务 B（token 计量）；行号均经核实。

## A. 附件/上传

### A.1 类型定义（dsh-attachment/lib/types/types.d.ts）

`ImageAttachmentRef`（:7-28）：
| 字段 | 类型 | 说明 | 行号 |
|---|---|---|---|
| `attachmentId` | `AttachmentId`（opaque string） | 非路径、非 bearer URL | :9 |
| `mediaType` | `'image/png'\|'image/jpeg'\|'image/webp'\|'image/gif'`（:5） | 由**存储字节验证**得出 | :11 |
| `bytes` | number | 精确编码长度 | :13 |
| `width`/`height` | number | 内在编码尺寸 | :15-17 |
| `name?` | string | 显示名（去除本地路径信息） | :19 |
| `originalDimensions?` | `{width,height}` | 仅当归一化缩小过才有（EXIF 定向后、缩放前） | :24-27 |

`FileAttachmentRef`（:34-41）：`{attachmentId, name, bytes}` —— 文件**逐字节存储、零归一化**，`attachmentId = sha256 摘要`（注释 :31-32）。

`ImageRequestPolicy`（:128-133）：`{maxPixels, maxBytes}`（每个模型路由的请求图像预算）。`RequestImageAttachment`（:135-152）：缓存的请求版本 `{variantId, attachment, data, mediaType, bytes, width, height, depth:'uchar', space:'srgb', hasAlpha}`。

### A.2 attachmentId 生成与存储布局（dsh-attachment-local/lib/index.js）

- **内容寻址**：`attachmentId = "sha256:" + hex(sha256(bytes))`（`ID_PATTERN /^sha256:([a-f0-9]{64})$/` :267；digest :270；拼装 :330）。图片存的是**归一化后的字节**的摘要；文件是原始字节摘要。
- 对象路径：`<root>/objects/<hex前2位>/<hex64>`（`targetFor` :289-290）；发布走 staged→immutable，重复写入按摘要去重（`publishImmutableObject` :425-429）。
- 与 DeepSeek Files API 的映射索引在 `DSH_HOME/llm-deepseek/files-v3.json`（dsh-llm-deepseek adapter.d.ts:104）。

### A.3 上传通道

1. **图片**：不走独立上传接口——直接作为 `PromptContentPart {type:'image', mediaType, data:base64, name?}` 随 `session/prompt` 提交（api-session-controller types.d.ts:64-75），Host 在建消息前 `ctx.attachments.admitPromptContent()` 提升为 durable 引用（attachment types.d.ts:84-88 注释），wire 调用方无法引用自己没传过的 id。回读用一元 RPC `session/attachment` → `{attachment: ImageAttachmentRef, data: base64}`。
2. **文件**：独立流式路由 `POST /api/session/uploadFileBinary?sessionId=<id>&name=<disp>`（dsh-client-file-upload/lib/index.js:73,19-22）：
   - 头必须 `content-type: application/octet-stream`（否则 415；:18），体 = 原始字节流（`requestBody:"streaming"` 注册，:170-175）；
   - 响应恒 HTTP 200，`{ok:true, value:{receiptId, file:FileAttachmentRef}}` 或 `{ok:false, error:{code,message,details}}`（:25-53；receiptId=randomUUID :281）；
   - receipt 按 Agent 暂存（WeakMap :163），prompt 携带 `{type:'file', receiptId}` 时 `bindPrompt` 绑定 requestId、提交成功后由 queue/history 观察回收（:240-256, :309）。
   - 大文件默认 RPC 体上限 300MiB（http-bridge DEFAULT_MAX_REQUEST_BODY_BYTES，dsh-client-connection/lib/index.js:20）。

### A.4 图片验证与下采样（"validates and downscales" 的实现）

库：**sharp**（dsh-attachment-local/lib/index.js:8）。两阶段：

1. **准入 admission**（上传时）：全解码验证（:174-186，`IMAGE_TOO_MANY_PIXELS` :186）；限额（`ImageAttachmentLimits`，types.d.ts:65-73；默认值 dsh-attachment-local:888-896）：`maxImageBytes=20MiB`（超限 `IMAGE_TOO_LARGE` :321,888）、`maxImagesPerMessage=20`（:890）、`maxMessageImageBytes=200MiB`（:892）、`maxImagePixels=64e6`（:894）、`maxImageDimension=8192`（:896）。
2. **归一化 normalization**（provider 无关，durable 形态）：单帧 8-bit sRGB/sRGBA、去 metadata；上限 `normalizedImageMaxPixels=2048*2048`、`normalizedImageMaxDimension=8192`、`normalizedImageMaxBytes=4MiB`（:904-908），质量阶梯（quality ladder）编码取最小满足者（:58 共享阶梯注释；:208-216 输出断言）。归一化只在图片**首次进入**时做一次，永久保存。
3. **请求版本 request-image**（每模型路由，进模型前）：按路由 `{maxPixels,maxBytes}` 从归一化图再投影（尺寸自适应 + 质量阶梯），缓存键 `variantId`（types.d.ts:135-139）。DeepSeek 路由预算 `imagePixelBudget=640000`（或 'low'=512×512）、`imageMaxBytes=1MiB`（dsh-llm-deepseek index.js:1944-1945），超 32MiB 的 chat 图强制走 Files API `file_id`（MAX_CHAT_IMAGE_BYTES index.js:872）。请求级超限时按 64MiB/10MiB/20 张的量子确定性丢弃最旧图（见 llm-core.md §2.7）。
4. **视觉 token 估算**：DeepSeek 路由声明 `imageRequestPricing`，按 14px patch/3:1 降采样/384 上限公式计算（deepSeekImageTokens，dsh-llm-deepseek index.js:422-432，详见 llm-core.md §2.8）；无声明的路由由 token-meter 回退固定启发式。

### A.5 文件/图片向模型文本的投影（模板原文）

- 文件 handle（`fileHandleText`，dsh-llm/lib/index.js:603）：
  `[<identity>: verbatim read-only copy saved at <quoted path>. Read that path with your file tools when its contents are needed; copy it to a writable location before modifying it. When delegating file work, include this saved path in the delegation prompt; only subagents sharing this execution environment can read it.]`
- 请求图片 handle（`requestImageHandleText` :554-557）：`Image <identity>; request preview <W>x<H>px.` +（有本地路径时）归一化访问文案；无路径时追加 `It may be resized or re-encoded; source dimensions, format, and byte size may differ.`
- 被丢弃图片占位（`offloadedImageText` :564-566）：`[image omitted to fit request image limits; <identity>. No local normalized image path is available; ask the user to attach it again if needed.]`
- DeepSeek 工具结果图片前导文本：`Attached image(s) from tool result:`（dsh-llm-deepseek index.js:24）。

## B. Token 计量（dsh-token-meter）

### B.1 tokenizer：**无 tiktoken，纯固定密度启发式**

`CHARS_PER_TOKEN = 4`、`BLOCK_OVERHEAD = 4`（dsh-token-meter/lib/index.js:16-18）。估算函数（lib/index.js:26-84）：
- text/reasoning 块：`ceil(len/4)+4`（:39）；tool-call：name 与 arguments 各 `ceil(len/4)` + 4（:42）；tool-result：递归 + 4（:45）；其它块（image/file/merge 扩展）：`max(4, ceil(JSON.stringify(block).length/4))`（:26-28）；
- 消息：`estimateContent+4`；system 消息：`ceil(chars/4)+4` 无块开销（:59-74）；
- 工具 schema：`ceil(JSON.stringify(tools).length/4)+4`（:81-84）。
`TokenMeterConfig = Record<string,never>`——估计器无设置项（types/types.d.ts:10）。真实 token 数一律以 **provider usage 为准**，启发式只用于增量 repricing。

### B.2 context window 占用计算与锚定

`TokenMeasurement`（types/types.d.ts:24-36）：`{logRevision, baseline, surfaceDeltaTokens, totalTokens, surfaceTokens, nodes[]}`；`baseline.kind: 'none'|'estimated'|'usage'`（:12-22）——一旦有 provider usage 就锚定它，之后只按 surface 变化做启发式差分（shadow-price O(1) 折叠，surface-fold :87-101）。`TokenSurfaceNode={seq,tokens(路由定价，图片用路由视觉价),heuristicTokens}`（:39-55）。

上报前端的是三个 session projection（projection.d.ts:64-72，经 `session/follow` 的 `snapshot.projections` / `{type:'projection'}` 帧下发，见 ws-protocol.md）：
1. **`tokenUsage`** = `TokenUsageProjection {uncachedInputTokens, outputTokens, cacheReadTokens, cacheWriteTokens}`（:12-17）——四桶不相交、全 log 累计；**reasoningTokens 已含在 outputTokens 内不再单列**（:9-10 注释）。来源：每条 `assistant/message` 事件的 `usage`（provider 报告）。
2. **`contextPressure`** = `ContextPressureProjection {pressureTokens?, projectedTokens?, contextWindow?}`（:28-46）：`pressureTokens`=最近一次请求 provider 报告的 prompt 规模（uncached+cacheRead+cacheWrite，不含输出）；`projectedTokens`=pressureTokens+此后 surface 变化的启发式 repricing（compaction 立即生效）；`contextWindow`=最近路由容量。均为 last-wins、非原子快照（:20-26 注释）。
3. **`contextBreakdown`** = `{systemTokens, toolsTokens, messageTokens}`（:56-63；wire schema breakdown-projection.d.ts:73-95）——纯启发式构成比例，明确不用于总数展示。

缓存折算：provider usage → harness 的映射在 adapter 层完成（DeepSeek `inputTokens=prompt_tokens−cached_tokens`，见 llm-core.md §2.5）；meter 只做投影累加。

### B.3 复刻建议（Go）

- 维护每 session 的三投影折叠即可满足前端占用条；锚点用最近 usage，增量用 `len/4+4` 启发式。
- 路由视觉 token 价做成可选接口（等价 `imageRequestPricing`），DeepSeek 路由按 patch-grid 公式，其余回退 `max(4, ceil(JSON/4))`。
