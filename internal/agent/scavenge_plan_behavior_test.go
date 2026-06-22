package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

type scavengingProvider struct{}

func (p *scavengingProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{
			Type:           EventReasoningDelta,
			ReasoningDelta: `need read {"name":"read_file","arguments":{"file_path":"x.txt"}}`,
		},
		endTurnEvent("done"),
	)
}

func TestScavengeFromReasoningWhenNoDeclaredToolCalls(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&scavengingProvider{},
		store,
		NewToolRegistry([]Tool{viewLikeTool{}}),
	)

	events, err := a.RunStream(context.Background(), "s-scavenge", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawScavenge bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolCallScavenged && ev.Scavenged != nil && ev.Scavenged.Count == 1 {
			sawScavenge = true
		}
	}
	if !sawScavenge {
		t.Fatal("expected tool_call_scavenged event")
	}
	msgs, _ := store.List(context.Background(), "s-scavenge")
	if len(msgs) < 3 {
		t.Fatalf("expected tool message generated, got %d messages", len(msgs))
	}
	if msgs[2].Role != RoleTool || len(msgs[2].ToolResults) == 0 {
		t.Fatalf("expected tool results from scavenged call, got: %+v", msgs[2])
	}
}

func TestPlanModeBlocksWriteTools(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalProvider{}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStream(context.Background(), "s-plan-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawModeBlocked bool
	var sawPlanBlockedResult bool
	var blockedContent string
	for ev := range events {
		if ev.Type == AgentEventTypeToolModeBlocked && ev.ToolBlocked != nil && ev.ToolBlocked.ReasonCode == "plan_mode_blocked" {
			sawModeBlocked = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "plan_mode_blocked") {
			sawPlanBlockedResult = true
			blockedContent = ev.Result.ModelText
		}
	}
	if !sawModeBlocked {
		t.Fatal("expected tool mode blocked event")
	}
	if !sawPlanBlockedResult {
		t.Fatal("expected plan mode blocked tool result")
	}
	for _, want := range []string{
		"retryable",
		"false",
		"do_not_retry_same_call",
		"write the final plan as your reply",
	} {
		if !strings.Contains(blockedContent, want) {
			t.Fatalf("plan mode blocked result missing %q:\n%s", want, blockedContent)
		}
	}
	if strings.Contains(blockedContent, "<proposed_plan>") {
		t.Fatalf("plan mode blocked guidance must not reference the sentinel:\n%s", blockedContent)
	}
	for _, forbidden := range []string{"/agent", "Shift+Tab", "suggested_modes", "Only the user or UI can switch modes"} {
		if strings.Contains(blockedContent, forbidden) {
			t.Fatalf("plan mode blocked result leaked noisy guidance %q:\n%s", forbidden, blockedContent)
		}
	}
}

func TestPlanModeBlockedDetailsAreConciseAndNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		call       core.ToolCall
		wantAction string
	}{
		{name: "shell", call: core.ToolCall{Name: "shell_run"}, wantAction: "do_not_retry_same_command"},
		{name: "write", call: core.ToolCall{Name: "write_file"}, wantAction: "do_not_retry_same_call"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, summary, data := modeBlockedDetailsForCall(session.ModePlan, tc.call)
			if code != "plan_mode_blocked" {
				t.Fatalf("expected plan_mode_blocked, got %q", code)
			}
			if !strings.Contains(summary, "do not retry") {
				t.Fatalf("summary missing do-not-retry guidance:\n%s", summary)
			}
			if tc.call.Name == "shell_run" && !strings.Contains(summary, "same shell operation with another shell command") {
				t.Fatalf("shell summary missing same-shell-operation retry guidance:\n%s", summary)
			}
			if !strings.Contains(summary, "write the final plan as your reply") {
				t.Fatalf("summary missing plan-as-reply guidance:\n%s", summary)
			}
			if strings.Contains(summary, "<proposed_plan>") {
				t.Fatalf("summary must not reference the sentinel:\n%s", summary)
			}
			for _, forbidden := range []string{"/agent", "Shift+Tab", "Only the user or UI can switch modes"} {
				if strings.Contains(summary, forbidden) {
					t.Fatalf("summary leaked noisy guidance %q:\n%s", forbidden, summary)
				}
			}
			if got := data["current_mode"]; got != "plan" {
				t.Fatalf("expected current_mode plan, got %#v", got)
			}
			if got := data["tool"]; got != tc.call.Name {
				t.Fatalf("expected tool %q, got %#v", tc.call.Name, got)
			}
			if got := data["action"]; got != tc.wantAction {
				t.Fatalf("expected action %q, got %#v", tc.wantAction, got)
			}
			if got := data["retryable"]; got != false {
				t.Fatalf("expected retryable false, got %#v", got)
			}
			if _, ok := data["suggested_modes"]; ok {
				t.Fatalf("plan mode blocked data should not suggest mode commands: %+v", data)
			}
		})
	}
}
