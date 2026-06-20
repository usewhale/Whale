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

type providerToolCaptureProvider struct {
	names []string
}

func (p *providerToolCaptureProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	p.names = p.names[:0]
	for _, tool := range tools {
		if tool != nil {
			p.names = append(p.names, tool.Name())
		}
	}
	return eventStream(endTurnEvent("done"))
}

type reasoningUpdatePlanProvider struct{}

func (p *reasoningUpdatePlanProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{
			Type:           EventReasoningDelta,
			ReasoningDelta: `try {"name":"update_plan","arguments":{"plan":[{"step":"Inspect","status":"in_progress"}]}}`,
		},
		endTurnEvent("done"),
	)
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
	if _, err := a.RunSession(context.Background(), "s-todo", "go"); err != nil {
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

func TestUpdatePlanHiddenFromProviderToolsInPlanMode(t *testing.T) {
	provider := &providerToolCaptureProvider{}
	a := NewAgentWithRegistry(
		provider,
		NewInMemoryStore(),
		NewToolRegistry([]Tool{
			regTestTool{name: "read_file"},
			regTestTool{name: "update_plan"},
		}),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStreamWithOptions(context.Background(), "s-plan-tools", "go", false)
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	if !stringSliceContains(provider.names, "read_file") {
		t.Fatalf("expected read_file to remain visible, got %v", provider.names)
	}
	if stringSliceContains(provider.names, "update_plan") {
		t.Fatalf("update_plan should not be provider-visible in plan mode, got %v", provider.names)
	}
}

func TestUpdatePlanVisibleFromProviderToolsOutsidePlanMode(t *testing.T) {
	provider := &providerToolCaptureProvider{}
	a := NewAgentWithRegistry(
		provider,
		NewInMemoryStore(),
		NewToolRegistry([]Tool{
			regTestTool{name: "read_file"},
			regTestTool{name: "update_plan"},
		}),
	)
	events, err := a.RunStreamWithOptions(context.Background(), "s-agent-tools", "go", false)
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	if !stringSliceContains(provider.names, "update_plan") {
		t.Fatalf("expected update_plan to remain visible outside plan mode, got %v", provider.names)
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
	var blocked string
	for ev := range events {
		if ev.Type == AgentEventTypePlanUpdate {
			sawPlanUpdate = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "plan_mode_blocked") {
			sawBlocked = true
			blocked = ev.Result.ModelText
		}
	}
	if sawPlanUpdate {
		t.Fatal("update_plan should not emit plan update in plan mode")
	}
	if !sawBlocked {
		t.Fatal("expected update_plan to be blocked in plan mode")
	}
	for _, want := range []string{
		"TODO/checklist",
		"not allowed in Plan mode",
		"emit_proposed_plan_block",
		"<proposed_plan>",
	} {
		if !strings.Contains(blocked, want) {
			t.Fatalf("blocked update_plan result missing %q:\n%s", want, blocked)
		}
	}
}

func TestPlanModeDoesNotScavengeHiddenUpdatePlanFromReasoning(t *testing.T) {
	a := NewAgentWithRegistry(
		&reasoningUpdatePlanProvider{},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{regTestTool{name: "update_plan"}}),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStreamWithOptions(context.Background(), "s-plan-scavenge-update-plan", "go", false)
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeToolCallScavenged:
			t.Fatalf("hidden update_plan should not be scavenged in plan mode: %+v", ev.Scavenged)
		case AgentEventTypePlanUpdate:
			t.Fatal("hidden update_plan should not emit plan update in plan mode")
		case AgentEventTypeToolResult:
			if ev.Result != nil && ev.Result.Name == "update_plan" {
				t.Fatalf("hidden update_plan should not be dispatched from reasoning: %+v", ev.Result)
			}
		}
	}
}

func TestParsePlanUpdateRejectsMultipleInProgress(t *testing.T) {
	_, err := parsePlanUpdate(`{"plan":[{"step":"A","status":"in_progress"},{"step":"B","status":"in_progress"}]}`)
	if err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("expected multiple in_progress error, got %v", err)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
