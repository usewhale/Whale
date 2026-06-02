package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventJSONRoundTrip(t *testing.T) {
	events := []Event{
		{Kind: EventAssistantDelta, Text: "hello"},
		{Kind: EventResponseReset},
		{
			Kind:          EventToolResult,
			TurnID:        "turn-1",
			ItemID:        "item-1",
			ParentID:      "parent-1",
			ApprovalID:    "approval-1",
			Decision:      "allow_session",
			DecisionScope: "session",
			ApprovalKeys:  []string{"approval-1"},
			WorkflowRunID: "run-1",
			Sequence:      42,
			StartedAt:     time.Unix(1700000000, 0).UTC(),
			ToolCallID:    "tc-1",
			ToolName:      "shell_run",
			Text:          "ok",
			Metadata:      map[string]any{"exit_code": float64(0)},
		},
		{
			Kind: EventApprovalRequired,
			Approval: &ApprovalRequest{
				SessionID: "s1",
				ToolCall:  ToolCall{ID: "tc-2", Name: "shell_run", Input: `{"cmd":"ls"}`},
				Reason:    "confirm command",
				Code:      "shell_command",
				Key:       "shell:ls",
			},
		},
		{
			Kind:          EventWorkflowSnapshot,
			WorkflowRunID: "run-2",
			Status:        "running",
			LocalResult: &LocalResult{
				Kind: "workflow",
				WorkflowPanelSnapshot: &WorkflowPanelSnapshot{
					RunID:        "run-2",
					Status:       "running",
					Summary:      "scan repo",
					CurrentPhase: "inspect",
				},
			},
		},
		{
			Kind:      EventSessionHydrated,
			SessionID: "s1",
			Messages:  []Message{{ID: "m1", Role: "assistant", Text: "done"}},
		},
	}

	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal %s: %v", ev.Kind, err)
		}
		var got Event
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", ev.Kind, err)
		}
		if got.Kind != ev.Kind {
			t.Fatalf("kind mismatch: got %q want %q", got.Kind, ev.Kind)
		}
	}
}

func TestClientAndServerMessageJSONRoundTrip(t *testing.T) {
	client := ClientMessage{
		Type: ClientMessageIntent,
		Intent: &Intent{
			Kind:       IntentSubmitUserInput,
			ToolCallID: "tool-1",
			UserInput: &UserInputResponse{Answers: []UserInputAnswer{{
				ID:    "choice",
				Label: "Yes",
				Value: "Yes",
			}}},
		},
	}
	clientData, err := json.Marshal(client)
	if err != nil {
		t.Fatalf("marshal client: %v", err)
	}
	var gotClient ClientMessage
	if err := json.Unmarshal(clientData, &gotClient); err != nil {
		t.Fatalf("unmarshal client: %v", err)
	}
	if gotClient.Type != ClientMessageIntent || gotClient.Intent == nil || gotClient.Intent.Kind != IntentSubmitUserInput {
		t.Fatalf("unexpected client round trip: %+v", gotClient)
	}

	server := ServerMessage{
		Type:          ServerMessageReady,
		SessionID:     "s1",
		WorkspaceRoot: "/tmp/work",
	}
	serverData, err := json.Marshal(server)
	if err != nil {
		t.Fatalf("marshal server: %v", err)
	}
	var gotServer ServerMessage
	if err := json.Unmarshal(serverData, &gotServer); err != nil {
		t.Fatalf("unmarshal server: %v", err)
	}
	if gotServer.Type != ServerMessageReady || gotServer.SessionID != "s1" || gotServer.WorkspaceRoot != "/tmp/work" {
		t.Fatalf("unexpected server round trip: %+v", gotServer)
	}
}
