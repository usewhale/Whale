---
name: claude-consensus
description: Use only when the user explicitly asks Codex to have Claude review a plan or existing file content, reach consensus with Claude, run Claude consensus, or perform a similar graded Claude review-and-revision loop.
---

# Claude Consensus

Use this skill only for explicit requests to send a Codex plan or existing file content to Claude for independent graded review and consensus. Do not trigger it for ordinary planning, code review, or editing requests unless the user clearly asks for Claude to be involved.

The user does not need to know the internal protocol. Codex infers whether Claude should review a `plan` or `file` input, starts an isolated consensus subagent, and lets that subagent run the Claude review, apply requested edits when appropriate, and request rereview until Claude returns `APPROVED` or `BLOCKED`.

## Isolation Model

One user requirement maps to exactly one fresh consensus subagent and one new Claude session.

- The main Codex agent reads enough local context to infer the input kind, targets, and starting instructions.
- The main Codex agent creates a fresh subagent for the current requirement.
- The subagent starts a new Claude session on its first review call by omitting `--session`.
- The subagent may reuse the returned Claude `session_id` only inside that same subagent and only for the same requirement.
- A new requirement must create a new subagent and a new Claude session.
- Never reuse an old consensus subagent or old Claude session for a different requirement.
- The main Codex agent must not store, restore, or reuse Claude session ids.
- Claude session ids stay inside the consensus subagent and are not returned for main-agent reuse.
- No `run_id`, manifest, or runtime index is needed. `.runtime/` only stores response markdown files.

## Main Codex Workflow

1. Read enough local context to identify the task, inferred input kind, target files when relevant, and a concise starting plan or file-review instruction for the subagent.
2. Infer the internal input kind:
   - `plan`: the user wants Claude to review a proposal, plan, approach, strategy, or intended edits before work proceeds.
   - `file`: the user wants Claude to review, modify, polish, proofread, or validate existing file or directory contents.
   - When both a plan and files are mentioned, choose the core object Claude needs to judge or help modify.
   - Ask the user only when the object to review cannot be inferred safely.
3. Spawn a fresh subagent without unrelated historical context.
4. Give the subagent only the current requirement context:
   - Original user request.
   - Initial Codex plan or file-review instruction.
   - Inferred input kind and target files if applicable.
   - Workspace path.
   - Relevant constraints and assumptions.
5. Wait for the subagent to complete the Claude review, edits when requested, and Claude rereview loop.
6. Report the subagent result to the user:
   - Complete `Final approved plan`, only for `input-kind=plan` with final status `APPROVED`.
   - Files modified.
   - Main changes made.
   - Number of Claude review rounds.
   - Final status.
   - Any `BLOCKED` reason or explicitly deferred notes.

For `plan`, when the subagent returns final status `APPROVED`, the main Codex agent must first replace its own intended next steps with the subagent's complete `Final approved plan` and treat that plan as the authoritative plan after consensus. The main Codex agent must show the user the complete `Final approved plan`, not just a summary and not the initial plan. If the user asked Codex to proceed after consensus, first show the complete final approved plan, then execute or report only from that final approved plan. For `file`, the target files on disk are the authoritative final state, and the subagent result should summarize modified files, expanded files, deferred notes, verification, round count, and status rather than returning full file contents unless the user explicitly requests them.

The main Codex agent should not call Claude directly for the consensus loop. For `file`, it should not perform the requested file edits after the subagent returns because the subagent owns the review-modify-rereview loop and the workspace edits.

## Caller Constraints

The caller may pass request-specific constraints to this skill, such as read-only references, writable targets, review focus areas, files that must not be changed, or stage-specific risks. Treat these constraints as part of the current consensus run.

This skill still owns the consensus protocol: isolated Claude session, status handling, review / revise / rereview loop, and final reporting. Claude remains read-only through explicit Claude CLI tool restrictions: the wrapper allows only `Read`, `Grep`, `Glob`, and `LS`. Any file changes are performed by the consensus subagent according to Claude's feedback and the caller-provided writable-target constraints.

If caller constraints conflict with this skill's isolation, status, Claude-read-only, or final-reporting rules, this skill's rules take precedence.

## Consensus Subagent Workflow

Use `scripts/ask_claude_consensus.sh` on Unix-like systems or `scripts/ask_claude_consensus.ps1` on PowerShell hosts.

1. Start with the initial plan or file-review instruction and inferred context from the main Codex agent.
2. Round 1: call the script without `--session`, using `--input-kind plan` or `--input-kind file`.
3. For `file`, pass every target file or directory with `--target`.
4. Save the `session_id=...` printed by the script for this requirement only.
5. Read the generated `output_path=...` markdown.
6. Normalize Claude's first meaningful status line before branching:
   - Strip leading whitespace.
   - Strip common markdown wrappers or heading markers such as `**APPROVED**`, `# REVISE`, and `BLOCKED:`.
   - Treat only normalized `APPROVED`, `APPROVED_WITH_NOTES`, `REVISE`, or `BLOCKED` as status tokens; preserve the full markdown body for human review.
7. If the normalized status is `APPROVED`, stop and return by input kind:
   - For `plan`, return `Final approved plan` containing the complete current plan Claude approved, plus final status, deferred notes if any, and Claude review round count.
   - For `file`, return modified files, expanded files, edit summary, verification summary, deferred notes if any, final status, and Claude review round count. Do not return full file contents unless the user explicitly requested them.
8. If the normalized status is `APPROVED_WITH_NOTES`, incorporate the notes into the plan or target files when appropriate, or explicitly defer low-risk notes with a short rationale. Then call the script again with the same `--session` and same `--input-kind`; `APPROVED_WITH_NOTES` is not a terminal status even when no file content changes.
9. If the normalized status is `REVISE`, apply the requested concrete changes to the plan or target files when possible. Then call the script again with the same `--session` and same `--input-kind`.
10. If the normalized status is `BLOCKED`, do not invent missing facts. Stop and return by input kind:
   - For `plan`, return the blocker, `Current best-known plan` containing the complete latest revised plan, deferred notes if any, and review round count.
   - For `file`, return the blocker, current file state summary, modified files if any, expanded files if any, deferred notes if any, and review round count.
11. If no status token is clear, ask Claude to restate the decision with a valid first status line and continue the same round count.
12. Do not impose a fixed round limit. Continue until Claude returns `APPROVED` or `BLOCKED`.

For resumed `plan` rounds, pass the complete updated plan in `--plan`. Do not repeat the original task or summarize what changed.

For resumed `file` rounds, pass the current targets again. Do not repeat the original task, restate what Claude is reviewing, or summarize file edits. Claude uses the same session context plus the current target file contents.

Claude is read-only in this workflow. The wrappers enforce this by passing `--tools "Read,Grep,Glob,LS"` on both new and resumed Claude invocations, so Claude can inspect context but cannot use mutation tools. Claude reviews plans or target file contents, builds the necessary workspace context in the first round, reuses that context in later rounds via the resumed session, and reads additional files only when the revised work introduces new scope, the prior context is missing, or the information may have changed. Claude asks blocking questions, identifies incorrect assumptions, requests specific changes, and calls out missing context or test gaps. Claude must not edit files.

The scripts default new Claude sessions to native `--permission-mode auto`, not `plan`. Native Claude Code plan mode is avoided by default because `ExitPlanMode` is an approval workflow and can interrupt or abort non-interactive consensus runs. The explicit `--permission-mode <mode>` / `-PermissionMode <mode>` override remains available only for new sessions; resumed sessions use `--resume` plus the same read-only tool restrictions.

The consensus subagent may edit workspace files. It should default to editing only the user-specified or inferred target files. If Claude explicitly identifies related files that must change to complete the task correctly, the subagent may expand the write set to those related files. The subagent must list every expanded file in its final result. It must not modify unrelated files.

## Internal Input Kind

`plan` and `file` are internal input kinds. They are not user-facing options and are not review stages.

- `plan`: Use when Claude is reviewing a Codex plan before the main work proceeds. Pass the plan text in `--plan`.
- `file`: Use when the user asks Claude to review one or more existing files or directories. Pass each explicit file or directory path with `--target`; the targets are the source of truth.

For file review requests, do not require the user to phrase the task as a plan review. Create concise instructions that say the subagent will use Claude's review to edit the target if Claude requests changes, then call the script with `--input-kind file --target <path>`.

Verification text is optional supporting context. Use `--verification` or `--verification-file` only when command output or test results are directly relevant.

## Claude Review Contract

Ask Claude to answer with one of these first-line status tokens. The first non-empty line must be exactly one token, with no markdown formatting, heading marker, prefix, or suffix:

Default review priorities:

1. Architecture design correctness and architecture option selection.
2. Execution reliability and verification sufficiency.

Before deep architecture review, Claude should classify the submitted plan or target file changes as `trivial`, `small`, `medium`, or `large` based on actual scope and risk, not line count.

Classification guide:

- `trivial`: localized text, comments, documentation wording, typo fixes, formatting-only changes, or one-line behavior-preserving edits with no interface or data-flow impact.
- `small`: localized implementation change inside one existing module or file, following established patterns, with no public API, dependency, state ownership, cross-module, or compatibility impact.
- `medium`: changes that touch multiple files or modules, modify internal interfaces, change non-trivial behavior, affect validation or testing strategy, or introduce meaningful maintenance tradeoffs.
- `large`: changes that affect public APIs, dependency direction, shared state ownership, persistence or wire formats, cross-module flow, major abstractions, rollout compatibility, or broad architectural direction.

For `trivial` or `small` changes, Claude should perform a lightweight architecture check only:

1. Confirm the change belongs in the touched module or file.
2. Confirm it follows existing local patterns.
3. Confirm it does not alter public APIs, dependency direction, state ownership, cross-module data flow, compatibility, or test isolation.
4. Do not require architecture alternatives unless one of those boundaries is touched.

For `medium` or `large` changes, Claude should review architecture in this order:

1. Scope and existing constraints: confirm the current task boundary, the architectural constraints already present in the workspace, and any compatibility limits that must not be broken.
2. Module responsibilities and boundaries: judge whether the work belongs in the proposed modules, keeps ownership clear, and preserves clean boundaries.
3. Data flow and state ownership: check where data comes from, where it goes, who owns state, and whether transformations and control flow stay understandable.
4. Interfaces, dependencies, and compatibility: check public API changes, dependency direction, coupling, compatibility risk, and test isolation risk.
5. Abstraction fit: judge whether the abstraction level matches the existing system style and avoids premature abstraction, duplicate abstraction, and public interface pollution.
6. Maintainability and extensibility consequences: assess whether the design creates avoidable long-term maintenance or extension risk.
7. In-scope architecture alternatives: actively check whether there is an architecture that still serves the current user request, remains inside the current task scope, and is better overall on complexity, consistency, maintenance cost, compatibility risk, verification difficulty, public API exposure, and dependency expansion. If a clearly better architecture exists and its benefit is enough to justify the change cost, Claude should require the plan or file changes to adopt it instead of approving a merely executable approach.

If a `trivial` or `small` change touches or risks touching module boundaries, public interfaces, dependencies, state ownership, data flow, compatibility, or test isolation, Claude should escalate to the full `medium` or `large` architecture review. Do not escalate a `trivial` or `small` change into full architecture review solely to find optional improvements.

Only after the appropriate lightweight or full architecture review is acceptable should Claude review execution steps, implementation detail, validation coverage, and rollout risk. Claude must not propose broad refactors unrelated to the current task. Architecture feedback must be concrete enough for the consensus subagent to convert into the current plan or target file changes.

After the status token, Claude must immediately output this minimal auditable classification block:

```text
Risk classification: <trivial|small|medium|large>
Classification reason: <one concise sentence>
Architecture review mode: <lightweight|full>
```

The `Classification reason` should briefly justify the chosen risk level and review mode. For `full` review, the `Classification reason` or the next brief sentence should explicitly name the main boundary risk source such as API, dependency, state ownership, data flow, compatibility, or test isolation. For `lightweight` review, the `Classification reason` should say the change is localized and does not touch architecture boundaries.

```text
APPROVED
Risk classification: <trivial|small|medium|large>
Classification reason: <one concise sentence>
Architecture review mode: <lightweight|full>
<brief rationale, optional>
```

Use `APPROVED` only when a `trivial` or `small` change passes the lightweight architecture check and important execution and verification risks are covered, or when a `medium` or `large` change has a sound architecture direction, no clearly better in-scope architecture alternative should be adopted, and the important execution and verification risks are covered. Keep `APPROVED` concise; for `trivial` or `small` changes, do not expand into long architecture analysis beyond the minimal auditable block and a brief rationale unless the change required `full` review.

```text
APPROVED_WITH_NOTES
Risk classification: <trivial|small|medium|large>
Classification reason: <one concise sentence>
Architecture review mode: <lightweight|full>
- <low-risk note or caveat that should be incorporated or explicitly deferred>
```

Use `APPROVED_WITH_NOTES` only for low-risk architecture caveats, architecture improvements that can be explicitly deferred, or execution-level cleanup that the consensus subagent should incorporate into the plan or target files when appropriate, or explicitly defer before another review round. Do not use `APPROVED_WITH_NOTES` for purely optional suggestions that require no follow-up. This is not a final approval state. For `plan`, the subagent sends the complete updated plan in the next round.

```text
REVISE
Risk classification: <trivial|small|medium|large>
Classification reason: <one concise sentence>
Architecture review mode: <lightweight|full>
- <required plan change, incorrect assumption, missing inspection, target file issue, or verification gap>
```

Use `REVISE` when a `trivial` or `small` change is in the wrong module, violates existing local patterns, actually touches architecture boundaries without accounting for them, or has an execution/verification issue that the consensus subagent can fix without asking the user. For `medium` or `large` changes, use `REVISE` when there is an architecture design problem, a clearly better in-scope architecture alternative that should be adopted, or an execution/verification issue that the consensus subagent can fix without asking the user. Architecture issues take priority even when the current execution steps are complete. `REVISE` feedback must be actionable. For file or document review, identify the affected location, the problem, and the expected result. If the reason for `REVISE` is that a seemingly `trivial` or `small` change actually triggered `full` review, state which boundary risk caused the escalation. For `plan`, the subagent sends the complete updated plan in the next round.

```text
BLOCKED
Risk classification: <trivial|small|medium|large>
Classification reason: <one concise sentence>
Architecture review mode: <lightweight|full>
- <missing user decision, inaccessible required context, or contradiction that prevents reliable execution>
```

Use `BLOCKED` when architecture judgment or reliable execution needs a missing user decision, inaccessible required context, or resolution of a contradiction. Do not only say that information is insufficient; identify the specific missing decision, inaccessible context, or contradiction and explain why it blocks a reliable judgment.

Claude should separate blocking concerns from non-blocking notes. `REVISE` and `APPROVED_WITH_NOTES` feedback should be concrete enough for the consensus subagent to turn into edits, deferrals, or verification steps. For file or document editing requests, Claude should identify the affected location, problem, and expected result, and a response that only reports opinions when the user asked for edits should be `REVISE`. Claude must not edit files.

## Script Usage

```bash
./scripts/ask_claude_consensus.sh \
  --workspace /path/to/workspace \
  --input-kind plan \
  --task "Original user request" \
  --plan "Current Codex plan" \
  --round 1
```

By default, the scripts do not pass `--model`, so Claude uses the Claude CLI default. Override it with `--model <name>` on Unix-like systems or `-Model <name>` in PowerShell.

For follow-up plan rounds inside the same consensus subagent and same requirement:

```bash
./scripts/ask_claude_consensus.sh \
  --workspace /path/to/workspace \
  --input-kind plan \
  --plan "Complete updated Codex plan" \
  --round 2 \
  --session "$CLAUDE_SESSION_ID"
```

For a file review inferred from a user request:

```bash
./scripts/ask_claude_consensus.sh \
  --workspace /path/to/workspace \
  --input-kind file \
  --target /path/to/file.md \
  --task "Original user request" \
  --plan "Use Claude's review to edit the file if changes are requested." \
  --round 1
```

For follow-up file rounds after the consensus subagent edits targets:

```bash
./scripts/ask_claude_consensus.sh \
  --workspace /path/to/workspace \
  --input-kind file \
  --target /path/to/file.md \
  --round 2 \
  --session "$CLAUDE_SESSION_ID"
```

The script prints:

```text
session_id=<claude-session-id>
output_path=<markdown-output-path>
```

## Verification

Recommended static checks:

```bash
bash -n scripts/ask_claude_consensus.sh
./scripts/ask_claude_consensus.sh --help
./scripts/ask_claude_consensus.sh --input-kind file --task "x" --plan "y"
./scripts/ask_claude_consensus.sh --input-kind nope --task "x" --plan "y"
```

When `claude` and `jq` are installed, run a dummy two-round review and confirm:

- Round 1 creates and prints a `session_id`.
- Round 2 reuses that session with `--session`.
- `.runtime/*.md` is generated.

When `pwsh` is installed, also verify PowerShell help and argument validation:

```powershell
./scripts/ask_claude_consensus.ps1 -Help
```
