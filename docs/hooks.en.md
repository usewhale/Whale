# Hooks

Hooks let you run your own scripts at important points in Whale's lifecycle. Common uses include adding team context when a session starts, checking a tool call before it runs, logging tool results, validating a user prompt, or asking Whale to continue before it ends a turn.

Hooks are shell commands. Whale writes the current event payload as JSON to the command's stdin. The command can print nothing, or it can print JSON on stdout to influence what Whale does next.

## Where to Put Hooks

Project hooks go in `.whale/config.toml`:

```toml
[[hooks.SessionStart]]
command = "printf 'Whale session started\n' >> .whale/hooks.log"
```

Personal hooks can go in `.whale/config.local.toml`. Do not commit that file:

```toml
[[hooks.UserPromptSubmit]]
command = "python3 .whale/hooks/check_prompt.py"
timeout = 10
```

Global hooks can go in `~/.whale/config.toml` and apply to every project.

## Smallest Useful Example

This hook runs before the `shell_run` tool executes:

```toml
[[hooks.PreToolUse]]
match = "shell_run"
command = "python3 .whale/hooks/check_shell.py"
timeout = 600
```

`timeout` is in seconds and defaults to `600`. If a hook times out, Whale records `decision:timeout`. For `PreToolUse`, `PermissionRequest`, and `UserPromptSubmit`, a timeout blocks the next action.

## Events

| Event | When it runs | Common use |
|---|---|---|
| `SessionStart` | When a new session starts | Add project notes, check setup |
| `UserPromptSubmit` | When the user submits a prompt | Validate input, block risky requests, rewrite prompts |
| `PreToolUse` | Before a tool runs | Review shell commands, block risky tool calls |
| `PermissionRequest` | When Whale asks for permission | Allow or deny specific permission requests |
| `PostToolUse` | After a tool runs | Log results, turn tool output into feedback |
| `PreCompact` | Before context compaction | Add details that must survive compaction |
| `PostCompact` | After context compaction | Record compaction results |
| `SubagentStart` | When a subagent starts | Add subagent-specific context |
| `SubagentStop` | Before a subagent ends | Check subagent output |
| `Stop` | Before Whale ends its turn | Run a final check or ask Whale to continue |

Only `PreToolUse` and `PostToolUse` use `match` to filter by tool name. `match` is a regular expression. Omit it or set it to `*` to match every tool.

## Input

Whale writes the event payload to stdin as one JSON line. Your script can read the fields it needs:

```sh
#!/usr/bin/env sh
payload="$(cat)"
printf '%s\n' "$payload" >> .whale/hook-input.log
```

Useful fields include:

| Field | Meaning |
|---|---|
| `event` | Current hook event |
| `cwd` | Current working directory |
| `session_id` | Session ID |
| `tool_name` | Tool name, such as `shell_run` |
| `tool_args` | Tool arguments |
| `tool_result` | Tool result, available on some after-events |
| `prompt` | User prompt |
| `last_assistant_text` | Whale's latest generated text |

Fields vary by event. When integrating a new hook, start by logging stdin to a temporary file so you can see the real payload before writing policy logic.

## Output

If the script exits with code `0` and stdout is JSON, Whale reads these fields:

```json
{
  "decision": "pass",
  "reason": "ok",
  "updated_input": {"command": "pwd"},
  "additional_context": "Remember that this repo uses make test."
}
```

| Field | Purpose |
|---|---|
| `decision` | `pass`, `warn`, `block`, `halt`, or `error` |
| `reason` / `message` | Explanation shown to the user or passed back to the model |
| `updated_input` | Rewrite tool arguments or the user prompt |
| `additional_context` | Add context for the model |
| `metadata` | Custom structured data |

For `PreToolUse`, `PermissionRequest`, and `UserPromptSubmit`, `decision = "block"` blocks the next action. On other events, `block` is downgraded to a warning and does not stop the main flow.

## Exit Codes

| Exit code | Behavior |
|---|---|
| `0` | Success; Whale parses stdout if it is JSON |
| `2` | Blocks blocking events; stderr is used as the reason |
| Other non-zero | Recorded as a warning or error, depending on the event |

Even if stdout contains `"decision":"pass"`, Whale will not let partial output override a timeout or process-start failure.

## Trust and Enablement

Shared project hooks in `.whale/config.toml` run shell commands, so Whale treats them as reviewable. Untrusted or modified shared project hooks do not run.

Personal hooks in `.whale/config.local.toml` and global hooks in `~/.whale/config.toml` / `$WHALE_HOME/config.toml` are treated as user-trusted config and are active by default. You can still disable them with `/hooks disable <key>`.

In the TUI, run:

```text
/hooks
```

You can inspect all hooks, see which ones are active, and review changed hooks. Common commands:

```text
/hooks trust all
/hooks trust <key>
/hooks disable <key>
/hooks enable <key>
```

Put shared team hooks in `.whale/config.toml`. Put personal experiments in `.whale/config.local.toml`.

## Integration Advice

Start with a logging-only hook to confirm the trigger point and payload. Add `decision:block` only after the basic path is predictable.

Do not run infinite waits, long network calls, or interactive commands inside hooks. If a hook calls an external service, set an explicit `timeout` and make failures write clear stderr.

Do not commit API keys, tokens, or personal paths in `.whale/config.toml`. Use environment variables, or put personal values in `.whale/config.local.toml`.
