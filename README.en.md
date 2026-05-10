# Whale

<p align="center">
  <img src="docs/logo.svg" alt="Whale — DeepSeek-native coding agent for the terminal" width="640">
</p>

<p align="center">
  <a href="./README.md">简体中文</a> · <strong>English</strong>
</p>

<p align="center">
  <a href="https://github.com/usewhale/whale/releases"><img src="https://img.shields.io/github/v/release/usewhale/whale?label=release" alt="release"></a>
  <a href="https://github.com/usewhale/whale/actions/workflows/ci.yml"><img src="https://github.com/usewhale/whale/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/usewhale/whale" alt="license"></a>
  <a href="https://github.com/usewhale/whale/stargazers"><img src="https://img.shields.io/github/stars/usewhale/whale?style=flat&logo=github&label=stars" alt="GitHub stars"></a>
</p>

<p align="center">
  <strong>Whale is an unofficial DeepSeek CLI / DeepSeek coding agent for the terminal.</strong><br>
  It can read code, edit files, run commands, and extend the agent with MCP and Skills.
</p>

<p align="center">
  <strong>90% live prefix-cache hit</strong> · <strong>~30x cheaper per task vs Claude Code</strong> · terminal-first · open source
</p>

---

## Quick Start

Install with Homebrew:

```bash
brew install usewhale/tap/whale
```

If you do not use Homebrew, use the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/usewhale/whale/main/scripts/install.sh | sh
```

First run:

```bash
whale setup
whale doctor
whale
```

Upgrade:

```bash
brew upgrade whale
# or rerun the install script
```

Install a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/usewhale/whale/main/scripts/install.sh | VERSION=v0.1.8 sh
```

Windows:

1. Download `whale-windows-amd64.zip` from [GitHub Releases](https://github.com/usewhale/whale/releases), then extract `whale.exe`.
2. Put `whale.exe` in a directory on PATH, such as `$env:USERPROFILE\bin`.
3. Run these commands in PowerShell:

```powershell
whale --version
whale setup
whale
```

On Windows, Whale uses PowerShell for shell commands. Installing PowerShell 7 (`pwsh`) is recommended; if `pwsh` is not available, Whale tries the built-in `powershell.exe`. If you download `whale-windows-amd64.exe` directly, rename it to `whale.exe` before placing it on PATH, or run it by the full file name.

Unsigned exe files may trigger Windows SmartScreen. Confirm the file comes from Whale's GitHub Release and verify the download against the Release `checksums.txt`.

Whale currently uses the DeepSeek API. Before running Whale, create an API key in the [DeepSeek Platform](https://platform.deepseek.com/). See the [DeepSeek API docs](https://api-docs.deepseek.com/) for API details.

<p align="center">
  <img src="docs/screenshot-01.png" alt="Whale TUI screenshot" width="860">
</p>

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
| Tool-call JSON is stable | Payloads can be malformed, escaped, or mixed into reasoning | repair / scavenge paths |
| Deep tool schemas survive intact | Some nested parameters may be dropped | flatter tool parameters |
| Failed tools should always trigger replan | Some failures should pass through to the model | finer failure classification and recovery |
| User cancellation is just another tool failure | Cancellation should not continue recovery or replanning | dedicated interrupt path |
| Reasoning depth is prompt-only | DeepSeek exposes `reasoning_effort` | runtime effort control |

Whale's goal is to make DeepSeek's pricing, cache behavior, and coding capability usable in a real terminal workflow.

</details>

---

## What Whale Can Do

- **Understand codebases**: read files, search code, and summarize project structure.
- **Modify code**: generate patches, edit files, add tests, fix bugs, and handle local refactors.
- **Run commands**: execute shell commands, tests, builds, and diagnostic scripts, then bring results back into the conversation.
- **Work interactively**: use the local TUI, persist sessions, and resume with `whale resume`.
- **Ask read-only questions**: use `/ask` when you want analysis without file edits.
- **Plan before execution**: use `/plan` to review a plan before letting the agent implement it.
- **Extend tools**: connect external tools with MCP and reuse workflows with Skills.
- **Run headlessly**: use `whale exec` from scripts, CI, or one-shot tasks.

## Common Commands

| Command | Purpose |
|---------|---------|
| `whale` | Start the interactive TUI |
| `whale setup` | Save a DeepSeek API key |
| `whale doctor` | Run health checks |
| `whale exec "prompt"` | Run one prompt non-interactively |
| `whale resume` | Open the session picker |
| `whale resume --last` | Resume the most recent session |
| `whale resume <id>` | Resume a specific session |
| `/ask [prompt]` | Read-only question mode |
| `/plan [prompt]` | Plan first, then decide whether to execute |
| `/skills` | List local skills |
| `/mcp` | Show MCP server status |

## MCP

Whale can load tools from MCP servers. MCP tools are registered as normal Whale tools and still go through approval handling.

Currently supported:

- stdio MCP servers
- Streamable HTTP MCP servers
- `disabled_tools`
- HTTP headers and environment variable expansion
- filesystem server allowed-directory checks

See [docs/mcp.md](docs/mcp.md) for setup.

## Skills

Whale supports local Agent Skills. A skill is an instruction folder containing `SKILL.md`, useful for reusable workflows, team conventions, or tool-specific guidance.

Default discovery paths:

- `.whale/skills`
- `.agents/skills`
- `~/.whale/skills`
- `~/.agents/skills`

Invoke a skill in the TUI with:

```text
$my-skill apply this workflow to the current task
```

See [docs/skills.md](docs/skills.md) for details.

## Configuration

Whale stores local state under `~/.whale/`. API keys, preferences, session records, and MCP configuration are managed locally.

See [docs/configuration.md](docs/configuration.md) for details.

---

## Non-goals

- **Not a generic multi-model wrapper.** Whale is DeepSeek-only for now and prioritizes DeepSeek's cache, tool-call, and cost advantages.
- **Not an IDE.** Whale is terminal-first and works with your shell, git, and test commands instead of replacing IDEs like Cursor.

## Project Status

Whale is moving quickly. It is best used first on personal projects, experimental repositories, or workflows where changes can be reviewed and rolled back.

> **Disclaimer:** This project is not affiliated with DeepSeek Inc. It is an independent open-source community project.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for cloning, local development, testing, issues, and pull requests.

## Security

For security-sensitive issues, see [SECURITY.md](SECURITY.md).
