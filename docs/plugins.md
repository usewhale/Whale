# Plugins

Whale 支持安装本地插件包，并用配置决定哪些插件在当前项目启用。安装和启用是两件事：安装只是把插件放进 Whale 的插件目录；启用后插件才会进入运行时。

## 概览

当前插件平台支持：

- 带明确能力和权限声明的插件清单
- 通过 `[plugins.<id>].enabled` 启用/禁用
- 插件拥有的工具、斜杠命令、启动上下文、技能、MCP 服务器、钩子、
  agents、rules、存储路径、服务状态和诊断
- TUI 中的 `/plugins` 已安装插件管理
- 一个官方内置插件：
  - `memory`：带工具的持久记忆、`/memory`、启动上下文和插件存储

当前阶段先稳定本地插件包和运行时加载，不做插件市场。

## 命令

CLI 管理命令：

```text
whale plugin list
whale plugin install <path>
whale plugin inspect <id>
whale plugin enable <id>
whale plugin disable <id>
whale plugin uninstall <id>
```

本地插件目录需要包含 `whale-plugin.toml`。安装后默认禁用；用 `whale plugin enable <id>` 或 `/plugins` 中的 Space 启用。

在 TUI 中运行：

```text
/plugins
```

列出已安装插件、简短描述和贡献的命令/工具/技能/钩子。
按 Space 启用或禁用选中的插件。按 Esc 关闭列表。

官方插件命令是常规斜杠命令：

```text
/memory
```

如果插件被禁用，其斜杠命令不可用。

配置文件示例：

```toml
[plugins.memory]
enabled = false

[plugins.my-local-plugin]
enabled = true

[plugins.my-local-plugin.mcp_servers.search]
enabled = false
disabled_tools = ["write_file"]
```

配置分层时，插件 MCP server 的 `disabled_tools` 使用覆盖语义，不做跨层合并。
例如项目配置里写了 `["tool_a"]`，项目本地配置里对同一个 server 写了
`["tool_b"]`，最终只会使用 `["tool_b"]`。如果你想同时禁用两个工具，
需要在最高优先级配置里完整写出 `["tool_a", "tool_b"]`。

## 本地插件包

一个最小插件目录长这样：

```text
my-local-plugin/
├── whale-plugin.toml
├── skills/
│   └── demo-skill/
│       └── SKILL.md
├── commands/
│   ├── explain.md
│   └── commands.toml
├── agents/
│   └── reviewer.md
├── rules/
│   └── style.md
├── mcp.json
└── hooks.toml
```

`whale-plugin.toml` 是必需文件：

```toml
id = "my-local-plugin"
name = "My Local Plugin"
version = "0.1.0"
description = "Demo plugin."

[components]
skills = "./skills"
commands = "./commands"
agents = "./agents"
rules = "./rules"
mcp = "./mcp.json"
hooks = "./hooks.toml"
```

启用插件后：

- `skills` 会出现在 `/skills` 和 `$skill-name` 选择里
- `commands/*.md` 会注册为提示型斜杠命令，例如 `/my-local-plugin:explain`
- `commands.toml` 会注册为 shell 型斜杠命令，执行时仍经过 Whale 的
  `shell_run`、审批、hooks 和 checkpoint 核心链路
- `agents/*.md` 会注册为 `spawn_subagent` 可用的 role，例如
  `my-local-plugin:reviewer`
- `rules/*.md` 会作为简短启动规则注入会话
- `mcp` 中的服务器会合并进 MCP 运行时，服务器名会加上插件前缀，
  例如 `my-local-plugin.search`
- `hooks` 会合并进 `/hooks`，插件钩子是受管理钩子，不需要再手动 trust

插件 MCP 配置使用 Whale 已有的 MCP 配置格式：

```json
{
  "mcpServers": {
    "search": {
      "command": "./bin/search-server"
    }
  }
}
```

相对 `command` 会按插件安装目录解析。Whale 还会给插件 MCP 服务器注入：

- `WHALE_PLUGIN_ROOT`
- `WHALE_PLUGIN_DATA_DIR`
- `WHALE_PLUGIN_PROJECT_DIR`

插件钩子使用 Whale hooks TOML 格式：

```toml
[[hooks.SessionStart]]
description = "Write startup marker"
command = "printf started > marker.txt"
timeout = 5
```

插件钩子默认从插件安装目录执行。禁用插件会移除它的技能、MCP 服务器和钩子。
也可以在 `/hooks` 里单独禁用某个插件钩子。

### 插件命令

提示型命令是 Markdown 文件。文件名决定命令名：

```text
commands/explain.md -> /my-local-plugin:explain
commands/review/code.md -> /my-local-plugin:review:code
```

示例：

```markdown
---
description: Explain a topic with plugin guidance.
argument_hint: "<topic>"
read_only: true
---
Explain {{args}} using this plugin's guidance.
```

Shell 型命令放在 `commands/commands.toml`：

```toml
[[commands]]
name = "fmt"
description = "Format plugin code"
command = "gofmt -w internal/plugins"
timeout_ms = 30000
class = "mutating"
```

Shell 命令不会绕过 Whale 直接执行。它们会变成一个隐藏 turn，让模型按声明调用
`shell_run`，因此仍然走正常权限和安全边界。

### 插件 Agents 和 Rules

`agents/*.md` 会变成 `spawn_subagent` 的 role：

```markdown
---
description: Review code using plugin conventions.
capabilities: workspace.read, web.search
max_tool_iters: 6
---
You are a reviewer for this plugin's conventions.
```

支持的 capability 包括：

- `workspace.read`
- `workspace.write`
- `shell.read`
- `shell.write`
- `web.search`
- `web.fetch`
- `mcp.read`

`workspace.write`、`shell.read`、`shell.write` 是 policy-gated：没有可用审批回调时会拒绝，
有审批回调时走 Whale 的正常审批链路。

`rules/*.md` 是简短、稳定的项目规则。启用插件后，Whale 会把这些规则作为启动上下文加入会话。

## 为什么是插件，而不是核心功能？

Whale 已经有两个扩展面：

- MCP 添加外部工具
- Skills 添加可复用的指令

但两者都不足以实现记忆功能。记忆需要：

- 注册工具（`remember`、`forget`、`recall_memory`）
- 在会话启动时注入简短记忆索引
- 拥有全局和项目级别的本地存储
- 在 TUI 中暴露 `/memory` 管理
- 与审批和文件系统边界交互
- 保持可替换——用户以后可以选择其他记忆策略

如果直接在核心中实现记忆，功能更快上线，但会让未来的插件边界更难做。
如果立即做成完全外部第三方插件，又需要过早解决信任、安装、沙箱、
版本控制和 UI 扩展等问题。

折衷方案是官方内置插件：架构上是插件，随 Whale 一起发布。

## 设计原则

- 正常 TUI 启动路径不阻塞
- 官方插件可替换，但第一个版本不要求外部安装
- 插件 API 比内部 Go API 更窄
- 核心掌管信任边界，插件不应自行决定文件系统或 shell 权限
- 优先使用用户可以检查和编辑的文件格式
- 启动上下文保持简短，详细信息通过工具提供

## 当前限制

- 没有插件市场或远程安装
- 本地插件支持 `skills`、`commands`、`agents`、`rules`、`mcp`、`hooks`
- Go 内部插件接口仍可继续演进；长期稳定边界优先是文件协议
