# lidsh — DeepSeek Harness 的 Go + Vue 3 复刻

> **lidsh** 是对 [DeepSeek Harness](https://github.com/deepseek-ai/dsh)（DSH）的忠实复刻：Go 后端 + Vue 3 前端，目标是"完美还原" DSH 的架构与行为。后端事件溯源、配置树、agent 循环、工具契约全部按 DSH 的 Cordis 插件/分层 patch/typert 语义逐条复刻；前端用 Vue 3 重写并像素级照抄布局与 CSS 变量。

## 为什么复刻、复刻什么

DSH 不是一个"CLI + 网页"，而是一套由五层拼起来的系统：

```
一个通用插件框架 (Cordis)
  + 一套声明式配置树组装 (cordis.patch.yml 分层 patch)
  + 一套类型化 RPC 复用层 (typert: zod codec → WS mux)
  + 一个 agent kernel (session 事件溯源 + agent loop + 20 个工具)
  + 一个 React 前端（45 个 ui-* 包，81KB CSS）
```

标题里"完美复刻"指的不是照抄 React 前端，而是复刻**架构语义**：分层可覆盖的配置树、类型化的远程流、事件溯源的会话模型、agent 循环状态机。前端用 Vue 3 重写，但布局尺寸、CSS 变量、交互行为与 DSH 对齐。逆向依据是 DSH 0.1.5-rc.3（230 个包 / 约 32 万行编译产物），详见 [`docs/`](#文档)。

对于你关心的"编译成静态网页变二进制"：Vue 3 是编译器优先框架，Vite `build` 出纯静态产物，经 `go:embed` 塞进 Go 服务，最终得到一个**单二进制**，前端资源只在二进制里只读嵌入。

## 当前进度

| 里程碑 | 内容 | 状态 |
|---|---|---|
| **M0** | 仓库骨架、patch 配置引擎 + `--dump-config`、服务容器、CLI 文法、逆向文档 | ✅ 完成 |
| **M1a** | session JSONL+zstd 存储、agent 循环（turn/step 状态机）、工具集（bash/read/write/edit/glob/grep）、headless 一次对话入口、端到端持久化测试 | ✅ 完成 |
| **M1b** | `/api/remote.mux` WS mux + HTTP 一元 RPC 回环 + 会话控制器；会话 JSONL+zstd 持久化写路径、`$events` 审批瀑布回环（`$events/result`）、附件文件上传接收 | ✅ 完成 |
| **M1c** | Vue 三栏 UI：消息流、工具卡片、会话列表、设置-模型页、双主题 | ⬜ |
| **M2** | compaction、goal/ralph、workflow、sandbox 提权、schedule | ⬜ |

当前所有能跑的包都有单元测试锁定语义（`go test ./...` 全绿），headless 入口与 `lidsh web` 的 WS/HTTP 会话环都用可注入的假 LLM 适配器做了端到端验证（会话创建 → agent 跑 → 事件 zstd 落盘 → 回读；create→follow→prompt→snapshot+event；上传→收据→内容寻址存储）。

## 目录结构

```
cmd/lidsh/            CLI launcher（--profile/--patch/--dump-config/--from-default-profile）
internal/
  config/             patch 引擎：Entry/Patch/Layer/ApplyEntryPatches/Compose + !!js 表达式求值
  config/patches/     base/web/headless 等内置 bundle 层的常量 YAML
  container/          依赖注入容器（替代 Cordis Service/inject）+ 事件总线
  session/            事件溯源会话：Event/Header/surface 折叠/JSONL+zstd 追加/flock 锁/撕裂恢复
  llm/                provider 抽象 + OpenAI/DeepSeek 兼容适配器（SSE 泵、7 种 StreamChunk）
  agent/              agent 循环：turn/step 状态机、流聚合 assembly、工具调度、崩溃恢复
  tools/              工具注册表 + bash/read/write/edit/glob/grep（复刻 dsh-tools 契约）
  app/                profile 装配、Boot、headless 入口
  protocol/           typert 远程流帧结构（open/cancel/item/end/error/ready/emit/waterfall）
  server/, webui/     （M1b/M1c 落地）
docs/                 DSH 逆向文档（00-architecture / integrations / ui-tokens / _parts 分块）
ref/                  DSH 参考源码拷贝（gitignored，逆向用）
```

## 构建与运行

环境：Go 1.23+、Node.js 20+ 与 pnpm（仅构建前端需要）。本仓库用的 `go` 若指向旧版本，用 `export PATH=/usr/local/go/bin:$PATH` 切换。

```bash
# 全量测试（不联网，全部本地）
go test ./...

# 构建 CLI
go build -o bin/lidsh ./cmd/lidsh

# 打印组装后的配置树（不启动）——复刻 DSH 的 --dump-config
bin/lidsh --profile web --dump-config
# 打印内置层组装结果
bin/lidsh --dump-default-config
```

> 前端 embed 的单二进制构建链（`web/` Vite 构建 → `go:embed`）属 M1c，届时提供 `make release`。目前先跑 M1a 的 headless 入口。

### headless 一次对话（M1a 已可用，无需 GUI）

```bash
export LIDSH_API_KEY=sk-...            # DeepSeek key（或写进 .env）
export LIDSH_BASE_URL=https://api.deepseek.com
export LIDSH_MODEL=deepseek-chat       # 默认 deepseek-chat
bin/lidsh --profile headless "帮我看看当前目录有什么"

# 或从 stdin 读提示词
echo "列出 ./internal 的包" | bin/lidsh --profile headless
```

headless 会：创建会话 → 逐步执行 bash/文件工具 → 把最终回答写到 stdout，事件流以 zstd 压缩的 JSONL 落盘到 `$LIDSH_HOME`（默认 `~/.lidsh`）。

### 配置环境

- `$LIDSH_HOME`（默认 `~/.lidsh`）— 会话、设置、profile 的根目录。对应 DSH 的 `$DSH_HOME`，但独立命名以免误读真实 DSH 配置。
- `.env`（项目目录）— 启动时读取；黑名单键（`PATH`/`HOME`/`NODE_OPTIONS`/`LD_PRELOAD`/`BASH_ENV`…）拒绝写入，忠实复刻 DSH 的引导期 env 保护。

## CLI 文法（复刻 DSH launcher）

```
lidsh --profile <name> [app args...]
lidsh --profile <name> --from-default-profile <template>
lidsh web [args...]                        # --profile web 别名
lidsh --profile <name> --dump-config       # 打印配置树，不启动
lidsh --dump-default-config                # 打印内置层组装
lidsh --profile <name> --patch <yaml>      # 追加覆盖层（可重复）
```

launcher 只解析自己的 flag，遇到第一个不认识的 token 起全部交给 profile 内的应用解析（与 DSH 一致）。非法命令、跨模式选项、配置错误、启动失败一律非零退出。

## 架构语义要点

- **分层 patch 配置树**（复刻 `applyEntryPatches`）：patch 按**整行覆盖**（`config` 整体替换，不做深合并）、`insert` 进 group、`name` 守卫不匹配即跳过、未命中只 warn 不失败。`--dump-config` 与启动共用同一函数，保证 dump 永不漂移。
- **会话 = 事件溯源**：append-only JSONL（每批一个 zstd 帧），`seq` = 日志下标、`time` = epoch 毫秒；LLM 消息历史是 surface 投影，只有 4 种事件能产生消息。
- **agent 循环**：turn/step 状态机，一次模型调用 = 一个 step；工具经 `tool/call → execute → tool/result` 落盘并带 `sourceEventSeqs` 引用。
- **LLM 抽象**：`stream(GenerateOptions) → 7 种 StreamChunk`，usage 在 finish 之前、finish 收尾、可重试码集合对齐 DSH。

## 文档

逆向过程沉淀在 `docs/`，全部标注"已在产物体中验证过的事实"并附 DSH 文件行号：

- [`docs/00-architecture.md`](docs/00-architecture.md) — 架构总纲、三层架构、技术选型、里程碑
- [`docs/integrations.md`](docs/integrations.md) — LLM 抽象 / DeepSeek 协议 / WS 帧格式 / MCP / skills / 附件 / subagent / 沙箱
- [`docs/ui-tokens.md`](docs/ui-tokens.md) — CSS 变量（亮/暗）/ 字体 / 布局尺寸 / 组件视觉规格（M1c 用）
- [`docs/_parts/`](docs/_parts/) — 按主题分块的逆向文档（ws-protocol / llm-core / mcp-skills / settings-env / subagent-workflow / bash-sandbox / attachment-token / core-session / core-loop / core-control）

## 归属

复刻过程中严格对照 DSH 的实测行为与源码（`ref/` 保留参考拷贝），语义文档逐条标注 DSH 文件行号，不以主观猜测替代可验证事实；未验证处明确标注"待逆向"。

---

License: [MIT](LICENSE)（与原始 DSH 的许可约定一致；本仓库为逆向复刻实现，非原始 DSH 代码）。
