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
- `preferences.json`
- `sessions/`
- `usage.jsonl`

Do not commit these files.

## Shell behavior

Whale runs shell commands and hooks through the platform shell:

- Linux and macOS use `/bin/sh -lc`.
- Windows uses PowerShell. Whale first tries `pwsh`, then `powershell.exe`.
- Windows does not use `cmd.exe`, Git Bash, or MSYS Bash as the default shell.

Whale does not translate hook commands between shells. Write Unix hooks with POSIX shell syntax and Windows hooks with PowerShell syntax.

## Hooks

Whale supports external shell hooks via JSON config files:

- project: `./.whale/settings.json`
- global: `~/.whale/settings.json`

Whale loads project hooks before global hooks.

Supported events:

- `PreToolUse`
- `PostToolUse`
- `UserPromptSubmit`
- `Stop`

Unix example:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "match": "exec_shell",
        "command": "echo 'blocked by policy' >&2; exit 2",
        "timeout": 5000
      }
    ]
  }
}
```

Windows example:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "match": "exec_shell",
        "command": "Write-Error 'blocked by policy'; exit 2",
        "timeout": 5000
      }
    ]
  }
}
```

Treat hook files as untrusted input when reproducing another workspace, because hook commands can execute shell commands.

## Runtime notes

- `whale exec` and the interactive TUI use the same underlying tool loop.
- Normal approval and hook behavior still applies in headless mode.
- Reasoning effort can be overridden per run with:

```bash
whale --config model_reasoning_effort=max exec "Think carefully and propose a plan"
```
