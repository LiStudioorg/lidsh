# 核心执行语义 · 第 3/4 节：Agent loop 状态机 + 工具契约

> 基础目录同 core-session.md；`$P = /usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`。
> 主循环文件：`$P/dsh-agent-loop/lib/index.js`（1936 行，esbuild 打包产物，源文件按 `//#region` 分段：inbox :14-212 / runtime-context :213-357 / assistant-stream :358-479 / tool-calls :480-714 / agent :715-1220 / constants :1221-1227 / plugin index :1228-1935）。

---

## 3. Agent loop 状态机（`ReactLoopAgent`）

### 3.1 相位（phase）模型

`phase: {kind:'idle'|'maintenance'|'running', …}`（`dsh-agent-loop/lib/index.js:766-769, 798-803`）：

- `running` 携带 `{ abort: AbortController, turn, step, wakeRequested }`（`index.js:844-849`）。
- `status` getter：idle/maintenance → `'idle'`，否则 `'running'`；`setPhase` 变化时发 `agent/status` 事件（`index.js:773-782`）。
- **一个 turn/step 共享一个 AbortController**：`phase.abort`。取消 = `phase.abort.abort(cause)`（`cancel`，`index.js:798-804`）。
- 驱动循环 `kick()`：`while (await this.turn());`，退出后回到 idle，若有 `wakeRequested && inbox.hasPending` 再唤醒（`index.js:870-881, 1002-1006`）。

### 3.2 Inbox（持久化的待处理输入队列）

- 两个有序列表：`'next-turn'`（排队等独立 turn）与 `'next-step'`（下一步边界注入）（`dsh-agent/lib/types/types.d.ts:25`；投影状态 `dsh-agent-loop/lib/index.js:21-24`）。
- 一切变更走 **durable splice**：append `agent/inbox/spliced {target,start,removedCount?,inserted}`，投影 fold 重放（`index.js:33-56`；fold 校验越界/重复 message id）。
- 对外 API（`Agent` 面，`dsh-agent/lib/types/runtime-types.d.ts`）：
  - `followup(msg)` = `send(msg,'next-turn',wake=true)` → 排队新 turn（`index.js:789-791`）
  - `steer(msg)` = `send(msg,'next-step',wake=true)` → 打断当前 turn、下一步边界生效（`index.js:792-794`）
  - `inject(msg)` = `send(msg,'next-step',wake=false)` → 不唤醒（`index.js:795-797`）
  - `cancel(cause,{keepInbox?})`：默认先 `inbox.clear()`（先清 next-step 再 next-turn，各一条 splice），再 abort 活体 phase（`index.js:798-804`；`inbox.clear` `index.js:94-97`）。
  - `runMaintenance(job)`：仅 idle 可进，phase='maintenance'，job 拿独立 signal；结束后如有挂起 wake 再唤醒（`index.js:812-824`）。
- claim（step 边界领取）：`claim(target, turn)` 先取全部 next-step，`target==='next-turn'` 时再取队首 1 条 next-turn；每条发 `agent/inbox/claimed`（`index.js:104-107`）。

### 3.3 逐步骤状态机（用户输入 → 最终回答）

**入口**：API `prompt(content, mode)` → `createUserMessage({content, source:{kind:'user'}})` → `mode==='steer' ? agent.steer : agent.followup`（`dsh-api-session-controller/lib/index.js:767-774`）。cancel RPC → `agent.cancel({kind:'user'},{keepInbox:true})`（`dsh-api-session-controller/lib/index.js:876`）。

以下全部在 `dsh-agent-loop/lib/index.js`：

```
kick()                                    :870-881  while turn()
 └ turn()                                  :919-1007
    turn = phase.turn + 1
    append "turn/start" {turn}            :926
    loop:
      step = phase.step + 1
      decision = preStep(target,{turn,step})           :937
        inbox.claim(target, turn)                      :889  (target 首轮 'next-turn')
        systemPrompt.assemble(...)                     :890  (sections/contexts/tools/vars)
        runtimeContext.project(...) → 可选 snapshot 消息 :892-893
        waterfall "agent/pre-step" {messages,turn,step,signal}
          默认决策 {kind:'enter', messages: claimed(+context)}  :894-901
        reject → return reject                        :902
      reject → turnEnds={kind:'blocked'}; end turn      :942
      空消息且有 turnEnds → break（结束 turn）          :940
      首轮空消息 → turnEnds={kind:'completed'}; end    :947
      append "step/start" {turn,step}                  :951-954
      stepEnd = await step(decision)                   :957
      finally append "step/end" {turn,step}            :960-963
      若 stepEnd 有值且 next-step 队列空:
        serial "agent/turn-stopping" {turn,signal}     :967-970   ← compaction 落盘检查点等挂这里
        break                                          :972-974
      target='next-step'（后续步骤只吃 steer）          :975
    finally append "turn/end" {turn, reason: turnEnds} :992-998
    turnEnds 语义: null→completed? 见下
 最后: !inbox.hasPending → return false（driver 退出）  :1002-1006
```

turn/end reason 决策表（`index.js:942-998`）：
- pre-step reject → `{kind:'blocked'}`（:942）
- 首轮无消息 → `{kind:'completed'}`（:947）
- 正常步后 `stepEnd` 非空且 next-step 空 → 用 stepEnd（`completed`/`max-tokens`；`max-tokens` 优先保留，:958）
- signal abort → `{kind:'aborted', reason}` 并把原错误 rethrow（:977-982）
- 其他异常 → `{kind:'error', error: LlmError.failure 或 {message:errorChain,code:'UNKNOWN'}}` + `agent/error`（:984-990, :856-866）

### 3.4 step()：一次模型调用（`index.js:1008-1125`）

```
prepareRequest(turn,step,signal)                       :1127-1164
  seed config = 已登录 header 的 proposal（剔除 adapterDefaults 字段）
                或 AgentOptions{provider,model,reasoningEffort,maxTokens}  :1138-1142
  config = waterfall "agent/request" 默认 seed          :1143-1147
  preparedCall = llm.prepareCall(config)（NO_ADAPTER 时退回裸 config） :1153-1158
systemPrompt.project(renderedPrompt, {inHistory, startsSeries})
  → 生成 system/message 的 append/replace 更新          :1019-1027  (规则见 3.6)
首次尝试: 对 decision.messages 逐条 append "user/message" (surfaceOp:'append')  :1028
request = buildRequest(config, preparedCall, tools, startsSeries, signal)  :1030, :1166-1218
  - canonicalHeader({config, adapterDefaults?, tools?}) 
  - request/header append 规则: 未登录过→reason:'initial'/'resume';
    与 baseline 不等→'change'(+startsSeries?); 等但新 series→'series'   :1176-1190
  - request/context: provider/model/contextWindow/systemPromptUpdate 变化才 append  :1192-1201
  - messages = session.deriveMessages()（deepFreeze 每条，WeakSet 记账）:1204-1210
  - 返回 markAgentLoopRequest(freeze({...config, messages, tools?, sessionId, signal})) :1211-1217
流消费:
  live = AssistantStreamAttempt(accumulator+assembler, attemptId=`${sid}:${n}`) :1031-1033, :384-390
  stream = preparedCall?.stream(request) ?? ctx.llm.stream(request)  :1036   (llm/stream waterfall 在内)
  live.start() → 每个 chunk: signal.throwIfAborted(); live.push(chunk) :1038-1043
  流抛错:
    aborted 且有可见内容 → append "assistant/message" {..., interrupted:true, usage?, stream} (surfaceOp:'append') :1048-1064
    否则 → append "assistant/attempt" {turn,step,stream}             :1065-1074
    rethrow（turn 收 aborted/error）
  正常结束:
    finish = assembler.finish（缺省 stop，dsh-llm assistant-stream.d.ts:70-78）
    finish.kind ∈ {error, aborted}:
      append "assistant/attempt"                        :1082-1087
      action = waterfall "agent/request-error" {turn,step,provider,failure,retryPolicy,signal} :1088-1095
      action.kind==='retry' → continue（回到 prepareRequest 重试同一 step） :1096-1098
      否则 throw LlmError(failure)                       :1097
    成功: message = createAssistantMessage(blocks, source{provider,model,replayState?})
      append "assistant/message" {turn,step,message,usage?,stream} (surfaceOp:'append') :1100-1114
      finish.kind==='max-tokens' → return {kind:'max-tokens'}  :1115   ← 不再执行工具
      toolCalls = content.filter(tool-call)                       :1116
      无 tool-call → return {kind:'completed'}（最终回答出现处）   :1117
      {concluded} = executeToolCalls(...) → concluded ? {completed} : null(继续下一步) :1118-1119
```

### 3.5 工具执行调度（`executeToolCalls`/`runGroup`，`index.js:512-667`）

**并发模型**（README：`dsh-agent-loop/README.md` Summary 段"maxParallelToolCalls limits concurrent parallel-safe calls, and exclusive calls retain ordering"）：

- 入参：本步 assistant 消息里的全部 `tool-call` blocks（模型序）。
- 每个 call 构造 `ToolExecutionInput {callId,name,arguments:parseArguments(raw),agent,signal}`；`parseArguments`：JSON.parse 失败保留原文字符串、空→`{}`（`index.js:540-547`）。
- 扫描指针 `next`：取第一个 call 的 `ctx.tools.executionMode(exec).kind`：
  - `'exclusive'` → 只跑它自己一组（屏障）（`index.js:527-530`；`ToolExecutionMode` `dsh-tools/lib/types/index.d.ts:226-229`）
  - `'parallel'` → 从当前位置取后缀成组，池内滚动并发
- 池参数：`maxParallelToolCalls`，**默认 10**，配置 schema `z.number().step(1).min(1).default(10)`（`index.js:559, 1226, 1465, 1491`；README 表格 `maxParallelToolCalls | 10 | Parallel-safe tool calls in flight per step; 1 is serial`）。
- 每个 call 生命周期：
  1. `append "tool/call" {turn,step,callId,name,arguments}` → 记 callSeq（`index.js:586, 688-695`）——**先落盘后派发**。
  2. `scheduler.prepare(exec)`：跑 pre-execute/guard/ask 闸（见 §4.4）→ `dispatch`/`post-result`/`final-result`（`index.js:588`）。
  3. `dispatch` → `scheduler.dispatch(exec)` 执行 body。
  4. 结果**按模型序提交**（`commitReady` 按 committed 指针顺序）：`finalize`（走 post-execute）或 `finish`（跳过）→ `append "tool/result" {turn,step,message,error?,meta?} (surfaceOp:'append', sourceEventSeqs:[callSeq])`（`index.js:571-581, 703-713`）。
  5. `result.additionalContexts` 逐条 `acceptContext` → `inbox.splice('next-step', tail, 0, [ctx])`（`index.js:578, 1118`）——工具回注上下文走 next-step 队列，下一步生效。
  6. `concluded ||= result.concludesTurn === true`（`index.js:579`）。
- 组内 reclassification：池启动每个后续 call 前重查 `executionMode`，遇 exclusive 即停（该 call 留给下一个屏障组）（`index.js:624-635`）。
- **取消语义**：abort 后停止启动新 call，等已开始的全部到达终态（drain），已完成的按模型序提交；**未启动的 call 合成 pair**：`append tool/call + tool/result{text:"Error: tool call aborted before dispatch", isError:true, error.info={name:'AbortError',code:'ABORTED_BEFORE_DISPATCH'}}`（`appendSkippedToolCall` `index.js:668-685`；剩余组外的 call 由外层 `executeToolCalls` 补齐 `index.js:533-535`）。
- 调度器内部故障：停止新派发、drain、无合成结果地 reject（`index.js:647-651`）。

### 3.6 system prompt 的 surface 治理（`SystemPromptProjection`）

- system prompt 不是请求字段，而是 **surface node 0 的 `system/message` 事件**（`dsh-session/lib/types/types.d.ts:205-207` 注释；`index.js:1023-1027`）。
- project 规则（`index.js:266-297`）：无 head → append 新 system 消息；`!inHistory || startsSeries || rendered===''` → 把非空后续节点 replace 成空 + head 不等则 replace head；否则 latest 非空节点文本相同 → 无更新，不同 → append 新 system 节点（in-history 路由允许在历史中部追加新 prompt）。
- replace 单节点写法：`{surfaceOp:{op:'replace',startSeq:seq,endSeq:seq}, sourceEventSeqs:[seq]}`（`index.js:285-297`）。
- `request/context.systemPromptUpdate === 'in-history'` 声明路由能力（`dsh-llm/lib/types/types.d.ts:313-319`）。

### 3.7 中断/取消（AbortSignal）传播链

1. 来源：HTTP/RPC cancel → `agent.cancel(cause,{keepInbox})`（`index.js:798-804`）；agent dispose（cause `{kind:'disposed'}`，`dsh-session/lib/types/types.d.ts:150-160` AgentCancelCause）。
2. `phase.abort.abort(cause)` → 该 turn 内所有持有 `signal` 的位置：turn 循环每个边界 `signal.throwIfAborted()`（`index.js:920, 931, 955, 965…`）、step 请求前后（:1012,1037,1041,1044）、工具 `exec.signal`（registry 融合 caller signal，§4.4）。
3. 已流式交付的文本保留：abort 时把 assembler 的 `interruptedBlocks()` 定稿为 `assistant/message{interrupted:true}`（`index.js:1048-1064`；README Summary "Cancellation preserves streamed text already delivered"）。
4. 未派发工具：`ABORTED_BEFORE_DISPATCH` 合成 pair（§3.5）；已开始工具：registry 等 body 到达静默后记 `TOOL_ABORTED`（`dsh-tools/lib/types/index.d.ts:353-355` 常量；`dsh-tools/lib/index.js` dispatchToolBody `finally` 融合信号）。
5. turn 收束：`turn/end {kind:'aborted', reason: signal.reason}`（`index.js:977-982`）。
6. `keepInbox:true` 时队列幸存（cancel RPC 走这条，用户可接着发新消息）。

### 3.8 LLM 重试（dsh-llm-retry 插件，独立包）

挂点：loop 在 finish error/aborted 后发 `agent/request-error` waterfall（`dsh-agent-loop/lib/index.js:1088-1095`）；插件监听并决定是否返回 `{kind:'retry'}`（`dsh-llm-retry/lib/index.js:175-178`）。

策略解析（provider 配置拥有；`dsh-llm/lib/types/retry-policy.js`）：
- `mode:'normal'`（默认）：`maxRetries=5`（:12）、`retryableCodes=['EMPTY_RESPONSE','RATE_LIMIT','SERVER','TIMEOUT','TRANSPORT']`（:16-22）、backoff `{initialDelayMs:500, maxDelayMs:10000, jitterRatio:0.1}`（:13-15）。
- `mode:'always'`：无限重试直到成功/取消/dispose（`retry-policy.d.ts:30-36`）。

执行算法（`recover`，`dsh-llm-retry/lib/index.js:151-174`）：
1. `normal` 且 `failure.code ∉ retryableCodes` → 交回 next（放弃重试）。
2. 计数是**持久化**的：投影 key `llmRetry`，key=`[provider, policyKey]`；`step/start` 或 `turn/end` 清零（:88-107）。`retry = previousRetry+1`；`normal` 且 `previousRetry >= maxRetries` → 放弃（:164）。
3. 延迟：`failure.providerRetryAfterMs` 有效且 ≤ maxDelayMs → 直接用它；> maxDelayMs 且 normal → 放弃、always → 用本地退避；无效 → 本地退避（:168-172）。
4. `localDelay = min(min(initial*2^min(retry-1,1024), max) * (1-j+2j*rand), max)`——对称抖动（:44-49）。
5. **先落盘再等待**：`append "llm/retry" {retryId,turn,step,provider,mode,policyKey,retry,maxRetries?,delayMs,failure}` → 可取消等待（abort 融合插件生命期信号）→ `append "llm/retry-started" {retryId,turn,step,retry}` → 返回 `{kind:'retry'}`（:116-150）。retryId 同链复用（`previous?.retryId ?? randomUUID()`，:166）。

### 3.9 turn 级配套

- `turnBoundary` 投影（`index.js:1300-1345`）：`{openTurnStartSeq, lastStepStartSeq, lastStepBoundary:{kind:'start'|'end',seq}, lastTurn}`，由 turn/step 事件 fold；loop 重启时 `phase.turn = lastTurn`（`index.js:765-769`）。
- 崩溃恢复：resume 时先 `interruptedTurnClosers` 补尾（`dsh-session/lib/types/repair.js`，见 core-session.md §1.8），再在其上开新 turn。
- 持久化 checkpoint 由独立插件保证（`dsh-session-checkpoint-policy`，`lib/index.js:59-77`）：`llm/stream` 前 flush、顶层 `tools/execute` 前 flush、`agent/pre-step` 后 flush——loop 本身不等 flush（`dsh-session/lib/types/types.d.ts:256-258` 注释）。

---

## 4. 工具契约（dsh-tools）

### 4.1 ToolDefinition 完整形状（`dsh-tools/lib/types/index.d.ts:106-172`）

```ts
interface ToolDefinition extends ToolSchema {            // :106 继承 name/description/parameters
  readonly output: ToolOutputDefinition                  // :108 强制的规范输出声明
  execute(args: unknown, exec: ToolRunContext): Promise<unknown>  // :119
  finalizeContent?(exec, result): ContentBlock[] | undefined      // :132 物化前最后改写
  timeoutMs?: number                                     // :139 协作超时（见 4.5），永不发给模型
  isConcurrencySafe?(args): boolean                      // :152 只有返回 true 才可并行，其余一律 exclusive
  presentCall?(args): ToolCallView | undefined           // :163 挂起态展示（纯函数）
  presentResult?(args, result: ToolResult): ToolResultView | undefined // :171 完成态展示（纯函数）
}
```

- `execute` 返回**必须是 `output.schema` 声明的 canonical lossless-JSON 值**；registry 用它做输出 schema 校验 + `render(args,value)` 生成模型 `ContentBlock[]`（`ToolOutputDefinition` = `{schema, render, presentationMeta?}`，`index.d.ts:96-104`）。
- `ToolResult`（presentResult 的入参投影，:174-189）：`{content, isError, meta?}`。
- 运行时结果 `ToolExecutionResult = ToolExecutionSuccess | ToolExecutionFailure`（:390-412）：成功 `{isError:false, value, content, meta?, additionalContexts?, concludesTurn?:true}`；失败 `{isError:true, error: ToolFailure, content, meta?, additionalContexts?}`。

### 4.2 注册与解析

- `ToolRuntime.register(definition): () => void`（:601），注册变更发 `tools/change`（:93）。
- 对模型的 schema 投影 `schemas(scope?)`：**白名单只取 name/description/parameters**（:135 注释、:676）——`timeoutMs`/presentation 永不外泄。
- 执行入口 `execute(exec: ToolExecutionInput): Promise<ToolExecutionResult>`（:730）。
- 作用域：注册可挂 agent scope（`dsh-scope`），`resolveExecution(name, agent, allowParent)` 沿 scope 链解析（子代理可有自己的工具集；`dsh-tools/lib/index.js` dispatchToolBody）。

### 4.3 执行上下文（工具如何拿到运行环境）

`ToolExecutionInput`（`index.d.ts:197-233`）：`{callId, rootCallId?, name, arguments, agent?: Agent, parent?: ToolExecutionToken, signal: AbortSignal}`；registry 补 `token/rootCallId` 成 `ToolExecution`（:261-267）；`ToolRunContext extends ToolExecution` 再加 `deferContext(UserMessage)`（结果附挂上下文，:291）与 `concludeTurn()`（终结本 turn，:299-300）。

- **工作目录/沙箱策略不在 tools 包里**：工具经 `exec.agent` → `agent.session.cwd` 自行取用；bash/fs 等再经各自 service（如 `ctx.shell`/sandbox provider）解析 mode。证据：permission preset 要求 confining executor（`dsh-permission-presets/lib/index.js:103-105`）。
- `signal` 融合规则：`tools/execute` wrapper 只能临时替换 signal，registry 派发前把 caller signal 再融合回去（:44-49 事件注释、`dsh-tools/lib/index.js` `fuseToolSignals`）。

### 4.4 执行管线（顺序固定，`dsh-tools/lib/index.js:3105-3158`）

```
createExecution → callerCancelled? → ABORTED_BEFORE_DISPATCH 结果
1. waterfall "tools/pre-execute" (exec) → PreToolDecision
     = {kind:'allow'} | {kind:'deny', reason} | {kind:'ask', reason?}   (index.d.ts:419-427)
2. ask → serviceAsk：ctx.get('approval') 缺失 → deny（reason 附注不支持）；
   approval.request({agent,toolName,callId,reason?,signal}) 结果映射：
     'allowed-once'→allow；'rejected'→deny(`the user rejected tool "X"`)；
     'cancelled'→deny；'unavailable'→deny(no approval channel)
   （dsh-tools/lib/index.js:3314-3360）
3. allow 后再过 **monotonic guard** 层：ctx.tools.guard(fn)，同步、返回 string 即拒、
   只能更严不能放行；全局层先、scope 链后（dsh-tools/lib/index.js:2807-2831）
   拒绝 → isError 结果，模型可见文本 `Error: ${reason}`（:3127-3140）
4. waterfall "tools/execute"（around-dispatch：超时/重试/指标 wrapper）→ tool.execute
5. waterfall "tools/post-execute" → PostToolDecision = accept{content?|value?} | block{feedback}
   （index.d.ts:432-446）
6. finalizeContent → 规范物化 → emit "tools/result"（只读观察者，:83）
```

### 4.5 超时策略在哪

工具级协作超时 = 独立插件 `dsh-tool-call-timeout-policy` 包 `tools/execute` wrapper：读 `ctx.tools.get(name)?.timeoutMs`，无则直通；有则 `deadline(exec.signal, timeoutMs, 'TOOL_TIMEOUT')`，仅当**自己定时器**触发才替换结果为 `tool call timed out after ${timeoutMs}ms`（`dsh-tool-call-timeout-policy/lib/types/index.d.ts:12-32`；`lib/index.js:123-131`）。bash 的进程级 timeoutMs 是工具参数自管（`dsh-tool-bash/lib/index.js:273-275`）。

### 4.6 展示契约（presentation.d.ts，全部 view union）

- `ToolCallKind = 'read'|'edit'|'delete'|'move'|'search'|'execute'|'fetch'|'other'`（`presentation.d.ts:13`）。
- `ToolCallView = GenericCallView{card:'generic',title,kind?,rawInput?,content?,locations?} | TerminalCallView{card:'terminal',…} | DiffCallView{card:'diff',diffs:FileDiff[]}`（:41-116；`FileLocation{path,line?}` :20-23，`FileDiff{path,oldText|null,newText}` :30-36）。
- `ToolResultView = GenericResultView{card:'generic'} | TerminalResultView{card:'terminal'} | DiffResultView{card:'diff'} | SearchResultView(=SearchMatches|SearchPaths, card:'search') | ReadResultView{card:'read'} | WebResultView(=WebSearch|WebFetch, card:'web')`（:130-366；WebFetch 字段 `url/statusCode/truncated` :351-366）。
- UI 桥按 `card` 标签分派，永不 switch 工具名（:37-40 注释）。

### 4.7 `dsh-tools/lib/types/index.d.ts` 定义的全部导出接口清单（逐个，带行号）

事件（declare Events）：`tools/pre-execute`(:38)、`tools/execute`(:49)、`tools/post-execute`(:61)、`tools/ptc-dispatch-log`(:75)、`tools/result`(:83)、`tools/change`(:93)。
类型/接口：`ToolOutputDefinition`（~:96-104）、`ToolDefinition`(:106)、`ToolResult`(:174)、`ToolExecutionToken`(:191)、`ToolExecutionInput`(:197)、`ToolExecutionMode`(:226)、`PtcDispatchLog`(:246)、`ToolExecution`(:261)、`ToolDispatchExecution`(:272)、`ToolRunContext`(:280)、`ScheduledToolPreparation`(:307)、`ScheduledToolDispatch`(:325)、`ToolRuntimeScheduler`(:345 internal)、`ToolErrorInfo`(:357)、`ToolFailure`(:362)、`ToolExecutionSuccess`(:390)、`ToolExecutionFailure`(:402)、`ToolExecutionResult`(:412)、`PreToolDecision`(:419)、`PostToolDecision`(:432)、`ToolPresentationMode`(:448)、`Config`(:450)、`ToolRuntime`(Service，register :601 / get :655 / schemas :676 / executionMode :688 / execute :730 / guard :2817(js) / restrict(js))。
常量：`TOOL_ABORTED='ABORTED'`(:353)、`TOOL_ABORTED_BEFORE_DISPATCH='ABORTED_BEFORE_DISPATCH'`(:355)、`TOOL_RUNTIME_SCHEDULER`(symbol :349)。
re-export：`defineTool`/schema 工具（:16）、JSON-schema 校验（:17）、TS/Py SDK 生成（:20-21）、presentation 全型（:23）。
**工具具体接口（bash/read/write/…）不在本文件定义**——每个 `dsh-tool-*` 包各自 `defineTool({...})` 后 `ctx.tools.register`；本文件只是契约中心。`defineTool` 的输入 schema 方言见 `schema.ts`（ValueSchemaSpec→JSON Schema 转换，`index.d.ts:16` re-export 名单）。

参考实例（bash 参数逐字段，`dsh-tool-bash/lib/index.js:263-294`）：`command*`、`description*`、`timeoutMs?`、`workdir?`、`run_in_background?`（能力开关才出现）、`sandbox_permissions?`（enum=ESCALATION_TARGETS）、`justification?`。
