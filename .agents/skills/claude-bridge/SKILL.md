---
name: claude-bridge
description: Delegate coding tasks to Claude Code CLI for execution. Only invoke this skill when the user explicitly asks to use Claude, for example "用 claude 来做", "让 claude 执行", "ask claude to...", or "claude 帮我写". Do not proactively delegate to Claude for general coding requests the user did not specifically ask Claude to handle. Claude Code is an autonomous coding agent with file read/write, grep, and bash tools; Codex's role is to understand the problem clearly and frame it well for Claude to execute.
---

## Critical rules

- Use the bundled shell script rather than calling `claude` CLI directly. The script handles output capture and progress streaming.
- Run the script once per task. If it succeeds, read the output file and proceed. Do not rerun just because the output is short.
- Quote file paths containing `[`, `]`, spaces, or special characters, for example `--file "src/app/[locale]/page.tsx"`.
- Keep the task prompt focused on the goal, constraints, and non-obvious context. Aim for under about 1000 words.
- Do not paste file contents into the prompt. Use `--file` to point Claude to key files; it can read them directly.
- Do not mention this skill or its configuration in the prompt. Claude does not need to know about it.

## How to call the script

### Linux/macOS

```bash
./scripts/ask_claude.sh "Your request in natural language"
```

With file context:

```bash
./scripts/ask_claude.sh "Refactor these components to use the new API" \
  --file src/components/UserList.tsx \
  --file src/components/UserDetail.tsx
```

### Windows PowerShell

```powershell
& ./scripts/ask_claude.ps1 "Your request in natural language"
```

With file context:

```powershell
& ./scripts/ask_claude.ps1 "Refactor these components to use the new API" `
  -f src/components/UserList.tsx `
  -f src/components/UserDetail.tsx
```

### Output format

On success the script prints:

```text
output_path=<path to markdown file>
```

Read the file at `output_path` to get Claude's response.

## Workflow

1. Understand the problem well enough to describe the goal and constraints.
2. Run the script with a focused task description. For analysis without edits, use `--read-only`.
3. Pass 1-4 entry-point files with `--file` as starting hints.
4. Read the output file and review any workspace changes Claude made.

## Options

- `--workspace <path>`: Target workspace directory. Defaults to current directory.
- `--file <path>`: Priority file path, repeatable. Relative paths are resolved under `--workspace`.
- `--model <name>`: Override Claude model.
- `--effort <level>`: Effort level: `low`, `medium`, `high`, or `max`. Defaults to `max`.
- `--permission-mode <mode>`: Claude permission mode. Defaults to `auto`.
- The wrapper always passes `--dangerously-skip-permissions` to Claude.
- `--read-only`: Analyze and report only; file mutation tools are disabled.
- `--output <path>`: Write the captured response to a specific markdown file.

## Failure handling

- `stream-json requires --verbose`: update this skill's scripts; Claude stream-json mode requires `--verbose`.
- Claude native `--permission-mode plan` is intentionally not used for `--read-only`; in non-interactive wrapper usage, rejected `ExitPlanMode` calls can produce non-zero exits.
- Exit code 1 with no output usually means Claude rejected an option or authentication is missing. Check stderr.
- Non-zero exits or missing assistant output save diagnostic artifacts next to the markdown output, using the same basename: `.jsonl` for raw Claude stream JSON and `.stderr` for stderr.
- `(no response from claude)` means Claude ran but no readable assistant/result text was captured.
