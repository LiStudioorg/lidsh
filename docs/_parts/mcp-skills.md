# MCP 接入与 Skills 机制逆向规格说明

> 调研对象：`$BASE = /usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai`（编译后 JS + .d.ts，只读）。
> 所有行号均为该目录下编译产物的行号。文中「源码」指 `lib/index.js`（bundled 后含 `//#region` 源文件标记）。

---

## 任务 A — MCP 接入（`$BASE/dsh-mcp-client`）

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

## 任务 B — Skills 机制

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

### 未找到项

- `settings.yaml`（`~/.dsh/settings.yaml`，`$BASE/dsh-settings-file/lib/index.js:27,32`）中 **无** MCP/skill 相关字段 —— MCP/skill 配置均走 cordis 组合文件，「settings 中的 mcp 字段」不存在。
- 旧式独立 **SSE transport** 配置：未找到（仅 SDK 内有 `sse.js`，未被引用）。
- MCP **resources / prompts** 桥接：未找到（明确 deferred）。
- `dsh-skill` 的 `SkillLayer` 跨层「nearest layer wins」仅注释级描述，运行时细节以 `ScopedLayers`（`dsh-scope`）为准，未逐行展开。
