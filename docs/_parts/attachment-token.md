# DSH 附件/上传 与 Token 计量 逆向规格说明

> 只读逆向调研。代码根 `BASE=/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`。
> 下文所有 `文件:行号` 均相对 BASE。行号取自已编译 `lib/index.js`（bundle 保留源模块行结构）与 `.d.ts`。

---

## 任务 A — 附件 / 上传

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

## 任务 B — token 计量（dsh-token-meter）

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

## 未找到项

- 图片/multipart / WS 帧上传通道：**未找到**（文件走 `application/octet-stream` HTTP POST 流式路由；图片随 `session/prompt` JSON base64 内联）。
- 客户端（浏览器）侧图片下采样：**未找到**——浏览器原样 base64 上传，缩放全部在宿主端 sharp。
- 除 `session/prompt`、`fileUploads/upload`、`POST /api/session/uploadFileBinary` 之外的第三条上传路由：**未找到**。
- `dsh-token-meter` 中任何真实 tokenizer（tiktoken/gpt-tokenizer 等）：**未找到**——纯 `len/4+4` 启发式 + 路由视觉 token 价。
- 独立 usage 推送事件名（如 `usage/update`）：**未找到**——usage 经 session projection 块（`{asOfSeq, values}`）随快照/基线下发；逐轮面板由前端 `deriveTurnTokenUsage` 现算。
