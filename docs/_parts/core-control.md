# 核心执行语义 · 第 5/6/7 节：审批与权限 + Compaction + Plan mode / Goal

> 基础目录同前；`$P = /usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`。
> 行号来自 read/grep 实际所见；未找到的显式标注。

---

## 5. 审批 / 权限模型

### 5.1 三个正交旋钮（preset 只是它们的打包）

会话上有三个独立 durable 旋钮，各自一个 log-only 事件，各自独立 fold：

| 旋钮 | 事件 | payload | 取值 |
|---|---|---|---|
| 审批策略 | `approval/policy` | `{ policy: ApprovalPolicy; source?: 'delegation' }` | `'ask' \| 'never'` |
| 沙箱模式 | `sandbox/mode` | `{ mode: SandboxMode; source?: 'delegation' }`（实测见 §2.4 样例） | `'read-only' \| 'workspace-write' \| 'danger-full-access'` |
| 预设名 | `permission/preset` | `{ preset: string }` | 任意预设名（下表） |

- `ApprovalPolicy` 定义与语义：`'ask'`（默认；交给已组装的应答者链，无应答者则 fail-closed `'unavailable'`）、`'never'`（无人被问，每次 ask 确定性判 `'rejected'`；CI/无人值守姿态）（`dsh-user-approval/lib/types/index.d.ts:36-44`）。事件声明与 `source:'delegation'` 含义（子会话在委派时被植入的 override）：`dsh-user-approval/lib/types/index.d.ts:15-31`。缺省策略 = `overrideOf(session) ?? config.policy ?? 'ask'`（`dsh-user-approval/lib/index.js:156`）。
- `SandboxMode` 定义：`'read-only' | 'workspace-write' | 'danger-full-access'`；`read-only` 只放行 `/dev/null` 之类必需 sink，`workspace-write` 另允许工作区 + 后端定义的 temp 区，`danger-full-access` 完全绕过约束（`dsh-sandbox/lib/types/index.d.ts:14-21`；`ConfinedSandboxMode = Exclude<SandboxMode,'danger-full-access'>` :21）。
- 三个旋钮的 fold（投影 `permission`，state `{preset,sandbox,approval,seeded}`）：`dsh-permission-presets/lib/index.js:25-61`（`applyPermissionEvent` 按事件类型改写对应字段，其他事件返回同引用）。

### 5.2 permission preset

- 默认预设表（`static Config.presets` 的 `.default(...)`，逐字）：
  - `"workspace-write"` → `{ sandbox:'workspace-write', approval:'ask', name:'workspace-write', description:'Write inside the workspace and permitted temporary directories; wider retries require approval.' }`
  - `"danger-full-access"` → `{ sandbox:'danger-full-access', approval:'never', name:'danger-full-access', description:'Full file access without approval prompts.' }`
  （`dsh-permission-presets/lib/index.js:82-93`）
- 名字 `custom` 是**保留字**，代表"当前旋钮组合不匹配任何预设"的派生态，永不是切换目标也永不写事件（`dsh-permission-presets/lib/index.js:22`、`dsh-permission-presets/lib/types/index.d.ts:121-127`；表里出现 `custom` 直接抛，`index.js:108`）。
- 切换实现：先写 `permission/preset`（保留用户意图），再写两个旋钮事件（`dsh-permission-presets/lib/types/index.d.ts:2-6` 设计说明）；要求挂载了 confining 的 `ctx.shell`（`ctx.shell.sandboxMode === undefined` → 装配错误，`index.js:109`）。`defaultPreset` 配置给新会话（`index.d.ts:91-96`）。

### 5.3 审批闸门在工具管线的哪一步

工具执行 `prepareExecution` 内的固定顺序（`dsh-tools/lib/index.js:3105-3158`）：

1. waterfall `tools/pre-execute` → `PreToolDecision = {kind:'allow'} | {kind:'deny',reason} | {kind:'ask',reason?}`（`dsh-tools/lib/types/index.d.ts:419-427`；事件注释"missing approval support turns `ask` into denial"，`index.d.ts:29-38`）。**不允许改写参数**（参数已入日志/已展示，:413-415）。
2. `kind==='ask'` → `serviceAsk(exec, gate)`（`dsh-tools/lib/index.js:3314-3362`）：
   - `ctx.get('approval')` 缺失 → `deny`，reason = `ask.reason ?? 'tool "<name>" requires approval (not yet supported)'`；
   - `exec.agent === undefined` → `deny`（无会话可审计、无 UI 可路由）；
   - 否则 `approval.request({agent, toolName, callId, reason?, signal})`，返回值一对一映射：
     `allowed-once → allow`；`rejected → deny(the user rejected tool "<name>")`；`cancelled → deny(approval for tool "<name>" was cancelled)`（并标 `approvalCancelled:true`）；`unavailable → deny(tool "<name>" requires approval, but no approval channel is available)`。
3. `allow` 后再过 **monotonic guard** 层（`ctx.tools.guard(fn)`：同步、返回 string 即拒、只能加严不能放行；全局层先、scope 链后）（`dsh-tools/lib/index.js:2807-2831`）。
4. 被拒的最终模型可见结果 = `content:[{type:'text',text:'Error: <reason>'}], isError:true, error:{message:<reason>}`（`dsh-tools/lib/index.js:3127-3140`）。

### 5.4 ApprovalService 的审计协议（fail-closed）

`request(req)`（`dsh-user-approval/lib/index.js:131-150`）：
- **必须在 open turn 内**，否则抛（注释 :116，抛错 :133，逐字："approval.request() outside an open turn: the approval/asked + approval/decided audit pair must be turn-enclosed (a bare event between turns is crash-tail garbage on reload)…"）。
- `id = ApprovalRequestId(randomUUID())`（:134）→ `append "approval/asked" {id, toolName, callId?, reason?}` → 等 `decide()` → `append "approval/decided" {id, outcome}`。任一条 append 提交失败也照样 reject（返回未记录的决策违反成对约束）（:118-124 注释）。
- `ApprovalOutcome = 'allowed-once' | 'rejected' | 'cancelled' | 'unavailable'`（`dsh-user-approval/lib/types/types.d.ts:26`）。语义：signal abort → `cancelled`；应答者缺失/抛错 → `unavailable`；野返回值归一成 `unavailable`（`index.d.ts:104-115`）。**只有 `allowed-once` 是放行**，且只授权所问的那一次动作（`index.d.ts:3-4`）。
- 事件字段：`approval/asked {id, toolName, callId?, reason?}`、`approval/decided {id, outcome}`（`dsh-user-approval/lib/types/types.d.ts:27-52`；两者都是 log-only、无 `surfaceOp`）。
- `ApprovalRequest` = `{ agent, toolName, callId?, reason?, signal? }`（`index.d.ts:56-78`）。`agent` 用于路由（UI 应答者只答它拥有的 agent），`callId` 让 UI 把提问挂到已流出的那次工具调用上（参数因此不必重复携带，`index.d.ts:51-53`）。
- 策略变更对模型的可见方式：`setPolicy(agent, policy)` 写事件 + `agent.inject(createUserMessage({content:[{type:'text',text:'The approval policy changed from "<old>" to "<new>" (changed by the user).'}], source:{kind:'plugin',plugin:'user-approval'}}))`（`dsh-user-approval/lib/index.js:105`）。另注入 system prompt section `approval:policy`（`effective(agent)==='never' ? NEVER_SENTENCE : ASK_SENTENCE`）（`dsh-user-approval/lib/index.js:81-86`）。

### 5.5 审批如何送到前端、答复如何回来

- 服务端 `ApprovalService.decide` 走应答者链（插件注册的应答方），前端侧经 typert 远程桥把 `ApprovalRequestEvent` 投给对应 Client Context（`dsh-user-approval/lib/types/types.d.ts:54-66` 的 "Client-safe payload declared for the approval answerer waterfall"）。
- **具体 RPC 方法名/wire 帧形状**：我在 `dsh-api-session-controller/lib/types/client/` 下 grep `approval` 未命中独立的审批方法文件（该包的 client 面主要暴露 sessions/projections/queue），应答通道的具体方法名 **未找到**（应答者实现应在 `dsh-client-ui-approval` 与 typert 服务桥接层）。Go 实现建议按 `ApprovalRequest`/`ApprovalOutcome` 字段自建一次 request/reply，携带 `id/toolName/callId/reason/signal`，返回四值枚举。
- 会话级"取消审批弹窗"= abort 该请求的 `signal` → `cancelled`（`index.d.ts:73-77`）。

### 5.6 `sandbox_permissions`（bash 提权重试）的取值与语义

- schema：`sandbox_permissions: {type:'string', enum:[...ESCALATION_TARGETS], description:'The wider sandbox mode this command needs. Only valid as a one-shot retry of a command the sandbox just denied; requires justification and user approval.'}`；伴随 `justification: {type:'string', description:'Required with sandbox_permissions: one sentence …'}`（`dsh-tool-bash/lib/index.js:285-294`）。
- `ESCALATION_TARGETS = ["workspace-write", "danger-full-access"]`（`read-only` 是地板，没有东西往它提权）（`dsh-sandbox/lib/index.js:42`；语义注释 `dsh-sandbox/lib/types/escalation.d.ts:21-27`）。
- 执行期校验用 **严格更宽表**（不是 schema enum）：`WIDER_MODES = { "read-only": ["workspace-write","danger-full-access"], "workspace-write": ["danger-full-access"] }`（`dsh-sandbox/lib/index.js:30-33`）。理由：schema 是 registry 全局的，而有效 mode 是 per-call 事实（`dsh-sandbox/lib/types/escalation.d.ts:16-20`）。
- 参数配对校验：`sandbox_permissions` 与 `justification` 必须同现（无原因的 ask / 无驱动的 reason 都是畸形 ask）（`dsh-sandbox/lib/types/escalation.d.ts:29-38`；bash 侧调用 `validateEscalationArgs`，`dsh-tool-bash/lib/index.js:123`）。
- 提权只在**有约束执行器的组合**里存在：`escalationModes = defaultMode === undefined ? [] : ESCALATION_TARGETS`（无沙箱执行器时两个字段根本不进 schema，`dsh-tool-bash/lib/index.js:222`；此时若仍传入 → 抛 `sandbox_permissions is not available in this composition (no sandboxing executor to escalate)`，`index.js:239`）。
- 审批作用域 = **单条命令、一次性**：`approvedMode = args.sandbox_permissions !== undefined && args.justification !== undefined ? await approveBashEscalation(args.sandbox_permissions, args.justification, exec, standingPolicy) : undefined`（`dsh-tool-bash/lib/index.js:389`）——批准只给这一次执行，不改变会话的 `sandbox/mode` 旋钮。
- 模型可见的拒绝标记与同轮提权提示有统一词表：`sandboxDenialMarker(mode)`、`escalationHintMarker(subject)`（`dsh-sandbox/lib/types/escalation.d.ts:40-58`），bash 结果里在 `escalationModes.length>0` 时附带（`dsh-tool-bash/lib/index.js:66, 94`）。
- 工具描述里的完整规则文本（本次会话的 bash 工具描述即逐字来自 `bashDescription(backgroundEnabled, escalationModes)`，`dsh-tool-bash/lib/index.js:125-130`），可照抄作为 Go 端的工具描述基线。

---

## 6. Compaction（上下文压缩）

### 6.1 分层

- `dsh-compaction`：契约层——`CompactionEngine` 抽象服务（`ctx.compaction`，`dsh-compaction/lib/index.js:172-176`）、`compaction/*` 事件声明、tool-pairing 边界判定、checkpoint source 标记。自身不压缩。
- `dsh-compaction-basic`：默认后端 `BasicCompactionEngine`（阈值触发 + 选区 + LLM 摘要 + 事务提交）。
- `dsh-compaction-tool-result-pruner`：可选伴随服务 `ToolResultPruner`（`ctx.toolResultPruner`）。
- `dsh-command-compact`：`/compact` 手动命令，仅调 `compactNow()`。
- `dsh-token-meter`：`ctx.tokenMeter` 计量。

### 6.2 触发阈值与算法

配置默认值（`dsh-compaction-basic/lib/index.js`）：`DEFAULT_THRESHOLD_RATIO = .8`（:15）、`DEFAULT_RETAIN_RATIO = .16`（:17）、`maxTokens ?? 8192`（:72）、`compactionRetries ?? 1`（:73）、`maxOverflowRetries ?? 1`（:74）、`auto ?? true`（:76）。

预算换算：`thresholdTokens = Math.floor(contextWindow * thresholdRatio)`；`retainTokens = policy.retainTokens ?? Math.floor(contextWindow * retainRatio)`；`retainTokens >= thresholdTokens` → `TargetPressureConfigError`；加载期还先查 `retainRatio >= thresholdRatio` 直接拒（子代理报告 `:108-136`，与实测一致）。`contextWindow` 来自 `ctx.llm.resolveModelInfo(provider,model).context.contextWindow`；`context` 缺失 → `TargetPressureConfigError`，自动路径对该 target 只 warn 一次然后放行（`dsh-compaction-basic/lib/index.js:873-903`）。

触发时机（`auto:true` 才注册监听，:786；`_registerAutomaticCompaction` :793）：
- **pressure**：`ctx.on("agent/pre-step", …)` 里 `compactIfNeeded(agent,'pressure',signal)`（:798）。在 loop 中该 waterfall 位于 `preStep()`（`dsh-agent-loop/lib/index.js:894`），即 `step/start` 之前、turn 内。失败只 `logger.warn` 后 `next()` 继续 turn。
- **context-overflow**：`ctx.on("agent/request-error")`，仅 `failure.code === 'CONTEXT_WINDOW_EXCEEDED'` 时进入；**跳过阈值与 retain 策略**，prune 后 `selectCompactableRange(..., retainTokens=0)` 做最大幅度收缩，成功且 `surface.replaceGeneration` 前进才返回 `{kind:'retry'}`；重试按 agent 计数，`agent/status==='idle'` 或新 `assistant/message` 到达时清零。
- **manual**：`compactNow(agent, signal, sourceCommandId)`，在 `runMaintenance` 内以 `retainTokens=0` 选区、`owner:null`、结束后 flush；agent 非 idle → `ManualCompactionError('busy')`。

`compactIfNeeded` 主流程（`dsh-compaction-basic/lib/index.js:873-922`）：
1. `measurement = tokenMeter.measure(session)`；
2. `context-overflow` 分支：prune → 重测 → `selectCompactableRange(session, measurement, 0)` → `compactRegion`，range 为 null 直接返回 null；
3. `pressure` 分支：`assertNoActiveCompaction` → `resolveCompactSpec(policy, contextWindow)` → `totalTokens < thresholdTokens` → 返回 null；
4. 有 pruner 则 `prune.pruneSession(session)` 后重测，仍低于阈值 → 返回 null；
5. `for attempt = 0..=compactionRetries`：选区 → `compactRegion` → 重测 → 低于阈值即返回；选区为 null 且尚无结果 → 返回 null；
6. 全部尝试后仍 ≥ 阈值 → `throw new Error("compaction still above threshold after N compaction attempts (X estimated tokens >= threshold Y)")`。

### 6.3 选区算法（`selectCompactableRange`，:393-416，逐行）

```
firstIdx = (node0 是 system/message) ? 1 : 0            // system head 永不入压缩区
从尾向前累加 pricedNodes[i].tokens，keepFromIdx = 首个使累加 >= retainTokens 的 idx
keepFromIdx <= firstIdx → null（无可压区）
while keepFromIdx > firstIdx && !toolPairingBalancedBefore(surfaceNodes[keepFromIdx]): keepFromIdx -= 1
再 <= firstIdx → null
return { start: surfaceNodes[firstIdx], end: surfaceNodes[keepFromIdx - 1] }   // 闭区间，按 surface 位置
```

tool-pairing 平衡（`dsh-compaction/lib/index.js:20-93`）：`assistant/message` 按其中 `tool-call` block 数 +delta、`tool/result` delta `-1`，累计为 0 的切点才算 balanced；按 `surface.replaceGeneration` 缓存；orphan tool/result 或缺事件 → corrupt surface 抛错。显式区间两端也强制平衡（`validateSurfaceRegion`，`dsh-compaction-basic/lib/index.js:533-549`）。

### 6.4 摘要生成与落盘事务

摘要 LLM 调用：`ctx.llm.stream({purpose:'compaction', maxTokens, sessionId, signal})`；目标解析优先级 = `summarizationProvider/Model` 配置对 > 最近一次 durable request header 的 provider/model > `agent.options`（全无 → 抛）（`dsh-compaction-basic/lib/index.js:269-302`）。**输入构造刻意逐字节重放对话前缀**（system + tools + 被压区间消息），再追加一条 `source:{kind:'plugin',plugin:'dsh-compaction-basic'}` 的 user message 承载 `COMPACTION_INSTRUCTION`（:282-291）——使该辅助调用是原请求的 KV-cache 前缀。`COMPACTION_INSTRUCTION` 全文 :220-255（8 个固定 Markdown 小节：`## Primary Request and Intent` / `## Key Technical Concepts` / `## Files and Code` / `## Errors and Fixes` / `## Pending Jobs` / `## Current Work` / `## Next Step` / `## Critical Context`；要求 terse bullets、空节写 `"(none)"`、保留精确路径/命令/报错/签名、不得提及压缩本身、不调工具、遇到旧 `<compacted-summary>` 要合并）。
落地包装（`frameSummary` :323-335）：`[ text(`${CHECKPOINT_PREAMBLE}\n\n<compacted-summary>`), ...summaryTextBlocks, text("</compacted-summary>") ]`；`CHECKPOINT_PREAMBLE` 全文 :257；标签常量 :211-212。摘要非更小则抛（shrink 校验）；`finish.kind ∈ {error,aborted,max-tokens}` 一律 fail-closed；含 image → `UNSUPPORTED_CONTENT`。

事务顺序（`compactSurfaceRegion` :433+ 与 `commitCompactionBody` :599-646，实测逐行）：
```
validate → assertCompactionInactive（锁）
append "compaction/start" {compactionId, sourceCommandId?, turn|null}     ← 入日志即持锁
await summarize（异步）
稳定性复检（自动=whole-surface / 手动=selected-span，:595-597）
append "compaction/summary" {compactionId, sourceCommandId?, summary, rawOutput?, llmStreamCall?,
                              shadowedRange{start,end}, shadowedSeqs[], shadowedTokenCount,
                              provider, model, maxTokens?, usage?}          :605-620
append "user/message" checkpointMessage,
       { surfaceOp:{op:'replace', startSeq:start, endSeq:end},
         sourceEventSeqs:[startEvent.seq, summaryEvent.seq, ...shadowedSeqs] }  :621-632
append "compaction/end" {compactionId, sourceCommandId?, turn|null, error?}
```
唯一的 surface mutation 就是那条 `user/message` replace；shadow 价的计价事件必须**同步紧邻**在 replace 之前（`compaction/summary` 之于摘要压缩、`compaction/prune` 之于 prune）。事件字段定义：`dsh-compaction/lib/types/types.d.ts:14-99`；`CompactionResult` :102-131。不变量伴生（`dsh-compaction/lib/invariant.js`）：start/summary/end 同 `compactionId`；一个 bracket 内 summary 不得重复；numbered bracket 必须完整落在同一 open turn 内，standalone bracket 必须无 open turn；`turn/start|turn/end` 不得跨过 open compaction bracket；`shadowedSeqs` 必须精确等于当前 surface 上 start..end 的每个节点。

### 6.5 tool-result-pruner 的裁剪规则（`dsh-compaction-tool-result-pruner/lib/index.js`）

- 常量：`PRUNE_MARKER = "\n\n[... tool result middle pruned ...]\n\n"`（:8，本次会话里你已在工具结果中看到它生效）；`DEFAULTS = {thresholdChars:8192, headChars:4096, tailChars:1024}`（:10-14）；按 **Unicode code point** 计数（`codePointLength`，:25-27）。约束：`headChars + len(PRUNE_MARKER) + tailChars <= thresholdChars`（:43-44）。配置 schema 三个字段带 `.default()`（:63-67）。
- 选取（`pruneSession` :137-194）：遍历 `session.surface.nodes` 快照里所有 `type==='tool/result'` 节点，**无年龄/数量/最近 N 豁免**；唯一跳过条件是 `measureContent <= thresholdChars`（非 text block 计 0）。触发时机本身就是豁免机制——只在 compaction 过阈值/overflow 时被调用，低于压力永不裁。
- 裁剪（`pruneContent` :91-124）：`removedStart=headChars`、`removedEnd=totalChars-tailChars`；逐 block 保留头部 + （跨删除区的首个 text block 插一次 marker）+ 尾部；非 text block 原序保留；切片按 code point 不拆 surrogate；断言结果既更小又不超阈值。
- 写回：不是原地改，每个被裁节点写**两条**事件——先 `append("compaction/prune", {shadowedRange:{start:seq,end:seq}, shadowedSeqs:[seq], shadowedTokenCount: tokenMeter.estimateMessage(原消息)})`（:162-169，注意影子价用启发式 estimator），紧接 `append("tool/result", {...原 event.data, message: 仅换 content}, {surfaceOp:{op:'replace',startSeq:seq,endSeq:seq}, sourceEventSeqs:[seq]})`（:170-180）。`event.data` 的 turn/step/error/meta 与 callId 全保留。中途一条 replace 被拒 → 整个 run 同步抛错，此前已落地的替换保持 durable。返回 `{pruned:[{originalSeq,replacementSeq,callId,charsBefore,charsAfter}], charsRemoved}`。

### 6.6 token 计量（简）

`ctx.tokenMeter.measure(session)` → `{totalTokens, surfaceTokens, nodes:[{seq,tokens,heuristicTokens}]}`；`totalTokens = max(0, baseline.tokens + surfaceDeltaTokens)`，最近一次成功请求的 canonical envelope 与当前 header 一致且 provider usage 足够时以 provider usage 为 baseline，否则全量重估（`dsh-token-meter/lib/index.js:656-682`）。启发式估算常量：`CHARS_PER_TOKEN=4`、`BLOCK_OVERHEAD=4`、`ROLE_OVERHEAD=4`（`dsh-token-meter/lib/types/estimate.js:9-13`）。触发比较用 `totalTokens`，保留尾/shrink 用节点 route 价，`shadowedTokenCount` 记启发式价。`TokenMeterConfig` 无配置字段（`Record<string, never>`）。

---

## 7. Plan mode 与 Goal

### 7.1 plan mode（`dsh-plan-mode`）

- 状态：**唯一持久事实 = log-only 事件 `plan/mode`，data 只有 `{ active: boolean }`**，"the last `plan/mode` wins"（`dsh-plan-mode/lib/types/index.d.ts:29-40`）。投影 key `plan`，wire 视图 `{active, pending}`（`lib/types/types.d.ts:19-22`；内部态 `PlanUnitState = {active, wanted:boolean|null, running:{commandId,wanted}|null, activeAtLastHeader:boolean|null}` :24-36）。
- 折叠规则（`lib/index.js` 的 `planProjectionDefinition`）：`command/run{name:'plan'}` → `running={commandId, wanted: args.trim()!=='off'}`；配对的 `command/done`（success）→ `wanted`；`plan/mode` → `active=value, wanted=null`；`request/header` → `activeAtLastHeader`；`pending = wanted!==null && wanted!==active`。
- 进入/退出：
  - slash 命令 `plan`（hint `[off|message]`，支持附件）：`/plan` → `set(active=true)`；`/plan <msg>` → set(true) 后 `agent.steer(...)`；`/plan off` → `set(false)`（带附件直接报错）。
  - 模型侧 `exit_plan_mode` 工具（下）。
  - 服务面 `ctx.planMode.set(agent, active)` 返回 `'committed' | 'queued' | 'cancelled' | 'noop'`（`lib/index.d.ts:116-132`）：**turn 未开 → 立即 append `plan/mode`**（+可选 narration 走 `inject`）；**turn 打开 → 只写进程内 `pendingIntents`（WeakMap）**，等下一个被接受的 turn 内 pre-step 落盘。
- **闸门位置 = `agent/pre-step` waterfall**（不是 tool pipeline）。监听器：先 `await next()`；`decision.kind==='reject'` 或 `signal.aborted` 或无 pending → 原样返回；否则 append `plan/mode` 并可向 `decision.messages` 追加一条 notice 用户消息（`lib/index.js:151-166`）。`PreStepDecision = {kind:'reject'} | {kind:'enter', messages: UserMessage[], startsRequestSeries?: true}`（`dsh-agent/lib/types/runtime-types.d.ts:92-99`）。pre-step reject 的效果 = 本步不产生：claimed 消息不落日志，turn 以 `{kind:'blocked'}` 结束（`dsh-agent-loop/lib/index.js:936-939`）。
- **plan mode 下没有工具黑白名单**——设计即软约束：`exit_plan_mode` 常驻注册（active/inactive 都注册，保证工具目录稳定），active 只切换一个 system prompt section `plan:policy`（active 或 pending 时输出配置的 `section` 文本，否则空串）（`lib/index.js:170-177`；README 明示 "Guidance, not enforcement"、"every tool stays callable"）。硬限制由独立的 sandbox/approval 承担，**不读也不写 plan 状态**（`lib/types/index.d.ts:4-7`）。Config 只允许 `{section: string}`（非空 string，多余 key 加载期报错，`lib/index.js:56-63`）。
- `exit_plan_mode`（`lib/index.js:229-294`）：`parameters: {plan: string(required)}`；`output.schema {approved: const true}`，成功渲染文本 `"Plan approved — plan mode exited; carry out the plan starting with your next step."`。execute 的精确错误文本（throw → 工具失败回给模型）：
  - 无 agent → `exit_plan_mode requires a calling agent (no session to switch)`
  - 非 plan mode → `exit_plan_mode is only available in plan mode`
  - plan 不匹配 `/^#\s+\S/` → `exit_plan_mode requires a non-empty markdown plan starting with a # heading`
  - 无 userQuestions 通道 → `no user-questions channel is available to review the plan; ask the user to switch the session mode instead`
  - 服务被 reload → `the plan-mode service was reloaded while the plan was under review; present the plan again`
  - 审批交互 = `userQuestions.ask`：question id `plan-review`、header `"Plan review"`、question `"Approve this plan and leave plan mode?"`、detail = plan 全文、options `Approve` / `Keep planning`、intent `{kind:'plan-review', approve:'Approve'}`。
  - 选中 `Approve` 且无 custom → 写 `pendingIntents{active:false, narrate:false}`（下一个 pre-step 落盘）并返回 `{approved:true}`；否则抛 `The user chose to keep planning; revise the plan and present it again.` 或 `The user chose to keep planning; their feedback: <feedback>`；用户关闭（`ASK_CANCELLED`）→ `The user dismissed the plan review to speak instead; stay in plan mode, stop here, and wait for their message.`
  - 所以：**exit 需审批（fail-closed），`/plan off` 不需要**。
- narration 文本（`source.kind='plugin', plugin:'plan-mode', form:'notice'`）：`"The user switched this session to plan mode."` / `"The user switched this session back to the default mode."`；仅当最后一次 `request/header` 描述的是另一模式才发。

### 7.2 goal 数据模型（`dsh-goal`）

类型（`dsh-goal/lib/types/types.d.ts`）：
```
GoalId = Branded<'GoalId'>                      // :15，运行时 `goal-${randomUUID()}`（lib/index.js:642）
GoalRef = { id: GoalId; revision: number }      // :17-22  CAS：每次变更 +1
GoalPhase = 'active'|'paused'|'blocked'|'complete'      // :38
GoalBlockReason = { code: string; message: string }     // :40-45  code 必须 lower-kebab-case
GoalSnapshot extends GoalRef { objective: string; phase: GoalPhase; blockedReason?: GoalBlockReason; maxGoalRounds: number }  // :47-56（blockedReason 仅在 phase==='blocked' 时存在）
GoalActivation = 'armed'|'disarmed'             // :58  进程本地，永不持久化；session-start 一律 disarmed
GoalView extends GoalSnapshot { roundsStarted; createdAt; updatedAt; activation }   // :74-83
GoalProjection = { goal: GoalSnapshot; roundsStarted; createdAt; updatedAt }         // :90-99  投影 key `goal`（null=未创建/已 clear）
```
持久事件 `goal/change`（`lib/types/domain.d.ts:13-32, 51`）：
- 快照形 `{kind:'goal/change', version:1, operation: 'create'|'edit'|'pause'|'resume'|'complete'|'block', goal: GoalSnapshot, roundsStarted, createdAt, updatedAt}`
- 墓碑形 `{kind:'goal/change', version:1, operation:'clear', cleared: GoalRef, clearedAt}`
- 操作枚举 `GoalOperation = 'create'|'edit'|'pause'|'resume'|'complete'|'block'|'clear'`（`domain.d.ts:12`）。

消息归因：`GoalMessageSource = {kind:'goal', goalId, revision, round:number(≥1)}`（`domain.d.ts:34-40`）挂在 `user/message` 的 `source` 上；fold 校验必须是当前 active goal 的 `roundsStarted+1` 且 ≤ `maxGoalRounds`，否则重放报错（`lib/index.js:273-279`）。

状态机（fold 强制，`lib/index.js:176-213` + 服务层）：每次 revision+1；`edit` 不能改 phase/blockedReason，可改 objective/maxGoalRounds；`pause: active→paused`；`resume: active|paused|blocked→active` 且 `roundsStarted < maxGoalRounds`；`complete: 非complete→complete`；`block: 仅 active→blocked`；`create` 要求 revision===1、phase active、roundsStarted 0、无未完成 current goal、id 不复用。CAS 失败 → `GOAL_STALE_REVISION`；agent 非 live → `GOAL_AGENT_NOT_LIVE`。服务配置 `{defaultMaxGoalRounds: 256}`（`lib/index.js:588`）。实时事件：`goal/changed`（agent-scoped）、`goal/activation-changed`（`{sessionId, goal?:{id,revision,activation}}`）。

### 7.3 goal 工具（`dsh-tool-goal`）

`create_goal(objective*, max_goal_rounds?)`（需 direct-human）；`get_goal()`（无参数）；`update_goal(goal_id*, revision*, action*, objective?, max_goal_rounds?, blocked_reason?)`，`action ∈ ['edit','pause','resume','complete','blocked']`。
`update_goal` 的校验与语义（`dsh-tool-goal/lib/index.js:335-373`，逐条）：
- `edit`：需 direct-human；带 `blocked_reason` → `blocked_reason is valid only with action blocked`（code `GOAL_TOOL_INVALID_UPDATE`）。
- `pause`/`resume`：需 direct-human；带 objective/max_goal_rounds/blocked_reason → `objective and max_goal_rounds are valid only with action edit; blocked_reason is valid only with action blocked`；**模型不能 resume 一个 paused goal** → `the model cannot resume a paused goal; the user must resume it`（code `GOAL_TOOL_RESUME_PAUSED`；服务层允许，只有工具层禁）。
- `complete`：authority = direct-human 或"当前正是该 goal 的 round"；带 blocked_reason → 拒。
- `blocked`：`blocked_reason` 必填（`blocked_reason is required with action blocked`）；goal-round 权限下 `roundsStarted < blockedAfterConsecutiveRounds` 直接拒 → `blocked requires at least N consecutive goal rounds; current round is X`（code `GOAL_TOOL_BLOCK_THRESHOLD`）；写入 `block({code:'model-reported', message: args.blocked_reason})`。
- `blockedAfterConsecutiveRounds` 默认 **3**（`Config` :115；resolve :199-203）——即"最少连续轮数"。
- 执行前置：必须有 calling agent、agent 是 live 精确实例且为当前 initiator、且存在 open turn；权限面 `requireDirectHuman` = agent ∈ `ctx.agents.roots()` 且当前 turn 内存在 `source.kind==='user'` 的 `user/message`。
- goal-round 权限下 complete/blocked 成功后 `exec.deferContext(...)` 注入收尾 notice（`renderWrapupContext`，source `{kind:'plugin',plugin:'tool-goal',form:'notice',summary:'<action>: <objective>'}`，正文为 `<goal_complete>` / `<goal_blocked>` 包裹文本，`dsh-tool-goal/lib/index.js:365-373`）。
- system prompt section `tool:goal` 文本 = `guidance(blockedAfter)`（:195-197）——即本会话里 goal 工具描述那段。

### 7.4 goal round driver（`dsh-goal-round-driver/lib/index.js`）

**驱动方式 = 事件驱动 + 单飞循环，不轮询。** 关键触发：`agent/status` 变 `idle` → requestDrive；`goal/changed` → 置 `needsCheckpoint` + requestDrive（:236）。就绪条件 `readyToDrive`：fiber 运行、非 stopping、agent 是 live 精确实例、`status==='idle'`、无 competing 输入（:79-84）。`agent/inbox/inserted` 发现 next-turn 有非本 driver 的消息 → 标 competing、把已预约的 attempt 标 stale（**人工输入优先**）（:240-247）。

`drive(state)`（:103-155）：
1. `needsCheckpoint` → `await ctx.sessions.flush(agent.session)`（:109）；flush 失败 → disarm。
2. goal 不存在 / `phase!=='active'` / `activation!=='armed'` → 返回（:123-124）。
3. `roundsStarted >= maxGoalRounds` → `ctx.goals.block(agent, ref, {code:'round-limit', message:'Goal reached its configured limit of N rounds.'})`（:125-131）。
4. `round = roundsStarted + 1`；`agent.followup(createUserMessage({content: renderGoalRoundPrompt(goal, round), source:{kind:'goal', goalId, revision, round}}))`（:132-154）。attempt 相位 `queued → claimed →(日志出现同 id 的 user/message)→ admitted`；**只有真正写进日志的 goal user/message 才推进 `roundsStarted`**，被 stale/reject 的预约不消耗轮次。followup 失败 → `block({code:'queue-failed', ...})`。

注入 prompt 的精确模板（`lib/index.js:11-16`，单个 text block）：
```
<goal_round>
Objective: ${JSON.stringify(goal.objective)}
Round: ${round}/${goal.maxGoalRounds}

Continue working toward the objective in this same session. Treat the current workspace, tool results, and durable session state as authoritative; inspect them instead of assuming earlier narration is still current. Make concrete progress and verify the result. Before claiming completion, gather evidence that the whole objective is achieved, read the current goal, and mark it complete. If work remains, leave the goal active for the next round. Follow the configured goal-tool policy before reporting a blocker.
</goal_round>
```

**pre-step 竞态闸门**（:277-341，核心拦截）：本步 messages 含 goal-sourced 消息时，先做全量预约核对（fiber/stopping/attempt.phase==='claimed'/非 stale/内容与 source 深相等/goal 当前 id+revision/active/armed/`round===roundsStarted+1`）；无效 → 清 attempt、`restoreOtherClaimed`（:95）把同批其他 claimed 消息 prepend 回 `next-step`、返回 `{kind:'reject'}`；有效 → `await next()`；`next()` 后 reject → `block({code:'prompt-rejected', message:'Goal round was rejected before entering its step.'})`；`next()` 之后再验一次；最终返回 `{...decision, startsRequestSeries: true}`（loop 据此起独立 request series，`dsh-agent-loop/lib/index.js:1018-1021`）。

停止/ disarm 条件：`turn/end{reason.kind==='max-tokens'}` → disarm（:265-267）；`aborted` 且 attempt claimed/admitted → attempt.cancelled，idle 时 pause goal；`agent/error` → disarm；teardown → 全部 disarm + 取消在跑 attempt（cause `{kind:'parent'}`）+ `await whenIdle()`。invariant 伴生：goal-sourced `user/message` 内容必须与由 durable 前缀重建的 prompt **逐字节相等**（`lib/invariant.js:48-54`）。

### 7.5 Go 落地要点（合并三节）

1. **审批**：闸门放在工具管线 pre-execute 之后、guard 之前；`ask` 必须有 turn 才能问；审计成对写、fail-closed；只有 `allowed-once` 放行且一次性。
2. **preset** = `{sandbox, approval}` 的命名打包 + 一个记录用户意图的 `permission/preset` 事件；`custom` 是派生态不可切换。
3. **sandbox_permissions**：schema enum = `['workspace-write','danger-full-access']`；执行期用 `WIDER_MODES` 严格更宽表校验；与 `justification` 成对；批准只作用于该次执行。
4. **compaction**：单锁 = 最后一条 unmatched `compaction/start`（晚于最近 `session/end-seed`）；事务 `start →（异步摘要）→ 稳定复检 →（summary + replace user/message 同步连写）→ end`，失败只允许一次 end 尝试且可故意留孤儿 start；`sourceEventSeqs` 必须含全部被 shadow 节点；system head 永不 shadow；切点必须 tool-pairing 平衡；默认 `thresholdRatio 0.8 / retainRatio 0.16 / maxTokens 8192 / retries 1 / auto true`；pruner 默认 `8192/4096/1024` code points + marker `\n\n[... tool result middle pruned ...]\n\n`。
5. **plan mode**：一个 bool 事件流 + 进程内 pending intent（turn 内切换要等被接受的 pre-step 才落盘）；无工具名单，纯 prompt section 软约束；退出走用户问答审批。
6. **goal**：事件溯源单例 + CAS(revision+1)；activation 进程本地、进程/会话启动即 disarmed；只在 idle+armed 时预约 `roundsStarted+1` 并注入固定 `<goal_round>` prompt；只有落盘的 goal user/message 消耗轮次；`goal/changed` 后 flush 检查点；轮次上界触发 `code:'round-limit'` blocker；blocked 最少连续轮数默认 3。

### 7.6 未找到 / 存疑清单（不要照猜实现）

- 审批请求送达前端的**具体 RPC 方法名与 wire 帧字段**：未在 `dsh-api-session-controller/lib/types/client/` 下定位到独立审批方法文件（应答者桥应在 `dsh-client-ui-approval` + typert 层）。仅有服务端契约（`ApprovalRequest` / `ApprovalOutcome`）可依据。
- JSONL 后端 `root` 目录名（实测为 `$DSH_HOME/sessions`）的**拼接代码位置**：未在 persistence 包内找到字面量，推测在装配层（`dsh-headless`/`dsh-base`）。
- `dsh-sandbox` 各后端（Landlock/bwrap/Windows ACL）的强制细节属沙箱实现，非执行语义，本次未展开。
