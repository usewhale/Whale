# Configuration

## Credentials

Whale uses the DeepSeek API.

- `whale setup` saves a key to `~/.whale/credentials.json`
- `DEEPSEEK_API_KEY` takes precedence over the saved credential

Example:

```bash
whale setup
DEEPSEEK_API_KEY=... whale
```

## Local state

Whale stores local state under `~/.whale/`, including:

- `credentials.json`
- `config.toml`
- `mcp.json`
- `sessions/`
- `usage.jsonl`

Do not commit these files.

## Config files

Whale reads user-editable configuration from:

- global: `~/.whale/config.toml`
- project: `./.whale/config.toml`

Project config overrides global config. The `--model` CLI flag can override
the configured model for one run.

Example:

```toml
model = "deepseek-v4-flash"
reasoning_effort = "high"
thinking_enabled = true

[permissions]
mode = "on-request"
allow_shell_prefixes = ["git status", "go test"]
deny_shell_prefixes = ["rm -rf"]

[api]
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"

[budget]
session_limit_usd = 1.0

[mcp]
config_path = "~/.whale/mcp.json"

[context]
auto_compact = true
compact_threshold = 0.85

[skills]
disabled = ["legacy-review"]

[project_doc]
enabled = true
max_bytes = 8000
fallback_filenames = ["AGENTS.md", ".claude/instructions.md", "CLAUDE.md"]
```

**Context window** is automatically inferred from the model name. `deepseek-v4-flash`
and `deepseek-v4-pro` get 1,000,000 tokens (1M); other models default to 128K. No
manual configuration is needed.

## Migrating old config

Whale v0.1.8 and earlier used `preferences.json` and `settings.json`. New
builds no longer read those files.

Run this once only if you used Whale v0.1.8 or earlier and still have those
legacy files:

```bash
whale migrate-config
```

If you started with Whale v0.1.9 or newer, you do not need this command.

## Runtime notes

- `whale exec` and the interactive TUI use the same underlying tool loop.
- Normal approval behavior still applies in headless mode.
- Reasoning effort and thinking are configured in `config.toml`.
- `DEEPSEEK_BASE_URL` overrides `[api].base_url`; if neither is set, Whale uses
  `https://api.deepseek.com`.
- Skill enable/disable choices are stored in project config under
  `[skills].disabled`.

## Shell behavior

Whale exposes shell execution through the `shell_run` tool. Commands run from
the current workspace root by default. Use relative paths, or pass the `cwd`
parameter to run from a workspace subdirectory.

On macOS and Linux, `shell_run` runs commands through `/bin/sh`. On Windows,
Whale first tries `pwsh`; if it is not available, it falls back to `ComSpec`
and then `cmd.exe`. Write hooks and allow/deny shell prefixes to match the
shell syntax used on the target platform.
