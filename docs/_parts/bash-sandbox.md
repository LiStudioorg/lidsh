# bash 工具与沙箱机制逆向规格

> 调研基线：`BASE=/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`（编译后 JS + .d.ts，只读）。
> 所有行号引用形如 `文件:行号`，均相对 BASE。本文为只读调研产物，未修改任何源码。

---

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
