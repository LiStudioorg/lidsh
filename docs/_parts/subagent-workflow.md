# 子代理与工作流 逆向调研规格说明

- 调研性质：只读逆向，基于已编译 JS + .d.ts。
- `BASE = /usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`
- 本文所有 `文件:行号` 均相对 BASE；文件路径以 `包名/lib/...` 形式书写。
- 缩写：
  - **ST** = `dsh-tool-subagent/lib/index.js`
  - **SC** = `dsh-tool-subagent-control/lib/index.js`
  - **LA** = `dsh-tool-subagent-control/lib/types/list-agents.js`
  - **SA** = `dsh-subagent/lib/index.js`
  - **DRV** = `dsh-subagent-in-process-driver/lib/index.js`
  - **SP** = `dsh-subagent-spawn-in-process/lib/index.js`
  - **FK** = `dsh-subagent-fork-in-process/lib/index.js`
  - **TW** = `dsh-tool-workflow/lib/index.js`
  - **WF** = `dsh-workflow/lib/index.js`
  - **WW** = `dsh-workflow-worker-thread/lib/index.js`（宿主侧）
  - **WK** = `dsh-workflow-worker-thread/lib/worker.cjs`（worker 侧）
  - **RL** = `dsh-tool-ralph/lib/index.js`
  - 补充：`dsh-brand/lib/index.js`、`dsh-jobs/lib/index.js`、`dsh-jobs-local/lib/index.js`、`dsh-tool-jobs/lib/index.js`（记 **TJ**）

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
