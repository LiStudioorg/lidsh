# 核心执行语义 · 第 1/2 节：会话数据模型 + 磁盘持久化格式

> 逆向基础目录（下文相对路径均相对它）：
> `/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai/`
> 所有断言来自编译产物 `lib/**/*.d.ts` 与 `lib/index.js` 的真实内容，行号以 read/grep 所见为准。
> 找不到的东西明确写「未找到」。

包根缩写：`$P = /usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`

---

## 1. 会话数据模型

### 1.0 分层总览（照此建 Go 类型）

- **dsh-llm**：provider 中立的 `Message` / `ContentBlock` / `StreamChunk` / `GenerateOptions` 词汇（`dsh-llm/lib/types/types.d.ts:1-5`）。
- **dsh-session**：事件溯源的 `Session`——append-only 事件日志（`SessionEvent`）是唯一真相源，LLM 消息历史是**派生投影**（surface）（`dsh-session/lib/types/index.d.ts:1-7`、`dsh-session/lib/types/types.d.ts:237-241`）。
- **dsh-session-persistence(-jsonl)**：持久化是插件关注点（订阅 `session/event`，`session/flush` 时排空）（`dsh-session/lib/types/index.d.ts:3-4`），JSONL+zstd 后端。
- 品牌类型（Branded/BrandedNumber）只是 TS 名义类型，**运行时就是 string / number**，Go 里直接用具名 string/int 类型即可。

### 1.1 品牌化 ID 与序列号

| 类型 | 运行时形态 | 生成规则 | 出处 |
|---|---|---|---|
| `SessionId` | string（无校验） | 由调用方铸造。API 会话：`session-${uuid}`（`dsh-api-session-controller/lib/index.js:573`）；fork 子会话同样 `session-${uuid}`（`dsh-api-session-controller/lib/index.js:692`）；声明式 agent：`${id}-session-${uuid}`（`dsh-agent-loop/lib/index.js:1540`）；subagent 默认裸 `randomUUID()`（`dsh-subagent/lib/index.js:1649`） | `dsh-session/lib/types/types.d.ts:5-11` |
| `SessionSeq` | 非负安全整数 | **`seq = 日志中事件下标`，从 0 连续**：`Session.append` 里 `seq: SessionSeq(this.log.length)`（`dsh-session/lib/index.js:1184`） | `dsh-session/lib/types/types.d.ts:13-19` |
| `SessionLogOffset` | 非负安全整数 | 日志间隙/前缀长度/读偏移，可等于事件总数 | `dsh-session/lib/types/types.d.ts:21-27` |
| `SessionSeqCursor` | `SessionSeq \| -1` | 含界水位；-1 表示尚无任何事件 | `dsh-session/lib/types/types.d.ts:29` |
| `MessageId` | string = `randomUUID()` | `createMessage` 里 `id: brandString(randomUUID())`，随后 deepFreeze | `dsh-llm/lib/types/brand.d.ts:14-20`、`dsh-llm/lib/types/message.js:33-38` |
| `ToolCallId` | string | **provider 下发**；mock/兜底才合成 | `dsh-llm/lib/types/brand.d.ts:22-31` |
| `LlmAttemptId` | string | `${sessionId}:${attemptCounter}`，Agent 生命周期内唯一 | `dsh-llm/lib/types/brand.d.ts:41-47`、`dsh-agent-loop/lib/index.js:389` |
| `ProviderRequestId` | string | provider 下发，仅诊断 | `dsh-llm/lib/types/brand.d.ts:33-39` |
| `ReasoningEffortId` | string | adapter 下发 | `dsh-llm/lib/types/brand.d.ts:49-55` |
| `ApprovalRequestId` | string = `randomUUID()` | `dsh-user-approval/lib/index.js:134` | `dsh-user-approval/lib/types/types.d.ts` |
| `CompactionId` | string = `randomUUID()` | `dsh-compaction-basic/lib/index.js:446` | `dsh-compaction/lib/types/brand.d.ts` |
| `GoalId` | string = `goal-${randomUUID()}` | `dsh-goal/lib/index.js:642` | `dsh-goal/lib/types/types.d.ts:15` |

**时间戳约定**：一律 Unix epoch **毫秒**。事件 envelope 的 `time` 在 append 时取 `Date.now()`（`dsh-session/lib/index.js:1185`；注释 `dsh-session/lib/types/types.d.ts:464-465` "Unix epoch milliseconds"）；header `createdAt` 同样 `Date.now()`（`dsh-session/lib/index.js:808, 1396`）。崩溃修复合成的 closing 事件**复用最后一条真实事件的 time**，不造假未来时间（`dsh-session/lib/types/repair.js:80-83`）。

### 1.2 SessionHeader（会话不可变元数据，日志之外）

`dsh-session/lib/types/types.d.ts:58-94`，逐字段：

| 字段 | 类型 | 语义 | 行号 |
|---|---|---|---|
| `version` | `3`（常量） | 当前逻辑格式版本 `SESSION_FORMAT_VERSION = 3`（:54） | :63 |
| `id` | `SessionId` | 与 Session.id 镜像 | :65 |
| `createdAt` | number | epoch ms | :67 |
| `cwd?` | string | 工作区目录 | :69 |
| `parentSession?` | `SessionId` | 父会话（subagent/fork） | :71 |
| `isSeeded` | boolean | 是否种子会话（fork/resume 继承前缀） | :76 |
| `origin?` | `'subagent'` | 粗粒度来源标签 | :81 |
| `delegationDepth?` | number | 委派深度 | :87 |
| `agentPreset?` | string | agent 预设名 | :94 |

版本升级规则（写实现时照抄语义）：单调整数、无 major/minor；**由写方**（而非读方）决定 bump；只有 header 形状 / `SessionEvent` envelope / 核心事件语义 / surface 机制变化才 bump；新增普通事件类型靠 per-event `ignorable` 兜底（`dsh-session/lib/types/types.d.ts:39-53`）。

`CreateSessionOptions`：`inheritedEventCount?: SessionLogOffset`、`meta?: { cwd?, parentSession?, createdAt?, isSeeded?, origin?, delegationDepth?, agentPreset? }`（`dsh-session/lib/types/types.d.ts:101-121`）。

### 1.3 消息模型（LLM 层）

统一消息表示 `Message`（`dsh-llm/lib/types/message.d.ts:120-129`）：

```ts
interface Message {
  readonly id: MessageId              // :122，randomUUID
  readonly role: 'system'|'user'|'assistant'  // :124
  readonly content: ContentBlock[]    // :126
  readonly source: MessageSource      // :128
}
```

specialization（同一表示，role/source 收紧）：
- `UserMessage`：role='user'（:131-133）
- `AssistantMessage`：role='assistant'，`source: ModelMessageSource`（:135-138）
- `SystemMessage`：role='system'，`source: {kind:'plugin', plugin}`；**content 为空数组 = "无 system prompt"，投影为无 wire 消息**（:139-147）
- `ToolResultMessage`：role='user'，`content: [ToolResultBlock]`（**恰好一个** tool-result block 的元组），`source: {kind:'tool', callId}`（:148-153）

`MessageSourceMap`（merge-extensible，plugins 可扩 kind；核心四种）（`dsh-llm/lib/types/message.d.ts:94-104`）：

```ts
{ user:   { kind:'user' }
, plugin: { kind:'plugin', plugin: string } & ContextFormed
, model:  ModelMessageSource   // { kind:'model', provider, model, replayState? }
, tool:   ToolMessageSource    // { kind:'tool', callId }
}
```

`ModelMessageSource` = `AssistantProvenance + {kind:'model'}`，`AssistantProvenance = { provider: string; model: string; replayState?: unknown }`（`dsh-llm/lib/types/message.d.ts:4-20`）。`replayState` 是 adapter 私有 lossless-JSON 回放态（:10-15）。插件扩展示例：goal 轮次消息扩了 `kind:'goal'` source（`{kind:'goal'; goalId; revision; round}`，`dsh-goal/lib/types/domain.d.ts:34-45`）。

`ContextForm`（plugin source 的可选语义标签，`dsh-llm/lib/types/message.d.ts:42-54`）：`'instructions' | 'catalog' | 'snapshot' | 'notice' | 'relay' | 'recall'`；`notice` 必须带 `summary`（上限 120 字符，`CONTEXT_SUMMARY_MAX_CHARS = 120`，`dsh-llm/lib/types/message.js:12`），`snapshot` 必须带 `sections: {name,text}[]`（`message.d.ts:56-89`）。

消息构造函数（Go 实现对齐行为）：`createMessage` = 填 `id=randomUUID()` → `structuredClone` → `deepFreeze`（`dsh-llm/lib/types/message.js:33-38, 24-28`）；`createSystemMessage(text, plugin)`：text 为空 → `content: []`（`message.js:69-76`）；`createToolResultMessage({callId, content, isError})`（`message.d.ts:201-212`）。

### 1.4 ContentBlock 全部 variant

Map 定义（`dsh-llm/lib/types/types.d.ts:91-98`）：`ContentBlock = ContentBlockMap[keyof ContentBlockMap]`，共 **6** 个 variant（与你已知一致，逐个列全字段）：

```ts
// :38-42
interface TextBlock      { type:'text';      text: string }
// :44-47
interface ReasoningBlock { type:'reasoning'; text: string }
// :54-58  —— 图片是“耐久引用”，不是内联 base64
interface ImageBlock     { type:'image';     attachment: ImageAttachmentRef }
// :66-70  —— 文件永不原生进 provider：请求组装期投影为确定性 handle 文本
interface FileBlock      { type:'file';      attachment: FileAttachmentRef }
// :72-79  —— arguments 是模型原样产出的 raw JSON 字符串（不解析存储）
interface ToolCallBlock  { type:'tool-call'; id: ToolCallId; name: string; arguments: string }
// :81-86
interface ToolResultBlock{ type:'tool-result'; toolCallId: ToolCallId; content: ContentBlock[]; isError?: boolean }
```

`ImageAttachmentRef`（`dsh-attachment/lib/types/types.d.ts:7-28`）：`{ attachmentId; mediaType; bytes; width; height; name?; originalDimensions?: {width,height} }`；文件引用 `FileAttachmentRef`（:34-41）：`{ attachmentId /* = 文件字节的 sha256 摘要，content-addressed */; name; bytes }`。

### 1.5 SessionEventMap（核心事件包络与每种事件的 data）

**事件 envelope**（判别联合，`dsh-session/lib/types/types.d.ts:460-483`）：

```ts
interface SessionEvent<T> {
  type: T                    // :462
  seq: SessionSeq            // :464 日志内单调 seq
  time: number               // :466 Unix epoch ms
  data: SessionEventMap[T]   // :467
  ignorable?: true           // :478 未识别类型可安全跳过的标记；缺省=必需
  // 仅 surface 事件可携带（:479-482）：
  surfaceOp?: SurfaceOp          // 见 1.6
  sourceEventSeqs?: SessionSeq[] // 引用的更早事件 seq
}
```

`ignorable` 语义：读方遇到不在 `KNOWN_SESSION_EVENT_TYPES`（`dsh-session/lib/types/known-event-types.js:20-77`，本 build 认识的完整事件名单，56 项）里的 type 时，仅当事件带 `ignorable:true` 才可跳过，否则拒绝读日志（`known-event-types.js:12-19`）。

**核心 `SessionEventMap`**（merge-extensible；核心成员逐字段，`dsh-session/lib/types/types.d.ts:242-405`）：

| type | data 字段 | 行号 |
|---|---|---|
| `turn/start` | `{ turn: number }`（在 claim 输入或跑 pre-step 前开 turn） | :249-251 |
| `turn/end` | `{ turn: number; reason: TurnEndReason }` | :260-263 |
| `step/start` | `{ turn: number; step: number }`（一步 = 一次模型调用 + 其请求的工具执行） | :265-268 |
| `step/end` | `{ turn: number; step: number }` | :270-273 |
| `user/message` | 直接就是 `UserMessage`（人输入 / `inject()` 合成上下文 / goal 续轮，三者靠 `source` 区分，content 一律逐字投影） | :281 |
| `system/message` | `{ turn, step, message: SystemMessage }`（渲染后的 system prompt；首条是 surface node 0） | :294-298 |
| `assistant/message` | `{ turn, step, message: AssistantMessage, stream: AssistantStreamRecord[], usage?: TokenUsage, interrupted?: true }`（usage 与输出同事件；中断时把已交付前缀定稿于此事件） | :309-317 |
| `assistant/attempt` | `{ turn, step, stream: AssistantStreamRecord[] }`（未产出 surface 消息的一次尝试：失败/重试/取消） | :323-327 |
| `tool/call` | `{ turn, step, callId, name, arguments: string }`（arguments 为模型原文，未解析） | :333-339 |
| `tool/result` | `{ turn, step, message: ToolResultMessage, error?: {name, code}, meta?: JsonValue }`（`error` 仅当 block `isError:true` 才允许；`meta` 是工具私有展示载荷，必须 JSON 可序列化） | :351-364 |
| `request/header` | `{ header: EpochHeader, reason: RequestHeaderReason, startsSeries?: true }`（下一个请求的完整 header 快照，log-only） | :366-373 |
| `request/context` | `RequestContext = { provider, model, contextWindow?, systemPromptUpdate? }`（仅在路由/容量/prompt 更新模式变化时写） | :378, :217-227 |
| `session/end-seed` | `{ inherited?: true }`（构造器种子结束标记；`Session` 构造器是唯一合法写方，:404） | :401-404 |

`TurnEndReason` 全 variant（`dsh-session/lib/types/types.d.ts:165-199`）：
`{kind:'completed'}` / `{kind:'aborted', reason: AgentCancelCause|{kind:'legacy'}|{kind:'disposed'}…}`（:167-170 与 :150-160）/ `{kind:'blocked'}`（pre-step 拒绝）/ `{kind:'error', error: LlmFailure}`（LlmError 原样，或其他错误 `{message: errorChain(error), code:'UNKNOWN'}`）/ `{kind:'max-tokens'}` / `{kind:'interrupted'}`（崩溃孤儿的补写标记，loop 从不活体发它）。

`EpochHeader`（`dsh-session/lib/types/types.d.ts:208-216`）：`{ config: LlmCallConfig, adapterDefaults?: {reasoningEffort?:true, maxTokens?:true}, tools?: ToolSchema[] }`。
`LlmCallConfig = { provider, model, reasoningEffort?, temperature?, maxTokens?, stop? }`（`dsh-llm/lib/types/call-config.d.ts:16-23`）。
`RequestHeaderReason = 'initial' | 'resume' | 'change' | 'series'`（`dsh-session/lib/types/types.d.ts:235`）。
`ToolSchema = { name, description, parameters: Record<string,unknown> }`（`dsh-llm/lib/types/types.d.ts:397-402`）。

**插件合并扩展的事件**（全部在 `KNOWN_SESSION_EVENT_TYPES` 名单，行号 `dsh-session/lib/types/known-event-types.js:21-76`；payload 定义在各自包，此处列名+定义处）：
`agent/inbox/spliced`（`dsh-agent/lib/types/types.d.ts:80-86`，`{target:'next-turn'|'next-step', start:number, removedCount?:number, inserted:UserMessage[], outcome?:'canceled'}`）、`approval/asked` / `approval/decided` / `approval/policy`（见 core-control.md）、`assistant/attempt`（核心）、`command/run` / `command/done`（dsh-commands）、`compaction/start|summary|end|prune`（见 core-control.md）、`goal/change`（见 core-control.md）、`hook/invoked` / `hook/result`、`llm/retry` / `llm/retry-started`（见 core-loop.md）、`model/selection`、`permission/preset`、`plan/mode`、`sandbox/mode`、`schedule/change`、`session/title` / `session/title-llm-request`、`subagent/catalog` / `subagent/descriptor` / `subagent/model-selection-policy`、`team/*`、`todo/write`、`tool-workflow/*`、`tool/ptc-dispatch(-start)`、`web/deepseek-search-llm-request`、`deliverables/presented`、`feedback/*`、`agent-preset/selected`、`session-log-deepseek/delivery-accepted`。

真实样例（本次调研用的活动会话里实际出现的类型序列，见 §2.5）。

### 1.6 Surface（模型可见投影）——这是"消息 union"的权威答案

- **只有 4 种事件产生 LLM 消息**：`SurfaceEventType = 'system/message' | 'user/message' | 'assistant/message' | 'tool/result'`（`dsh-session/lib/types/types.d.ts:413`；运行时集合 `dsh-session/lib/types/surface.js:13-18`）。其余事件（边界/attempt/header/审批审计…）是 log-only 轨迹。
- `SurfaceOp = 'append' | { op:'replace', startSeq: SessionSeq, endSeq: SessionSeq }`（`dsh-session/lib/types/types.d.ts:429-433`）。`replace`：用本节点替换 surface 上 startSeq..endSeq **按位置**（含两端）的节点；两端必须存在于当前 surface；`sourceEventSeqs` 必须包含全部被 shadow 的节点（:415-428 注释）。**replace 事件本身也进 append-only 日志**（append 一个新事件去遮蔽旧节点），原事件永不删——人读转录用 append-origin 事件（`isAppendSurfaceEvent`，`surface.js:49-52`）。
- 每节点投影规则 `deriveEventMessage(event)`（`dsh-session/lib/types/surface.js:75-110`，声明注释 `surface.d.ts:51-64`——"THE per-node projection rule"）：
  - `user/message` → `event.data` 原样（:86-93；框架化文本是生产者责任，投影层永不包壳）
  - `system/message` / `assistant/message` → `content.length===0` 时 **null**（不注入空消息），否则 `event.data.message`（:99-104）
  - `tool/result` → `event.data.message`（:105-107）
  - 其他 → null（:108-110）
- 折叠：`foldSurface(events)`（`surface.js:393`）/ 增量 `SurfaceManager`（`surface.js:404`；`validateNext` 在 append 前校验候选，`dsh-session/lib/index.js:1188`）。`Session.deriveMessages()`（`dsh-session/lib/index.js:1269`）对 live surface 折 `deriveEventMessage` 得到请求 messages。
- `append` 的 surface 元数据规则：message-producing 事件**必须**带 `surfaceOp`，log-only 事件**禁止**（`dsh-session/lib/types/types.d.ts:436-458`；`assistant/message` 不许带 `sourceEventSeqs`）。
- `replace` 后 `startSeq` 可以 **大于** `endSeq`（数值上），因为区间按 surface 位置解释；权威集合是 shadowedSeqs（`dsh-compaction/lib/types/types.d.ts:115-126` 同样陈述）。

### 1.7 Session 运行时对象（Go 需实现的行为面）

- `append(type, data, opts?)`：快照 data（非 JSON 可序列化直接抛，`dsh-session/lib/index.js:1177, 1179`）→ `seq=log.length`（:1184）、`time=Date.now()`（:1185）、deepFreeze → `validateSessionEventData`（:1189）→ `surfaceManager.validateNext`（:1190）→ push 日志 → 同步派发 `session/event`。禁止重入（:1181）。
- `Session.firstLiveSeq`：构造函数种子的右界（`dsh-session/lib/index.js:1034, 1077`），落盘为 `session/end-seed`（:1086-1087）。
- `fork(source, boundary?, childSessionId?)`：拷贝到含 boundary 的前缀（含边界事件，可停在 turn 间但不能停在 open turn 内），`SessionStore` 决定 id 策略（`dsh-session/lib/types/index.d.ts:425-439`）。
- 服务事件：`session/created`（同步 throw 可否决并回滚）、`session/disposed`、`session/event`（post-commit fire-and-forget）、`session/flush`（并行持久化排空）（`dsh-session/lib/types/index.d.ts:40-63`）。

### 1.8 崩溃修复（resume 前置步骤）

`interruptedTurnClosers(events)`（`dsh-session/lib/types/repair.js:23-140`，声明 `repair.d.ts:16-28`）：扫日志维护 openTurn/openStep/pendingCalls（assistant/message 注册 tool-call blocks、tool/call 记 callSeq、tool/result 销账，:29-72）；不平衡时按序合成：**每个未销账 call → 一条错误 `tool/result`**（文本逐字，:95-108：已开始=`"The tool call was interrupted after it was recorded, but no result was durably recorded. Its outcome is unknown. …"`（code `TOOL_OUTCOME_UNKNOWN`），未开始=`"The tool call was interrupted before the Harness recorded it as started. Retry it if it is still needed."`（code `TOOL_NOT_STARTED`））**→ 补 `step/end` → 补 `turn/end{reason:{kind:'interrupted'}}`**；seq 续接、time 复用最后事件（:80-83）。常量：`TOOL_NOT_STARTED`（:11）、`TOOL_OUTCOME_UNKNOWN`（:13）。

---

## 2. 磁盘持久化格式

### 2.1 DSH_HOME 路径解析

- 目录名 `.dsh`、显示名 `~/.dsh`、环境变量 `DSH_HOME`（`dsh-home-paths/lib/types/index.d.ts:7, 9, 11`）。
- 优先级：显式配置路径 > `$DSH_HOME` > `~/.dsh`；空白 `$DSH_HOME` 视同未设（`dsh-home-paths/lib/types/index.d.ts:36-48`）。`dshHomePath(...segments)` 拼子路径（:54）。
- 实测本机：`DSH_HOME=/root/.dsh`，下有 `sessions/`、`storages/`、`profiles/`、`settings.yaml`、`.credentials.yaml`（`ls /root/.dsh` 直接观察）。

### 2.2 目录布局

JSONL 后端配置：`{ root: string(必填，无默认), compression?: 'zstd'|'none'（默认 zstd） }`（`dsh-session-persistence-jsonl/lib/types/index.d.ts:19-30`）。root 由部署拼到 home 下；**本次在本机观察到的实际值**：`$DSH_HOME/sessions/`（`/root/.dsh/sessions/--root-work-lidsh--/<session-id>/`）。root 具体拼名（"sessions"）在装配层的 grep 未直接命中该字面量于 persistence 包——**root 目录名的拼接代码未找到**，但实测数据即上述布局（下述快照）。

层级：`root/〈projectKey(cwd)〉/〈encodeSegment(sessionId)〉/`（`dsh-session-persistence-jsonl/lib/index.js:913`，sessionDir = join(projectDir, encodeSegment(id))）：

- `projectKey(cwd)`：分隔符 `/ \ :` 折叠成单个 `-`，安全字符 `[A-Za-z0-9._-]` 原样，其余码点转 `~XXXX`（4 位大写 hex），去首部 `-`，空→`root`，截断 251，外套 `--…--`（`index.js:874-893`）。例：`/root/work/lidsh` → `--root-work-lidsh--`（实测目录名吻合）。`cwd === undefined` → 目录 `_no-cwd`（`index.js:901-904`）。
- `encodeSegment(id)`：SessionId 是未校验字符串，必须单射编码防路径穿越；`.`→`~002E`、`..`→`~002E~002E`，非 `[A-Za-z0-9._-]` 的码点→`~XXXX`（`index.js:852-863`；声明 `format.d.ts:69-77`）。

会话目录内文件：
1. `session.v{N}.jsonl.zstd`（或 `.jsonl`）——当前/历史格式世代日志。
   - 文件名：v0 = `session.jsonl`，v≥1 = `session.v{N}.jsonl`（`dsh-session-format/lib/index.js:472-475`）；再按编码加后缀 `.zstd` 或空（`persistence index.js:746-762`）。实测当前文件：`session.v3.jsonl.zstd`。
   - **一个世代一个不可变文件**；格式迁移=把旧世代整代重写为新世代文件名（`generation.d.ts:1-8`；`prepareJsonlMigration` :155）。拒绝旧扁平单文件布局（`index.d.ts:254-259`）。
2. `session.lock`——跨进程写锁（`lease.d.ts:26-27` LEASE_FILENAME）。POSIX 用 `flock(2)`（native），持锁=打开 handle 期间；进程死亡内核自动释放；**故意无过期**；POSIX 锁文件释放后不删除（稳定 inode 供后来者校验）；读者不碰锁（`lease.d.ts:1-25`）。实测该文件 0 字节存在。

### 2.3 文件物理格式

**明文形态**（解压后）= JSONL：
- **第 1 行 = header 记录**（`type:'session'`），逐字段（`format.d.ts:42-53` 的 `HeaderLine`；实测吻合）：

```json
{"type":"session","version":3,"id":"ece0f3e7-...","createdAt":1790308843680,
 "cwd":"/root/work/lidsh","parentSession":"session-eaa9b93c-...",
 "isSeeded":false,"origin":"subagent","delegationDepth":1,"agentPreset":"standard"}
```

  必填键 `type,version,id,createdAt,isSeeded,delegationDepth`；可选 `cwd,parentSession,origin,agentPreset`（`index.js:776-789`）；**禁止**出现 `sandboxMode`/`approvalPolicy`（退役字段，出现即拒，`index.js:796-799`）；缺省字段**省略键**而非 null（`format.d.ts:67`）。seeded 头必须带 inheritedEventCount（写入经 catalog encodeCurrentHeader，`index.js:807-815`）。
- **之后每行 = 一个 `SessionEvent`**（`eventLine = JSON.stringify(event)`，`index.js:953-955`；"every event occupies one row"，`index.js:938-941` 注释）。
- 尾部容错：**缺换行的最后一条记录按撕裂尾忽略**，返回可安全 append 的字节偏移（`format.d.ts:182-186` `SessionLogScanner.finish`、`scanLog` `index.js:1123`）。
- 版本防呆：header `version` 非本 build 的 3 → 报 "不支持的格式版本" 而非"日志损坏"（`index.js:960-967`）。

**zstd 编码形态**（`compression:'zstd'`，默认）：文件 = **concatenated Zstandard frames** 容器——header 与每个持久化批次各自压成**一个独立、带 checksum 的 frame** 追加，从而可只追加不解全档（`zstd.d.ts:1-8`；`compressZstdFrame` 单帧压缩 :31-35；`scanZstdFrames` 不解压定位完整帧、EOF 撕裂帧返回起点供修复 :19-27；`decompressZstdPrefix` 从未完成尾帧抢救明文 :60-66）。**Go 实现**：用 `github.com/klauspost/compress/zstd` 以独立 frame 模式依次 `EncodeAll` 追加即可（每帧独立解压 + xxhash 校验和是 zstd 标准）。`'none'` 即纯明文 JSONL。

**读路径合约**：fail-closed——不认识的必需事件类型（无 `ignorable`）拒绝解释日志（`known-event-types.js:12-19`）；撕裂尾只截到安全偏移后仍可写（`format.d.ts:190-198`）。写路径 lazily materialize：created 会话首次 append/flush 才落盘，之前崩溃则视同不存在（`index.d.ts:50-54`）。

### 2.4 真实文件内容样例（去 `$DSH_HOME` 取到的实际数据）

源文件：`/root/.dsh/sessions/--root-work-lidsh--/ece0f3e7-c812-461a-8ea9-827be7de6863/session.v3.jsonl.zstd`（`unzstd -c` 后 146 行）。代表性行（内容截断，字段原样）：

```jsonc
// 行1 header
{"type":"session","version":3,"id":"ece0f3e7-…","createdAt":1790308843680,"cwd":"/root/work/lidsh","parentSession":"session-eaa9b93c-…","isSeeded":false,"origin":"subagent","delegationDepth":1,"agentPreset":"standard"}
// 种子事件（构造器写入的委派基线）
{"type":"subagent/descriptor","seq":0,"time":1790308843686,"data":{"version":3,"mode":"continuable","provider":"fork","label":"逆向 UI 设计 token","agentProvider":"hcnsec","agentModel":"Qwen3.8-Flash-Next"}}
{"type":"sandbox/mode","seq":1,"time":1790308843686,"data":{"mode":"danger-full-access","source":"delegation"}}
{"type":"approval/policy","seq":2,"time":1790308843686,"data":{"policy":"never","source":"delegation"}}
{"type":"permission/preset","seq":3,"time":1790308843702,"data":{"preset":"danger-full-access"}}
{"type":"agent/inbox/spliced","seq":4,"time":1790308843718,"data":{"target":"next-turn","start":0,"inserted":[{…UserMessage…}]}}
{"type":"turn/start","seq":5,"time":1790308843721,"data":{"turn":1}}
// 活体：surface 事件带 surfaceOp；注意 system/message 是 node0
{"type":"system/message","seq":8,…,"surfaceOp":"append"}
{"type":"user/message","seq":9,"time":1790308843776,"data":{"content":[{"type":"text","text":"目标：…"},{"type":"text","text":"Your parent agent id is …"}],"source":{"kind":"user"},"role":"user","id":"1dd9849f-f471-4df1-bdf7-df5fc66cd865"},"surfaceOp":"append"}
{"type":"request/header","seq":11,…,"data":{"header":{"config":{"provider":"hcnsec","model":"Qwen3.8-Flash-Next"},"tools":[{…ToolSchema…}]},"reason":"initial"}}
{"type":"request/context","seq":12,…,"data":{"provider":"hcnsec","model":"Qwen3.8-Flash-Next","contextWindow":262144}}
{"type":"session/title","seq":13,…,"data":{"title":"目标：从已安装的 DeepSeek Harnes","messageSeqs":[9],"source":{"kind":"fallback"}}}
// 失败尝试 + 重试审计（见 core-loop.md §3.8）
{"type":"assistant/attempt","seq":14,…,"data":{"turn":1,"step":1,"stream":[{"type":"chunk","time":…,"chunk":{"type":"finish","reason":{"kind":"error","failure":{"message":"500: …","code":"SERVER"}}}}]}}
{"type":"llm/retry","seq":15,…,"data":{"retryId":"42b61ad0-…","turn":1,"step":1,"provider":"hcnsec","mode":"normal","policyKey":"[\"normal\",5,[\"EMPTY_RESPONSE\",\"RATE_LIMIT\",\"SERVER\",\"TIMEOUT\",\"TRANSPORT\"],500,10000,0.1]","retry":1,"maxRetries":5,"delayMs":546.6467802915569,"failure":{…}}}
{"type":"llm/retry-started","seq":16,…,"data":{"retryId":"42b61ad0-…","turn":1,"step":1,"retry":1}}
{"type":"assistant/message","seq":20,…,"surfaceOp":"append","data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"reasoning","text":"…"},{"type":"text","text":"…"},{"type":"tool-call","id":"call_3f4a…","name":"bash","arguments":"{\"command\":…}"},…],"source":{"kind":"model","provider":"hcnsec","model":"Qwen3.8-Flash-Next","replayState":{…}},"id":"d8e53466-…"},"usage":{"inputTokens":951,"outputTokens":256,"totalTokens":9591,"cacheReadTokens":8384},"stream":[{"type":"chunk","time":…,"chunk":{"type":"block-start","index":0,"blockType":"reasoning"}},{"type":"reasoning-chunks","time0":…,"index":0,"dt":[22,24,24,…],"texts":["Let","'s"," start",…]},…]}}
{"type":"tool/call","seq":21,…,"data":{"turn":1,"step":1,"callId":"call_3f4a…","name":"bash","arguments":"{…}"}}
{"type":"tool/result","seq":24,…,"surfaceOp":"append","sourceEventSeqs":[23],"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_ef21…"},"content":[{"type":"tool-result","toolCallId":"call_ef21…","content":[{"type":"text","text":"total 52\n…"}],"isError":false}],"role":"user","id":"2c71db5f-…"}}}
```

注意实测点：**同一 step 可有多条 tool/call+tool/result**（并行工具）；`tool/result.sourceEventSeqs=[callSeq]` 必指回自己的 `tool/call`；`assistant/message.stream` 是压缩的 `AssistantStreamRecord[]`（下节）。

### 2.5 AssistantStreamRecord（assistant 流的无损紧凑表示）

union 全 variant（`dsh-llm/lib/types/assistant-stream.d.ts:16-40`）：

```ts
| { type:'text-chunks';       time0:number; index:number; dt:number[]; texts:string[] }
| { type:'reasoning-chunks';  time0:number; index:number; dt:number[]; texts:string[] }
| { type:'tool-call-chunks';  time0:number; index:number; dt:number[]; id:ToolCallId; name?:string; args:string[] }
| { type:'chunk';             time:number;  chunk:StreamChunk }   // 非 delta 的原始块（block-start/usage/finish…）
```

- 语义：packed delta run 的 `dt` 是相邻成员相对 `time0` 的毫秒增量链（首个相对 time0），`texts/args` 逐 fragment 保留 delta 边界（:41-44、`expandAssistantStream` 无损展开 :65-71）。
- 生成：`AssistantStreamAccumulator.push({time:Date.now(),chunk})` 增量打包（:50-64）；`assembleAssistantStream` 可把紧凑流直接喂 `BlockAssembler` 重建消息（:155-165）。
- `StreamChunk` 全 variant（`dsh-llm/lib/types/types.d.ts:359-389`）：`block-start{index,blockType}` / `text-delta{index,text}` / `reasoning-delta{index,text}` / `tool-call-delta{index,id,name?,argumentsDelta}` / `block-end{index,block}` / `usage{usage}` / `finish{reason:FinishReason, replayState?:ReplayEnvelope}`。
- `FinishReason` 全 variant（`dsh-llm/lib/types/types.d.ts:107-127`）：`stop` / `tool-calls` / `max-tokens` / `aborted{failure}` / `error{failure}`。
- `TokenUsage`（`dsh-llm/lib/types/types.d.ts:136-150`）：`inputTokens, outputTokens, totalTokens?, cacheReadTokens?, cacheWriteTokens?, reasoningTokens?`——**计数互斥：inputTokens 只含未缓存输入**，缓存单独记（:128-135 注释）。

### 2.6 LlmFailure / 错误码词汇（日志与重试路由都靠它）

`LlmFailure = { message: string; code: string; status?: number; providerRetryAfterMs?: number; requestId?: ProviderRequestId }`（`dsh-llm/lib/types/types.d.ts:26-37`）。`HarnessError.code` 是路由依据、message 只给人看（`dsh-llm/lib/types/error.d.ts:12-16`）。核心码常量：`CONTEXT_WINDOW_EXCEEDED`（:18）、`QUOTA`（:20）、`EMPTY_RESPONSE`（:30）、`INVALID_CREDENTIAL`（:38）；重试默认码表另含 `RATE_LIMIT`/`SERVER`/`TIMEOUT`/`TRANSPORT`（`dsh-llm/lib/types/retry-policy.js:16-22`，见 core-loop.md）。

### 2.7 Go 存储层落地清单（摘要）

1. `Session` = { header, events[]（seq 连续下标）, surface 折叠态（nodes + replaceGeneration）}；append 顺序 = 快照→freeze→校验→surface.validateNext→push→广播（`dsh-session/lib/index.js:1170-1190`）。
2. 磁盘：`{root}/{projectKey(cwd)}/{encodeSegment(id)}/session.v3.jsonl.zstd` + `session.lock`（flock）。JSONL 首行 header，后续每行一个事件；zstd 用 concatenated frames（每帧一个批次、独立可解压带校验和）。
3. 加载：解帧→逐行解析→unrecognized type 且无 `ignorable` → 拒载→`interruptedTurnClosers` 补尾→再构造。
4. `deriveMessages` 只 fold 四种 surface 事件；replace 按位置遮蔽。
