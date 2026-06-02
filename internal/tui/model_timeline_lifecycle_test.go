package tui

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func TestTimelineRendersHookLifecycleNotice(t *testing.T) {
	m := newModel(nil, "", "", "")
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventHookStarted,
		Hook: &protocol.HookRun{ID: "hook-1", Name: "approval gate", Status: "running"},
		Text: "PermissionRequest hook running",
	}))
	m = next.(model)
	if !m.hasPendingLifecycleItems() {
		t.Fatal("hook started should keep lifecycle pending")
	}

	next, _ = m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventHookCompleted,
		Hook: &protocol.HookRun{ID: "hook-1", Name: "approval gate", Status: "blocked", Message: "blocked by policy"},
		Text: "PermissionRequest hook blocked: blocked by policy",
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if !strings.Contains(rendered, "PermissionRequest hook blocked") || strings.Contains(rendered, "hook running") {
		t.Fatalf("expected completed hook notice from timeline:\n%s", rendered)
	}
	if m.hasPendingLifecycleItems() {
		t.Fatal("hook completion should clear lifecycle pending state")
	}
}

func TestTimelineRendersUserInputLifecycle(t *testing.T) {
	m := newModel(nil, "", "", "")
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:       protocol.EventUserInputRequired,
		ToolCallID: "input-1",
		ToolName:   "request_user_input",
		Questions:  []protocol.UserInputQuestion{{ID: "confirm", Question: "Proceed?"}},
	}))
	m = next.(model)
	live := strings.Join(tuirender.ChatLines(m.liveTranscriptMessages(), 100), "\n")
	if !strings.Contains(live, "User input required") || !m.hasPendingLifecycleItems() {
		t.Fatalf("expected pending user input live row:\n%s", live)
	}

	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventUserInputDone, ToolCallID: "input-1", ToolName: "request_user_input", Status: "submitted"}))
	m = next.(model)
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if !strings.Contains(rendered, "User input submitted") || m.hasPendingLifecycleItems() {
		t.Fatalf("expected committed user input done notice:\n%s", rendered)
	}
}

func TestTimelineRendersWorkflowSnapshotInsteadOfAssistantTerminal(t *testing.T) {
	m := newModel(nil, "", "", "")
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:          protocol.EventWorkflowSnapshot,
		WorkflowRunID: "run-1",
		Status:        "running",
		LocalResult: &protocol.LocalResult{Kind: "workflow", WorkflowPanelSnapshot: &protocol.WorkflowPanelSnapshot{
			RunID:        "run-1",
			Status:       "running",
			Summary:      "reviewing repo",
			CurrentPhase: "inspect",
		}},
	}))
	m = next.(model)
	live := strings.Join(tuirender.ChatLines(m.liveTranscriptMessages(), 100), "\n")
	if !strings.Contains(live, "Workflow") || !strings.Contains(live, "run-1") || !m.hasPendingLifecycleItems() {
		t.Fatalf("expected running workflow snapshot live row:\n%s", live)
	}

	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:          protocol.EventWorkflowTerminal,
		WorkflowRunID: "run-1",
		Text:          "Workflow\n\nexecutiveSummary: should stay in panel",
		LocalResult: &protocol.LocalResult{Kind: "workflow-terminal", PlainText: "Workflow\n\nexecutiveSummary: should stay in panel", WorkflowPanelSnapshot: &protocol.WorkflowPanelSnapshot{
			RunID:   "run-1",
			Status:  "completed",
			Summary: "done",
		}},
	}))
	m = next.(model)
	if len(m.transcript) != 1 {
		t.Fatalf("expected one committed workflow lifecycle row, got %+v", m.transcript)
	}
	if msg := m.transcript[0]; msg.Role == "assistant" || strings.Contains(msg.Text, "executiveSummary") || !strings.Contains(msg.Text, "Workflow") {
		t.Fatalf("workflow terminal should commit lifecycle row, got %+v", msg)
	}
}
