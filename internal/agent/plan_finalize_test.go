package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/usewhale/whale/internal/session"
)

// call 1 ends with only a "Here's the plan:" preamble (no <proposed_plan> block);
// the nudged retry returns the real block.
type planPreambleThenBlockProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *planPreambleThenBlockProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return eventStream(endTurnEvent("I now have a clear picture. Here's the plan:"))
	}
	return eventStream(endTurnEvent("Here's the plan:\n\n<proposed_plan>\n- step one\n- step two\n</proposed_plan>"))
}

func TestPlanModeNudgesWhenProposalMissingThenRecovers(t *testing.T) {
	store := NewInMemoryStore()
	provider := &planPreambleThenBlockProvider{}
	a := NewAgent(provider, store, []Tool{echoTool{}})
	WithSessionMode(session.ModePlan)(a)

	events, err := a.RunStream(context.Background(), "s-plan-nudge", "make a plan")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var planCompleted bool
	var planContent string
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		case AgentEventTypePlanCompleted:
			planCompleted = true
			planContent = ev.Content
		}
	}
	if !planCompleted {
		t.Fatal("expected a proposed plan to be emitted after the nudge")
	}
	if !strings.Contains(planContent, "step one") {
		t.Fatalf("unexpected plan content: %q", planContent)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls: want 2 (preamble + nudged retry), got %d", provider.calls)
	}
	msgs, err := store.List(context.Background(), "s-plan-nudge")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, m := range msgs {
		if m.Hidden && strings.Contains(m.Text, "plan_not_finalized") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the plan-not-finalized nudge to be persisted as a hidden marker")
	}
}

// always ends with a bare preamble, never a block.
type planPreambleAlwaysProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *planPreambleAlwaysProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return eventStream(endTurnEvent("Here's the plan:"))
}

func (p *planPreambleAlwaysProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestPlanModeNudgeGivesUpAfterCap(t *testing.T) {
	store := NewInMemoryStore()
	provider := &planPreambleAlwaysProvider{}
	a := NewAgent(provider, store, nil)
	WithSessionMode(session.ModePlan)(a)

	events, err := a.RunStream(context.Background(), "s-plan-giveup", "make a plan")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var done, planCompleted bool
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		case AgentEventTypePlanCompleted:
			planCompleted = true
		case AgentEventTypeDone:
			done = true
		}
	}
	if planCompleted {
		t.Fatal("no plan was ever proposed; PlanCompleted must not fire")
	}
	if !done {
		t.Fatal("expected the turn to finish (Done) after exhausting nudges")
	}
	if want := 1 + maxPlanProposalNudges; provider.callCount() != want {
		t.Fatalf("provider calls: want %d (initial + %d nudges), got %d", want, maxPlanProposalNudges, provider.callCount())
	}
}

// A bare-preamble end_turn outside Plan mode must NOT be nudged — the nudge is
// scoped to Plan mode only.
func TestAgentModeDoesNotNudgeForMissingPlan(t *testing.T) {
	store := NewInMemoryStore()
	provider := &planPreambleAlwaysProvider{}
	a := NewAgent(provider, store, nil) // default agent mode

	events, err := a.RunStream(context.Background(), "s-agent-no-nudge", "do something")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("agent mode must not nudge for a missing plan, got %d calls", provider.callCount())
	}
}
