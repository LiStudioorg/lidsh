// Package patches 编译期内置的 bundle patch 层。
//
// DSH 里每个 bundle 是一个 npm 包，其 package.json 的 dsh.bundle.patch 指向目录内
// 的 cordis.patch.yml（例如 dsh-base 那份 487 行的文件），启动时按 profile 的
// bundles 顺序逐层打补丁。Go 没有运行时包解析，这里把同样的层以常量 YAML 编译
// 进来：文本保持与 DSH 行同名同结构，行为差异只应是「层的数量」而非「层的语义」。
//
// M0 收录内核必需的层；其余 DSH bundle（ui-*、subagent、workflow、ralph、schedule
// 等）随对应功能里程碑追加。
package patches

import "lidsh/internal/config"

// base 是 lidsh-base 层，对照 dsh-base/cordis.patch.yml 的核心行。
// DSH 原文件的组织原则在此保留：随模式而异的值不进 base，由 mode bundle 整行重写。
const base = `
- insert:
    - id: llm
      name: lidsh-llm

    - id: session
      name: lidsh-session

    - id: session-persistence-jsonl
      name: lidsh-session-persistence-jsonl
      config:
        root: !!js dshHomePath('sessions')

    - id: attachment-local
      name: lidsh-attachment-local
      config:
        root: !!js dshHomePath('attachments')

    - id: typert
      name: lidsh-typert-registry

    - id: typert-gateway
      name: lidsh-api-gateway

    - id: agent
      name: lidsh-agent

    - id: agent-default-model
      name: lidsh-agent-default-model
      config:
        provider: deepseek-official
        model: deepseek-flash

    - id: settings
      name: lidsh-settings-file
      config:
        path: !!js dshHomePath('settings.yaml')

    - id: credentials
      name: lidsh-credentials-local
      config:
        path: !!js dshHomePath('.credentials.yaml')

    - id: tools
      name: lidsh-tools
      group: true
      config:
        - id: tool-bash
          name: lidsh-tool-bash
        - id: tool-fs
          name: lidsh-tool-fs
        - id: tool-fs-search
          name: lidsh-tool-fs-search
        - id: tool-str-replace-editor
          name: lidsh-tool-str-replace-editor
        - id: tool-present
          name: lidsh-tool-present
        - id: tool-ask-user
          name: lidsh-tool-ask-user
        - id: tool-todo
          name: lidsh-tool-todo

    - id: plan-mode
      name: lidsh-plan-mode

    - id: skill
      name: lidsh-skill
      config:
        roots:
          - !!js dshHomePath('skills')
          - .lidsh/skills

    - id: mcp
      name: lidsh-mcp-client
      config:
        servers: {}

    - id: authorization
      name: lidsh-authorization
      config:
        mode: ask
`

// webApp 是 lidsh-web-app 层，对照 dsh-web-app/cordis.patch.yml 的服务端行。
// DSH 的 68 行里约半数是 ui-* 前端挂载，Go 里前端是嵌入的静态产物，只保留服务端行；
// UI 侧的等价物是 Vue 的路由与组件注册表。
const webApp = `
- insert:
    - id: webserver
      name: lidsh-host-webserver
      config:
        port: 3080
        host: 127.0.0.1

    - id: session-controller
      name: lidsh-api-session-controller

    - id: workspace-controller
      name: lidsh-api-workspace-controller

    - id: settings-controller
      name: lidsh-api-settings-controller

    - id: workspace-files
      name: lidsh-api-workspace-files

    - id: file-upload
      name: lidsh-client-file-upload

    - id: frontend-static
      name: lidsh-host-frontend-static
      config:
        assets: embedded

    - id: web-startup
      name: lidsh-web-startup
`

// headless 是 lidsh-headless 层（一次性任务模式，DSH 里 31 行）。
const headless = `
- insert:
    - id: headless-app
      name: lidsh-headless
`

var bundleSources = map[string]string{
	"base":     base,
	"web-app":  webApp,
	"headless": headless,
}

// All 解析内置层为 config.BundlePatch。解析失败 panic：这是编译期常量的错误，
// 必须在开发期暴露，不应留到运行时。
func All() map[string]config.BundlePatch {
	out := make(map[string]config.BundlePatch, len(bundleSources))
	for name, src := range bundleSources {
		patches, err := config.ParsePatchesYAML([]byte(src))
		if err != nil {
			panic("patches: bundle " + name + ": " + err.Error())
		}
		out[name] = config.BundlePatch{Patches: patches}
	}
	return out
}
