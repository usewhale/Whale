# Plugins

Whale ships a built-in plugin host for official plugins. The current host is
intentionally small: it proves the plugin boundary inside Whale before opening a
filesystem plugin runtime or marketplace.

## Summary

The current plugin platform supports:

- plugin manifests with explicit capabilities and permissions;
- global/project enablement through `[plugins].disabled`;
- plugin-owned tools, slash commands, startup context, skills, hooks, storage
  paths, service status, and diagnostics;
- `/plugins` discovery, `/plugins status`, `/plugins doctor`, and `/plugins reload`;
- three official built-in plugins:
  - `memory`: functional explicit memory with tools, `/memory`, startup context,
    and plugin storage;
  - `skills-improver`: scaffold plugin that contributes a Skill and command
    surface, plus a no-op `Stop` hook for future evidence collection, but does
    not rewrite skills yet;
  - `local-indexer`: scaffold plugin that declares local indexing/model
    capabilities and a no-op `Stop` hook, but does not run inference yet.

The first milestone is not a marketplace. It is a stable internal plugin
contract proven by official plugins.

## Commands

Use `/plugins` to inspect the host:

```text
/plugins
/plugins status memory
/plugins status skills-improver
/plugins status local-indexer
/plugins doctor
/plugins reload
```

Official plugin commands are regular slash commands:

```text
/memory
/skills-improver status
/local-indexer status
```

If a plugin is disabled, its slash command is unavailable and `/plugins` marks
the plugin as disabled.

## Why Plugins

Whale already has two extension surfaces:

- MCP adds external tools.
- Skills add reusable instructions.

Neither is enough for memory. A memory feature needs to:

- register tools such as `remember`, `forget`, and `recall_memory`;
- inject a short memory index when a session starts;
- own global and project-scoped local storage;
- expose `/memory` management in the TUI;
- interact with approval and filesystem boundaries;
- stay replaceable so users can later choose another memory strategy.

If memory is implemented directly as core logic, Whale gets the feature faster
but makes the future plugin boundary harder. If memory is implemented as a fully
external third-party plugin immediately, Whale must solve trust, installation,
sandboxing, versioning, and UI extensibility too early.

The compromise is an official built-in plugin: architecturally a plugin, shipped
with Whale.

## Design Principles

- Keep the normal TUI startup path non-blocking.
- Make official plugins replaceable, but not necessarily externally installed in
  the first version.
- Keep plugin APIs narrower than internal Go APIs.
- Let core own trust boundaries. Plugins should not decide their own filesystem
  or shell permissions.
- Prefer file formats users can inspect and edit.
- Keep startup context short. Details should be available through tools.
- Avoid automatic self-modification until proposal and review flows are proven.

## Non-goals

The first plugin milestone should not include:

- plugin marketplace;
- remote plugin installation;
- arbitrary dependency installation;
- arbitrary plugin UI rendering;
- plugin-managed approval bypasses;
- background autonomous memory extraction;
- bundled local model inference;
- automatic skill rewrites.

## Plugin Loading

The first version supports built-in official plugins only. The host still models
them through the same registry shape that future filesystem plugins would use.

Future plugin directories can be:

```text
~/.whale/plugins/<plugin-id>/
./.whale/plugins/<plugin-id>/
```

Project plugins should not be trusted by default. A malicious repository should
not be able to install a plugin that gains local storage, shell, or broad file
access simply because the user opened the directory.

## Manifest

Each plugin has a manifest shape even when compiled into Whale:

```toml
id = "memory"
name = "Memory"
version = "0.1.0"
kind = "official"

[capabilities]
tools = true
slash_commands = true
startup_context = true
storage = true
background_jobs = false
skills = false
hooks = false
local_model = false

[storage]
scope = "user-and-project"

[permissions]
read_plugin_data = true
write_plugin_data = true
workspace_read = false
workspace_write = false
shell = false
network = false
```

The manifest makes capabilities explicit and gives `/plugins` and diagnostics
something concrete to show.

## Host Interfaces

The host exposes narrow interfaces.

### Metadata

```go
type Plugin interface {
    ID() string
    Name() string
    Version() string
    Capabilities() PluginCapabilities
}
```

The ID must be stable and filename-safe. Official plugin IDs are reserved.

### Tools

```go
type ToolProvider interface {
    Tools(ctx PluginContext) []core.Tool
}
```

Plugin tools should be registered into the same tool registry as built-in and
MCP tools. Tool names should be stable and conflict-checked. If conflicts happen,
Whale should fail plugin startup for that plugin and show a diagnostic rather
than silently shadowing another tool.

### Slash Commands

```go
type SlashCommandProvider interface {
    SlashCommands(ctx PluginContext) []SlashCommand
}
```

For the first version, commands return structured actions or text. Plugins do
not render arbitrary TUI components. Core owns the TUI shell so keyboard
behavior, theming, and layout remain consistent.

### Startup Context

```go
type StartupContextProvider interface {
    StartupContext(ctx PluginContext) (ContextBlock, error)
}
```

The host enforces:

- max bytes per plugin;
- max total bytes across plugins;
- deterministic ordering;
- diagnostics when a plugin exceeds budget;
- no startup blocking on slow background work.

Startup context is for short indexes and durable facts, not full transcripts or
large documents.

### Hooks

Whale has one hook pipeline for both user-configured shell hooks and
plugin-provided in-process hooks. The current event set is intentionally small:

```text
UserPromptSubmit
PreToolUse
PostToolUse
Stop
```

Shell hooks still come from config files. Plugin hooks are contributed through
the plugin host:

```go
type HookProvider interface {
    Hooks(ctx PluginContext) []HookHandler
}
```

Hook results are structured. A hook can pass, warn, block the current action,
halt, add context, rewrite tool input before execution, and attach metadata for
diagnostics. The shell-hook compatibility path still supports exit-code based
behavior, and can also return JSON on stdout with fields such as
`decision`, `reason`, `additional_context`, `updated_input`, and `metadata`.

Plugin hooks are for trusted official plugins in this milestone. External
project plugins should not be allowed to register hooks until Whale has an
explicit trust model.

### Storage

Core gives each plugin helper paths rather than making plugins compute their own
layout:

```text
~/.whale/plugins/<plugin-id>/data/
~/.whale/plugins/<plugin-id>/projects/<workspace-hash>/
```

The workspace hash should be stable for a canonical project root. Core should
own path normalization and traversal checks.

## Config

Plugin enablement is configurable at global and project scope:

```toml
[plugins]
disabled = ["memory"]
```

Project config can disable a plugin for the workspace. Enabling an untrusted
project plugin should require an explicit trust step in a later milestone.

## Permissions

Plugin permissions are capability-based, not implicit.

Recommended first rules:

- A plugin can always read its own manifest and static assets.
- A plugin can read/write its own data directory only if declared.
- A plugin tool can access plugin data through plugin storage APIs.
- General tools do not automatically gain write access to plugin data.
- Shell access is not part of the first plugin interface.
- Network access is not part of the first plugin interface.

Future approval UI should mention plugin identity when a plugin tool asks for
action. The first memory tools are handled as plugin-owned operations, not as
general workspace file writes.

## Official Memory Plugin

Memory is the first official plugin.

### Purpose

Memory stores durable user preferences and project facts across sessions. It is
not a transcript resume feature.

Examples that belong in memory:

- user prefers concise Chinese answers;
- user corrected a recurring process;
- a project has a non-obvious release rule;
- a useful external dashboard or document location.

Examples that do not belong in memory:

- current task todos;
- temporary debugging status;
- facts that can be cheaply derived from files;
- sensitive secrets;
- large pasted documents.

### Storage Layout

```text
~/.whale/plugins/memory/data/global/
├── MEMORY.md
└── <topic>.md

~/.whale/plugins/memory/projects/<workspace-hash>/
├── MEMORY.md
└── <topic>.md
```

`MEMORY.md` is an index. It is loaded into startup context. It should contain
short one-line pointers only:

```markdown
- [response-style](response-style.md) - user prefers concise Chinese answers.
- [whale-product](whale-product.md) - plugin-first memory is the preferred direction.
```

Topic files carry details:

```markdown
---
name: response-style
type: user
scope: global
description: user prefers concise Chinese answers
created: 2026-05-18
updated: 2026-05-18
---

The user usually wants direct Chinese answers grounded in local repo evidence.
Avoid generic neutral lists when they ask for a judgment call.
```

Markdown should be the source of truth in the first version. A generated JSON
index can be added later if performance requires it.

### Memory Types

Use a small closed set:

- `user`: user preferences, role, collaboration style.
- `feedback`: corrections to Whale's behavior or prior mistakes.
- `project`: durable project facts not obvious from files.
- `reference`: pointers to external systems or documents.

### Tools

The plugin registers three tools.

#### remember

Saves or updates a memory.

Use when the user:

- explicitly asks Whale to remember something;
- states a durable preference;
- corrects Whale's behavior;
- shares a non-obvious project fact.

Do not use for transient task state.

Input shape:

```json
{
  "scope": "global",
  "type": "user",
  "name": "response-style",
  "description": "user prefers concise Chinese answers",
  "content": "..."
}
```

The tool result should repeat the saved fact so the model can use it in the
current session even though startup context is not rebuilt mid-session.

#### forget

Deletes a topic file and removes its index entry. It should require explicit
user intent when called by the model.

#### recall_memory

Reads a full topic file when `MEMORY.md` is not enough. Most turns should not
need it.

### Startup Context

At launch, `/new`, and any future explicit new-session operation, the memory
plugin provides a short block:

```markdown
# Whale memory

Use these remembered facts when relevant. If the user says not to use memory,
ignore this block. Use `recall_memory` for details.

## Global
<global MEMORY.md, capped>

## Project
<project MEMORY.md, capped>
```

The first cap should be conservative:

- 4 KB for global index;
- 4 KB for project index;
- truncate with a visible note if exceeded.

The plugin should not inject full topic files automatically.

### TUI

Current command surface:

- `/memory` shows current global and project indexes.
- `/memory show <scope>/<name>` shows a body.
- `/memory forget <scope>/<name>` deletes a named memory.
- `/memory path` shows storage paths for manual inspection.

Later shortcut:

- `# note` writes project memory directly.
- `#g note` writes global memory directly.

The shortcut should wait until `/memory` usage proves the model/tool path is not
enough.

### Current-session Behavior

Writes are eager. Startup context is stable.

When `remember` writes a memory, Whale should not rebuild the current prompt.
The tool result tells the model to treat the fact as established for the rest of
the session. The memory becomes part of startup context on the next session.

This keeps prompt caching predictable and avoids surprise context mutation.

### Background Extraction

Do not implement background extraction in v1.

Codex-style extraction and consolidation can be powerful, but it adds:

- extra model calls;
- unclear user consent;
- more failure modes;
- more context and privacy complexity.

Whale should first support explicit memory. Background extraction can be a later
memory plugin setting after users trust the basic flow.

## Skills Improver Plugin

This is the second official plugin. It currently ships as a scaffold so the host
can expose skill contribution and plugin command/status behavior before Whale
implements proposal generation.

Purpose:

- inspect existing skills;
- look at durable user feedback and selected session evidence;
- propose skill improvements;
- require user approval before writing.

It should not automatically rewrite `SKILL.md`.

Target command shape:

- `/skills improve`: show proposals.
- `/skills improve <name>`: focus on one skill.
- `/skills apply-proposal <id>`: apply a confirmed patch.

The plugin depends on memory because stable feedback should first become memory
before it changes reusable instructions.

## Local Indexer Plugin

This is the third official plugin. It currently ships as a scaffold so the host
can expose local-model/indexing capabilities before Whale bundles a local model.

Purpose:

- index memory topic files;
- optionally use a local embedding or small model;
- expose read-only retrieval tools;
- avoid API calls for simple indexing work.

Do not ship this before solving:

- cross-platform packaging;
- model download and cache location;
- CPU and memory limits;
- upgrade and rollback behavior;
- Windows behavior;
- whether embeddings are actually needed for small memory directories.

The memory plugin should work with simple file indexes first.

## Phased Plan

### Phase 1: Plugin Host Skeleton

- Add built-in plugin registry.
- Add plugin metadata and enable/disable config.
- Add plugin data path helpers.
- Add startup context provider hook.
- Add tool registration hook.
- Add slash command registration hook.
- Add plugin diagnostics.
- Add plugin skill contribution.
- Add plugin hook contribution through the unified hook pipeline.
- Add service status and plugin doctor surfaces.

### Phase 2: Memory Plugin

- Implement memory store.
- Implement path validation and workspace hashing.
- Register `remember`, `forget`, and `recall_memory`.
- Inject capped global/project `MEMORY.md` blocks.
- Add `/memory` management.
- Add tests for path traversal, index caps, tool behavior, and config disabling.

### Phase 3: Trust and Packaging

- Separate official, user, and project plugin trust.
- Add `/plugins`.
- Document plugin authoring.
- Decide whether external plugin loading is allowed by default.

### Phase 4: Skills Improver

- Add proposal-only skill review.
- Require explicit confirmation before writes.
- Link proposals to evidence.

### Phase 5: Local Indexer

- Add optional local indexing.
- Keep disabled by default.
- Treat bundled model support as an experiment until packaging is boring.

## Open Questions

- Should global memory be enabled by default, or should Whale ask once?
- Should project memory live under plugin data only, or should there also be a
  committable project memory file later?
- Should `#` and `#g` ship in memory v1 or wait?
- Should official plugins be compiled into Whale forever, or moved to local
  plugin packages after the host stabilizes?
- Should the host support background tasks before the local indexer exists?

## Recommendation

Proceed with a plugin-shaped architecture but keep the first implementation
official and built in.

Build only the host features required by the memory plugin. Do not build a
marketplace, arbitrary plugin runtime, local model layer, or automatic skill
rewriter yet. Use memory to prove the extension boundary, then let
skills-improver and local-indexer pressure-test the API later.
