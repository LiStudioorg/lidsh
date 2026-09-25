# DSH settings 模型与环境变量 逆向调研规格说明

> 只读调研。所有路径以 `BASE=/usr/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai` 为根；
> 外层 CLI 包记为 `OUTER=/usr/lib/node_modules/@deepseek-ai/dsh`。引用格式 `文件:行号`。
> 行号以本次调研读取的编译产物（lib/*.js、lib/**/*.d.ts）为准。

---

## 任务 A — settings 模型

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

## 任务 B — 环境变量

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
