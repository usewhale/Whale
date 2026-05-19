package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/session"
)

type todoProvider struct{ calls int }

func (p *todoProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonToolUse, ToolCalls: []ToolCall{{ID: "t1", Name: "todo_add", Input: `{"text":"ship tools","priority":3}`}}}}
	} else if p.calls == 2 {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonToolUse, ToolCalls: []ToolCall{{ID: "t2", Name: "todo_list", Input: `{}`}}}}
	} else {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

type updatePlanProvider struct{ calls int }

func (p *updatePlanProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonToolUse, ToolCalls: []ToolCall{{ID: "p1", Name: "update_plan", Input: `{"explanation":"starting","plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"},{"step":"Test","status":"pending"}]}`}}}}
	} else {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

func TestTodoToolsPersistInSession(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	a := NewAgentWithRegistry(
		&todoProvider{},
		NewInMemoryStore(),
		NewToolRegistry(nil),
		WithSessionsDir(sessionsDir),
	)
	if _, err := a.Run(context.Background(), "s-todo", "go"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	st, err := session.LoadTodoState(sessionsDir, "s-todo")
	if err != nil {
		t.Fatalf("load todo: %v", err)
	}
	if len(st.Items) != 1 || !strings.Contains(st.Items[0].Text, "ship tools") {
		t.Fatalf("unexpected todo state: %+v", st)
	}
}

func TestUpdatePlanEmitsPlanUpdate(t *testing.T) {
	a := NewAgentWithRegistry(
		&updatePlanProvider{},
		NewInMemoryStore(),
		NewToolRegistry(nil),
	)
	events, err := a.RunStreamWithOptions(context.Background(), "s-plan-update", "go", false)
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var got *PlanUpdateInfo
	for ev := range events {
		if ev.Type == AgentEventTypePlanUpdate {
			got = ev.PlanUpdate
		}
	}
	if got == nil || got.Explanation != "starting" || len(got.Plan) != 3 {
		t.Fatalf("expected plan update event, got %+v", got)
	}
	if got.Plan[1].Status != "in_progress" {
		t.Fatalf("expected in_progress step, got %+v", got.Plan[1])
	}
}

func TestUpdatePlanBlockedInPlanMode(t *testing.T) {
	a := NewAgentWithRegistry(
		&updatePlanProvider{},
		NewInMemoryStore(),
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStreamWithOptions(context.Background(), "s-plan-mode-update", "go", false)
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawPlanUpdate bool
	var sawBlocked bool
	for ev := range events {
		if ev.Type == AgentEventTypePlanUpdate {
			sawPlanUpdate = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.Content, "plan_mode_blocked") {
			sawBlocked = true
		}
	}
	if sawPlanUpdate {
		t.Fatal("update_plan should not emit plan update in plan mode")
	}
	if !sawBlocked {
		t.Fatal("expected update_plan to be blocked in plan mode")
	}
}

func TestParsePlanUpdateRejectsMultipleInProgress(t *testing.T) {
	_, err := parsePlanUpdate(`{"plan":[{"step":"A","status":"in_progress"},{"step":"B","status":"in_progress"}]}`)
	if err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("expected multiple in_progress error, got %v", err)
	}
}
