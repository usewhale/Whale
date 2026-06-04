---
name: claude-proposal-consensus
description: Use only when the user explicitly asks for Claude to propose the plan first, an OpenAI agent to review it, and Claude/OpenAI-agent iteration until the OpenAI agent reaches consensus.
---

# Claude Proposal Consensus

Use this skill only for explicit requests where Claude must own the initial proposal and an OpenAI agent must review and feed back on that proposal until consensus is reached. Do not trigger it for ordinary planning, code review, implementation, or the forward `claude-consensus` workflow where the OpenAI agent drafts first and Claude reviews.

This skill is intentionally separate from `/home/jh/claude-consensus`. Version 1 only produces a consensus plan; it does not execute the plan and does not modify business files.

## Roles

- Claude is the proposal owner. Claude inspects the workspace and writes the complete proposed plan.
- The OpenAI agent is the reviewer. The OpenAI agent judges Claude's proposal, writes concrete feedback, and decides whether the loop is `APPROVED`, `REVISE`, or `BLOCKED`.
- The host OpenAI agent only identifies the explicit request, starts a fresh consensus subagent, waits for the result, and reports the final consensus outcome.
- The consensus subagent owns the Claude CLI calls, proposal review, feedback files, and multi-round loop.

Claude is always read-only in this workflow. The wrappers pass only `Read`, `Grep`, `Glob`, and `LS` as allowed tools. Claude must not edit files, run mutation tools, or execute the final plan.

## Isolation Model

One user requirement maps to exactly one fresh consensus subagent and one fresh Claude session.

- The host OpenAI agent creates a fresh subagent for the current requirement.
- The subagent starts a new Claude session on round 1 by omitting `--session`.
- The subagent may reuse the returned Claude `session_id` only inside that same subagent and only for the same user requirement.
- Every new requirement must create a new subagent and a new Claude session.
- Do not reuse old Claude sessions or old consensus subagents for a different requirement.
- Do not expose Claude session ids to the user, store them for reuse, or return them to the main agent as reusable state.
- `.runtime/` is only for per-round Claude markdown output and OpenAI-agent feedback files.

## Main OpenAI-Agent Workflow

1. Confirm that the user explicitly asked for Claude to propose first and an OpenAI agent to review or reach consensus.
2. Read only enough local context to pass the workspace path, original user problem, and relevant constraints to the subagent.
3. Spawn a fresh subagent without unrelated historical context.
4. Tell the subagent to use this skill, the original user problem, workspace path, and any explicit constraints.
5. Wait for the subagent to complete the proposal-review loop.
6. Report the result:
   - `Final consensus plan` in full when final status is `APPROVED`.
   - Final status.
   - Number of Claude proposal rounds.
   - Any `BLOCKED` reason and the current best plan when blocked.

The host OpenAI agent must not call Claude directly for this workflow and must not execute the consensus plan unless the user separately asks to proceed after seeing it.

## Consensus Subagent Workflow

Use `scripts/ask_claude_proposal_consensus.sh` on Unix-like systems or `scripts/ask_claude_proposal_consensus.ps1` on PowerShell hosts.

1. Round 1: call the wrapper without `--session`, passing `--workspace`, `--task`, and `--round 1`.
2. Save the printed `session_id` only inside this subagent/request.
3. Read the generated `output_path` markdown. This is Claude's complete proposed plan.
4. Review Claude's proposal internally and decide:
   - `APPROVED`: Claude's proposal is coherent, in scope, matches the workspace, has a sound architecture direction, and includes sufficient execution and verification steps.
   - `REVISE`: the proposal has fixable gaps, wrong assumptions, missing workspace inspection, weak architecture, unclear execution steps, or insufficient verification.
   - `BLOCKED`: reliable planning requires a missing user decision, inaccessible required context, or a contradiction that the subagent cannot resolve.
5. For `APPROVED`, stop and return a complete `Final consensus plan`. The final plan must be the latest complete Claude proposal, optionally with clearly labeled OpenAI-agent-approved clarifications if they do not change meaning.
6. For `REVISE`, write a concrete markdown feedback file under `.runtime/`, then call the wrapper again with the same `--session`, the `--feedback-file`, and the next `--round`. Feedback must ask Claude to return a full revised plan, not a diff, patch, or partial update.
7. For `BLOCKED`, stop and return the blocker, the latest current best plan if one exists, and the round count.
8. Do not impose a fixed round limit. Continue until the OpenAI agent can return `APPROVED` or `BLOCKED`.

OpenAI-agent feedback should be specific and actionable. It should identify incorrect assumptions, missing files or workspace checks, architectural issues, execution gaps, verification gaps, and any ambiguity that Claude must resolve in the next full proposal.

## Script Usage

Round 1 starts a fresh Claude session:

```bash
./scripts/ask_claude_proposal_consensus.sh \
  --workspace /path/to/workspace \
  --task "Original user problem" \
  --round 1
```

Later rounds reuse the same Claude session for the same requirement:

```bash
./scripts/ask_claude_proposal_consensus.sh \
  --workspace /path/to/workspace \
  --feedback-file /path/to/openai-agent-review.md \
  --round 2 \
  --session "$CLAUDE_SESSION_ID"
```

By default, the scripts do not pass `--model`, so Claude uses the local Claude CLI default. Override with `--model <name>` on Unix-like systems or `-Model <name>` in PowerShell.

The scripts print:

```text
session_id=<claude-session-id>
output_path=<markdown-output-path>
```

The subagent must not expose `session_id` outside the subagent.

## Claude Prompt Contract

Round 1 asks Claude to inspect the workspace in read-only mode and produce a complete proposal for the original user problem.

Resumed rounds send OpenAI-agent review feedback into the same Claude session and require Claude to produce a complete revised plan. Claude must not reply with only a diff, summary of changes, or local patch.

Claude proposals should include:

- Restated goal and assumptions.
- Relevant workspace findings.
- Recommended approach and architecture rationale.
- Files or modules expected to change if the plan is later executed.
- Step-by-step implementation plan.
- Verification plan.
- Database changes (if applicable): a dedicated, clearly labeled section listing all database-related changes, including schema migrations, new or altered tables/columns/indexes, data migrations, stored procedure changes, ORM model changes that imply DDL, and connection or pool configuration changes. Each item should state what changes and why. Database-related work must not appear only inside implementation steps; it must be surfaced in this section for at-a-glance visibility.
- Risks, blockers, and decisions needed from the user.

Claude should clearly say when required context is missing rather than inventing facts.

## OpenAI Agent Review Criteria

The OpenAI agent should review the proposal for:

- Correct interpretation of the user's task.
- Sufficient workspace inspection and correct assumptions.
- Architecture fit with existing module boundaries, dependencies, state ownership, data flow, compatibility, and test isolation.
- Whether a simpler or more local in-scope approach is clearly better.
- Concrete, executable steps rather than vague recommendations.
- Adequate verification for the risk and scope.
- When the proposal involves database changes, whether a dedicated "Database changes" section is present, clearly labeled, and covers all database-related work. The reviewer must not return `APPROVED` for a database-related plan that lacks this section or buries database changes only in implementation steps.
- Clear handling of blockers, user decisions, and deferred work.

Return `APPROVED` only when the latest complete proposal is ready to present as the final consensus plan. Return `REVISE` when Claude can fix the issue with feedback. Return `BLOCKED` when continuing would require missing user input or inaccessible context.
