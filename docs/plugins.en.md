# Plugins

Whale ships a built-in plugin host for official plugins. The current host is
intentionally small — it proves the plugin boundary before opening up
external plugin loading.

## Current Plugin: Memory

Whale ships one official built-in plugin:

**`memory`** — Durable memory across sessions. Provides:

- `remember` / `forget` / `recall_memory` tools
- `/memory` management in the TUI
- Startup context with memory index
- Global and project-scoped storage

Memory is enabled by default.

## Managing Plugins

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
- No third-party plugin loading (official built-in plugins only)
- Plugin API is still stabilizing
