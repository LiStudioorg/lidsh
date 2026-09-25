# DeepSeek Harness Web 前端 —— 设计系统规格（像素级复刻用）

> 提取时间对应已安装版本 `@deepseek-ai/dsh@0.1.5-rc.3`（`dsh-client-ui-theme` 版本亦为 `0.1.5-rc.3`，见 `dsh-client-ui-theme/package.json`）。
> **本文档所有数值均直接来自文件内容**，每条均标注来源文件与行号/字节区间。凡是产物里不存在的，明确写「未找到」。

---

## 0. 阅读前必知：CSS 的真实分布（与你的预期不同）

任务给出的两份构建产物 CSS **只包含整个设计系统的一小部分**。实测结论：

| 载体 | 路径 | 内容 | 规模 |
|---|---|---|---|
| 构建产物 CSS | `dsh-web-frontend/dist/assets/index-DPX2bQLO.css` | **单行压缩**，441 条规则，其中仅 **65 条**自定义属性声明（37 个唯一变量）。内容是**共享 UI 原语**（Button/Tag/Menu/Dialog/Tooltip/Toast/CodeBlock/TerminalBlock/ReadBlock/DiffBlock/SearchBlock/WebBlock/JsonTree/Markdown/DockKit 多面板）。类名 hash：`1fywu boot`、`1tljr StateDot`、`luwio GenericRow`、`cfgyt Button`、`e3ygd Pill`、`brmue Tag`、`1vyxu Switch`、`1g6ru SearchInput`、`1nxmc Menu`、`1b2ny HoverCard`、`w1urq Dialog`、`1cfrq Onboarding`、`1nu42 Confirmation`、`1ycze Meter`、`1wejo FileTypeIcon`、`z12h9 InlineChip`、`1nw3t Tooltip`、`e5v0f Toast`、`4qrvp JsonTree`、`1gdtu TerminalBlock`、`onbk6 ReadBlock`、`12o37 DiffBlock`、`1h7p4 SearchBlock`、`rsn9u CodeBlock`、`kcgor Markdown`、`19q7d WebBlock`、`1fdcq details`、`17p4l DockKit` | 51 947 字节 / 1 行 |
| 构建产物 CSS | `dsh-web-frontend/dist/assets/vendor-BNsW4eBh.css` | **纯 KaTeX**：20 个 `@font-face`（KaTeX_* 字体）+ `.katex*` 选择器。**不含任何自定义属性**（`grep -o -- '--[a-z-]*:' vendor-BNsW4eBh.css` 结果为 0） | 29 288 字节 / 1 行 |
| **运行时注入样式（重点）** | 各插件 `*/lib/client.js` 内联字符串常量 | **108 张样式表 / 280 155 字节**，运行时由插件用 `document.createElement("style")` + `dataset.pluginCss` 注入 `<head>`。**全部颜色 token、字号阶梯、布局骨架、组件规格都在这里** | 见 §0.1 |
| 主题 token 权威表 | `dsh-client-ui-theme/lib/client.js` | 6 张全局样式表：`base.css` / `corner-shape.css` / `design-platform.css` / `scrollbar.css` / `gradient-shadow-text.css` / `shiki.css` | 31 400 字节 |

因此本文的行号引用分两类：
- **单行压缩文件**（dist CSS）用**字节偏移区间**（字符索引，0-based）标注；
- **多行 `lib/client.js`**（tab 缩进的 bundle，每张样式表是一行 `const css = "…"`）用**行号**标注。

### 0.1 108 张运行时样式表的位置索引（本报告引用的关键项）

来源标记 = 插件 `lib/client.js` 中的行号（该行 `const css…= "…"` 的完整压缩内容即为对应 `*.module.css`）。

| 逻辑样式表（源码路径） | 安装包内位置 |
|---|---|
| `packages/client/ui-theme/src/styles/base.css` | `dsh-client-ui-theme/lib/client.js:1046` |
| `…/ui-theme/src/styles/corner-shape.css` | `dsh-client-ui-theme/lib/client.js:1049` |
| `…/ui-theme/src/styles/design-platform.css` | `dsh-client-ui-theme/lib/client.js:1052` |
| `…/ui-theme/src/styles/scrollbar.css` | `dsh-client-ui-theme/lib/client.js:1055` |
| `…/ui-theme/src/styles/gradient-shadow-text.css` | `dsh-client-ui-theme/lib/client.js:1058` |
| `…/ui-theme/src/styles/shiki.css` | `dsh-client-ui-theme/lib/client.js:1061` |
| `…/ui-layout/src/client/AppFrame.module.css` | `dsh-client-ui-layout/lib/client.js:70` |
| `…/ui-sidebar/src/client/SidebarRoot.module.css` | `dsh-client-ui-sidebar/lib/client.js:27` |
| `…/ui-workspace/src/client/rows/Rows.module.css` | `dsh-client-ui-workspace/lib/client.js:554` |
| `…/ui-conversation/src/client/skeleton/ConversationRoot.module.css` | `dsh-client-ui-conversation/lib/client.js:14651` |
| `…/ui-conversation/src/client/skeleton/InputBar.module.css` | `dsh-client-ui-conversation/lib/client.js:15756` |
| `…/ui-conversation/src/client/skeleton/HeroShell.module.css` | `dsh-client-ui-conversation/lib/client.js:14498` |
| `…/ui-conversation/src/client/input/editor/composer-editor.module.css` | `dsh-client-ui-conversation/lib/client.js:12220` |
| `…/ui-chat/src/client/chat/MessageItem.module.css` | `dsh-client-ui-chat/lib/client.js:154` |
| `…/ui-chat/src/client/chat/ChatView.module.css` | `dsh-client-ui-chat/lib/client.js:1507` |
| `…/ui-chat/src/client/chat/ReasoningRow.module.css` | `dsh-client-ui-chat/lib/client.js:2847` |
| `…/ui-chat/src/client/chat/AssistantMarkdown.module.css` | `dsh-client-ui-chat/lib/client.js:2933` |
| `…/ui-chat/src/client/chat/MessageIconActions.module.css` | `dsh-client-ui-chat/lib/client.js:1003` |
| `…/ui-tool/src/client/tool/components/ToolRow.module.css` | `dsh-client-ui-tool/lib/client.js:1143` |
| `…/ui-tool/src/client/tool/ToolCallTree.module.css` | `dsh-client-ui-tool/lib/client.js:1431` |
| `…/ui-tool/src/client/tool/toolviews/bash-sample.module.css` | `dsh-client-ui-tool/lib/client.js:1696` |
| `…/ui-sidebar-right/src/client/shell/SidebarRight.module.css` | `dsh-client-ui-sidebar-right/lib/client.js:607` |

（其余 86 张同规律，全部位于对应插件 `lib/client.js` 的 `//#region \0dsh-css:` / `\0dsh-inline-css:` 标记之后。）

### 0.2 主题切换机制（复刻时唯一需要实现的状态）

- 亮/暗的唯一开关是 **`<body>` 上的 `data-ds-dark-theme` 属性**（存在=暗色）。同时 `<html>` 上写内联 `style="color-scheme: light|dark"`。
  来源：`dsh-client-ui-layout/lib/client.js:443`（`const DARK_ATTRIBUTE = "data-ds-dark-theme"`）与 `:464–472`（`apply()`：`document.documentElement.style.colorScheme = scheme; if (scheme === "dark") body.setAttribute(DARK_ATTRIBUTE,"") else body.removeAttribute(DARK_ATTRIBUTE)`）。
- **产物 CSS 中不存在** `[data-theme=...]`、`.dark`、`prefers-color-scheme` 形式的主题分支（对 `index-DPX2bQLO.css`/`vendor-BNsW4eBh.css` grep 结果均为 0 命中）。唯一使用 `prefers-color-scheme` 的地方是 JS 里解析 `system` 偏好的 `matchMedia("(prefers-color-scheme: dark)")`（`dsh-client-ui-theme/lib/client.js:1256`），它**不产生 CSS 分支**，只决定 `data-ds-dark-theme` 是否出现。
- 正文字号轴：`<body style="--dsh-content-font-size: 14px">`（同一 `apply()`，`dsh-client-ui-layout/lib/client.js:472`）。取值范围 **12–17 px，默认 14**（`dsh-client-ui-theme/lib/client.js:906`：`Schema.number().step(1).min(12).max(17).default(14)`）。
- 派生增量：`--dsh-content-font-delta: calc(var(--dsh-content-font-size,14px) - 14px)`；低一档字号 `--dsh-content-font-size-secondary: min(calc(… - 1px), max(13px, calc(… - 2px)))`（即设置 ≤14 时为 +1，>14 时为 −2；默认下 =13px），来源 `dsh-client-ui-theme/lib/client.js:1058`。
- 第三方主题扩展：`ThemePresenter.apply()` 之后会把 `snapshot.active.tokens` 逐个 `body.style.setProperty()` 覆写别名 token（`dsh-client-ui-layout/lib/client.js:473–477`），即内联样式优先级压过样式表。内置只有 `light`/`dark` 两个 id，`tokens` 均为空对象（`dsh-client-ui-theme/lib/client.js:1122–1130`）。
- 插件加载前（首帧）的调色板引导：文档 `dsh-client-ui-theme/README.zh.md`（「插件前调色板」一节）描述宿主把设置嵌入 index 响应并在首帧前设置 `color-scheme`、`body[data-ds-dark-theme]`、`--dsh-content-font-size`；但**安装包里找不到该宿主注入代码**（`dsh-host-frontend-static/lib/index.js` 全文 3 898 字节，grep `data-ds-dark-theme` 无命中），实际下发的 `dsh-web-frontend/dist/index.html` 是**不含任何主题脚本**的裸壳（只有 `<div id="root">` 与两条 CSS link）。**未找到**首帧引导代码本体 —— 复刻时不必实现。

### 0.3 圆角 + corner-shape 全局机制

`dsh-client-ui-theme/lib/client.js:1049`：

```css
@supports (corner-shape: superellipse(1.5)) {
  :root { --dsw-corner-shape: superellipse(1.5) }
}
*, *::before, *::after { corner-shape: var(--dsw-corner-shape) }
```

即所有圆角默认被平滑为超椭圆；需要正圆/胶囊的组件必须显式 `corner-shape: round` 与半径成对出现（产物中大量 `border-radius:50%;corner-shape:round`、`border-radius:999px;corner-shape:round` 即此约定）。**复刻到 Vue 时若目标浏览器不支持 `corner-shape`，这段整段跳过即可（自动降级为普通圆弧）。**

---

## 1. CSS 自定义属性全量清单

**总量核对**：对「两份 dist CSS + 108 张运行时样式表」全文做原始计数 `--[a-z0-9-]+\s*:` 得 **730** 处自定义属性声明；逐条解析后同样 **730** 条（100% 覆盖），**441 个唯一变量名**。前缀分布：`--dsw-*` 358、`--dsh-*` 31、`--dsl-*` 19、`--shiki-*` 11、`--json-tree-*` 7、`--ds-*` 5、`--trajectory-*` 3、`--deliverable-*` 2、`--turn-*` 2、`--meter-*`/`--produced-*`/`--request-*` 各 1。

命名分层：`--ds-*`（最底层常量：字体族/缓动/时长）→ `--dsw-static-*`（原始色板）→ `--dsw-alias-*`（语义别名，随主题切换）→ `--dsw-specific-*`（局部语义）→ `--dsw-font-*`（排版 token）→ `--dsh-*`（运行时动态轴与局部变量）→ `--dsl-*`（工具/代码块级局部）。

### 1.1 静态色板 `--dsw-static-*`（73 个）

来源：`dsh-client-ui-theme/lib/client.js:1052`（`design_platform_css_default`，即 `design-platform.css`）。亮色块选择器 `body`，暗色块选择器 `body[data-ds-dark-theme]`。
**关键事实：静态色板两主题几乎完全相同**——逐 token diff 后仅 **1 处**不同：`--dsw-static-neutral-bluish-60` 亮 `#f5f6f7` / 暗 `#f9fafb`。主题切换全部由别名层完成。


（下表由脚本从 `dsh-client-ui-theme/lib/client.js:1052` 解析生成，73 个 token；「暗色覆盖」仅 `neutral-bluish-60` 一处非「同」。）

| 色阶 | token | 值（两主题相同，除注明） | 暗色覆盖 |
|---|---|---|---|
| amber · 100 | `--dsw-static-amber-100` | `#fef5e7` | 同 |
| amber · 400 | `--dsw-static-amber-400` | `#f7ad31` | 同 |
| amber · 500 | `--dsw-static-amber-500` | `#f59e0b` | 同 |
| amber · 600 | `--dsw-static-amber-600` | `#dd8629` | 同 |
| amber · 900 | `--dsw-static-amber-900` | `#27241f` | 同 |
| blue · 100 | `--dsw-static-blue-100` | `#dbeafe` | 同 |
| blue · 300 | `--dsw-static-blue-300` | `#93c5fd` | 同 |
| blue · 400 | `--dsw-static-blue-400` | `#60a5fa` | 同 |
| blue · 450 | `--dsw-static-blue-450` | `#4d93f8` | 同 |
| blue · 50 | `--dsw-static-blue-50` | `#eff6ff` | 同 |
| blue · 500 | `--dsw-static-blue-500` | `#3b82f6` | 同 |
| blue · 50p | `--dsw-static-blue-50p` | `#eaf3ff` | 同 |
| blue · 600 | `--dsw-static-blue-600` | `#2563eb` | 同 |
| blue · 75 | `--dsw-static-blue-75` | `#e5f0ff` | 同 |
| blue · 800 | `--dsw-static-blue-800` | `#1e40af` | 同 |
| blue · 900 | `--dsw-static-blue-900` | `#0e3074` | 同 |
| blue · 950 | `--dsw-static-blue-950` | `#172554` | 同 |
| deepseek · 100 | `--dsw-static-deepseek-100` | `#e4edfd` | 同 |
| deepseek · 200 | `--dsw-static-deepseek-200` | `#d3e2ff` | 同 |
| deepseek · 300 | `--dsw-static-deepseek-300` | `#b7c8fe` | 同 |
| deepseek · 400 | `--dsw-static-deepseek-400` | `#679efe` | 同 |
| deepseek · 450 | `--dsw-static-deepseek-450` | `#5686fe` | 同 |
| deepseek · 50 | `--dsw-static-deepseek-50` | `#edf3fe` | 同 |
| deepseek · 500 | `--dsw-static-deepseek-500` | `#4176e6` | 同 |
| deepseek · 600 | `--dsw-static-deepseek-600` | `#4868b2` | 同 |
| deepseek-700 · delete | `--dsw-static-deepseek-700-delete` | `#2f4c8f` | 同 |
| deepseek · 800 | `--dsw-static-deepseek-800` | `#34415b` | 同 |
| deepseek · 900 | `--dsw-static-deepseek-900` | `#283142` | 同 |
| green · 100 | `--dsw-static-green-100` | `#e6faed` | 同 |
| green · 400 | `--dsw-static-green-400` | `#4ed17e` | 同 |
| green · 500 | `--dsw-static-green-500` | `#22c55e` | 同 |
| green · 900 | `--dsw-static-green-900` | `#233c2c` | 同 |
| neutral · 00 | `--dsw-static-neutral-00` | `#fff` | 同 |
| neutral · 100 | `--dsw-static-neutral-100` | `#f5f5f5` | 同 |
| neutral · 1000 | `--dsw-static-neutral-1000` | `#000` | 同 |
| neutral · 150 | `--dsw-static-neutral-150` | `#ededed` | 同 |
| neutral · 200 | `--dsw-static-neutral-200` | `#e5e5e5` | 同 |
| neutral · 250 | `--dsw-static-neutral-250` | `#dcdcdc` | 同 |
| neutral · 300 | `--dsw-static-neutral-300` | `#d4d4d4` | 同 |
| neutral · 400 | `--dsw-static-neutral-400` | `#a2a4a6` | 同 |
| neutral · 50 | `--dsw-static-neutral-50` | `#fafafa` | 同 |
| neutral · 500 | `--dsw-static-neutral-500` | `#7f8287` | 同 |
| neutral · 550 | `--dsw-static-neutral-550` | `#65676b` | 同 |
| neutral · 600 | `--dsw-static-neutral-600` | `#545557` | 同 |
| neutral · 700 | `--dsw-static-neutral-700` | `#3c3c3d` | 同 |
| neutral · 800 | `--dsw-static-neutral-800` | `#292929` | 同 |
| neutral · 850 | `--dsw-static-neutral-850` | `#212123` | 同 |
| neutral · 900 | `--dsw-static-neutral-900` | `#0f0f0f` | 同 |
| neutral-bluish · 00 | `--dsw-static-neutral-bluish-00` | `#fff` | 同 |
| neutral-bluish · 100 | `--dsw-static-neutral-bluish-100` | `#ebeef2` | 同 |
| neutral-bluish · 1000 | `--dsw-static-neutral-bluish-1000` | `#0f1115` | 同 |
| neutral-bluish · 150 | `--dsw-static-neutral-bluish-150` | `#e9ecf2` | 同 |
| neutral-bluish · 200 | `--dsw-static-neutral-bluish-200` | `#e1e5ee` | 同 |
| neutral-bluish · 300 | `--dsw-static-neutral-bluish-300` | `#cfd3d6` | 同 |
| neutral-bluish · 400 | `--dsw-static-neutral-bluish-400` | `#adb2b8` | 同 |
| neutral-bluish · 50 | `--dsw-static-neutral-bluish-50` | `#f9fafb` | 同 |
| neutral-bluish · 500 | `--dsw-static-neutral-bluish-500` | `#979da6` | 同 |
| neutral-bluish · 60 | `--dsw-static-neutral-bluish-60` | `#f5f6f7` | **`#f9fafb`** |
| neutral-bluish · 600 | `--dsw-static-neutral-bluish-600` | `#81858c` | 同 |
| neutral-bluish · 700 | `--dsw-static-neutral-bluish-700` | `#61666b` | 同 |
| neutral-bluish · 75 | `--dsw-static-neutral-bluish-75` | `#f1f3f5` | 同 |
| neutral-bluish · 750 | `--dsw-static-neutral-bluish-750` | `#43454a` | 同 |
| neutral-bluish · 800 | `--dsw-static-neutral-bluish-800` | `#353638` | 同 |
| neutral-bluish · 850 | `--dsw-static-neutral-bluish-850` | `#2c2c2e` | 同 |
| neutral-bluish · 875 | `--dsw-static-neutral-bluish-875` | `#232324` | 同 |
| neutral-bluish · 900 | `--dsw-static-neutral-bluish-900` | `#1b1b1c` | 同 |
| neutral-bluish · 950 | `--dsw-static-neutral-bluish-950` | `#151517` | 同 |
| red · 100 | `--dsw-static-red-100` | `#fee2e2` | 同 |
| red · 400 | `--dsw-static-red-400` | `#f25a5a` | 同 |
| red · 50 | `--dsw-static-red-50` | `#fef2f2` | 同 |
| red · 500 | `--dsw-static-red-500` | `#ef4444` | 同 |
| red · 600 | `--dsw-static-red-600` | `#ec1313` | 同 |
| red · 900 | `--dsw-static-red-900` | `#570c0c` | 同 |



### 1.2 语义别名 `--dsw-alias-*` / `--dsw-specific-*`（90 个，**主题切换的全部所在**）

来源：同 `design-platform.css`（`dsh-client-ui-theme/lib/client.js:1052`）。亮色块 = 选择器 `body`，暗色块 = 选择器 `body[data-ds-dark-theme]`。
「计算值」= 把 `var(--dsw-static-*)` 代入后得到的最终值（即 `getComputedStyle` 的结果）。**复刻时建议直接采用两列计算值，用 `[data-ds-dark-theme]` 切换。**

| token | 亮色（计算值） | 暗色（计算值） | 引用链 | 备注 |
|---|---|---|---|---|
| **背景 `bg`** | | | | |
| `--dsw-alias-bg-base` | `#fff` | `#151517` | `static:neutral-bluish-00` / `static:neutral-bluish-950` |  |
| `--dsw-alias-bg-layer-1` | `#fff` | `#232324` | `static:neutral-bluish-00` / `static:neutral-bluish-875` |  |
| `--dsw-alias-bg-layer-2` | `#fff` | `#2c2c2e` | `static:neutral-bluish-00` / `static:neutral-bluish-850` |  |
| `--dsw-alias-bg-layer-3` | `#fff` | `#353638` | `static:neutral-bluish-00` / `static:neutral-bluish-800` |  |
| `--dsw-alias-bg-mask-1` | `#0000003d` | `#00000080` | — |  |
| `--dsw-alias-bg-mask-2` | `#0000001f` | `#0003` | — |  |
| `--dsw-alias-bg-mask-3` | `#0000007a` | `#0000007a` | — | 两主题同值 |
| `--dsw-alias-bg-mask-drop` | `#ffffffb3` | `#272730b3` | — |  |
| `--dsw-alias-bg-mask-photo` | `#000000e0` | `#000000e0` | — | 两主题同值 |
| `--dsw-alias-bg-module-platform` | `#f5f6f7` | `#353638` | `static:neutral-bluish-60` / `static:neutral-bluish-800` |  |
| `--dsw-alias-bg-multi-select` | `#f5f6f7` | `#212123` | `static:neutral-bluish-60` / `static:neutral-850` |  |
| `--dsw-alias-bg-overlay` | `#e9ecf2` | `#61666b` | `static:neutral-bluish-150` / `static:neutral-bluish-700` |  |
| `--dsw-alias-bg-skeleton` | `#0000000a` | `#ffffff14` | — |  |
| **描边 `border`** | | | | |
| `--dsw-alias-border-inverted` | `#0000` | `#ffffff0f` | — |  |
| `--dsw-alias-border-inverted2` | `#0000` | `#ffffff14` | — |  |
| `--dsw-alias-border-l1` | `#0000000a` | `#ffffff0f` | — |  |
| `--dsw-alias-border-l2` | `#0000001a` | `#ffffff1f` | — |  |
| `--dsw-alias-border-l2-darkmode-thin` | `#0000001a` | `#ffffff0f` | — |  |
| `--dsw-alias-border-l3` | `#0000001f` | `#ffffff29` | — |  |
| `--dsw-alias-border-l4` | `#00000029` | `#fff3` | — |  |
| **文字 `label` / 链接 `link`** | | | | |
| `--dsw-alias-label-caption` | `#adb2b8` | `#81858c` | `static:neutral-bluish-400` / `static:neutral-bluish-600` |  |
| `--dsw-alias-label-dimmed` | `#e1e5ee` | `#43454a` | `static:neutral-bluish-200` / `static:neutral-bluish-750` |  |
| `--dsw-alias-label-primary` | `#0f1115` | `#f9fafb` | `static:neutral-bluish-1000` / `static:neutral-bluish-50` |  |
| `--dsw-alias-label-primary-bluish` | `#0e3074` | `#f9fafb` | `static:blue-900` / `static:neutral-bluish-50` |  |
| `--dsw-alias-label-primary-dimmed` | `#151517` | `#ebeef2` | `static:neutral-bluish-950` / `static:neutral-bluish-100` |  |
| `--dsw-alias-label-primary-foreground` | `#fff` | `#0f1115` | `static:neutral-bluish-00` / `static:neutral-bluish-1000` |  |
| `--dsw-alias-label-primary-inverted` | `#fff` | `#353638` | `static:neutral-bluish-00` / `static:neutral-bluish-800` |  |
| `--dsw-alias-label-secondary` | `#61666b` | `#cfd3d6` | `static:neutral-bluish-700` / `static:neutral-bluish-300` |  |
| `--dsw-alias-label-tertiary` | `#81858c` | `#adb2b8` | `static:neutral-bluish-600` / `static:neutral-bluish-400` |  |
| `--dsw-alias-link` | `#4176e6` | `#679efe` | `static:deepseek-500` / `static:deepseek-400` |  |
| **品牌 `brand`** | | | | |
| `--dsw-alias-brand-primary` | `#0f1115` | `#f9fafb` | `static:neutral-bluish-1000` / `static:neutral-bluish-50` |  |
| `--dsw-alias-brand-primary-invert` | `#0f1115` | `#f9fafb` | `static:neutral-bluish-1000` / `static:neutral-bluish-50` |  |
| `--dsw-alias-brand-primary-new-colorprimary-new-color` | `#4176e6` | `#5686fe` | `#4176e6` / `static:deepseek-450` |  |
| `--dsw-alias-brand-text` | `#0f1115` | `#f9fafb` | `static:neutral-bluish-1000` / `static:neutral-bluish-50` |  |
| **按钮 `button`** | | | | |
| `--dsw-alias-button-contrast-fill` | `#61666b` | `#f9fafb` | `static:neutral-bluish-700` / `static:neutral-bluish-50` |  |
| `--dsw-alias-button-elevated-fill` | `#fff` | `#43454a` | `static:neutral-bluish-00` / `static:neutral-bluish-750` |  |
| `--dsw-alias-button-floating-fill` | `#fff` | `#2c2c2e` | `static:neutral-bluish-00` / `static:neutral-bluish-850` |  |
| `--dsw-alias-button-floating-hover` | `#f1f3f5` | `#353638` | `static:neutral-bluish-75` / `static:neutral-bluish-800` |  |
| `--dsw-alias-button-ghost-active-border` | `#979da6` | `#81858c` | `static:neutral-bluish-500` / `static:neutral-bluish-600` |  |
| `--dsw-alias-button-ghost-active-fill` | `#ebeef2` | `#43454a` | `static:neutral-bluish-100` / `static:neutral-bluish-750` |  |
| `--dsw-alias-button-ghost-active-hover` | `#e9ecf2` | `#61666b` | `static:neutral-bluish-150` / `static:neutral-bluish-700` |  |
| `--dsw-alias-button-info-fill` | `#4176e6` | `#679efe` | `static:deepseek-500` / `static:deepseek-400` |  |
| `--dsw-alias-button-info-hover` | `#679efe` | `#4176e6` | `static:deepseek-400` / `static:deepseek-500` |  |
| `--dsw-alias-button-primary-dimmed` | `#ebeef2` | `#43454a` | `static:neutral-bluish-100` / `static:neutral-bluish-750` |  |
| `--dsw-alias-button-primary-fill` | `var(--dsw-alias-brand-primary)` | `var(--dsw-alias-brand-primary)` | `alias:brand-primary` / `alias:brand-primary` | 两主题同值 |
| `--dsw-alias-button-primary-hover` | `#43454a` | `#ebeef2` | `static:neutral-bluish-750` / `static:neutral-bluish-100` |  |
| `--dsw-alias-button-tool-bar-fill` | `#54555780` | `#54555780` | — | 两主题同值 |
| `--dsw-alias-button-tool-bar-fill-invisible` | `#1f1f1f5c` | `#1f1f1f5c` | — | 两主题同值 |
| `--dsw-alias-button-tool-bar-hover` | `#54555799` | `#54555799` | — | 两主题同值 |
| **交互态 `interactive`** | | | | |
| `--dsw-alias-interactive-bg-active` | `#2631481a` | `#ffffff24` | — |  |
| `--dsw-alias-interactive-bg-hover` | `#2631480f` | `#ffffff14` | — |  |
| `--dsw-alias-interactive-bg-hover-accent` | `#26314824` | `#ffffff3d` | — |  |
| `--dsw-alias-interactive-bg-hover-danger` | `#ec13130d` | `#f25a5a26` | — |  |
| `--dsw-alias-interactive-bg-hover-solid` | `#f1f3f5` | `#353638` | `static:neutral-bluish-75` / `static:neutral-bluish-800` |  |
| **状态色 `state`** | | | | |
| `--dsw-alias-state-business-primary` | `#4176e6` | `#679efe` | `static:deepseek-500` / `static:deepseek-400` |  |
| `--dsw-alias-state-business-tertiary` | `#e4edfd` | `#34415b` | `static:deepseek-100` / `static:deepseek-800` |  |
| `--dsw-alias-state-error-primary` | `#ec1313` | `#f25a5a` | `static:red-600` / `static:red-400` |  |
| `--dsw-alias-state-error-secondary` | `#f25a5a` | `#f25a5a` | `static:red-400` / `static:red-400` | 两主题同值 |
| `--dsw-alias-state-success-primary` | `#22c55e` | `#22c55e` | `static:green-500` / `static:green-500` | 两主题同值 |
| `--dsw-alias-state-success-secondary` | `#4ed17e` | `#4ed17e` | `static:green-400` / `static:green-400` | 两主题同值 |
| `--dsw-alias-state-success-tertiary` | `#e6faed` | `#233c2c` | `static:green-100` / `static:green-900` |  |
| `--dsw-alias-state-warn-label` | `#dd8629` | `#dd8629` | `static:amber-600` / `static:amber-600` | 两主题同值 |
| `--dsw-alias-state-warn-primary` | `#f59e0b` | `#f59e0b` | `static:amber-500` / `static:amber-500` | 两主题同值 |
| `--dsw-alias-state-warn-secondary` | `#f7ad31` | `#f7ad31` | `static:amber-400` / `static:amber-400` | 两主题同值 |
| `--dsw-alias-state-warn-tertiary` | `#fef5e7` | `#27241f` | `static:amber-100` / `static:amber-900` |  |
| **Markdown 专用** | | | | |
| `--dsw-alias-markdown-citation` | `#ebeef2` | `#353638` | `static:neutral-bluish-100` / `static:neutral-bluish-800` |  |
| `--dsw-alias-markdown-code-block` | `#f9fafb` | `#1b1b1c` | `static:neutral-bluish-50` / `static:neutral-bluish-900` |  |
| `--dsw-alias-markdown-code-block-banner` | `#f9fafb` | `#2c2c2e` | `static:neutral-bluish-50` / `static:neutral-bluish-850` |  |
| `--dsw-alias-markdown-code-segment-selected` | `#fff` | `#353638` | `static:neutral-bluish-00` / `static:neutral-bluish-800` |  |
| `--dsw-alias-markdown-code-segment-unselected` | `#f1f3f5` | `#1b1b1c` | `static:neutral-bluish-75` / `static:neutral-bluish-900` |  |
| `--dsw-alias-markdown-inline-code` | `#fafafa` | `#292929` | `static:neutral-50` / `static:neutral-800` |  |
| `--dsw-alias-markdown-placeholder` | `#f5f6f7` | `#2c2c2e` | `static:neutral-bluish-60` / `static:neutral-bluish-850` |  |
| `--dsw-alias-markdown-tag` | `#f1f3f5` | `#2c2c2e` | `static:neutral-bluish-75` / `static:neutral-bluish-850` |  |
| **滚动条 `scrollbar`** | | | | |
| `--dsw-alias-scrollbar-bg-l1` | `#e5e5e5` | `#3c3c3d` | `static:neutral-200` / `static:neutral-700` |  |
| `--dsw-alias-scrollbar-bg-l2` | `#e5e5e5` | `#545557` | `static:neutral-200` / `static:neutral-600` |  |
| `--dsw-alias-scrollbar-hover-l1` | `#d4d4d4` | `#545557` | `static:neutral-300` / `static:neutral-600` |  |
| `--dsw-alias-scrollbar-hover-l2` | `#d4d4d4` | `#65676b` | `static:neutral-300` / `static:neutral-550` |  |
| **浮层 `toast` / `tooltip`** | | | | |
| `--dsw-alias-toast-bg` | `#353638` | `#43454a` | `static:neutral-bluish-800` / `static:neutral-bluish-750` |  |
| `--dsw-alias-tooltip-bg` | `#2c2c2e` | `#43454a` | `static:neutral-bluish-850` / `static:neutral-bluish-750` |  |
| **specific（局部语义）** | | | | |
| `--dsw-specific-bubble` | `#edf3fe` | `#2c2c2e` | `static:deepseek-50` / `static:neutral-bluish-850` |  |
| `--dsw-specific-bubble-highlight` | `#d3e2ff` | `#43454a` | `static:deepseek-200` / `static:neutral-bluish-750` |  |
| `--dsw-specific-input-major` | `#fff` | `#2c2c2e` | `static:neutral-bluish-00` / `static:neutral-bluish-850` |  |
| `--dsw-specific-login-input` | `#f9fafb` | `#1b1b1c` | `static:neutral-bluish-50` / `static:neutral-bluish-900` |  |
| `--dsw-specific-menu` | `var(--dsw-alias-bg-layer-3)` | `var(--dsw-alias-bg-layer-3)` | `alias:bg-layer-3` / `alias:bg-layer-3` | 两主题同值 |
| `--dsw-specific-selector` | `#f5f6f7` | `#353638` | `static:neutral-bluish-60` / `static:neutral-bluish-800` |  |
| `--dsw-specific-sidebar-fill` | `#f9fafb` | `#1b1b1c` | `static:neutral-bluish-50` / `static:neutral-bluish-900` |  |
| `--dsw-specific-sidebar-nav-item-active` | `#ebeef2` | `#43454a` | `static:neutral-bluish-100` / `static:neutral-bluish-750` |  |
| `--dsw-specific-sidebar-nav-item-active-accent` | `#e4edfd` | `#353638` | `static:deepseek-100` / `static:neutral-bluish-800` |  |
| `--dsw-specific-sidebar-nav-item-hover` | `#f1f3f5` | `#2c2c2e` | `static:neutral-bluish-75` / `static:neutral-bluish-850` |  |
| `--dsw-specific-tip` | `#f5f6f7` | `#353638` | `static:neutral-bluish-60` / `static:neutral-bluish-800` |  |

---

### 1.3 阴影 / 高度（elevation）token

来源：`dsh-client-ui-theme/lib/client.js:1058`（`gradient-shadow-text.css`）。

| token | 亮色（`body`） | 暗色（`body[data-ds-dark-theme]`） |
|---|---|---|
| `--dsw-shadow-lv1` | `0 2px 4px 0 #0000000d` | 未单独覆盖（沿用亮值） |
| `--dsw-shadow-lv1-blur` | `0 4px 12px 0 #00000005` | 同上 |
| `--dsw-shadow-lv2` | `0 4px 12px 0 #00000005, 0 2px 8px 0 #0000000a` | 同上 |
| `--dsw-shadow-lv3` | `0 0 1px 0 #0003, 0 0 4px 0 #00000005, 0 12px 32px 0 #00000014` | 同上 |
| `--dsw-elevation-stroke-color` | `var(--dsw-alias-border-l4)` | 同上（可在组件上重绑） |
| `--dsw-mask-blur` | `blur(2px)` | 同上 |
| `--dsw-linear-gradient-think` | `linear-gradient(180deg, #fff 20.19%, #fff0 100%)` | `linear-gradient(180deg, #151517 20.19%, #15151700 100%)` |
| `--dsw-linear-think-select` | `linear-gradient(180deg, #f5f6f7 20.19%, #f5f6f700 100%)` | `linear-gradient(180deg, #232325 20.19%, #23232500 100%)` |

「elevation 三件套」定义在选择器 **`body, body *`**（不是 body 单独），以便逐元素重绑描边色：

```css
body, body * {
  --dsw-elevation-stroke: 0 0 0 .5px var(--dsw-elevation-stroke-color);
  --dsw-elevation-panel: var(--dsw-elevation-stroke), 0 3px 8px 0 #00000008, 0 0 16px 0 #00000005;
  --dsw-elevation-prominent: var(--dsw-elevation-stroke), 0 3px 8px 0 #0000000a, 0 0 20px 0 #0000000d;
  --dsw-elevation-soft: var(--dsw-elevation-stroke), 0 4px 16px 0 #00000008, 0 0 24px 0 #00000008; /* 输入框专用：更大模糊、更低透明度 */
}
```

使用约定：高层表面用 `border: 0; box-shadow: var(--dsw-elevation-panel)`，不再使用占布局的 border（`dsh-client-ui-theme/README.zh.md`「样式表」一节 + 各组件实测：菜单 `box-shadow:var(--dsw-elevation-prominent)`、composer `var(--dsw-elevation-soft)`）。重绑描边色的实例：`._list_1nxmc_8` → `--dsw-elevation-stroke-color: var(--dsw-alias-border-l1)`（`index-DPX2bQLO.css` offset 8549–11543）、`.uV2eYG_card` → `var(--dsw-alias-border-l2)`（`dsh-client-ui-conversation/lib/client.js:15756`）。

### 1.4 字号 / 排版 token `--dsw-font-*`（116 个声明 = 26 组 × 4~5 条）

来源：`dsh-client-ui-theme/lib/client.js:1058`。每组含一个 `font` 简写 + 4 个分量（`-font-family/-font-weight/-line-height/-font-size`，另有 `-font-style`）。设 `D = var(--dsh-content-font-delta)`、`S2 = var(--dsh-content-font-size-secondary)`、`D2 = var(--dsh-content-font-delta-secondary)`、`F = var(--dsw-font-family)`、`C = var(--ds-font-family-code)`。

**Markdown 阶梯（随正文字号联动）**

| token | 值（font shorthand） | 默认（14px 设置下）实际 |
|---|---|---|
| `--dsw-font-markdown-h1` | `700 calc(21px + D)/calc(30px + D) F` | 21/30 · 700 |
| `--dsw-font-markdown-h2` | `700 calc(19px + D)/calc(28px + D) F` | 19/28 · 700 |
| `--dsw-font-markdown-h3` | `700 calc(18px + D)/calc(26px + D) F` | 18/26 · 700 |
| `--dsw-font-markdown-h4` | `600 var(--dsh-content-font-size,14px)/calc(24px + D) F` | 14/24 · 600 |
| `--dsw-font-markdown-base` | `var(--dsh-content-font-size,14px)/calc(24px + D) F` | 14/24 · 400 |
| `--dsw-font-markdown-base-strong` | 同上 + `600` | 14/24 · 600 |
| `--dsw-font-markdown-base-italic` | 同上 + `italic` | 14/24 400 italic |
| `--dsw-font-markdown-base-strong-italic` | 同上 + `italic 600` | 14/24 600 italic |
| `--dsw-font-markdown-table` | `S2/calc(22px + D2) F` | 13/22 · 400 |
| `--dsw-font-markdown-table-head` | `500 S2/calc(22px + D2) F` | 13/22 · 500 |
| `--dsw-font-markdown-small`（+`-strong/-italic/-strong-italic`） | `12px/20px F`（strong=600） | 12/20 **固定** |
| `--dsw-font-markdown-code` | `12px/19px C` | 12/19 **固定** |
| `--dsw-font-markdown-code-block` | `11px/19px C` | 11/19 **固定** |
| `--dsw-font-markdown-code-block-small` | `11px/16px C` | 11/16 **固定** |

**固定 UI 阶梯（不随设置变化）**

| token | font shorthand | 默认 |
|---|---|---|
| `--dsw-font-xl-24` | `600 24px/32px F` | 24/32 · 600 |
| `--dsw-font-l-20` | `500 20px/28px F` | 20/28 · 500 |
| `--dsw-font-m-18` | `500 16px/28px F`（**名字 18，字号实为 16**） | 16/28 · 500 |
| `--dsw-font-base-16` / `--dsw-font-base-strong-16` | `16px/24px F` / `500 16px/24px F` | 16/24 |
| `--dsw-font-s-14` / `--dsw-font-s-strong-14` | `14px/22px F` / `500 14px/22px F` | 14/22 |
| `--dsw-font-xs-13` / `--dsw-font-xs-strong-13` | `13px/20px F` / `500 13px/20px F` | 13/20 |
| `--dsw-font-xxs-12` / `--dsw-font-xxs-strong-12` | `12px/18px F` / `500 12px/18px F` | 12/18 |
| `--dsw-font-xxxs-11` / `--dsw-font-xxxs-strong-11` | `11px/14px F` / `500 11px/14px F` | 11/14 |

> ⚠️ 缺陷照录：`dsh-client-ui-tool/lib/client.js:1143`（`ToolRow.module.css`）里 `.o3BgMG_imageLabel` 使用 `font:var(--dsw-font-sm-13)`，而 **`--dsw-font-sm-13` 在全语料中没有任何定义**（grep `--dsw-font-sm-13:` = 0 命中）→ 该声明整条无效。复刻时用 `--dsw-font-xs-13`（13/20）。

### 1.5 间距、圆角、z-index：**没有 token 化**

**结论：本设计系统不存在间距刻度变量**（未找到 `--spacing` / `--gap-*` / `--space-*` 类定义；`design-platform.css` 与 `gradient-shadow-text.css` 中没有任何间距类自定义属性）。间距一律写死在组件里。全语料统计的高频 `gap`/`padding` 值为 `4 / 6 / 8 / 10 / 12 / 14 / 16 / 20 / 24 px`（示例：卡片内边距 `padding:12px 16px`，栅格 gap `8px`）。

**圆角同样无 token**，为经验枚举值（全语料 `border-radius` 出现频次，去重后）：

| 值 | 频次 | 典型用途（实测来源） |
|---|---|---|
| `2px` | 6 | 构建号徽标 `.hHd-Xa_buildVersion`、tab 下划线 |
| `3px` | 6 | json-tree 复制按钮、小箭头 |
| `4px` | 12 | 重命名输入框、dockkit menu item |
| `5px` | 3 | compact menu item |
| `6px` | 25 | inline code、call row、折叠体、tabstrip menu |
| `7px` | 3 | compact menu 容器 |
| `8px` | 24 | 侧栏/会话行、图标按钮、toast icon、tag 无关 |
| `10px` | 13 | menu item、回到底部按钮(100px 圆) |
| `12px` | 20 | **所有工具/代码块（`--dsl-*-radius`）**、模态、hovercard、panel 卡片 |
| `14px` | 7 | toast |
| `16px` | 13 | 消息附件 FileCard（240×64） |
| `18px` | 10 | Button（md 36px 高） |
| `20px` | 12 | Menu 容器、浮动面板、字号步进器 |
| `22px` | 6 | **用户气泡、composer 卡片** |
| `24px` | 6 | Dialog |
| `28px` | 10 | 圆形图标按钮 |
| `999px` / `100px` / `50%` | 16/1/18 | 胶囊按钮、发送按钮、状态点 |

**z-index（全语料 68 条声明，实际层级阶梯）**

| z-index | 用途 | 来源 |
|---|---|---|
| 1 | dialog 内容、divider、copy button | dist CSS |
| 2 / 3 | json-tree 展开箭头 / 复制锚点 | dist CSS |
| 4 / 5 / 6 | trajectory 轨道层、代码块 sticky banner(`z-index:6`) | trajectory sheets、dist CSS |
| 7 | composer seat（`data-phase=active`） | `dsh-client-ui-conversation/lib/client.js:14651` |
| 8 | 回到底部按钮、会话列宽拖拽手柄 | chat/conversation sheets |
| 9 | composer seat 且含 trigger menu | `…ConversationRoot.module.css.mjs` |
| 10 | 右栏面板、dock hint/scrim | `dsh-client-ui-sidebar-right/lib/client.js:607`、dist CSS |
| 11 | 侧栏拖拽手柄 `.pI_x6G_handle` | `dsh-client-ui-layout/lib/client.js:70` |
| 20 | shell overlay layer | 同上 |
| 30 / 40 | cordis panel / 右栏全屏 | cordis、sidebar-right |
| 60 | 右栏浮动宿主 | sidebar-right |
| 70 | DockKit 右键菜单 | dist CSS |
| 100 | 下拉菜单 list、hovercard、tooltip、popover | dist CSS + 各插件 |
| 101 | 子菜单 submenu | dist CSS |
| 1000 | 模态遮罩层（Dialog、lightbox、drop overlay、settings overlay） | dist CSS、`dsh-client-ui-attachment` |
| **1100** | **最高层**：toast、portal 菜单、onboarding overlay、stat dialog、模型选择菜单 | dist CSS、`dsh-client-ui-chat/lib/client.js:3457`、model-selection |

### 1.6 动效 token（`--ds-*`，定义于 `base.css`，`dsh-client-ui-theme/lib/client.js:1046`）

```css
:root {
  --ds-ease-in-out: cubic-bezier(.4, 0, .2, 1);
  --ds-transition-duration: .2s;
  --ds-transition-duration-fast: .1s;
  --ds-transition-duration-slow: .3s;
}
```

其余动效时长为组件内硬编码，高频值（全语料统计次数）：`.12s`(35) / `.18s`(15) / `.1s`(9) / `.16s`(9) / `.15s`(8) / `.14s`(6) / `.2s`(6) / `2.6s`(5，工具行 running 扫光) / `.8s`(3，spinner) / `1.6s`(2) / `1.8s`(1，turn status 微光) / `80ms`(1，消息操作揭示)。

**无障碍约定**：几乎每张带过渡/动画的样式表末尾都有 `@media (prefers-reduced-motion: reduce){ … transition:none; animation:none }`（AppFrame、SidebarRoot、Rows、MessageItem、ReasoningRow、ChatView、HeroShell、tooltip、toast 等均实测存在）。复刻时建议同样加。

### 1.7 运行时动态变量 `--dsh-*`（非颜色，31 个中除 boot 外的全部）

| token | 值 / 来源 |
|---|---|
| `--dsh-content-font-size` | JS 内联在 `<body>`：12–17 px，默认 14（`dsh-client-ui-layout/lib/client.js:472`） |
| `--dsh-content-font-delta` | `calc(var(--dsh-content-font-size,14px) - 14px)`（theme `:1058`） |
| `--dsh-content-font-size-secondary` | `min(calc(fs - 1px), max(13px, calc(fs - 2px)))`，默认 13px（同上） |
| `--dsh-content-font-delta-secondary` | `calc(var(--dsh-content-font-size-secondary) - 13px)`（同上） |
| `--dsh-scrollbar-width` | `8px`（theme `:1055`，镜像 WebKit 滚动条布局宽度） |
| `--dsh-scrollbar-thumb` / `-thumb-hover` | 亮/暗基线 `var(--dsw-alias-scrollbar-bg-l1)` / `…-hover-l1)`（theme `:1055`）；高层容器重绑为 `l2`（21 处）；`ui-sidebar` 指针不在栏内时重绑 `transparent`（`dsh-client-ui-sidebar/lib/client.js:27` `.hHd-Xa_quietBars`） |
| `--dsh-conversation-column-width` | JS 实测：`root.style.setProperty("--dsh-conversation-column-width", column+"px")`，`column = root.offsetWidth`（`dsh-client-ui-conversation/lib/client.js:14816`） |
| `--dsh-chat-user-width` | 用户可拖拽的会话列宽偏好，JS 写入（同上 `:14819/:14843`） |
| `--dsh-chat-content-width` | `var(--dsh-chat-user-width, clamp(680px, calc(var(--dsh-conversation-column-width,0px) * .64), 920px))`（conversation `:14651`） |
| `--dsh-composer-height` | JS 实测 `seat.offsetHeight`（`:14806`）；CSS 兜底 `152px` |
| `--dsh-conversation-viewport-height` | JS 实测 `scroller.clientHeight`（`:14807`）；兜底 `100dvh` |
| `--dsh-composer-card-max-width` | `calc(var(--dsh-chat-content-width) + 32px)` |
| `--dsh-composer-side-clearance` | `16px` |
| `--dsh-composer-dock-inset` | `8px` |
| `--dsh-composer-stack-gap` | `6px` |
| `--dsh-composer-text-max-height` | `336px` |
| `--dsh-composer-hint` | JS 逐元素内联，`JSON.stringify(hint)`（`:16117`），用于 `::after { content: var(--dsh-composer-hint) }` |
| `--dsh-chat-flow-gap` | 默认 `16px`（消费端兜底）；`[data-turn-process-answer]` 上为 `8px`（chat `:1507`） |
| `--dsh-sidebar-inline-padding` | `12px`（`dsh-client-ui-sidebar/lib/client.js:27`） |
| `--dsh-width-handle-pointer-y` | 指针 Y（默认 `50%`），驱动手柄渐变 |
| `--dsh-table-spare` / `--dsh-table-lead` | `max(0px, calc((100cqw - var(--dsh-chat-content-width)) / 2))` / `calc(spare + min(width,100cqw) - 100%)`（chat `:2933`） |
| `--dsh-state-ongoing` | `var(--dsw-static-deepseek-450)` = `#5686fe`（dist CSS） |
| `--dsh-boot-*` | 启动页 6 个亮/暗双值，见 §5.9 |
| `--dsh-toast-hold` | 默认 `3s`（消费端 `var(--dsh-toast-hold, 3s)`；未见 JS 写入） |
| `--dsh-trajectory-toolbar-height` | `32px`（trajectory views） |
| `--dsh-trajectory-bottom-clearance` | `calc(var(--dsh-composer-height,152px) + 16px)` |
| `--dsh-file-type-violet` | `rgb(139, 118, 246)`（dist CSS `_icon_1wejo_1`） |
| `--dsh-file-type-default-color` | 按文件类型：code/html/markdown `--dsw-static-deepseek-500`；excel `green-500`；folder `amber-400`；image/video violet；other `neutral-bluish-300`；pdf `red-600`；ppt `amber-500`；word `deepseek-450` |

### 1.8 代码块局部变量 `--dsl-*`（19 个）与 `--json-tree-*`（7 个）

`--dsl-*`（全部在共享原语里，dist CSS）：

| token | 定义值 | 覆盖实例 |
|---|---|---|
| `--dsl-code-block-background` | `var(--dsw-alias-markdown-code-block)` | 预览面板改 `transparent` |
| `--dsl-code-block-banner-background-color` | `var(--dsw-alias-markdown-code-block-banner)` | — |
| `--dsl-code-block-border-radius` | `12px` | 预览面板 `0px` |
| `--dsl-code-block-banner-font` | `11px/18px var(--dsw-font-family)` | — |
| `--dsl-code-block-content-font` | `var(--dsw-font-markdown-code-block)`（11/19） | ToolRow、CodeBody 改 `…-small`（11/16） |
| `--dsl-code-block-line-number-width` | 由 JS 按行数位数设置（CSS 中仅消费，未见定义） | — |
| `--dsl-code-block-line-white-space` | 消费端 `pre-wrap`；预览面板 `pre` | — |
| `--dsl-terminal-radius/line-height/font/gutter` | `12px` / `22px` / `var(--dsw-font-markdown-code-block)` / `30px` | ToolRow：`line-height:18px`、`font:…-small`、`--dsl-terminal-output-max-height:224px` |
| `--dsl-read-radius/line-height/gutter` | `12px` / `22px` / `48px` | — |
| `--dsl-diff-radius/line-height` | `12px` / `22px` | — |
| `--dsl-search-radius/line-height` | `12px` / `22px` | — |
| `--dsl-web-radius` | `12px` | — |

`--json-tree-*`（JSON 树查看器；**这是产物中除 shiki 外唯一使用 `body[data-ds-dark-theme] .comp` 双主题的局部表**，dist CSS）：

| token | 亮 | 暗 |
|---|---|---|
| `--json-tree-property` | `#881391` | `#5db0d7` |
| `--json-tree-string` | `#c41a16` | `#f28b82` |
| `--json-tree-number` | `#1c00cf` | `#99c8ff` |
| `--json-tree-keyword` | `#1c00cf` | `#99c8ff` |
| `--json-tree-punctuation` | `#202124` | `#e8eaed` |
| `--json-tree-icon` | `#5f6368` | `#9aa0a6` |
| `--json-tree-hover` | `rgb(60 64 67 / 4%)` | `rgb(232 234 237 / 5%)` |

### 1.9 其余局部变量（全部列出）

| token | 定义处 | 值（亮 / 暗） |
|---|---|---|
| `--deliverable-fill` / `--deliverable-hover` | `dsh-client-ui-deliverables/lib/client.js`（`Deliverables.module.css`） | `--dsw-static-neutral-50` / `--dsw-static-neutral-100`（暗：`neutral-850` / `neutral-800`，走 `body[data-ds-dark-theme] .nyYjTG_root`） |
| `--trajectory-*`（3 个） | `dsh-client-ui-trajectory/lib/client.js` | 见 trajectory sheets |
| `--turn-rail-band` | `dsh-client-ui-chat/…/TurnNavigator.module.css`（`:1625`） | `calc(var(--dsh-conversation-viewport-height,100dvh) - var(--dsh-composer-height,152px))` |
| `--meter-*`、`--produced-*`、`--request-*` | conversation ContextMeter / deliverables ProducedFiles / trajectory | 各 1 个局部值 |
| `--shiki-*`（11 个） | 见 §7 | — |

---

## 2. 字体栈

### 2.1 UI / 正文（`--dsw-font-family`）

定义：`dsh-client-ui-theme/lib/client.js:1046`（`base.css`，选择器 `:root`）

```css
--dsw-font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
                   "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Helvetica, Arial, sans-serif;
```

`<body>` 上的应用（含兜底字面量）在 `index-DPX2bQLO.css` 尾部全局段（offset 51399–51947）：

```css
html, body, #root { height: 100%; margin: 0 }
body {
  font-family: var(--dsw-font-family, -apple-system, …同左…);
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  color: var(--dsw-alias-label-primary, #0f1115);
  background: var(--dsw-alias-bg-base, #fff);
  text-autospace: normal;
}
code, pre, [data-diff], [data-read], [data-search], [data-terminal] { text-autospace: no-autospace }
button, input, select, textarea { font-family: inherit }
```

### 2.2 代码（`--ds-font-family-code`）

定义：同 `base.css`（`dsh-client-ui-theme/lib/client.js:1046`）

```css
--ds-font-family-code: "SF Mono", "JetBrains Mono", "Fira Code", Consolas,
                       "Liberation Mono", Menlo, Courier, "PingFang SC", "Microsoft YaHei";
```

> 注意前缀不对称：字体族 token 是 `--dsw-font-family`（三个 w），代码族却是 `--ds-font-family-code`（两个 w）。复刻时照抄，别改。

### 2.3 `@font-face`：**产品 UI 不加载任何自托管字体**

- `index-DPX2bQLO.css` 中 `@font-face` 数量 = **0**，`url()` 数量 = **0**。UI 完全依赖系统字体栈。
- 全部 20 个 `@font-face` 位于 `vendor-BNsW4eBh.css`（KaTeX），族名为 `KaTeX_AMS / KaTeX_Caligraphic / KaTeX_Fraktur / KaTeX_Main / KaTeX_Math / KaTeX_SansSerif / KaTeX_Script / KaTeX_Size1 / KaTeX_Size2 / KaTeX_Size3 / KaTeX_Size4 / KaTeX_Typewriter`，`font-display: block`，src 均为 `url(./fonts/<Name>-<variant>-<hash>.woff2) format("woff2")` 再回退 `.woff` / `.ttf`；字体文件确在 `dist/assets/fonts/`（62 个文件，如 `KaTeX_Main-Regular-B22Nviop.woff2`）。
  例（`vendor-BNsW4eBh.css` 开头）：`@font-face{font-display:block;font-family:KaTeX_AMS;font-style:normal;font-weight:400;src:url(./fonts/KaTeX_AMS-Regular-BQhdFMY1.woff2) format("woff2"),url(./fonts/KaTeX_AMS-Regular-DMm9YOAa.woff) format("woff"),url(./fonts/KaTeX_AMS-Regular-DRggAlZN.ttf) format("truetype")}`
- 若复刻不做数学公式渲染，可整块忽略 §2.3。

---

## 3. 布局骨架

### 3.1 三栏网格（`AppFrame.module.css`，来源 `dsh-client-ui-layout/lib/client.js:70`；JS 逻辑同文件 `:13–45`、`:280`、`:343`、`:360–368`）

```css
.pI_x6G_frame {                       /* 三栏容器 */
  background: var(--dsw-alias-bg-base);
  height: 100%;
  display: grid;
  grid-template-rows: 100%;
  grid-template-columns: <sidebar>px minmax(0, 1fr) <rightbar>px;  /* JS 内联 */
  transition: grid-template-columns var(--ds-transition-duration-slow) var(--ds-ease-in-out);
  position: relative; overflow: hidden;
}
.pI_x6G_sidebarCol { background: var(--dsw-specific-sidebar-fill);
  border-right: .5px solid var(--dsw-alias-border-l3); min-width: 0; overflow: hidden }
.pI_x6G_centerCol  { display: flex; flex-direction: column; min-width: 0; overflow: hidden }
.pI_x6G_rightbarCol{ min-width: 0; position: relative; overflow: visible }
.pI_x6G_handle     { width: 8px; margin-left: -4px; cursor: col-resize; z-index: 11;
                     position: absolute; top: 0; bottom: 0;
                     transition: left var(--ds-transition-duration-slow) var(--ds-ease-in-out) }
.pI_x6G_overlayLayer { position: absolute; inset: 0; z-index: 20; pointer-events: none }
```

**尺寸常量（JS，权威）**

| 量 | 值 | 来源 |
|---|---|---|
| 侧栏默认宽 | **280 px** | `dsh-client-ui-layout/lib/client.js:343`（`sidebar: 280`） |
| 侧栏拖拽范围 | **264 – 420 px** | `:39` `clampWidth(sidebar, 264, 420)`、`:362` |
| 侧栏折叠（rail）宽 | **56 px** | `:38` `const s = sidebar === 0 ? 56 : clampWidth(sidebar, 264, 420)` |
| 自动折叠断点 | 视口 **< 1024 px** | `:13 SIDEBAR_AUTO_COLLAPSE = 1024`、`:233` |
| 主区（center）最小可用宽 | 400 px（参与 `available = viewport - s - 400` 计算） | `:39` |
| 右栏（文档/文件面板）最小/默认/最大 | 最小 **300 px**；首次打开 = 视口 **× 0.45**；最大 = 视口 **× 0.7** | `:15 RIGHTBAR_MAX_RATIO=.7`、`:17 RIGHTBAR_DEFAULT_RATIO=.45`、`:40` |
| 右栏收起条件 | `rightbar === 0` 或 `available < 300` → 右栏轨道归 0 | `:40` |
| 右栏打开方式 | 绝对定位浮层 + `transform: translate(100%)` → `[data-sidebar-right-open]` 归零，`.3s` 过渡；全屏时 `position:fixed; inset:0; z-index:40` | `dsh-client-ui-sidebar-right/lib/client.js:607` |

### 3.2 侧栏内部（`SidebarRoot.module.css`，`dsh-client-ui-sidebar/lib/client.js:27`）

| 部位 | 规格 |
|---|---|
| 根 `.hHd-Xa_root` | `padding: 6px var(--dsh-sidebar-inline-padding=12px)`；`background: var(--dsw-specific-sidebar-fill)`；`font-size:14px`；折叠态 `padding: 18px 10px 6px` |
| Logo 行 | 高 **60 px**，`margin-bottom:8px`，`padding:8px 0 8px 4px`；折叠态高 36 px、`margin-bottom:12px`、padding 0 |
| 品牌名 | `font-size:18px / line-height:24px / font-weight:600 / letter-spacing:.04em`（回退名 17px、字距 0） |
| 新建会话按钮 | 高 **38 px**，`border-radius:12px`，`.5px solid var(--dsw-alias-border-l3)`，`background: var(--dsw-alias-button-elevated-fill)`，hover `--dsw-alias-button-floating-hover`，`padding:8px 16px`，14px/22px/500，`margin:0 2px 8px`；折叠态 36×36、无边框透明 |
| 面板导航行 `.hHd-Xa_panelRow` | `min-height:36px`，`padding:7px 8px`，`border-radius:8px`，`color:var(--dsw-alias-label-secondary)`；hover `--dsw-alias-interactive-bg-hover`；active `--dsw-alias-interactive-bg-active` + `color:label-primary` + `font-weight:500`；focus-visible `outline:2px solid var(--dsw-alias-label-primary); outline-offset:-2px`；折叠态 36×36 居中 |
| 图标按钮 | 28×28（折叠态 36×36），`border-radius:50%; corner-shape:round`，`color:label-secondary`，hover `--dsw-alias-interactive-bg-hover` |
| 动画 | 展开 `wide-in .2s`；折叠 rail 逐项 `rail-in .15s`（`translate(49px)` 起始）；`COLLAPSE_SETTLE_MS = 150`（`dsh-client-ui-sidebar/lib/client.js:93`） |

### 3.3 会话列表行（`Rows.module.css`，`dsh-client-ui-workspace/lib/client.js:554`）

| 元素 | 规格 |
|---|---|
| `.YDXeBa_sessionRow` | 高 **32 px**，`border-radius:8px`，`padding:0 8px`，`gap:0`，`color:var(--dsw-alias-label-primary)`，入场 `row-in .15s` |
| `.YDXeBa_projectRow` | 高 **34 px**，`gap:6px` |
| hover / selected / menuOpen | 统一 `background: var(--dsw-alias-interactive-bg-hover)` |
| 标题 `.YDXeBa_title` | 14px / 20px，单行省略；会话行 `margin:0 6px 0 4px` |
| 时间 `.YDXeBa_time` | 12px / 20px，`color:var(--dsw-alias-label-tertiary)`；hover 时隐藏（让位给操作按钮） |
| 状态槽 `.YDXeBa_slot` | 16 × 20 px，`color:label-tertiary` |
| 行内操作 | `display:none` → hover/menuOpen 时 `inline-flex`，`gap:12px`；按钮 16×16，`border-radius:4px` |
| 搜索结果显示行 | `min-height:48px`，`padding:4px 8px`，标题 14/20，meta 12/17 |
| 拖拽插入指示 | `::before/::after` 高 12 px，三层 `linear-gradient` 拼出箭头+横线，颜色 `var(--dsw-alias-state-business-primary)` |

### 3.4 会话主区与内容列（`ConversationRoot.module.css`，`dsh-client-ui-conversation/lib/client.js:14651`）

| 项 | 值 |
|---|---|
| 内容列宽 `--dsh-chat-content-width` | `clamp(680px, 列宽 × 0.64, 920px)`（可用 `--dsh-chat-user-width` 覆盖） |
| 消息滚动区 | `scrollbar-gutter: stable`；`margin-right:2px`；`::-webkit-scrollbar-track{margin:2px}` |
| ChatView 内边距 | `padding: 16px calc(var(--dsh-composer-side-clearance) + 16px)` = 16px 32px（`dsh-client-ui-chat/lib/client.js:1507`） |
| 消息流间隔 | 相邻非空子项 `margin-top: var(--dsh-chat-flow-gap, 16px)`；`[data-turn-process-answer]` 处为 8px |
| 顶栏 `.wSkVaW_header` | `min-height: 76px`，`padding: 10px 28px 0 20px`，`border-bottom: .5px solid var(--dsw-alias-border-l3)`；标题行 `min-height:30px`；面包屑 14/20，`max-width:220px`，`border-radius:12px`，`padding:4px 8px` |
| 顶栏 tab | `gap:36px`，`margin-top:10px`，13px/16px/500，active 色 `--dsw-alias-state-business-primary`，下划线 `height:2px; bottom:-1px; border-radius:2px` |
| composer 停靠位 | `position:sticky; bottom:0; z-index:7`，背景 `linear-gradient(180deg, color-mix(… 0%, transparent) 0px, var(--dsw-alias-bg-base) 36px)`（顶部 36px 渐隐） |
| 列宽拖拽手柄 | 两侧各一条，`width:min(40px, (100% - content-width)/2 - 48px)`，hover 显示 3px 竖条（跟随指针 Y 的 80px 渐变段） |
| Hero 空会话态 | 居中；headline `font-size:26px / line-height:32px / font-weight:500`；工作区选择器 `min-height:28px`，13/20/500，`border-radius:16px`，`max-width:min(100%,360px)`（`…/HeroShell.module.css`，`:14498`） |

### 3.5 顶栏高度小结

**不存在独立的固定「顶栏」token。** 实测：会话区顶栏 `min-height:76px`（含 tab 行）；右栏 DockKit 的 tab 条 `height:28px; padding:10px 6px 0 10px`（dist CSS `_tabStrip_17p4l_156`）；侧栏 Logo 行 60px。三处各自定义，无统一常量。

---

## 4. 类名命名规律与 CSS 组织方式

**结论：CSS Modules（每个 `.module.css` 编译为哈希前缀），完全不是 Tailwind / BEM / vanilla-extract。**

1. **类名形态**：`_<camelCase局部名>_<5位hash>_<行号>`，例如 `_bubble_1nw3t_1`、`._markdown_kcgor_5`、`._tableScroll_kcgor_190`。
   - 首字符 `_`（构建产物 CSS 中）或无下划线的 `<hash>_<local>` 形态（运行时注入的样式表里是 `<hash>_<local>`，如 `Sixlwa_bubble`、`.pI_x6G_frame`、`.hHd-Xa_root`、`.o3BgMG_root`、`.YDXeBa_sessionRow`、`.wSkVaW_root`、`.uV2eYG_card`、`.kcgor`/`rsn9u` 等）。**hash 是 6 字符的 base64url（可含 `-`）**，如 `pI_x6G`、`hHd-Xa`、`o3BgMG`、`Sixlwa`、`YDXeBa`、`wSkVaW`、`uV2eYG`、`pXSMma`、`lcKema`、`EvIC1a`、`nyYjTG`、`ztWv_q`、`CY-8Ka`。
   - 后缀数字 = 源文件中的行号（`_1gdtu_156` 的 156 即行号），可用来在复刻时反推「同一组件的类属于同一源文件」。
   - 状态用 **`data-*` 属性选择器**而非修饰类：`[data-state=running]`、`[data-tone=danger]`、`[data-expanded]`、`[data-running]`、`[data-dragging]`、`[data-sidebar-right-open]`、`[data-phase=active]`、`[data-turn-process-answer]`、`[data-actions-reveal=hover]`、`[data-chat-flow-kind=user]`、`[data-after-produced-files=true]`。这是全系统一致的约定（BEM 的 `--modifier` 一次也没出现）。
   - 组合态用 `:not()` / `:has()` / `:where()`：`:not(:disabled):hover`、`:root:has([data-conversation-composer-overlay])`、`:where(h1,h2,h3,h4,h5,h6) strong`。
2. **两种投递路径**（复刻时只需合成一份 CSS）：
   - 共享原语（Button / Tag / Pill / Switch / Menu / Dialog / Tooltip / Toast / HoverCard / JsonTree / CodeBlock / TerminalBlock / ReadBlock / DiffBlock / SearchBlock / WebBlock / Markdown / DockKit / StateDot / FileTypeIcon / InlineChip / boot）→ 打进 `index-DPX2bQLO.css`。归属包为 `dsh-client-ui-primitives` / `dsh-client-ui-renderer` 一类；**注意 `dsh-client-ui-primitives` 包本体不在此安装包内**（组件出口见 `dist/assets/index-BKQ_L1z6.js` 中 `Object.freeze({__proto__:null,BrandWordmark:…,Button:…,CodeBlock:…,DiffBlock:…,DisclosureRow:…,FileTypeIcon:…,HoverCard:…,…})` 的模块导出表），因此这些原语的 CSS 只能从 dist CSS 取。
   - 功能插件的样式 → 编译成 JS 字符串常量，运行时创建 `<style data-plugin="@deepseek-ai/dsh-client-ui-xxx" data-plugin-css="<pkg>/<File>.module.css">` 插入 `<head>`；卸载/HMR 时移除。每张表还附带一个同名 JS 映射对象（`{"root":"hHd-Xa_root", …}`）。
   - 全局 token 表同样走运行时路径，但用 `data-plugin-css="<pkg>/<name>.css"`，6 张表按固定顺序：base → corner-shape → design-platform → scrollbar → gradient-shadow-text → shiki（`dsh-client-ui-theme/lib/client.js:1066–1096` 的 `STYLES` 数组与 `installThemeStyles()`）。**`scrollbar.css` 必须排在 `design-platform.css` 之后**（唯一消费 `--dsw-alias-scrollbar-*` 的表）。
3. **全局基底选择器**：token 表挂在 `body`（而非 `:root`）上；只有 `--dsw-font-family` / `--ds-font-family-code` / `--ds-ease-in-out` / `--ds-transition-duration*` 和 `--shiki-*` 在 `:root`。因此**复刻时把主题属性放在 `<body data-ds-dark-theme>` 上，且 token 块也用 `body{}` 选择器**，否则 `body[data-ds-dark-theme]` 的后代优先级会错位。
4. **CSS 书写规范**（从压缩产物可反推）：Lightning CSS 打包（`text-autospace`、`corner-shape` 等前沿特性 + 属性排序为「布局→盒模型→外观→排版→其他」，是 Lightning CSS 的 `tieOrder` 特征）；文件级 `.module.css` 每包一个目录；无障碍隐藏统一为 `.visuallyHidden{ clip:rect(0 0 0 0); white-space:nowrap; width:1px; height:1px; position:absolute; overflow:hidden }`（多包各自重复定义）。
5. **无预处理器**：产物里没有嵌套规则残留（除 `@media`/`@supports`），没有 `:export`，没有 CSS 变量回退链之外的自定义语法。

---

## 5. 关键组件视觉规格

以下每条都可回溯到来源。**通用换算**：`--dsh-content-font-size = 14px`（默认）时 `D=0`、`S2=13px`、`D2=0`，因此 `calc(24px + D)` 即 24px、`var(--dsh-content-font-size-secondary,13px)` 即 13px。

### 5.1 消息行 / 用户气泡（`ui-chat/…/MessageItem.module.css`，`dsh-client-ui-chat/lib/client.js:154`）

| 元素 | 规格 |
|---|---|
| 用户行 `.Sixlwa_userRow` | `display:flex; flex-direction:column; align-items:flex-end; gap:6px` |
| 用户栈 `.Sixlwa_userStack` | `max-width: min(calc(var(--dsh-chat-content-width,748px) * .702), 82%)`；`gap:8px` |
| **气泡 `.Sixlwa_bubble`** | `background: var(--dsw-specific-bubble)`（亮 `#edf3fe` / 暗 `#2c2c2e`）；`border-radius: 22px`；`padding: 10px 16px`；`font-size: var(--dsh-content-font-size,14px)`；`line-height: calc(22px + D)`；`color: var(--dsw-alias-label-primary)`；`white-space:pre-wrap; word-break:break-word` |
| 引用摘要 | 13px / `calc(18px + D2)`，`color: label-tertiary` |
| 压缩(compact)行按钮 | 高 `calc(24px + D)`，`border-radius:6px`，hover `--dsw-alias-interactive-bg-hover`；标题 13px/`calc(24px+D)` `label-primary-dimmed`；摘要 13px `label-tertiary` 单行省略；分隔点 `width:2px;height:2px;border-radius:1px;background:label-caption;margin:0 8px` |
| 压缩体 | `padding: 4px 0 4px calc(22px + D)`，13px，`label-tertiary` |
| 重试行 | details/summary；summary 13px / `calc(20px + D2)`，`border-radius:3px`，右侧 6×6 箭头（`border-bottom/right:1.5px solid`，`rotate(-45deg)` → `open` 时 `rotate(45deg)`，`transition:transform .12s`）；`focus-visible: outline:1.5px solid var(--dsw-alias-button-info-fill); outline-offset:2px`；active 时文字用渐变微光 `linear-gradient(90deg, label-tertiary 0/40%, label-secondary 50%, label-tertiary 60/100%)` + `background-clip:text` + `1.6s ease-in-out infinite` |
| 错误行 | grid `10px minmax(0,1fr) auto`，`gap:8px`，`padding:2px 0`；标题 `color:var(--dsw-alias-state-error-primary); font-weight:600`；错误码 `font: var(--dsw-font-markdown-code-block-small)`（11/16 代码族） |
| max-tokens 提示标题 | `color: var(--dsw-alias-state-warn-primary)`，600 |
| 附件卡 `.Sixlwa_fileCard` | `border:.5px solid var(--dsw-alias-border-l2)`；`background:var(--dsw-specific-input-major)`；`width:240px; min-height:64px; flex:0 0 240px`；`border-radius:16px`；`padding:8px 12px; gap:10px`；图标 28×28；文件名 14/22/500；meta 12/15 `label-tertiary` |

**助手侧（无气泡）**：`.hWmORq_root`（AssistantMarkdown，`:2933`）`font-size:var(--dsh-content-font-size,14px); line-height:calc(24px + D); color:var(--dsw-alias-label-primary)`，块级 `gap:16px`；「已停止」徽标 `background:var(--dsw-alias-interactive-bg-hover); color:label-tertiary; border-radius:6px; padding:0 6px; font-size:11px; line-height:18px`。

**会话级容器 `.EvIC1a_root`**（ChatView，`:1507`）：滚动 `padding:16px calc(var(--dsh-composer-side-clearance) + 16px)`；列 `max-width:var(--dsh-chat-content-width); margin:0 auto`；`container-type:inline-size`。
「回到底部」按钮：34×34，`border-radius:100px`，`background:var(--dsw-alias-button-floating-fill)`（亮 `#fff` / 暗 `#2c2c2e`），`box-shadow:var(--dsw-elevation-panel)` + 描边重绑 `--dsw-elevation-stroke-color:var(--dsw-alias-border-l3)`，hover `--dsw-alias-button-floating-hover`；停靠 `bottom:16px`（有 composer 时 `bottom:calc(var(--dsh-composer-height,152px) + 16px)`）。
「加载更早」按钮：`background:var(--dsw-alias-interactive-bg-hover-solid); border-radius:14px; padding:4px 12px; font-size:12px`。
**生成中的状态行** `.EvIC1a_turnStatus`：高 `calc(26px + D)`，14px/`calc(22px + D)`，渐变文字微光 `linear-gradient(90deg, deepseek-500 0/40%, deepseek-200 50%, deepseek-500 60/100%)` + `background-clip:text; -webkit-text-fill-color:transparent; background-size:250% 100%; animation:1.8s linear infinite`。

**行内操作按钮 `.xzv4MW_action`**（MessageIconActions，`:1003`）：`calc(28px + D)` 见方，`border-radius:28px`，`padding:6px`，图标 `calc(15px + D)`，`color:label-tertiary`；hover `background:var(--dsw-alias-interactive-bg-hover); color:label-secondary`；`[data-unavailable]` → `opacity:.4`。在 `@media (hover:hover)` 内整组默认 `opacity:0`，`:hover` / `:focus-within` 时 `opacity:1`，`transition:opacity 80ms`。

### 5.2 工具调用行（`ui-tool/…/ToolRow.module.css`，`dsh-client-ui-tool/lib/client.js:1143`；外壳共享行组件 `_root_luwio_9`/`_row_luwio_16` 在 dist CSS offset 3022–4076）

**折叠态（单行，行高 = 一行正文）**

| 部位 | 规格 |
|---|---|
| 行外壳 | `height: calc(24px + D)`（默认 24px）；`overflow:hidden`；`[data-expandable]` → `cursor:pointer` |
| 前导图标 `.o3BgMG_leading` / `_leading_luwio_29` | `calc(16px + D)` 见方槽，`margin-right:6px`，`color:label-tertiary`；内部 svg `calc(14px + D)`；hover 时静态图标淡出、chevron 淡入（`transition:opacity .1s ease`） |
| 标题 `.o3BgMG_title` | `font-weight:400`，13px / `calc(24px + D)`，`color: label-secondary`（外壳 `_title_luwio_79`） |
| 分隔点 `.o3BgMG_sep` | `2×2px`，`border-radius:1px`，`background: var(--dsw-alias-label-caption)`，`margin:0 8px` |
| 摘要 `.o3BgMG_summary` | 13px / `calc(24px + D)`，`color:var(--dsw-alias-label-tertiary)`，单行省略 |
| 摘要后缀 | 同字号，`margin-left:4px` |
| diff 统计 `.o3BgMG_diffStat` | `font-family: var(--ds-font-family-code)`；`font-size: calc(13px - 2px)` = **11px**；`color: var(--dsw-alias-label-caption)`；`margin-left:10px; transform:translateY(.5px)` |
| 文件链接 `.o3BgMG_fileLink` | `text-decoration: underline dotted var(--dsw-alias-label-tertiary)`，`text-underline-offset:3px`，`text-decoration-thickness:1px`；hover → `color:label-primary` + 下划线 `currentColor` |
| **失败摘要 `.o3BgMG_errorSummary`** | `color: var(--dsw-alias-state-error-primary)` |
| **运行中扫光**（`[data-state=running] .o3BgMG_row::after`） | `width:300px`，`background: linear-gradient(90deg, transparent 0%, color-mix(in srgb, var(--dsw-alias-bg-base) 60%, transparent) 55%, transparent 100%)`，`animation: 2.6s ease-out infinite`（`left:-300px → 100%`，90% 起贴右） |
| **状态图标**（JS 决定，`dsh-client-ui-tool/lib/client.js:1188–1194`） | `error` → StateDot `state="error"`；`stopped` → StateDot `state="warning"`；StateDot 本体在 dist CSS（offset 2048–2921）：`::before` 满圆 `background:currentColor; opacity:.1`，`::after` `inset:20%` 实心；`[data-state=done]` → `color:var(--dsw-alias-state-success-primary)`，`warning` → `…-warn-primary`，`error` → `…-error-primary`，`idle` → `label-tertiary`；进行中为 `_matrix_1tljr_4` 方阵格子 `fill:currentColor; opacity:.15` + `animation:1s infinite` 追逐（`--dsh-state-ongoing: var(--dsw-static-deepseek-450)`） |
| cordis 自定义工具 | `[data-tool^=cordis_]` 的 leading/title/sep 全部 `color/background: var(--dsw-alias-state-business-primary)`，标题 `font-weight:500` |

**展开态**

| 部位 | 规格 |
|---|---|
| 折叠容器 `.ztWv_q_callRow` | `border-radius:6px`（ToolCallTree，`:1431`） |
| 子调用缩进 `.ztWv_q_subCalls` | `border-left: .5px solid var(--dsw-alias-border-l2)`；`margin:4px 0 2px 22px; padding-left:8px; gap:4px` |
| 输出滚动 | `.o3BgMG_bodyScroll { max-height: 260px; overflow-y:auto }`；每个 IO 段 `max-height:150px` |
| IO 卡片 `.o3BgMG_ioCard` | `border:.5px solid var(--dsw-alias-border-l1)`；`background: var(--dsw-alias-markdown-code-block)`（亮 `#f9fafb` / 暗 `#1b1b1c`）；`border-radius:12px`；`margin:4px 0 4px 4px`；`font: var(--dsw-font-markdown-code-block-small)`（11/16 代码族） |
| IO 栅格 | `grid-template-columns: max-content 1fr`；`column-gap:14px`；`padding:12px 16px`；标签 `position:sticky; top:0; color:var(--dsw-alias-label-caption)`；分隔线 `height:.5px; background:var(--dsw-alias-border-l2)`；文本 `color:label-secondary`，`[data-error]` → `state-error-primary` |
| 细分滚动条 | `::-webkit-scrollbar-thumb{ border:2px solid transparent; background-clip:padding-box; border-radius:6px }`，`::-webkit-scrollbar-track{ margin:6px 0 }` |
| Inspect 胶囊 `.o3BgMG_inspectButton` | 默认 `opacity:0`，父级 hover / `:focus-visible` → `1`（`transition:opacity .1s`）；`border:.5px solid var(--dsw-alias-border-l3)`；`background:var(--dsw-alias-bg-base)`；`border-radius:999px; corner-shape:round`；`font-size:11px; line-height:16px; padding:2px 8px`；`margin:4px 0 2px 4px`；hover `background:var(--dsw-alias-interactive-bg-hover-solid)` |
| 默认折叠阈值 | diff/read/search/terminal 各 **16 行**（`DEFAULT_*_MAX_LINES = 16`，见 `dist/assets/index-BKQ_L1z6.js`，导出名 `DEFAULT_DIFF_MAX_LINES/DEFAULT_READ_MAX_LINES/DEFAULT_SEARCH_MAX_LINES/DEFAULT_TERMINAL_MAX_LINES`） |

**退出码标签**（Terminal 卡；来源 `dsh-client-ui-tool/lib/client.js:472–490` 文案装配 + `:523–524` 判定 + 状态色 CSS）

- 文案来自 locale：`"terminal.exitCode": "退出码 {code}"`（`dsh-client-ui-conversation/lib/client.js:13784`）/ `"exit code {code}"`（`:13946`）。其余标签：`terminal.running` / `failed` / `done` / `noOutput` / `signal` / `collapse` / `expand`。
- 判定：`terminalFailed(model) = running !== true && (exitCode !== undefined && exitCode !== 0 || signal !== undefined)`（`dsh-client-ui-tool/lib/client.js:523–525`）→ 折叠行即显示红色（注释明确：这是折叠行唯一的失败信号）。
- 退出码状态色：失败 → `var(--dsw-alias-state-error-primary)`（亮 `#ec1313`＝red-600 / 暗 `#f25a5a`＝red-400）；成功/完成 → `var(--dsw-alias-state-success-primary)`（`#22c55e`，两主题同值）；中断 → `state-warn-primary`（`#f59e0b`，两主题同值）。
- 标签本体复用 **Tag 原语**（dist CSS offset 6008–7076）：`display:inline-flex; border-radius:999px; corner-shape:round; padding:1px 8px; font-size:11px; line-height:17px; font-weight:500`，tone 变体：
  - `danger` → `background:color-mix(in srgb, var(--dsw-alias-state-error-primary) 10%, transparent); color:var(--dsw-alias-state-error-primary)`
  - `warning` → 同上但 **12%** 混合，色 `state-warn-primary`
  - `success` → 10% 混合 + `state-success-primary`
  - `info` → 10% + `state-business-primary`
  - `neutral` → `background:var(--dsw-alias-bg-module-platform); color:var(--dsw-alias-label-secondary)`
  - `quiet` → 仅 `color:var(--dsw-alias-label-tertiary)`
  - `outline` → `border:.5px solid var(--dsw-alias-border-l4); color:label-tertiary`
  - `solid` → `background:var(--dsw-alias-label-primary); color:var(--dsw-alias-bg-layer-3)`
- 命令终端块本体（TerminalBlock，dist CSS offset 23642–26311）：`margin:16px 0`；`padding-left: var(--dsl-terminal-gutter=30px)`；`background:var(--dsw-alias-markdown-code-block)`；`border-radius:12px`；header `padding:9px 14px 9px 30px; margin-left:-30px; max-height:150px`，非运行时 `border-bottom:.5px solid var(--dsw-alias-border-l2)`；cwd `color:label-tertiary`；命令 `color:label-primary; white-space:pre`；状态位 `position:sticky; top:0; height:var(--dsl-terminal-line-height); color:var(--dsw-alias-state-error-primary)`；输出 `padding:12px 14px 12px 0`，`max-height: var(--dsl-terminal-output-max-height, none)`（工具行内为 224px）；展开按钮 `color:label-tertiary`，hover `label-secondary`。

**推理（thinking）行**（ReasoningRow，`:2847`）：与 ToolRow 同构，折叠高 `calc(24px + D)` + `contain:size layout`；running 扫光同上（2.6s）；思考体 `padding:4px 0 4px calc(22px + D)`，`color:label-tertiary`，13px / `calc(20px + D2)`，`white-space:pre-wrap`。

### 5.3 代码块（CodeBlock 原语，dist CSS offset 31450–33862）

```css
._block_rsn9u_4 {                     /* 容器 */
  --dsl-code-block-banner-background-color: var(--dsw-alias-markdown-code-block-banner); /* 亮 #f9fafb / 暗 #2c2c2e */
  --dsl-code-block-border-radius: 12px;
  --dsl-code-block-banner-font: 11px/18px var(--dsw-font-family);
  --dsl-code-block-content-font: var(--dsw-font-markdown-code-block);  /* 11px/19px 代码族 */
  --dsl-code-block-background: var(--dsw-alias-markdown-code-block);    /* 亮 #f9fafb / 暗 #1b1b1c */
  position: relative; margin: 16px 0; color: var(--dsw-alias-label-primary);
  background: var(--dsl-code-block-background);
  border-radius: var(--dsl-code-block-border-radius);
}
._bannerWrap_rsn9u_24 { position: sticky; top: 0; z-index: 6;
  background-color: var(--dsw-alias-bg-base);
  border-top-left/right-radius: var(--dsl-code-block-border-radius) }
._banner_rsn9u_24 {                   /* 标题栏 */
  background: var(--dsl-code-block-banner-background-color);
  padding: 9px 14px; display: flex; justify-content: space-between; align-items: center; gap: 12px;
  font: var(--dsl-code-block-banner-font);
  border-top-left/right-radius: var(--dsl-code-block-border-radius) }
._infostring_rsn9u_45 {               /* 语言 / info string 标签 */
  color: var(--dsw-alias-label-primary);
  font-family: var(--ds-font-family-code); font-size: 11px; line-height: 18px;
  min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap }
._copyButton_rsn9u_62 { background-color: #fff0; border: none; padding: 0; margin: 0;
  color: inherit; cursor: pointer; font: inherit }
._block_rsn9u_4 :where(pre) {
  font: var(--dsl-code-block-content-font); padding: 16px; margin: 0 !important;
  overflow-x: auto; white-space: pre-wrap; word-break: break-all;
  background: var(--dsl-code-block-background);
  border-bottom-left/right-radius: var(--dsl-code-block-border-radius) }
._block_rsn9u_4 :where(pre) code { font: inherit; background: none; padding: 0 }
```

- 非末个代码块额外 `margin-bottom: 11px`（`:not(:last-child)`）。
- 复制按钮**无背景、无独立色**，继承 banner 的 `color`（= `label-primary`）；文案 `copy`/`copied`（工具 locale）。
- **行号变体** `.numbered`：`code { counter-reset: source-line; display:block; white-space:normal }`；`.line { padding-inline-start: calc(var(--dsl-code-block-line-number-width) + 12px); counter-increment: source-line; min-height:1lh }`；`.line::before { content:counter(source-line); position:absolute; inset-inline-start:0; width:var(--dsl-code-block-line-number-width); color:var(--dsw-alias-label-tertiary); text-align:end; user-select:none }`。
- 工具内的代码体把内容字号降一档：`--dsl-code-block-content-font: var(--dsw-font-markdown-code-block-small)`（11/16），来源 `dsh-client-ui-tool/lib/client.js:1143`（`.o3BgMG_codeBody`）。

### 5.4 Markdown 正文排版（`kcgor` 模块，dist CSS offset 34097–38899）

| 元素 | 规格 |
|---|---|
| 根 `.markdown` | `font: var(--dsw-font-markdown-base)`（14/24）；`color:var(--dsw-alias-label-primary)`；`overflow-wrap:anywhere`；`min-width:0` |
| 首个/末个子元素 | `margin-top/bottom: 0 !important` |
| **段落** | `margin: 16px 0`（首段除外） |
| h1 | `font: var(--dsw-font-markdown-h1)`（21/30 · 700）；`margin: 32px 0 16px` |
| h2 | 19/28 · 700；`margin: 32px 0 16px` |
| h3 | 18/26 · 700；`margin: 32px 0 16px` |
| h4 | 14/24 · 600；`margin: 16px 0` |
| h5/h6 | `font: var(--dsw-font-markdown-base-strong)`（14/24 · 600）；`margin:16px 0`；标题内 `strong { font-weight: inherit }` |
| h4/h5/h6 紧邻列表 | 列表 `margin-top: 8px`，标题 `margin-bottom: 8px`（`:has(+:where(ul,ol))`） |
| **列表** | `ul,ol { margin:16px 0; padding-left:18px }`；`li:not(:first-child) { margin-top:6px }`；`li > ul/ol { margin-top:4px }`；`li::marker { line-height:24px; color:var(--dsw-alias-label-secondary) }`；`ol` 嵌 `ol { list-style-position: inside; padding-left:0 }` 且其 `li p { display:inline }`；`li > p { margin:8px 0 }`；`li > *:first-child{margin-top:0}`、`li > *:last-child:not(.md-code-block){margin-bottom:0}` |
| **链接** | `color:var(--dsw-alias-link)`；`font-weight:500`；默认无下划线；`:hover/:focus` → `text-decoration: underline dotted var(--dsw-alias-link); text-underline-offset:3px`；`:focus-visible` → `box-shadow:0 0 0 2px var(--dsw-alias-state-business-primary)`；左右各留 `border-left/right:3px solid transparent; margin-inline:-3px`（放大点击区）；外链图标 `_linkIcon` 1.1em，`vertical-align:-.25em; margin-right:5px` |
| **blockquote** | `border-left: 2px solid var(--dsw-alias-label-caption)`；`margin: 16px 0 0`；`padding-left: 14px` |
| **inline code** | `background-color: var(--dsw-alias-markdown-inline-code)`（亮 `#fafafa` / 暗 `#292929`）；`border: .5px solid var(--dsw-alias-border-l1)`；`border-radius: 6px`；`padding: 0 5px`；`display:inline-flex; align-items:center`；`font: var(--dsw-font-markdown-code)`（12/19 代码族）；**字号强制 `font-size: .875em !important`**（≈12.25px @14px 正文）；标题内的 code `font: inherit`（保留代码族） |
| `pre` | `margin:16px 0; font-family:var(--ds-font-family-code); overflow:auto` |
| **hr** | `border:none; height:.5px; margin:32px 0; background:var(--dsw-alias-border-l2)` |
| **表格** | 外层 `.tableScroll { max-width:100%; overflow-x:auto; overscroll-behavior-x:contain }`；`table { border-collapse:collapse; width:max-content; max-width:max-content }`；`th { text-align:start; padding:10px 16px; border-bottom:.5px solid var(--dsw-alias-border-l3); font: var(--dsw-font-markdown-table-head); max-width:min(30vw,320px); min-width:100px }`；`td { padding:10px 16px; border-bottom:.5px solid var(--dsw-alias-border-l2); font:var(--dsw-font-markdown-table); max-width:min(30vw,320px); min-width:100px }`；首列 `padding-left:0`，末列 `padding-right:0`；表格内 code 强制 `font-size:11px`；宽表变体 `.md-table-wide` 默认 `overflow-x:hidden` + `padding-bottom:var(--dsh-scrollbar-width,8px)`，hover/focus 时才 `overflow-x:auto`；`.tableFill table { width:100% }`；`.markdown .katex-display { max-width:100%; overflow-x:auto }` |
| 任务列表复选框 | `margin:0 8px 0 0; accent-color: var(--dsw-alias-label-secondary)` |
| 图片 | `.image { display:block; max-width:100%; height:auto; border-radius:8px; background:var(--dsw-alias-bg-base); object-fit:contain }`；alt 文本 `color:label-tertiary; font-style:italic` |
| 文件提及按钮 | `font-weight:500; color:var(--dsw-alias-link)`；hover 同链接（dotted 下划线） |

### 5.5 输入区 / composer（`InputBar.module.css`，`dsh-client-ui-conversation/lib/client.js:15756`）

| 元素 | 规格 |
|---|---|
| 外层 `.uV2eYG_root` | `padding: 0 var(--dsh-composer-side-clearance=16px) 8px`；居中列 |
| **卡片 `.uV2eYG_card`** | `max-width: var(--dsh-composer-card-max-width)`（= 内容列宽 + 32px）；`background: var(--dsw-specific-input-major)`（亮 `#fff` / 暗 `#2c2c2e`）；**`border: 0` + `box-shadow: var(--dsw-elevation-soft)`**，且 `--dsw-elevation-stroke-color: var(--dsw-alias-border-l2)`；**`border-radius: 22px`**；`padding-top: 8px`；`gap: 12px`；`font-size: var(--dsh-content-font-size,14px)`；`line-height: calc(24px + D)`；滚动条重绑为 `l2` |
| 工作区拖入态 `::after` | `inset:-1px`，`border-radius:22px`，`background:var(--dsw-alias-border-l4)`（hover → `state-business-primary`），用 SVG **`mask`（`stroke-dasharray:4 4`）** 画出 1px 虚线胶囊轮廓 |
| 文本区 `.uV2eYG_input` | `min-height: 36px`（hero 态 **52px**）；`padding: 4px 8px 0 14px`；`font-family: var(--dsw-font-family)`；`color: var(--dsw-alias-label-primary)`；**`caret-color: var(--dsw-alias-state-business-primary)`**；`outline: none`；`white-space:pre-wrap; word-break:break-word; overflow-wrap:anywhere` |
| 占位符 `.uV2eYG_placeholder` | `position:absolute; inset:4px 8px auto 14px`；`color: var(--dsw-alias-label-caption)`；hero 态 2 行截断（`-webkit-line-clamp:2`）；另有末段 `::after { content: var(--dsh-composer-hint); color: var(--dsw-alias-label-caption) }` |
| **聚焦态** | **composer 卡片没有 `:focus-within` 特殊样式**（对整张表 grep `focus` = 0 命中）。聚焦反馈仅由光标色 + 无 outline 构成。这是刻意的：层次靠 `elevation-soft` 常驻阴影表达。**未找到「聚焦高亮边框」规格。** 系统性的焦点样式是 `:focus-visible` → `outline: 2px solid var(--dsw-alias-label-primary); outline-offset:-2px`（侧栏导航行）或 `box-shadow: 0 0 0 2px var(--dsw-alias-state-business-primary)`（链接/表格），复刻时沿用这两式之一。 |
| 文本高度上限 | `.uV2eYG_scroll { max-height: var(--dsh-composer-text-max-height=336px) }`，`margin-right:4px` |
| 底行 `.uV2eYG_row` | `padding:2px 8px 6px; gap:12px; flex-wrap:wrap; container-type:inline-size` |
| 添加按钮 `.uV2eYG_add` | 28×28，`border-radius:999px; corner-shape:round`，`background: var(--dsw-specific-selector)`（亮 `#f5f6f7` / 暗 `#353638`），`color:label-primary`；hover `var(--dsw-alias-interactive-bg-hover-solid)`；`disabled` → `opacity:.5` |
| 选择器（模型/权限） `.uV2eYG_select` | 高 28px，`max-width:220px`，`border-radius:8px`，`font-size:13px; font-weight:500; line-height:20px`，`color:label-secondary`，`padding:0 20px 0 8px`；箭头是内联 SVG data-URI（12×12，stroke `#81858C`，`background-position: right 4px center`）；hover `background-color:var(--dsw-alias-interactive-bg-hover)` |
| **发送按钮 `.uV2eYG_primary`** | **34×34**，`border-radius:999px; corner-shape:round`；`background: var(--dsw-alias-button-info-fill)`（亮 `#4176e6`＝deepseek-500 / 暗 `#679efe`＝deepseek-400）；`color:#fff`；`transition: background-color .1s`；hover → `var(--dsw-alias-button-info-hover)`（亮 `#679efe` / 暗 `#4176e6`，与信息蓝反相）；`disabled` → `opacity:.4`；`transform: translateY(-2px)`（视觉上浮） |
| 排队指示点 `.uV2eYG_pending` | 8×8，`border-radius:50%; corner-shape:round`，`background:var(--dsw-alias-state-business-primary)`，`animation:1s ease-in-out infinite alternate`（opacity .35 ↔ 1） |
| 通知条 `.uV2eYG_notice` | `background:var(--dsw-alias-interactive-bg-hover)`；`color:label-secondary`；`border-radius:8px`；`padding:4px 8px`；12px/18px；`margin-bottom:6px` |
| 重试按钮 `.uV2eYG_retry` | `border:1px solid`（继承色）；`border-radius:4px`；`padding:1px 8px`；12px；`margin-left:8px` |
| 编辑器内引用高亮 | `.q44v1G_textRef { color: var(--dsw-alias-state-business-primary); box-decoration-break: clone }`（`:12220`） |
| Hero（空会话）输入 | 卡片同款；外层 `.pXSMma_stack { max-width: var(--dsh-composer-card-max-width); gap:12px }`；标题 26/32/500；模态输入 `height:44px; border-radius:22px; border:.5px solid var(--dsw-alias-border-l4); padding:7px 14px`，14/22 |

**按钮原语规格**（dist CSS offset 4258–5384，供其它按钮复用）：

```css
._button_cfgyt_4 { display:inline-flex; align-items:center; justify-content:center; gap:4px;
  border:none; border-radius:18px; cursor:pointer; font-size:14px; line-height:22px;
  color:var(--dsw-alias-label-primary); background:transparent; padding:0 14px }
._md_cfgyt_24  { height:36px }
._sm_cfgyt_30  { height:28px; font-size:12px; line-height:18px; padding:0 10px; border-radius:14px }
._primary_cfgyt_38 { background:var(--dsw-alias-button-primary-fill); color:var(--dsw-alias-label-primary-foreground) }
._primary_cfgyt_38:hover:not(:disabled) { background:var(--dsw-alias-button-primary-hover) }
._ghost_cfgyt_47:hover:not(:disabled)  { background:var(--dsw-alias-interactive-bg-hover) }
._ghost_cfgyt_47:active:not(:disabled) { background:var(--dsw-alias-interactive-bg-active) }
._outline_cfgyt_56 { border:.5px solid var(--dsw-alias-border-l3); background:transparent }
._toolbar_cfgyt_65 { background:var(--dsw-alias-button-tool-bar-fill) }   /* #a2a4a680 / #54555780 */
._icon_cfgyt_73 { display:inline-flex; width:16px; height:16px; align-items:center; justify-content:center }
._button_cfgyt_4:disabled { cursor:not-allowed; opacity:.4 }
```

### 5.6 浮层与通用控件原语（dist CSS，字节区间见各行）

| 组件 | 规格 |
|---|---|
| **Tooltip** `_bubble_1nw3t_1`（offset 18176–18792） | `position:fixed; z-index:100`；`max-width:50vw`；`padding:3px 7px`；`border-radius:8px`；`background:var(--dsw-alias-tooltip-bg)` = 亮 `#2c2c2e`（neutral-bluish-850）/ 暗 `#43454a`（neutral-bluish-750）——**两主题都是深底**；`color: var(--dsw-static-neutral-bluish-00)` = `#fff`（两主题白字）；`font-size:13px; line-height:20px`；`white-space:pre-line`；`animation: tooltip-in .15s var(--ds-ease-in-out)`；`[data-side=right] translateY(-50%)`、`[data-side=bottom] translate(-50%)`、`[data-side=top] translate(-50%,-100%)` |
| **Toast** `_toast_e5v0f_6`（offset 18824–19705） | `position:fixed; top:40px; left:50%; z-index:1100`；`max-width:min(640px, calc(100vw - 48px))`；`padding:12px 16px`；`border-radius:14px`；`background:var(--dsw-alias-button-contrast-fill)`（亮 `#61666b` / 暗 `#f9fafb`）；`color:var(--dsw-alias-label-primary-inverted)`（亮 `#fff` / 暗 `#353638`）；14/22；`box-shadow:var(--dsw-shadow-lv3)`；图标 `color:var(--dsw-alias-state-warn-label)`；入场 `.16s ease-out`（`translate(-50%,-6px)→translate(-50%)`）+ 在 `var(--dsh-toast-hold,3s)` 后 1s 淡出 |
| **Menu** `_list_1nxmc_8`（offset 8549–11543） | 容器 `padding:4px; border:0; border-radius:20px; background:var(--dsw-specific-menu)`；`box-shadow: var(--dsw-elevation-prominent)` + `--dsw-elevation-stroke-color:var(--dsw-alias-border-l1)`；滚动条重绑 `l2`；`min-width:218px; max-width:360px`；`position:absolute; top:calc(100% + 4px); left:0; z-index:100`（portal 变体 `position:fixed; z-index:1100`；`.sideTop` → `bottom:calc(100% + 4px)`、`.alignEnd` → `right:0`；`.scrollable` → `max-height:calc(100vh - 24px)`）。**item**：`min-height:40px; padding:8px 10px; border-radius:10px; gap:8px; font-size:14px; line-height:22px; color:label-primary`，hover `var(--dsw-alias-interactive-bg-hover)`，`disabled` → `opacity:.4`；图标 16×16 `label-tertiary`；`selected` → `background:transparent`（靠勾选图标 `_check_1nxmc_182` 表达）。**dense 变体**：item `min-height:34px; padding-block:5px`。**compact 变体**：容器 `min-width:164px; padding:2px; border-radius:7px`，item `min-height:26px; padding:3px 7px; border-radius:5px; font-size:12px; line-height:18px; gap:6px`，图标 14×14，分隔线 `margin:2px`，分组标题 11/16。**footer**：`margin-top:4px; padding-top:4px; border-top:.5px solid var(--dsw-alias-border-l2)` |
| **HoverCard** `_card_1b2ny_13`（offset 11631–12190） | `position:fixed; z-index:100`；`width:244px`；`padding:12px 16px`；`border-radius:12px`；**`--dsh-hovercard-bg:#2C2C2E`（硬编码深底，两主题一致）**；`box-shadow: var(--dsw-shadow-lv3)`；`focus-visible: outline:2px solid var(--dsw-alias-state-business-primary); outline-offset:2px`。文字色由消费方定义（会话行 hover 卡：标题 `#fff` 14/20，路径 `#cfd3d6` 12/16，状态 `#adb2b8` 12/20） |
| **Dialog** `_root_w1urq_2`（offset 12299–13600） | 遮罩层 `position:fixed; inset:0; z-index:1000; display:flex; align-items:center; justify-content:center; padding:24px`；`.mask { background:var(--dsw-alias-bg-mask-1); backdrop-filter:var(--dsw-mask-blur) }`；**对话框** `width:min(380px,100%); border:0; border-radius:24px; background:var(--dsw-alias-bg-layer-2); box-shadow:var(--dsw-elevation-prominent); gap:20px; padding:0 0 24px`；header `padding:22px 14px 12px 24px` |
| **Pill** `_pill_e3ygd_1`（offset 5485–5832） | `display:inline-flex; gap:4px; height:24px; padding:0 8px; border:none; border-radius:12px; font-size:12px; line-height:18px; color:var(--dsw-alias-label-secondary); background:var(--dsw-alias-bg-layer-2)`；可点变体 hover `--dsw-alias-interactive-bg-hover`；`active` → `color:label-primary; background:var(--dsw-alias-button-ghost-active-fill); box-shadow: inset 0 0 0 1px var(--dsw-alias-button-ghost-active-border)` |
| **确认对话框** `_confirmation_1nu42_1`（offset 14028–15069） | 在 Dialog 之上加 `width:min(440px,100%); max-height:calc(100dvh - 48px); overflow:hidden`；警告行 `gap:10px`，14/22，`color:label-secondary`，图标 `color:var(--dsw-alias-state-error-primary)`（`margin-top:2px`）；勾选确认块 `margin-top:20px; gap:10px`，`color:label-primary` |

### 5.7 侧栏「面板导航项」的 active/hover（对照 §3.2）

设计系统为侧栏导航专门定义了 3 个语义 token（`design-platform.css`）：

| token | 亮 | 暗 |
|---|---|---|
| `--dsw-specific-sidebar-nav-item-hover` | `#f1f3f5`（neutral-bluish-75） | `#2c2c2e`（neutral-bluish-850） |
| `--dsw-specific-sidebar-nav-item-active` | `#ebeef2`（neutral-bluish-100） | `#43454a`（neutral-bluish-750） |
| `--dsw-specific-sidebar-nav-item-active-accent` | `#e4edfd`（deepseek-100） | `#353638`（neutral-bluish-800） |

但实测组件**并未使用这三个 token**，而是走通用交互 token：hover = `--dsw-alias-interactive-bg-hover`（亮 `#2631480f` / 暗 `#ffffff14`，半透明叠加），active/selected 同值或 `--dsw-alias-interactive-bg-active`（亮 `#2631481a` / 暗 `#ffffff24`）。会话行（§3.3）同理。**复刻时以组件实测值为准，token 表里的三个 sidebar-nav-item 变量可作为「设计意图」备用。**

### 5.8 右栏面板（文件/预览）

`dsh-client-ui-sidebar-right/lib/client.js:607`：`background:var(--dsw-alias-bg-base)`；`border-left:.5px solid var(--dsw-alias-border-l4)`；`z-index:10`；默认 `visibility:hidden; transform:translate(100%)`，`[data-sidebar-right-open]` → `transform:none`，过渡 `.3s var(--ds-ease-in-out)`（visibility 延迟 .3s）；全屏 `position:fixed; inset:0; z-index:40; border:none`。图标按钮 28×28 / `border-radius:28px` / `padding:6px` / svg 15×15。折叠按钮图标 `transform:scaleX(-1)`。
DockKit 标签条（dist CSS `_tabStrip_17p4l_156`，offset 41378–51319）：`height:28px; padding:10px 6px 0 10px`；tab `border-radius:8px 8px 0 0`；`paneBody` 重绑滚动条 l2；浮动面板 `border-radius:12px` + `--dsw-elevation-stroke-color:var(--dsw-alias-border-l2)`；拖拽提示 `.5px dashed var(--dsw-alias-border-l4)`，`z-index:10`。

### 5.9 启动页（`1fywu`，dist CSS offset 6–1746）

自带一套亮/暗硬编码变量（因为要在 token 表加载前渲染）：

| token | 亮 | 暗（`body[data-ds-dark-theme] ._boot_1fywu_3`） |
|---|---|---|
| `--dsh-boot-bg` | `#fff` | `#151517` |
| `--dsh-boot-label-primary` | `#0f1115` | `#f9fafb` |
| `--dsh-boot-label-secondary` | `#61666b` | `#cfd3d6` |
| `--dsh-boot-label-tertiary` | `#81858c` | `#adb2b8` |
| `--dsh-boot-border` | `rgb(0 0 0 / 10%)` | `rgb(255 255 255 / 12%)` |
| `--dsh-boot-brand` | `#0f1115` | `#f9fafb` |

每处消费都写成 `var(--dsw-alias-xxx, var(--dsh-boot-yyy))`，即 token 表加载后自动接管。布局：`display:grid; place-items:center; height:100%`；卡片 `gap:16px`；wordmark 16/24/600 + `letter-spacing:.08em`；提示 12/18；spinner 20×20，`border:2px solid` + `conic-gradient` 弧（`--dsh-boot-arc:72deg`）+ `radial-gradient` mask 描边，`.8s linear infinite`；失败面板 `max-width:480px`，条目用代码族 12/18。

---

## 6. 滚动条（全局规格，`scrollbar.css`，`dsh-client-ui-theme/lib/client.js:1055`）

```css
body {
  --dsh-scrollbar-thumb: var(--dsw-alias-scrollbar-bg-l1);      /* 亮 #e5e5e5 / 暗 #3c3c3d */
  --dsh-scrollbar-thumb-hover: var(--dsw-alias-scrollbar-hover-l1); /* 亮 #d4d4d4 / 暗 #545557 */
  --dsh-scrollbar-width: 8px;
}
@supports not selector(::-webkit-scrollbar) {           /* Firefox 路径 */
  body, body * { scrollbar-width: thin; scrollbar-color: var(--dsh-scrollbar-thumb) transparent }
}
::-webkit-scrollbar { width: 8px; height: 8px }         /* WebKit 路径 */
::-webkit-scrollbar-track { background: transparent }
::-webkit-scrollbar-thumb { background: var(--dsh-scrollbar-thumb); border-radius: 4px }
::-webkit-scrollbar-thumb:hover { background: var(--dsh-scrollbar-thumb-hover) }
::-webkit-scrollbar-corner { background: transparent }
```

**再绑定约定（3 条，均为实测）**：
1. 高层级表面（菜单 / 浮层 / 对话框 / composer / 预览面板 body）在**自己的容器**上把两变量重绑为 `l2` 档（共 21 处，如 `._list_1nxmc_8`、`.hHd-Xa_root`、`.uV2eYG_card`、`._paneBody_17p4l_478`、`._floatBody_17p4l_686`）。
2. 合法的另一取值是 `transparent` —— `ui-sidebar` 在指针不在栏内时整列隐藏滚动条（`.hHd-Xa_quietBars { --dsh-scrollbar-thumb:transparent; --dsh-scrollbar-thumb-hover:transparent }`）。
3. `--dsh-scrollbar-width` 供需要与占位滚动条对齐的表面使用（宽表 `.md-table-wide` 用它做 `padding-bottom`）。
两条渲染路径在构造上互斥（`@supports not selector(::-webkit-scrollbar)` vs 伪元素），因此 hover 色只在 WebKit 路径生效。局部细滚动条再加一层透明边框：`::-webkit-scrollbar-thumb{border:2px solid transparent;background-clip:padding-box;border-radius:6px}`（TerminalBlock / ToolRow IO 段实测）。

---

## 7. shiki 语法高亮的双主题机制（**不是 github-dark 那类内置主题对**）

**结论：完全没有使用 `github-dark` / `nord` 之类的具名主题。** 机制是 **一个自定义的 `css-variables` 主题 + `body[data-ds-dark-theme]` 上的 token 覆盖**，因此主题切换**不重新高亮**，只是 CSS 变量换值。

### 7.1 高亮器构造（`dsh-web-frontend/dist/assets/index-BKQ_L1z6.js`）

```js
// 主题定义：createCssVariablesTheme 等价物，前缀 --shiki-
const jm = da({ name: "css-variables", variablePrefix: "--shiki-", fontStyle: true });
// 引擎：oniguruma 兼容层，容错 + 惰性正则编译
const bm = fa({ forgiving: true, regexConstructor: t => ga(t, { lazyCompileLength: Infinity }) });
const highlighter = ha({ themes: [jm], langs: Lm, engine: bm });   // Lm = 预置语言集合
// 三种调用：codeToHtml / codeToTokens / codeToTokensBase 一律 theme: "css-variables"
Om(code, lang) => highlighter.codeToHtml(code, { lang, theme: "css-variables" })
```

`dist/assets/langs/*.js`（23 个：c、cpp、csharp、css、go、html、ini、java、kotlin、less、lua、markdown、mdx、php、python、ruby、rust、scss、sql、swift、toml、xml、yaml）是**运行时按需 `loadLanguageSync` 注入的文法**，加载完成后触发重绘回调。

输出容器（同 bundle）：

```js
const vg = { className: "shiki css-variables",
             style: { backgroundColor: "var(--shiki-background)", color: "var(--shiki-foreground)" } };
```

行/词以 `<span class="line">` + 内联 `style="color:…"`（由 token 的 `color`/`fontStyle` 位翻译成 `font-style` / `font-weight` / `text-decoration`）渲染；CodeBlock 的 `pre._shiki` 再用 `!important` 把背景强制回 `var(--dsl-code-block-background)`（`._block_rsn9u_4 :where(pre._shiki_rsn9u_93){background:var(--dsl-code-block-background)!important}`）。

### 7.2 双主题色板（`shiki.css`，`dsh-client-ui-theme/lib/client.js:1061`）

```css
:root {                                   /* = 亮色 */
  --shiki-foreground: var(--dsw-alias-label-primary);        /* #0f1115 */
  --shiki-background: var(--dsw-alias-markdown-code-block);  /* #f9fafb */
  --shiki-token-constant: #1c7ed6;
  --shiki-token-string: #2f9e44;
  --shiki-token-comment: #868e96;
  --shiki-token-keyword: #d6336c;
  --shiki-token-parameter: #e8590c;
  --shiki-token-function: #6741d9;
  --shiki-token-string-expression: #2b8a3e;
  --shiki-token-punctuation: #495057;
  --shiki-token-link: #1971c2;
}
body[data-ds-dark-theme] {                /* = 暗色覆盖 */
  --shiki-token-constant: #4dabf7;
  --shiki-token-string: #69db7c;
  --shiki-token-comment: #adb5bd;
  --shiki-token-keyword: #faa2c1;
  --shiki-token-parameter: #ffa94d;
  --shiki-token-function: #b197fc;
  --shiki-token-string-expression: #8ce99a;
  --shiki-token-punctuation: #ced4da;
  --shiki-token-link: #74c0fc;
}
```

要点：
- `--shiki-foreground` / `--shiki-background` **不写死**，指回语义 token，因此自动随主题（亮 `#0f1115`/`#f9fafb`，暗 `#f9fafb`/`#1b1b1c`）。
- 9 个 token 色是**硬编码字面量**，且**没有对应的 `--dsw-alias-*` 语义变量**（即语法色不进入主色板体系）。这套色相取自 Open Color（`1c7ed6 / 2f9e44 / d6336c / 6741d9 / 4dabf7 / 69db7c …`），**未找到**其命名出处。
- 暗色块只覆盖 9 个 token 色，**不重复声明** `-foreground` / `-background`（它们已通过 alias 联动）。
- `variablePrefix:"--shiki-"` 意味着 shiki 会把每种语义（`constant/string/comment/keyword/parameter/function/string-expression/punctuation/link`）映射到同名变量；若某语言用到未覆盖的语义键，则回退为无 color（继承 `--shiki-foreground`）。
- `fontStyle:true` 让 `italic`/`bold` 也走 token，因此注释斜体、关键字粗体在两种主题下都成立。
- **复刻要点**：给 `<pre class="shiki css-variables">` 输出内联 `var(--shiki-token-*)` 引用（或直接改用真实 highlight.js/prismjs 类名并自建同名变量映射），然后把上面两张表照抄即可得到与原版一致的观感；切换主题时无需重跑高亮器。

---

## 8. 复刻落地清单（Vue 3 + 纯 CSS 变量）

按「照抄即可」的顺序给出最小必要集：

1. **一个 `tokens.css`**：把 §1.1（73 个 static）、§1.2（90 个 alias/specific）、§1.3（8 个 shadow/elevation/gradient）、§1.4（字号 token）、§1.6（4 个 `--ds-*` 常量）、§6（滚动条 3 条）、§7.2（11 个 `--shiki-*`）合并。结构必须是：
   ```css
   :root { /* --dsw-font-family, --ds-font-family-code, --ds-ease-in-out, --ds-transition-duration*, --shiki-* 基线 */ }
   body  { /* --dsw-static-* + --dsw-alias-* + --dsw-specific-* + shadow + 字号阶梯 + scrollbar */ }
   body[data-ds-dark-theme] { /* 仅 --dsw-alias-* / --dsw-specific-* / 2 个 gradient / 9 个 --shiki-token-* */ }
   ```
   （§1.2 表里的两列计算值可以直接写成 flat 值，省略 static 层；但保留 static 层有利于对齐语义。）
2. **切换属性只认一个**：`document.body.toggleAttribute('data-ds-dark-theme', isDark)` + `document.documentElement.style.colorScheme = isDark ? 'dark' : 'light'`。同时 `document.body.style.setProperty('--dsh-content-font-size', px + 'px')`（12–17，默认 14）。
3. **字号联动**：实现 `--dsh-content-font-delta`、`--dsh-content-font-size-secondary`、`--dsh-content-font-delta-secondary` 三个派生式（§1.7），所有会话内文本用 `calc(24px + var(--dsh-content-font-delta,0px))` 这类写法，不要写死 24px。
4. **布局**：`display:grid; grid-template-columns: <sb>px minmax(0,1fr) <rb>px`，常量按 §3.1 表（280 / 264–420 / 56 / 1024 / 300 / 0.45 / 0.7）。列宽过渡 `grid-template-columns .3s cubic-bezier(.4,0,.2,1)`，并在 `[data-dragging]` 与 reduced-motion 下关掉。
5. **`--dsh-chat-content-width` 必须 JS 驱动**：用 `ResizeObserver` 写入列宽（§1.7），CSS 里保留 `clamp(680px, calc(var(--dsh-conversation-column-width,0px) * .64), 920px)` 兜底。
6. **圆角**：没有 token，用 §1.5 频次表选值；关键锚点：代码/工具块 12、卡片 12/16/20、菜单 20、行 6/8、气泡与 composer 22、图标按钮 50%/999px。若目标浏览器支持 `corner-shape`，加 §0.3 那段并遵守「正圆必须配对 `corner-shape:round`」。
7. **z-index**：用 §1.5 的 6 档就够（1 / 7–11 / 20 / 100 / 1000 / 1100）。
8. **状态一律用 `data-*` 属性**（§4 第 1 条），不要造 `.is-xxx` 修饰类，才能与原版选择器一一对应。
9. **reduced-motion**：每个带 transition/animation 的组件补 `@media (prefers-reduced-motion: reduce){ * { transition:none; animation:none } }` 的等价局部块。
10. **可省略项**：KaTeX 字体与 `--deliverable-*` / `--trajectory-*` / DockKit 多面板（若不做多面板工作台）。

### 8.1 明确「未找到」的项

| 询问项 | 结果 |
|---|---|
| Tailwind / BEM / vanilla-extract 的痕迹 | **未找到**（判定为 CSS Modules，见 §4） |
| 间距刻度 token（`--spacing`/`--space-*`/`--gap-*`） | **未找到**，间距硬编码 |
| 圆角 token（`--radius-*`） | **未找到**（只有 `--dsl-*-radius` 这类组件局部量） |
| z-index token 层（`--z-*`） | **未找到**，全部数字字面量 |
| `[data-theme=…]` / `.dark` / CSS 媒体查询式主题切换 | **未找到**，只有 `body[data-ds-dark-theme]` |
| 具名 shiki 主题（github-dark 等） | **未找到**，用 `css-variables` 自定义主题（§7） |
| 产品自托管 UI 字体（woff2） | **未找到**，UI 纯系统字；`dist/assets/fonts/` 只有 KaTeX 数学字体 |
| 输入框聚焦态高亮（focus ring / 边框变色） | **未找到**（composer 表内 `focus` 关键字 0 命中，仅 `caret-color`） |
| 统一的「顶栏高度」常量 | **未找到**（76px / 28px / 60px 三处各自定义，§3.5） |
| 语法高亮色的 `--dsw-alias-*` 语义对应 | **未找到**（9 个 shiki 色是孤立字面量） |
| `--dsw-font-sm-13` | 被 ToolRow **引用但从未定义**（§1.4 缺陷条目） |
| 首帧调色板引导脚本 | README 有描述，安装包内**未找到**实现 |
| 各包 `src/` 目录 | **不存在**（发布物只有 `lib/`；样式以字符串内联在 `lib/client.js`），本文所有「源文件」定位均指该字符串在 `lib/client.js` 中的行号 |

---

## 附录 A：字体栈（可直接复制）

```css
:root {
  --dsw-font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Helvetica, Arial, sans-serif;
  --ds-font-family-code: "SF Mono", "JetBrains Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, Courier, "PingFang SC", "Microsoft YaHei";
}
```

## 附录 B：本文档的复现方法（供校验）

1. 列出 108 张运行时样式表：在 `…/node_modules/@deepseek-ai/*/lib/*.js` 中匹配 `//#region \0dsh-css:` / `\0dsh-inline-css:` 标记，取紧随其后的 `const <name> = "…"` 字符串常量（注意转义；部分字符串超过 6 KB，搜索窗要放宽）。标记数 = 108，提取数 = 108，**零遗漏**（已核对）。
2. 自定义属性全量：把 108 张表 + 2 份 dist CSS 一起做原始计数 `--[a-zA-Z0-9-_]+\s*:` 得 730；逐条解析同样得 730（441 个唯一名），二者相等即无遗漏。
3. 亮/暗归组：只看选择器是否为 `body` 或 `body[data-ds-dark-theme]`（token 表），以及 `body[data-ds-dark-theme] .xxx` 形式的组件覆盖（全语料仅 5 张表用到：deliverables、sidebar-right/guide、theme 的 3 张表）。
4. dist CSS（单行文件）用字节偏移定位；`lib/client.js`（多行）用行号。

## 附录 C：来源文件清单（本文全部引用）

| 文件 | 用途 |
|---|---|
| `…/dsh-web-frontend/dist/assets/index-DPX2bQLO.css` | 共享原语 CSS（单行，51 947 B）；行号以字节偏移表示 |
| `…/dsh-web-frontend/dist/assets/vendor-BNsW4eBh.css` | KaTeX（20 `@font-face` + `.katex*`） |
| `…/dsh-web-frontend/dist/index.html` | 外壳（确认无主题引导脚本） |
| `…/dsh-web-frontend/dist/assets/index-BKQ_L1z6.js` | shiki 装配、原语导出表、`DEFAULT_*_MAX_LINES=16` |
| `…/dsh-client-ui-theme/lib/client.js` | 6 张全局 token 表（1046 / 1049 / 1052 / 1055 / 1058 / 1061）、字号 schema（906）、locale 文案 |
| `…/dsh-client-ui-theme/README.zh.md` | token 权威性、`scrollbar.css` 顺序、corner-shape 约定、`--dsh-content-font-size-secondary` 语义 |
| `…/dsh-client-ui-layout/lib/client.js` | 三栏常量与 `ThemePresenter`（13 / 15 / 17 / 38–45 / 70 / 280 / 343 / 360–368 / 443 / 464–477） |
| `…/dsh-client-ui-sidebar/lib/client.js` | 侧栏（27、93） |
| `…/dsh-client-ui-workspace/lib/client.js` | 会话/项目行（554） |
| `…/dsh-client-ui-conversation/lib/client.js` | 会话骨架与 composer（13784 / 13946 / 14498 / 14651 / 14806–14843 / 15756 / 16117 / 12220） |
| `…/dsh-client-ui-chat/lib/client.js` | 消息流与消息项（154 / 1003 / 1507 / 1625 / 2847 / 2933 / 3457） |
| `…/dsh-client-ui-tool/lib/client.js` | 工具行与终端卡（472–490 / 517–525 / 1143 / 1431 / 1696） |
| `…/dsh-client-ui-sidebar-right/lib/client.js` | 右栏（36 / 187 / 607） |
| `…/dsh-client-ui-deliverables/lib/client.js` | 组件级双主题覆盖范例 |
| `…/dsh-client-ui-trajectory/lib/client.js` | z-index 与 `--trajectory-*` |
