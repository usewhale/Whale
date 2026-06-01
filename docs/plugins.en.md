# Plugins

Plugins extend Whale with new capabilities: slash commands, skills, subagents, rules, MCP servers, and hooks.
Installing and enabling are separate — plugins are **disabled** by default after install.

---

## User Guide

### Quick Start

Three steps to get a plugin running:

```text
# 1. Install from a local directory
whale plugin install ./my-plugin

# 2. Confirm it's installed
whale plugin list

# 3. Enable it
whale plugin enable my-plugin
```

Once installed and enabled, open the TUI to use the plugin's features.
If the plugin provides slash commands, just type `/` to see them.

---

### Command Reference

| Command | What it does |
|---------|--------------|
| `whale plugin install <path>` | Install a plugin from a local directory |
| `whale plugin list` | List installed plugins (enabled/disabled, version) |
| `whale plugin enable <id>` | Enable a plugin |
| `whale plugin disable <id>` | Disable a plugin (keep it installed) |
| `whale plugin uninstall <id>` | Remove a plugin from disk completely |
| `whale plugin inspect <id>` | Show plugin details: what it contributes, diagnostics |

---

### TUI Usage

Type `/plugins` in the TUI to open the plugin manager panel:

- Shows all installed plugins. **Green** = enabled, **gray** = disabled.
- Press `Space` to toggle enable/disable.
- Press `Esc` to close the panel.

When a plugin is enabled, its slash commands appear in the command list — just type `/`.

---

### FAQ

**Q: I installed a plugin but don't see it working.**
A: Plugins are **disabled** by default after install. Run `whale plugin enable <id>` to turn it on.

**Q: How do I know what a plugin provides?**
A: `whale plugin inspect <id>` lists all commands, skills, agents, rules, and MCP servers it contributes.

**Q: How do I completely remove a plugin?**
A: `whale plugin uninstall <id>` deletes it from disk. You'd need to reinstall to use it again.

**Q: Where are plugins stored on disk?**
A: Installed plugin cache is at `~/.whale/plugins/cache/local/<id>/<version>/`.

---

## Developer Guide

### A Plugin is a Directory

A minimal plugin is just a folder with `whale-plugin.toml`:

```
my-first-plugin/
├── whale-plugin.toml   ← Required, the plugin's identity
└── skills/
    └── hello/
        └── SKILL.md    ← Optional, add a skill to try it out
```

### Step 1: Write whale-plugin.toml

The minimum is just `id`:

```toml
id = "my-first-plugin"
name = "My First Plugin"
version = "0.1.0"
description = "My first Whale plugin"
```

Then install and enable it:

```text
whale plugin install ./my-first-plugin
whale plugin enable my-first-plugin
```

### Step 2: Add a Skill

Put a SKILL.md under `skills/`:

```markdown
# Hello

When asked about "hello", respond in a friendly tone.
```

Start a new TUI session and the plugin's skill will be available.

> The `skills/` directory is auto-detected — even if the `whale-plugin.toml` doesn't declare `[components]`, Whale will load skills from `skills/` if it exists.

---

### What Else Can a Plugin Add?

Plugins can contribute six types of components. Here's the minimum example for each.

#### Slash Command (Prompt type)

`.md` files under `commands/` become slash commands. The file path determines the name:

```
commands/
├── explain.md             → /my-first-plugin:explain
└── review/
    └── code.md            → /my-first-plugin:review:code
```

`commands/explain.md` example:

```markdown
---
description: Explain a topic from the plugin's perspective
argument_hint: "<topic>"
---
Explain {{args}} from my plugin's perspective.
```

Type `/my-first-plugin:explain what is a plugin` in the TUI to trigger it.

#### Slash Command (Shell type)

To run shell commands, put a `commands.toml` under `commands/`:

```toml
[[commands]]
name = "status"
description = "Check if the plugin is working"
command = "echo 'plugin is running'"
timeout_ms = 10000
```

Typing `/my-first-plugin:status` makes Whale execute the command through `shell_run` (normal permissions and approval apply — no bypass).

#### Subagent (Agent)

`.md` files under `agents/` become spawnable subagent roles:

```markdown
---
description: Review code using plugin conventions
capabilities: workspace.read
---
You are an expert on {{plugin_id}}. Review the code against this plugin's conventions.
```

#### Session Rules (Rules)

Rules under `rules/` are injected into every session's startup context:

```markdown
This project uses the my-first-plugin plugin. Please follow its conventions.
```

#### MCP Server

`mcp.json` adds external tools. The server name gets a plugin prefix automatically:

```json
{
  "mcpServers": {
    "search": {
      "command": "node",
      "args": ["server.js"]
    }
  }
}
```

Whale registers it as `my-first-plugin.search`.

Plugin MCP servers get these environment variables:

- `WHALE_PLUGIN_ROOT` — plugin install directory
- `WHALE_PLUGIN_DATA_DIR` — plugin-scoped data directory
- `WHALE_PLUGIN_PROJECT_DIR` — per-project plugin data directory

#### Hooks

`hooks.toml` adds automated hooks that take effect when the plugin is enabled:

```toml
[[hooks.SessionStart]]
description = "Write a startup marker"
command = "echo 'plugin started' >> plugin.log"
timeout = 5
```

---

### Development Iteration

After changing plugin files, just re-install to overwrite:

```text
whale plugin install ./my-first-plugin
```

Installation is atomic: if the copy fails midway, it rolls back to the old version.
Skill and rule changes take effect after starting a new TUI session.
Command changes take effect after pressing Ctrl+R in the TUI.

---

### Naming Rules

- **Plugin ID**: lowercase letters, digits, `.` `-` `_`. Underscores are converted to hyphens.
- **Commands and agents**: automatically prefixed with `<pluginID>:`.
- **File path determines name**: `commands/review/code.md` → `/my-first-plugin:review:code`.
- **Override the name**: set `name: xxx` in the frontmatter to override.

Important notes:

- Plugin IDs must not conflict with built-in plugin IDs (those are reserved).
- Component paths in `whale-plugin.toml` must be relative and stay inside the plugin directory.
- A path pointing to a non-existent directory won't error, but produces a warning (`whale plugin inspect` shows it).

---

### Complete Working Example

Here's a plugin directory you can copy verbatim:

```
my-plugin/
├── whale-plugin.toml
├── commands/
│   ├── explain.md
│   └── commands.toml
├── skills/
│   └── greet/
│       └── SKILL.md
├── agents/
│   └── reviewer.md
└── rules/
    └── convention.md
```

**whale-plugin.toml**

```toml
id = "my-plugin"
name = "My Plugin"
version = "0.1.0"
description = "A demo plugin"
```

**commands/explain.md**

```markdown
---
description: Explain a concept
argument_hint: "<concept>"
read_only: true
---
Explain {{args}} from my plugin's perspective.
```

**commands/commands.toml**

```toml
[[commands]]
name = "ping"
description = "Test if the plugin is online"
command = "echo pong"
```

**skills/greet/SKILL.md**

```markdown
# Greet

When the user says "hello", greet them enthusiastically and introduce yourself.
```

**agents/reviewer.md**

```markdown
---
description: Review code using plugin conventions
capabilities: workspace.read
---
You are a code review expert for the my-plugin plugin.
```

**rules/convention.md**

```markdown
This session uses the my-plugin plugin.
```

Install and enable:

```text
whale plugin install ./my-plugin
whale plugin enable my-plugin
```

Then open the TUI and you'll see:

- `/my-plugin:explain` slash command
- `/my-plugin:ping` slash command
- `my-plugin:reviewer` subagent role
- `greet` skill
- Startup rules automatically injected
