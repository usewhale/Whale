# Whale Token Efficiency Plan

This plan targets the current remaining token-efficiency problems found in recent Whale sessions:

- tool result replay is still the largest avoidable input-token source
- reasoning replay is required for some DeepSeek tool-call histories, but is not yet visible enough
- subagent usage must be counted as real spend, not hidden behind parent-session totals
- malformed DeepSeek message chains should be repaired before they cause retries or poisoned resumes

## Goals

1. Reduce repeated prompt/input tokens from large historical tool results.
2. Preserve DeepSeek tool-calling correctness, including `tool_call_id` and `reasoning_content` requirements.
3. Make parent and subagent spend visible in `/stats profile`.
4. Keep prefix-cache stability high by avoiding unnecessary historical message rewrites.
5. Add focused tests so future changes cannot silently reintroduce token bloat.

## Non-Goals

- Do not redesign the whole agent harness in this pass.
- Do not add a full DeepSeek-TUI-style swarm/RLM control plane.
- Do not hide child-agent cost by treating it as a parent-session formatting detail only.
- Do not compact away user-visible evidence needed for resume/debugging.

## Reference Findings

### reasonix

Most directly applicable reference.

- Repairs loaded/sent messages by shrinking oversized tool results and dropping invalid tool-call chains.
- Uses token-aware caps for tool results, not only char caps.
- Preserves `reasoning_content` when required, while avoiding unnecessary stamping for non-thinking modes to protect prefix cache.
- Records subagent usage as first-class spend and includes it in main usage buckets.
- Positions subagents as useful for true parallelism or context blow-up, not routine single-step reads.

### DeepSeek-TUI

Best protocol and observability reference.

- Runs a final request sanitizer before sending to DeepSeek.
- Adds placeholder `reasoning_content` for tool-call assistant messages when required.
- Logs/estimates replayed reasoning cost so users can see how much input budget is spent on prior thinking traces.
- Compaction summaries include snippets of tool results and skip thinking blocks.
- Tracks child recursive-LM usage and exposes child input/output tokens in metadata.

### deepcode-cli

Useful but less precise for this task.

- Uses coarse session compaction once active tokens exceed a threshold.
- Rebuilds OpenAI messages with tool-call pairing before send.
- Hard-caps shell output, but the cap is relatively large and not enough by itself for Whale's current efficiency target.

## Phase 1: Finish Subagent Usage Visibility

Status: mostly implemented.

Work:

- Keep `/stats profile` main-session totals backward compatible.
- Add subagent totals grouped under parent work sessions.
- Add all-in totals so hidden child-agent spend is visible.
- Support both parent-id-derived subagent sessions and explicit metadata such as `kind=subagent` plus `parent_session_id`.

Acceptance criteria:

- Existing stats output remains stable for sessions without subagents.
- A parent session with child usage shows child count, child cost, child token total, and all-in total.
- Tests cover both naming-based and metadata-based child association.

Validation:

- `make test`

## Phase 2: Tool Result History Compaction and Healing

Priority: highest.

Work:

- Add a send-time or load-time history healing pass for model messages.
- Detect oversized historical tool results before they are replayed into the next model request.
- Replace large tool result content with a bounded summary envelope that preserves:
  - original tool name when available
  - `tool_call_id`
  - success/error status when available
  - truncation marker
  - byte/char/token estimate saved
  - enough leading/trailing content for debugging
- Preserve the assistant `tool_calls` to `tool` message pairing so DeepSeek does not reject the request.
- Drop or repair invalid chains:
  - stray `tool` messages without a preceding assistant tool call
  - assistant tool calls with missing ids
  - assistant tool calls with incomplete tool responses
  - dangling trailing assistant tool calls on resume
- Prefer token-aware limits where practical; keep char fallback for cheap deterministic tests.

Acceptance criteria:

- Large historical tool results are not resent verbatim after the cap.
- Valid tool-call chains remain valid after compaction.
- Invalid chains are repaired before send, not retried until failure.
- The transcript/session file remains useful for debugging; the model-facing replay can be compacted even if persisted raw data is retained.

Validation:

- Unit tests for oversized tool result shrink.
- Unit tests for valid pair preservation.
- Unit tests for stray/incomplete/dangling tool-call repair.
- Replay test with a real-looking shell/read_file-heavy session.
- `make test`

## Phase 3: DeepSeek Reasoning Replay Observability

Priority: high.

Work:

- Track historical assistant `reasoning_content` size included in each outgoing request.
- Estimate reasoning replay tokens using the same token estimator used by stats where possible.
- Add `/stats profile` fields for reasoning replay:
  - total replayed reasoning chars/tokens
  - top sessions by reasoning replay
  - ratio of reasoning replay to total input tokens
- Separate raw generated reasoning volume from replayed reasoning volume.
- Avoid deleting required reasoning content until the tool-call protocol rules are fully respected.

Acceptance criteria:

- `/stats profile` can distinguish "model generated a lot of reasoning" from "we keep replaying old reasoning".
- Sessions with tool-call reasoning chains show measurable replay cost.
- Sessions without reasoning content do not show noisy empty fields.

Validation:

- Stats fixture with assistant reasoning plus tool calls.
- Stats fixture with assistant reasoning but no required replay path.
- `make test`

## Phase 4: Final DeepSeek Message Sanitizer

Priority: high after Phase 2.

Work:

- Add one final provider-specific sanitizer immediately before DeepSeek request serialization.
- Ensure assistant messages that require `reasoning_content` have it.
- Preserve emitted non-empty `reasoning_content` even when the current model mode would otherwise not stamp it.
- Add placeholder only when required for protocol validity.
- Log enough context on provider 400s to identify:
  - missing `reasoning_content`
  - missing `tool_call_id`
  - stray tool messages
  - malformed tool call arguments
- Keep logs bounded and secret-safe.

Acceptance criteria:

- Sanitizer is the last line of defense and does not replace normal history hygiene.
- DeepSeek 400s from invalid tool-call/reasoning chains are reduced or made diagnosable.
- Sanitizer does not mutate unrelated providers' requests.

Validation:

- Provider request-shaping tests for DeepSeek.
- Tests for placeholder insertion only when required.
- Tests for no-op behavior on non-DeepSeek providers.
- `make test`

## Phase 5: Stats Profile Diagnostics for Tool Bloat

Priority: medium.

Work:

- Extend `/stats profile` with model-facing replay diagnostics:
  - top sessions by tool-result replay chars/tokens
  - top tool names by replay footprint
  - count of compacted/healed tool results
  - estimated tokens saved by tool compaction
- Keep current "Top tools" raw result chars, but add a separate "replayed/history footprint" view.
- Add a profile note when raw tool output is high but replay was compacted successfully.

Acceptance criteria:

- Stats can answer whether token usage is coming from fresh tool output or historical replay.
- Tool-heavy sessions show which tools drove replay pressure.
- Token savings are visible enough to compare before/after sessions.

Validation:

- Stats fixtures with raw large tool output and compacted replay output.
- `make test`

## Phase 6: Subagent Harness Guardrails

Priority: medium, after observability is solid.

Work:

- Add prompt/tool descriptions that discourage subagent use for cheap local reads.
- Recommend subagents only for:
  - true parallel independent investigations
  - broad multi-file surveys
  - context-heavy research where only distilled output is needed
- Ensure parent history stores only child summaries by default, not child raw tool traces.
- Keep child usage logged with parent association.
- Add a small per-child result summary schema if current child outputs are too verbose.

Acceptance criteria:

- Parent sessions do not absorb child raw tool outputs.
- Child cost remains visible in profile/all-in stats.
- Prompts make the spawn/no-spawn boundary explicit.

Validation:

- Subagent session fixture with child tool-heavy run.
- Stats profile verifies child raw tool footprint is separate from parent replay.
- `make test`

## Phase 7: Regression Dataset and Before/After Report

Priority: final verification.

Work:

- Select recent effective Whale sessions that are not trivial "hi" style sessions.
- Build a small local replay/profile fixture set from anonymized or sanitized session shapes.
- Run before/after profile comparison:
  - total input tokens
  - max prompt
  - cache hit/miss
  - tool-result replay footprint
  - reasoning replay footprint
  - all-in parent plus subagent cost
- Document the measured delta in a short report.

Acceptance criteria:

- We can show whether token efficiency improved on representative sessions.
- The report distinguishes real savings from cache artifacts.
- The profile no longer undercounts subagent spend.

Validation:

- `make test`
- Manual `/stats profile` comparison on latest effective sessions.

## Recommended Implementation Order

1. Finish and keep the current subagent usage profile change.
2. Implement tool-result healing/compaction.
3. Add reasoning replay diagnostics.
4. Add final DeepSeek sanitizer.
5. Extend stats with tool replay savings.
6. Add subagent guardrails.
7. Run before/after session comparison.

## Open Design Questions

- Should Whale persist compacted tool results, or keep raw session logs and compact only the model-facing replay?
- What default cap should be used for model-facing historical tool results?
- Should caps be per tool type, global, or both?
- Should `reasoning_content` replay diagnostics count exact tokenizer tokens or a fast estimate?
- Should child session association be inferred only by metadata going forward, with id-prefix support kept for old sessions?

