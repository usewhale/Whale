package tui

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

// TestInterleavedTextAndToolOrderingRepro reproduces the TUI reordering bug.
//
// A single Plan-mode turn interleaves text and tools the way the model really
// streams them:
//
//	think  "Let me inspect the diff first."
//	tool   git diff   (ran to gather context)
//	plan   "FINAL_PLAN_MARKER ..."   (written AFTER inspecting)
//
// A subagent is in flight for the whole turn, so it stays a pending lifecycle
// item (Item.Pending == true at PhaseProgress). That pending item gates the
// per-tool-result commit in handleToolResultEvent:
//
//	if !m.hasPendingLifecycleItems() { m.commitLiveTranscript(false) }
//
// so the git diff result is NEVER committed in order. The old live render then
// split the assembler into a leading "before" block + timeline + "after", which
// collapsed ALL model text into "before" and pushed every tool below it. Result:
// the final plan text rendered ABOVE the git diff that chronologically preceded
// it — exactly the "新增的内容跑到前面去了" symptom from session 019ee34e
// (example 2) and the subagent / "Plan mode enabled" ordering from example 1.
//
// The fix stamps a render sequence onto both buffers and merges them by that
// sequence (see mergeBySeq), so the correct order is preserved:
// think → git diff → FINAL_PLAN_MARKER. This test guards against a regression.
func TestInterleavedTextAndToolOrderingRepro(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.busy = true
	m.chatMode = "plan"

	feed := func(ev protocol.Event) {
		next, _ := m.Update(svcMsg(ev))
		m = next.(model)
	}

	// 1. Subagent spawned and running for the whole turn -> pending lifecycle item.
	feed(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "sub-1", ToolName: "spawn_subagent", Text: "spawn_subagent: review · inspect repo"})
	feed(protocol.Event{
		Kind:       protocol.EventTaskProgress,
		ToolCallID: "sub-1",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent running · review · reading internal/agent/plan_mode.go",
		Metadata:   map[string]any{"child_session_id": "parent--subagent-sub-1", "child_tool": "read_file", "role": "review"},
	})

	if !m.hasPendingLifecycleItems() {
		t.Fatal("precondition failed: running subagent must be a pending lifecycle item so commits are deferred")
	}

	// 2. Model writes a short preamble (reasoning).
	feed(protocol.Event{Kind: protocol.EventReasoningDelta, Text: "Let me inspect the diff first."})

	// 3. Model runs git diff to gather context (completes).
	feed(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "diff-1", ToolName: "shell_run", Text: "shell_run: git diff --stat"})
	feed(protocol.Event{
		Kind:        protocol.EventToolResult,
		ToolCallID:  "diff-1",
		ToolName:    "shell_run",
		ToolOutcome: "success",
		ToolCode:    "ok",
		Text:        `{"success":true,"payload":{"command":"git diff --stat","stdout":"GITDIFF_MARKER\n"}}`,
	})

	// 4. Only now does the model write the final plan (after inspecting).
	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "FINAL_PLAN_MARKER: create the PR."})

	rendered := strings.Join(tuirender.ChatLines(m.liveTranscriptMessages(), 120), "\n")

	posDiff := strings.Index(rendered, "git diff --stat")
	posPlan := strings.Index(rendered, "FINAL_PLAN_MARKER")
	if posDiff == -1 || posPlan == -1 {
		t.Fatalf("markers missing (diff=%d plan=%d):\n%s", posDiff, posPlan, rendered)
	}
	if posDiff > posPlan {
		t.Fatalf("REORDERING BUG: git diff (ran before the plan) rendered AFTER the final plan text.\n"+
			"diff@%d plan@%d\n---\n%s", posDiff, posPlan, rendered)
	}
}

// TestSameRoleDeltaOrderingAcrossToolBoundary guards the harder case the first
// test sidesteps by using two different roles (think then plan): when the SAME
// stream role resumes after a tool, the assembler coalesces the post-tool delta
// into the pre-tool message. If that coalescing is not broken at the tool
// boundary the resumed text inherits the pre-tool Seq and mergeBySeq renders it
// above the tool — the very bug this patch fixes still reproduces.
//
// Flow mirrors a real Plan-mode turn: plan_delta preamble → shell_run → final
// plan_delta, all the plan role, with a pending subagent deferring commits.
func TestSameRoleDeltaOrderingAcrossToolBoundary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.busy = true
	m.chatMode = "plan"

	feed := func(ev protocol.Event) {
		next, _ := m.Update(svcMsg(ev))
		m = next.(model)
	}

	// Pending subagent for the whole turn -> commits stay deferred.
	feed(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "sub-1", ToolName: "spawn_subagent", Text: "spawn_subagent: review · inspect repo"})
	feed(protocol.Event{
		Kind:       protocol.EventTaskProgress,
		ToolCallID: "sub-1",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent running · review · reading internal/agent/plan_mode.go",
		Metadata:   map[string]any{"child_session_id": "parent--subagent-sub-1", "child_tool": "read_file", "role": "review"},
	})

	// plan_delta preamble.
	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "PREAMBLE: let me inspect the diff first.\n\n"})

	// shell_run completes between the two plan deltas.
	feed(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "diff-1", ToolName: "shell_run", Text: "shell_run: git diff --stat"})
	feed(protocol.Event{
		Kind:        protocol.EventToolResult,
		ToolCallID:  "diff-1",
		ToolName:    "shell_run",
		ToolOutcome: "success",
		ToolCode:    "ok",
		Text:        `{"success":true,"payload":{"command":"git diff --stat","stdout":"GITDIFF_MARKER\n"}}`,
	})

	// Final plan_delta — SAME role as the preamble, so it coalesces unless the
	// tool boundary breaks it.
	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "FINAL_PLAN_MARKER: create the PR."})

	rendered := strings.Join(tuirender.ChatLines(m.liveTranscriptMessages(), 120), "\n")

	posDiff := strings.Index(rendered, "git diff --stat")
	posPlan := strings.Index(rendered, "FINAL_PLAN_MARKER")
	if posDiff == -1 || posPlan == -1 {
		t.Fatalf("markers missing (diff=%d plan=%d):\n%s", posDiff, posPlan, rendered)
	}
	if posDiff > posPlan {
		t.Fatalf("REORDERING BUG (same-role coalescing): git diff rendered AFTER the final plan text.\n"+
			"diff@%d plan@%d\n---\n%s", posDiff, posPlan, rendered)
	}
}

// TestPlanCompletedCardOrdersAfterTool locks the finalization half of the fix:
// once coalescing is broken at tool boundaries a Plan-mode turn holds multiple
// plan rows, and SetPlan must keep the LAST row's position so the finalized
// Proposed Plan card renders after the investigation tool, not at the pre-tool
// preamble position.
func TestPlanCompletedCardOrdersAfterTool(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.busy = true
	m.chatMode = "plan"

	feed := func(ev protocol.Event) {
		next, _ := m.Update(svcMsg(ev))
		m = next.(model)
	}

	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "PREAMBLE: inspect first.\n\n"})
	feed(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "diff-1", ToolName: "shell_run", Text: "shell_run: git diff --stat"})
	feed(protocol.Event{
		Kind:        protocol.EventToolResult,
		ToolCallID:  "diff-1",
		ToolName:    "shell_run",
		ToolOutcome: "success",
		ToolCode:    "ok",
		Text:        `{"success":true,"payload":{"command":"git diff --stat","stdout":"GITDIFF_MARKER\n"}}`,
	})
	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "draft plan...\n"})
	// Finalize: the authoritative plan card replaces the streamed plan rows.
	feed(protocol.Event{Kind: protocol.EventPlanCompleted, Text: "FINAL_CARD_MARKER\n- step one"})

	// plan_completed commits the live transcript, so assert on the committed view.
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 120), "\n")
	posDiff := strings.Index(rendered, "git diff --stat")
	posCard := strings.Index(rendered, "FINAL_CARD_MARKER")
	if posDiff == -1 || posCard == -1 {
		t.Fatalf("markers missing (diff=%d card=%d):\n%s", posDiff, posCard, rendered)
	}
	if posDiff > posCard {
		t.Fatalf("finalized plan card rendered BEFORE the investigation tool.\n"+
			"diff@%d card@%d\n---\n%s", posDiff, posCard, rendered)
	}
}

// TestSequenceMonotonicAcrossResponseReset guards the multi-round tool-use case:
// EventResponseReset resets the assembler but deliberately KEEPS the timeline's
// tool rows. If the assembler's sequence floor restarts at zero on that reset,
// the post-reset final plan delta reuses a sequence below the retained tool and
// mergeBySeq renders it above the tool. The floor must stay monotonic while the
// timeline is retained.
func TestSequenceMonotonicAcrossResponseReset(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.busy = true
	m.chatMode = "plan"

	feed := func(ev protocol.Event) {
		next, _ := m.Update(svcMsg(ev))
		m = next.(model)
	}

	// Round 1: plan preamble + a tool call that is still in flight (so the
	// timeline retains it — no commit happens).
	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "PREAMBLE: inspect first.\n\n"})
	feed(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "diff-1", ToolName: "shell_run", Text: "shell_run: git diff --stat"})

	if !m.hasPendingLifecycleItems() {
		t.Fatal("precondition: in-flight tool call should keep the timeline pending/retained")
	}

	// New response round begins: resets the live assembler, keeps the timeline.
	feed(protocol.Event{Kind: protocol.EventResponseReset})

	// Round 2: the final plan streams fresh.
	feed(protocol.Event{Kind: protocol.EventPlanDelta, Text: "FINAL_PLAN_MARKER: create the PR."})

	rendered := strings.Join(tuirender.ChatLines(m.liveTranscriptMessages(), 120), "\n")
	posDiff := strings.Index(rendered, "git diff --stat")
	posPlan := strings.Index(rendered, "FINAL_PLAN_MARKER")
	if posDiff == -1 || posPlan == -1 {
		t.Fatalf("markers missing (diff=%d plan=%d):\n%s", posDiff, posPlan, rendered)
	}
	if posDiff > posPlan {
		t.Fatalf("REORDERING BUG (response reset): retained tool rendered AFTER the post-reset plan.\n"+
			"diff@%d plan@%d\n---\n%s", posDiff, posPlan, rendered)
	}
}
