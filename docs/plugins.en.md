# Plugins

Whale can install local plugin packages and use config to decide which plugins
are active in the current project. Installing and enabling are separate:
installing puts a plugin in Whale's plugin directory; enabling makes it part of
the runtime.

## Current Plugin: Memory

Whale ships one official built-in plugin:

**`memory`** — Durable memory across sessions. Provides:

- `remember` / `forget` / `recall_memory` tools
- `/memory` management in the TUI
- Startup context with memory index
- Global and project-scoped storage

Memory is enabled by default.

## What Local Plugins Can Load

Current local plugins can contribute:

- Skills
- Prompt and shell slash commands
- Subagent roles
- Startup rules
- MCP servers
- Command hooks

Official built-in plugins can also provide tools, slash commands, startup
context, storage, services, and diagnostics.

## Managing Plugins

CLI management commands:

```text
whale plugin list
whale plugin install <path>
whale plugin inspect <id>
whale plugin enable <id>
whale plugin disable <id>
whale plugin uninstall <id>
```

Local plugin directories must contain `whale-plugin.toml`. Installed local
plugins are disabled by default; enable them with `whale plugin enable <id>` or
Space in `/plugins`.

In the TUI, run:

```text
/plugins
```

This lists installed plugins, their descriptions, and contributed
commands/tools/skills/hooks.

- Press `Space` to enable or disable a plugin
- Press `Esc` to close

Disabled plugins hide their commands and tools.

### Plugin-specific commands

```text
/memory
```

If a plugin is disabled, its slash command is unavailable.

Config example:

```toml
[plugins.memory]
enabled = false

[plugins.my-local-plugin]
enabled = true

[plugins.my-local-plugin.mcp_servers.search]
enabled = false
disabled_tools = ["write_file"]
```

When config files are layered, plugin MCP server `disabled_tools` uses
replacement semantics, not cross-layer merging. For example, if project config
sets `["tool_a"]` and project-local config sets `["tool_b"]` for the same
server, the final value is only `["tool_b"]`. To disable both tools, write the
complete list in the highest-priority config: `["tool_a", "tool_b"]`.

## Local Plugin Packages

A minimal local plugin package looks like this:

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

`whale-plugin.toml` is required:

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

After you enable the plugin:

- `skills` appear in `/skills` and `$skill-name` selection
- `commands/*.md` become prompt slash commands, for example
  `/my-local-plugin:explain`
- `commands.toml` becomes shell slash commands. They still go through Whale's
  `shell_run`, approval, hooks, and checkpoint path.
- `agents/*.md` become `spawn_subagent` roles, for example
  `my-local-plugin:reviewer`
- `rules/*.md` become short startup rules for the session
- `mcp` servers are merged into the MCP runtime, with plugin-prefixed names
  such as `my-local-plugin.search`
- `hooks` are merged into `/hooks` as managed hooks, so they do not need a
  separate trust step

Plugin MCP config uses Whale's normal MCP config shape:

```json
{
  "mcpServers": {
    "search": {
      "command": "./bin/search-server"
    }
  }
}
```

Relative `command` values are resolved from the installed plugin directory.
Whale also sets these environment variables for plugin MCP servers:

- `WHALE_PLUGIN_ROOT`
- `WHALE_PLUGIN_DATA_DIR`
- `WHALE_PLUGIN_PROJECT_DIR`

Plugin hooks use Whale's hooks TOML shape:

```toml
[[hooks.SessionStart]]
description = "Write startup marker"
command = "printf started > marker.txt"
timeout = 5
```

Plugin hooks run from the installed plugin directory by default. Disabling the
plugin removes its skills, MCP servers, and hooks from the runtime. You can also
disable an individual plugin hook from `/hooks`.

### Plugin Commands

Prompt commands are Markdown files. The file path determines the command name:

```text
commands/explain.md -> /my-local-plugin:explain
commands/review/code.md -> /my-local-plugin:review:code
```

Example:

```markdown
---
description: Explain a topic with plugin guidance.
argument_hint: "<topic>"
read_only: true
---
Explain {{args}} using this plugin's guidance.
```

Shell commands live in `commands/commands.toml`:

```toml
[[commands]]
name = "fmt"
description = "Format plugin code"
command = "gofmt -w internal/plugins"
timeout_ms = 30000
class = "mutating"
```

Shell commands do not execute by bypassing Whale. Whale turns them into a hidden
turn that asks the model to call `shell_run` with the declared input, so normal
permissions and safety boundaries still apply.

### Plugin Agents and Rules

`agents/*.md` files become `spawn_subagent` roles:

```markdown
---
description: Review code using plugin conventions.
capabilities: workspace.read, web.search
max_tool_iters: 6
---
You are a reviewer for this plugin's conventions.
```

Supported capabilities:

- `workspace.read`
- `workspace.write`
- `shell.read`
- `shell.write`
- `web.search`
- `web.fetch`
- `mcp.read`

`workspace.write`, `shell.read`, and `shell.write` are policy-gated: without an
approval callback they are denied; with one they use Whale's normal approval
path.

`rules/*.md` files are short, stable project rules. When the plugin is enabled,
Whale adds them to startup context.

## Why Plugins instead of core features?

Whale already has two extension surfaces — MCP (external tools) and
Skills (reusable instructions). But neither is enough for features like
memory, which needs to:

- register tools (`remember`, `forget`, etc.)
- inject context at session startup
- own local storage (global + project-scoped)
- expose a TUI management interface (`/memory`)
- interact with approval and filesystem boundaries
- remain replaceable (users can swap memory strategy later)

The plugin architecture makes all of this possible while keeping core
Whale lean.

## Design Principles

- Startup is non-blocking — plugins don't slow down the TUI
- Official plugins are replaceable, but don't require external installation
- Plugin APIs are narrower than internal Go APIs
- Core owns trust boundaries — plugins don't decide their own permissions
- File formats users can inspect and edit are preferred

## Current Limitations

- No plugin marketplace or remote installation
- Local plugins load `skills`, `commands`, `agents`, `rules`, `mcp`, and `hooks`
- Internal Go plugin APIs can still evolve; the long-term stable boundary is the
  file contract
