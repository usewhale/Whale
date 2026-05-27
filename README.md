# Whale

<p align="center">
  <img src="docs/logo.svg" alt="Whale — DeepSeek-native coding agent for the terminal" width="640">
</p>

<p align="center">
  <a href="./README.zh.md">简体中文</a> · <strong>English</strong>
</p>

<p align="center">
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/releases"><img src="https://img.shields.io/github/v/release/usewhale/DeepSeek-Code-Whale?label=release" alt="release"></a>
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/actions/workflows/ci.yml"><img src="https://github.com/usewhale/DeepSeek-Code-Whale/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/usewhale/DeepSeek-Code-Whale" alt="license"></a>
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/stargazers"><img src="https://img.shields.io/github/stars/usewhale/DeepSeek-Code-Whale?style=flat&logo=github&label=stars" alt="GitHub stars"></a>
</p>

<p align="center">
  <strong>Whale is an unofficial DeepSeek CLI / DeepSeek coding agent for the terminal.</strong><br>
  It can read code, edit files, run commands, and extend the agent with MCP and Skills.
</p>

<p align="center">
  <strong>90% live prefix-cache hit</strong> · <strong>~30x cheaper per task vs Claude Code</strong> · terminal-first · open source
</p>

<p align="center"><a href="./ROADMAP.md">📋 Roadmap · View current direction and available tasks</a></p>

---

## Quick Start

Install with the script:

```bash
curl -fsSL https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.sh | sh
```

Install with Homebrew:

```bash
brew install usewhale/tap/whale
```

**Windows PowerShell:**

```powershell
irm https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 | iex
```

**Windows CMD (Command Prompt):**

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 | iex"
```

The installer prefers the Windows x64 or ARM64 package for your system and adds `whale.exe` to the current user's `PATH`. If an older release does not include an ARM64 package, it falls back to x64 emulation. You can also download the matching Windows zip manually from [GitHub Releases](https://github.com/usewhale/DeepSeek-Code-Whale/releases).

First run:

```bash
whale setup
whale doctor
whale
```

Upgrade:

```bash
brew upgrade usewhale/tap/whale
# or rerun the install script
```

Whale currently uses the DeepSeek API. Before running Whale, create an API key in the [DeepSeek Platform](https://platform.deepseek.com/). See the [DeepSeek API docs](https://api-docs.deepseek.com/) for API details.

> **Platform support:** Whale currently supports **macOS**, **Linux**, and **Windows**.

You can also run a one-shot prompt:

```bash
whale exec "Explain what this repository does"
printf 'Summarize the current directory\n' | whale exec
```

---

## How It Compares

|                          | Whale | Claude Code | Codex CLI | Cursor | Aider |
|--------------------------|-------|-------------|-----------|--------|-------|
| Primary interface        | Terminal TUI/CLI | Terminal agent | Terminal agent | IDE | CLI |
| Default backend          | DeepSeek | Anthropic | OpenAI | Multi-model | Multi-model |
| DeepSeek optimized       | yes | no | no | no | limited |
| Prefix-cache friendly    | yes | n/a | n/a | model-dependent | limited |
| Local code read/write    | yes | yes | yes | yes | yes |
| Shell / test execution   | yes | yes | yes | partial | yes |
| `/ask` read-only mode    | yes | partial | partial | n/a | partial |
| `/plan` planning mode    | yes | yes | yes | n/a | partial |
| MCP                      | yes | yes | version-dependent | partial | partial |
| Skills / reusable workflows | yes | yes | yes | partial | limited |
| Open source              | yes | no | yes | no | yes |

Whale is not trying to support every model. Its focus is turning the DeepSeek API into a stable, low-cost local coding agent that can stay open for long development sessions.

<details>
<summary><strong>Why DeepSeek-only?</strong></summary>

DeepSeek's low token price is only part of the story. The real advantage for long-running coding agents is prefix caching.

DeepSeek's prefix cache is sensitive to byte stability. Whale's loop is designed around that constraint: append-only turns, stable context ordering, and recoverable session records help long tasks keep benefiting from cached prefixes.

That is why Whale is not rushing toward a generic provider abstraction. Claude, OpenAI, and DeepSeek differ in cache mechanics, tool-call behavior, and reasoning controls. A generic wrapper usually hides the DeepSeek-specific parts that matter most.

Whale includes DeepSeek-specific handling for:

| Generic agent assumption | What DeepSeek can do | Whale's handling |
|--------------------------|----------------------|------------------|
| Tool-call JSON is stable | Payloads can be malformed, escaped, or mixed into reasoning | schema-guided repair / scavenge paths |
| Deep tool schemas survive intact | Some nested parameters may be dropped | flatter tool parameters |
| Failed tools should always trigger replan | Some failures should pass through to the model | finer failure classification and recovery |
| User cancellation is just another tool failure | Cancellation should not continue recovery or replanning | dedicated interrupt path |
| Reasoning depth is prompt-only | DeepSeek exposes `reasoning_effort` | runtime effort control |

Whale validates tool inputs against the schema first, then repairs common recoverable shape errors only on failing paths: `null` optional fields, stringified arrays, bare strings for array fields, markdown-autolink paths, and `read_file` calls that provide only offset or limit. Repair and invalid-input counts are visible in `/stats`.

Whale's goal is to make DeepSeek's pricing, cache behavior, and coding capability usable in a real terminal workflow.

</details>

---

## What Whale Can Do

- **Understand codebases**: read files, search code, and summarize project structure.
- **Modify code**: generate patches, edit files, add tests, fix bugs, and handle local refactors.
- **Run commands**: execute shell commands, tests, builds, and diagnostic scripts, then bring results back into the conversation.
- **Work interactively**: use the local TUI, persist sessions, and resume with `whale resume`.
- **Reference local paths quickly**: type `@` in the TUI composer to search and insert workspace file or directory paths.
- **Ask read-only questions**: use `/ask` when you want analysis without file edits.
- **Plan before execution**: use `/plan` to review a plan before letting the agent implement it.
- **Extend tools**: connect external tools with MCP and reuse workflows with Skills.
- **Run headlessly**: use `whale exec` from scripts, CI, or one-shot tasks.
- **1M context window**: DeepSeek V4 models automatically use 1M token context with no manual config.

## Common Commands

| Command | Purpose |
|---------|---------|
| `whale` | Start the interactive TUI |
| `whale setup` | Save a DeepSeek API key |
| `whale doctor` | Run health checks |
| `whale exec "prompt"` | Run one prompt non-interactively |
| `whale --worktree [name]` | Create or reuse an isolated git worktree for this interactive session |
| `whale exec --worktree [name] "prompt"` | Run one prompt inside an isolated git worktree |
| `whale --dangerously-skip-permissions` | Skip tool approval prompts for this run; suitable for external sandboxes or fully trusted repos |
| `whale migrate-config` | Migrate Whale v0.1.8-or-earlier config files to `config.toml` |
| `whale resume` | Open the session picker |
| `whale resume --last` | Resume the most recent session |
| `whale resume <id>` | Resume a specific session |
| `/model` | Change model, reasoning effort, and thinking |
| `/permissions` | Adjust tool approval mode |
| `/focus` | Toggle focused view to hide thinking and tool details |
| `/ask [prompt]` | Read-only question mode |
| `/plan [prompt]` | Plan first, then decide whether to execute |
| `/review [target]` | Build a code-review prompt for local changes, branches, PRs, or commits |
| `/status` | Show current session, mode, model, and config status |
| `/compact` | Compact the current conversation context |
| `/init` | Generate AGENTS.md for the current repository |
| `/skills` | Open the Skills menu to list, insert, or enable/disable local skills |
| `/plugins` | Open the built-in plugin manager to enable or disable official plugins |
| `/memory` | View or manage long-term memory saved by the official memory plugin |
| `/mcp` | Show MCP server status |

## MCP

Whale can load external tools from MCP servers.

See [docs/mcp.md](docs/mcp.md) for setup and supported features.

## Skills

Whale supports local Agent Skills for reusable workflows, team conventions, or tool-specific guidance.

In the TUI, type `$` to search and insert a `$skill-name`. You can also run `/skills`: `List skills` opens the same `$` picker and inserts the selected skill into the composer, while `Enable/Disable Skills` opens a searchable toggle manager.

See [docs/skills.md](docs/skills.md) for details.

## Plugins / Memory

Whale currently ships with the official memory plugin for saving and recalling long-term memory. Use `/memory` to view or manage memories, and `/plugins` to open the plugin manager and press Space to enable or disable plugins.

See [docs/plugins.md](docs/plugins.md) for details.

## Configuration

Whale uses `~/.whale/config.toml` for global settings, `./.whale/config.toml` for shared project settings, and `./.whale/config.local.toml` for private project-local overrides. On Windows, the global directory is `%USERPROFILE%\.whale`. Set `WHALE_HOME` to use a custom global Whale data directory. Config loads in this order:

```text
defaults < global < project shared < project local < CLI flags/env
```

Run this only if you used Whale v0.1.8 or earlier and have local
`preferences.json` or `settings.json` files:

```bash
whale migrate-config
```

If you started with Whale v0.1.9 or newer, you do not need this command.

See [docs/configuration.md](docs/configuration.md) for details.

## Worktrees

In a git repository, use `--worktree` to create or reuse an isolated worktree for the current run:

```bash
whale --worktree feature-x
whale exec --worktree feature-x "implement and test this change"
```

Whale stores managed worktrees under `./.whale/worktrees/<name>` and uses branches named `worktree-<name>`. If you omit the name, Whale generates a `session-*` name. On creation, Whale best-effort copies only `./.whale/config.local.toml`; it does not copy session logs, API keys, private MCP config, or the whole `./.whale` directory.

When you exit an interactive worktree session, Whale removes a clean worktree automatically. If the worktree has uncommitted files or commits after the original checkout head, Whale prompts you to keep or remove it. Removing a worktree discards that checkout and its uncommitted changes, but it does not delete the conversation — `whale resume <id>` resumes from the original workspace.

`whale exec --worktree` runs non-interactively and leaves its worktree on disk. To review and clean it up, re-enter it interactively with `whale --worktree <name>` and exit, which runs the same cleanup prompt. This version does not yet include tmux or stale sweeping.

## Coding Plan Support

Yes. Whale connects to Coding Plan / third-party DeepSeek-compatible endpoints via a custom API endpoint that supports `/chat/completions`.

For Alibaba Cloud Bailian, configure `~/.whale/config.toml`:

```toml
model = "deepseek-v4-flash"
reasoning_effort = "high"
thinking_enabled = true

[api]
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"
```

Then set the corresponding API key:

```bash
DEEPSEEK_API_KEY=sk-... whale
```

Or run:

```bash
whale setup
```

Notes:

- `base_url` should be the root endpoint — do not include `/chat/completions`. Whale appends it automatically.
- The `DEEPSEEK_BASE_URL` environment variable overrides `[api].base_url` in `~/.whale/config.toml`.
- Whale still prioritizes DeepSeek-native models, streaming, thinking, tool calls, and prefix-cache workflows. Full compatibility with third-party endpoints depends on server-side support.

---

## Non-goals

- **Not a generic multi-model wrapper.** Whale is DeepSeek-only for now and prioritizes DeepSeek's cache, tool-call, and cost advantages.
- **Not an IDE.** Whale is terminal-first and works with your shell, git, and test commands instead of replacing IDEs like Cursor.

## Project Status

Whale is moving quickly. It is best used first on personal projects, experimental repositories, or workflows where changes can be reviewed and rolled back.

> **Disclaimer:** This project is not affiliated with DeepSeek Inc. It is an independent open-source community project.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for cloning, local development, testing, issues, and pull requests.

Current development direction and available tasks are listed in [ROADMAP.md](ROADMAP.md).

## Security

For security-sensitive issues, see [SECURITY.md](SECURITY.md).
