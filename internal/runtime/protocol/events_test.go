package protocol

import (
	"encoding/json"
	"testing"
)

func TestEventJSONRoundTrip(t *testing.T) {
	events := []Event{
		{Kind: EventAssistantDelta, Text: "hello"},
		{
			Kind:       EventToolResult,
			ToolCallID: "tc-1",
			ToolName:   "shell_run",
			Text:       "ok",
			Metadata:   map[string]any{"exit_code": float64(0)},
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
