package timeline

import (
	"testing"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

func TestBuilderMapsApprovedToolFlow(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "tc-1", ToolName: "list_dir", Text: "list_dir: ."})
	b.HandleEvent(protocol.Event{
		Kind:       protocol.EventApprovalRequired,
		ToolCallID: "tc-1",
		ToolName:   "list_dir",
		Approval: &protocol.ApprovalRequest{
			Key:      "approval-list",
			ToolCall: protocol.ToolCall{ID: "tc-1", Name: "list_dir"},
		},
	})
	b.HandleEvent(protocol.Event{Kind: protocol.EventApprovalDecision, ToolCallID: "tc-1", ToolName: "list_dir", ApprovalID: "approval-list", Decision: "allow_session"})
	b.HandleEvent(protocol.Event{
		Kind:       protocol.EventToolResult,
		ToolCallID: "tc-1",
		ToolName:   "list_dir",
		Text:       `{"success":true}`,
		Metadata:   map[string]any{"exit_code": 0},
	})

	snap := b.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected one timeline item, got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.Kind != ItemKindTool || item.Phase != PhaseCompleted || item.ToolCallID != "tc-1" || item.ApprovalID != "approval-list" {
		t.Fatalf("unexpected tool item: %+v", item)
	}
	if item.WorkflowRunID != "" {
		t.Fatalf("regular tool should not infer workflow run id from unrelated metadata: %+v", item)
	}
	assertPhases(t, item, PhaseRequested, PhaseApprovalRequired, PhaseApprovalDecided, PhaseCompleted)
}

func TestBuilderMapsApprovalDecisionWithoutPrompt(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventApprovalDecision, ToolCallID: "tc-1", ToolName: "shell_run", ApprovalID: "approval-shell", Decision: "allow_session"})

	snap := b.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected one item, got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.Phase != PhaseApprovalDecided || item.ApprovalID != "approval-shell" {
		t.Fatalf("unexpected approval decision item: %+v", item)
	}
}

func TestBuilderMapsSubagentProgressFlow(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "tc-sub", ToolName: "spawn_subagent", Text: "spawn_subagent: prefill-smoke"})
	b.HandleEvent(protocol.Event{Kind: protocol.EventTaskProgress, ToolCallID: "tc-sub", ToolName: "spawn_subagent", Text: "spawn_subagent running · explore · reading"})
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "tc-sub", ToolName: "spawn_subagent", Text: `{"success":true}`})

	snap := b.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected one timeline item, got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.Kind != ItemKindSubagent || item.Phase != PhaseCompleted {
		t.Fatalf("unexpected subagent item: %+v", item)
	}
	assertPhases(t, item, PhaseRequested, PhaseProgress, PhaseCompleted)
}

func TestBuilderMapsWorkflowLaunchAndTerminal(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "workflow-1", ToolName: "workflow", Text: `workflow: {"name":"scan"}`})
	b.HandleEvent(protocol.Event{
		Kind:       protocol.EventToolResult,
		ToolCallID: "workflow-1",
		ToolName:   "workflow",
		Text:       `{"success":true,"data":{"runId":"run-123"}}`,
		Metadata:   map[string]any{"workflow_run_id": "run-123"},
	})
	b.HandleEvent(protocol.Event{
		Kind:          protocol.EventWorkflowTerminal,
		WorkflowRunID: "run-123",
		Text:          "Workflow result",
	})

	snap := b.Snapshot()
	if len(snap.Items) != 2 {
		t.Fatalf("expected launch tool and terminal workflow items, got %+v", snap.Items)
	}
	if snap.Items[0].Kind != ItemKindWorkflow || snap.Items[0].ToolCallID != "workflow-1" || snap.Items[0].WorkflowRunID != "run-123" {
		t.Fatalf("unexpected workflow launch item: %+v", snap.Items[0])
	}
	if snap.Items[1].Kind != ItemKindWorkflow || snap.Items[1].WorkflowRunID != "run-123" || snap.Items[1].Phase != PhaseCompleted {
		t.Fatalf("unexpected workflow terminal item: %+v", snap.Items[1])
	}
}

func TestBuilderMergesWorkflowSnapshots(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{
		Kind:          protocol.EventWorkflowSnapshot,
		WorkflowRunID: "run-123",
		Status:        "running",
		LocalResult: &protocol.LocalResult{WorkflowPanelSnapshot: &protocol.WorkflowPanelSnapshot{
			RunID:        "run-123",
			Status:       "running",
			Summary:      "reviewing",
			CurrentPhase: "inspect",
		}},
	})
	if !b.Snapshot().HasPendingItems() {
		t.Fatalf("running workflow snapshot should be pending: %+v", b.Snapshot())
	}
	b.HandleEvent(protocol.Event{
		Kind:          protocol.EventWorkflowSnapshot,
		WorkflowRunID: "run-123",
		Status:        "completed",
		LocalResult:   &protocol.LocalResult{WorkflowPanelSnapshot: &protocol.WorkflowPanelSnapshot{RunID: "run-123", Status: "completed"}},
	})

	snap := b.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected one workflow item, got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.Kind != ItemKindWorkflow || item.WorkflowRunID != "run-123" || item.Phase != PhaseCompleted || snap.HasPendingItems() {
		t.Fatalf("unexpected workflow snapshot item: %+v pending=%v", item, snap.HasPendingItems())
	}
	assertPhases(t, item, PhaseProgress, PhaseCompleted)
}

func TestBuilderMapsHookAndUserInputLifecycle(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventHookStarted, Hook: &protocol.HookRun{ID: "hook-1", Name: "gate", Status: "running"}})
	b.HandleEvent(protocol.Event{Kind: protocol.EventHookCompleted, Hook: &protocol.HookRun{ID: "hook-1", Name: "gate", Status: "blocked"}})
	b.HandleEvent(protocol.Event{Kind: protocol.EventUserInputRequired, ToolCallID: "input-1", ToolName: "ask", Questions: []protocol.UserInputQuestion{{ID: "q", Question: "Proceed?"}}})
	b.HandleEvent(protocol.Event{Kind: protocol.EventUserInputDone, ToolCallID: "input-1", ToolName: "ask", Status: "submitted"})

	snap := b.Snapshot()
	if len(snap.Items) != 2 || snap.HasPendingItems() {
		t.Fatalf("expected completed hook and user input items, got %+v pending=%v", snap.Items, snap.HasPendingItems())
	}
	if snap.Items[0].Kind != ItemKindHook || snap.Items[0].Phase != PhaseFailed {
		t.Fatalf("unexpected hook item: %+v", snap.Items[0])
	}
	if snap.Items[1].Kind != ItemKindUserInput || snap.Items[1].Phase != PhaseCompleted {
		t.Fatalf("unexpected user input item: %+v", snap.Items[1])
	}
}

func TestBuilderKeepsInterleavedToolsSeparate(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "a", ToolName: "read_file"})
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "b", ToolName: "grep"})
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "b", ToolName: "grep", Text: `{"success":true}`})
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "a", ToolName: "read_file", Text: `{"success":true}`})

	snap := b.Snapshot()
	if len(snap.Items) != 2 {
		t.Fatalf("expected two items, got %+v", snap.Items)
	}
	if snap.Items[0].ToolCallID != "a" || snap.Items[1].ToolCallID != "b" {
		t.Fatalf("expected original item order, got %+v", snap.Items)
	}
	if snap.Items[0].Phase != PhaseCompleted || snap.Items[1].Phase != PhaseCompleted {
		t.Fatalf("expected completed phases, got %+v", snap.Items)
	}
}

func TestBuilderCreatesOrphanForResultWithoutCall(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolResult, ToolName: "read_file", Text: `{"success":true}`})

	snap := b.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("expected orphan item, got %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.ID != "orphan:tool_result:1" || item.Phase != PhaseCompleted {
		t.Fatalf("unexpected orphan item: %+v", item)
	}
}

func TestHydrationEventsFromMessageMapsStoredToolLifecycle(t *testing.T) {
	events := HydrationEventsFromMessage(protocol.Message{
		ToolCalls: []protocol.ToolCall{{ID: "read-1", Name: "read_file", Input: `{"file_path":"README.md"}`}},
		ToolResults: []protocol.ToolResult{{
			ToolCallID: "read-1",
			Name:       "read_file",
			Content:    `{"success":true,"data":{"content":"ok"}}`,
			Metadata:   map[string]any{"returned_lines": 1},
		}},
	})
	if len(events) != 2 {
		t.Fatalf("expected call and result events, got %+v", events)
	}
	if events[0].Kind != protocol.EventToolCall || events[0].ToolCallID != "read-1" || events[0].ToolName != "read_file" {
		t.Fatalf("unexpected hydrated call event: %+v", events[0])
	}
	if events[1].Kind != protocol.EventToolResult || events[1].ToolCallID != "read-1" || events[1].Metadata["returned_lines"] != 1 {
		t.Fatalf("unexpected hydrated result event: %+v", events[1])
	}
}

func TestBuilderKeepsLateWorkflowTerminalSeparateFromPendingTool(t *testing.T) {
	b := NewTurnTimelineBuilder()
	b.HandleEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "tool-1", ToolName: "list_dir"})
	b.HandleEvent(protocol.Event{Kind: protocol.EventWorkflowTerminal, WorkflowRunID: "run-late", Text: "Workflow result"})

	snap := b.Snapshot()
	if len(snap.Items) != 2 {
		t.Fatalf("expected pending tool and workflow terminal, got %+v", snap.Items)
	}
	if snap.Items[0].ToolCallID != "tool-1" || snap.Items[0].Phase != PhaseRequested {
		t.Fatalf("unexpected pending tool: %+v", snap.Items[0])
	}
	if snap.Items[1].WorkflowRunID != "run-late" || snap.Items[1].Phase != PhaseCompleted {
		t.Fatalf("unexpected terminal item: %+v", snap.Items[1])
	}
}

func assertPhases(t *testing.T, item Item, phases ...Phase) {
	t.Helper()
	if len(item.Events) != len(phases) {
		t.Fatalf("event count = %d, want %d: %+v", len(item.Events), len(phases), item.Events)
	}
	for i, phase := range phases {
		if item.Events[i].Phase != phase {
			t.Fatalf("event %d phase = %q, want %q: %+v", i, item.Events[i].Phase, phase, item.Events)
		}
	}
}
