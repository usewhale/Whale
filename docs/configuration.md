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
- project shared: `./.whale/config.toml`
- project local: `./.whale/config.local.toml`

Configuration is merged in this order:

```text
defaults < global < project shared < project local < CLI flags/env
```

Use `./.whale/config.toml` for settings that are safe to share with the repo.
Use `./.whale/config.local.toml` for personal overrides in this project, and do
not commit it. The `--model` CLI flag can override the configured model for one
run.

Whale also supports one-time CLI overrides for reasoning settings:

```bash
whale --thinking=false
whale exec --effort=max "summarize this repo"
whale resume <session-id> --thinking=true --effort=high
```

`--thinking`, `--effort`, and `--dangerously-skip-permissions` are runtime-only
overrides. Whale applies them after merging default, global, project shared, and
project local config for the current process, and it does not write them back to
config files.

`--dangerously-skip-permissions` enables permission auto-accept for the current
process. It does not write back to config files. Use it only in a trusted
workspace or an external sandbox; Whale permissions are UX guardrails, not OS
sandboxing.

Example:

```toml
model = "deepseek-v4-flash"
reasoning_effort = "high"
thinking_enabled = true

[permissions]
default = "allow"
auto_accept = false

[permissions.read]
"*" = "allow"
"*.env" = "ask"
"*.env.*" = "ask"
"*.env.example" = "allow"

[permissions.edit]
"*" = "ask"

[permissions.shell]
"*" = "allow"
"rm *" = "ask"
"rm -r*" = "deny"
"rm -R*" = "deny"
"rm -f -r*" = "deny"
"rm -r -f*" = "deny"
"rm -fr*" = "deny"
"rm --force -r*" = "deny"
"rm --force -R*" = "deny"
"git push*" = "ask"
"gh pr merge*" = "ask"
"npm install*" = "ask"
"pnpm install*" = "ask"
"yarn add*" = "ask"
"git reset*" = "ask"
"git restore*" = "ask"
"git rm*" = "ask"
"git clean*" = "ask"
"sudo *" = "ask"
"dd *" = "ask"
"mkfs*" = "deny"
"diskutil erase*" = "deny"
"curl *" = "ask"
"wget *" = "ask"
"rm -rf*" = "deny"

[permissions.external_directory]
"*" = "ask"

[permissions.mcp]
"*" = "ask"

[permissions.memory]
"*" = "ask"

[permissions.mutating_tool]
"*" = "ask"

[api]
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"

[retry]
max_attempts = 4
stream_max_attempts = 6
max_delay = "60s"

[budget]
session_limit_usd = 1.0

[mcp]
config_path = "~/.whale/mcp.json"

[ui]
view_mode = "default" # "default" or "focus"

[context]
auto_compact = true
compact_threshold = 0.85

[skills]
disabled = ["legacy-review"]

[plugins]
disabled = []

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
- `whale exec --dangerously-skip-permissions "prompt"` auto-accepts permission
  prompts for that one headless run.
- `reasoning_effort` and `thinking_enabled` in `config.toml` remain the
  long-term defaults when `--effort` or `--thinking` are not passed.
- `DEEPSEEK_BASE_URL` overrides `[api].base_url`; if neither is set, Whale uses
  `https://api.deepseek.com`.
- `[retry]` controls transient API retries. Whale retries 429, 500, 502, 503,
  504, and network errors with an internal 1s exponential backoff, 10% jitter,
  and `Retry-After` support. `max_attempts` counts request attempts before
  streaming starts; set it to `0` to send one request and disable request
  retries. `stream_max_attempts` counts full stream attempts when the provider
  disconnects after streaming has started.
- `[ui].view_mode = "focus"` starts the TUI in focus view. `/focus` toggles this
  global preference and hides thinking/tool detail while keeping prompts, tool
  summaries, and final responses visible.
- Skill enable/disable choices are stored in project local config under
  `[skills].enabled` and `[skills].disabled`. A project local enabled entry
  overrides a shared project disabled entry.
- Official plugin enable/disable choices are stored in project local config
  under `[plugins].enabled` and `[plugins].disabled`. A project local enabled
  entry overrides a shared project disabled entry. The current built-in plugin
  ID is `"memory"`. Use `/plugins` in the TUI to inspect installed plugins and
  press Space to enable or disable them.

## Shell behavior

Whale exposes shell execution through the `shell_run` tool. Commands run from
the current workspace root by default. Use relative paths, or pass the `cwd`
parameter to run from a workspace subdirectory.

By default, Whale allows normal workspace shell commands and ships explicit
default shell rules for common prompts and blocks. The default shell table asks
for patterns such as `rm *`, `git push*`, `gh pr merge*`, package installation,
`curl *`, and `wget *`. It denies literal recursive remove patterns such as
`rm -rf*`, `rm -fr*`, `rm -r -f*`, and `rm -R*`.

These shell rules are normal permission patterns, not an OS sandbox or a deep
shell-language safety boundary. A user or project config can override them with
later `[permissions.shell]` entries, including `"*" = "allow"`. Write explicit
rules for the shell forms you want to prompt or block.

For common file commands such as `cat`, `ls`, `cp`, `mv`, `rm`, `stat`, and
`du`, Whale also evaluates `[permissions.external_directory]` when path operands
point outside the workspace or temp directories. Shell redirections are not
treated as external directory operands.

File edits (`edit`, `write`, `apply_patch`) ask for approval by default; set
`[permissions.edit]` to `"allow"` to apply edits without prompting, or to
`"deny"` to block them. Reading files is allowed by default except for `.env`
files, which ask. Custom or plugin tools that advertise a `mutates_state`
capability are evaluated under `[permissions.mutating_tool]` and ask by
default.

## Worktrees

`--worktree` creates or reuses an isolated git worktree before Whale loads
project config, hooks, skills, MCP state, or tools:

```bash
whale --worktree feature-x
whale exec --worktree feature-x "run this task in isolation"
whale --worktree
```

Worktrees live under `./.whale/worktrees/<name>`, with branches named
`worktree-<name>`. Names may contain `/`; Whale stores those paths and branches
with `/` flattened to `+`. If no name is passed, Whale generates
`session-YYYYMMDD-HHMMSS`.

When Whale creates a worktree, it best-effort copies only
`./.whale/config.local.toml` into the new checkout. It does not copy
`settings.json`, credentials, MCP private config, session logs, usage logs, or
the whole `./.whale` directory.

Worktree startup is supported for `whale --worktree` and
`whale exec --worktree`. `doctor`, `setup`, `migrate-config`, and `resume`
reject `--worktree`.

When exiting an interactive session from a worktree, Whale removes a clean
worktree automatically. If the worktree has uncommitted files or commits after
the original checkout head, Whale prompts you to keep or remove it. Removing a
worktree discards that checkout and its uncommitted changes, but it does not
delete the conversation. After a worktree session exits, `whale resume
<session-id>` resumes the conversation from the original workspace rather than
re-entering the exited worktree.

The current worktree implementation still does not include tmux, stale sweeps,
or sparse checkout.

On macOS and Linux, `shell_run` runs commands through `/bin/sh`. On Windows,
Whale first tries `pwsh`; if it is not available, it falls back to `ComSpec`
and then `cmd.exe`. Write shell rules to match the shell syntax used on the
target platform.

Configured shell hooks and official plugin hooks run through the same hook
pipeline. Shell hooks can keep using exit codes, or return JSON on stdout with
fields such as `decision`, `reason`, `additional_context`, `updated_input`, and
`metadata`.
