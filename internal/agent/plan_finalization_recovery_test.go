package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/session"
)

// prefillPlanProvider reproduces the observed deepseek failure: the first
// plan-mode turn ends with an analysis preamble (and reasoning) but no
// <proposed_plan> block; once the turn loop re-runs with the plan tag prefilled,
// StreamResponseWithPrefix returns the real block.
type prefillPlanProvider struct {
	calls       int
	prefixCalls int
	prefix      string
}

func (p *prefillPlanProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      "Here's the situation. I'll present the plan.",
			Reasoning:    "The plan: tag, push, gh release create.",
		},
	})
}

func (p *prefillPlanProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, prefix string, _ []string) <-chan ProviderEvent {
	p.prefixCalls++
	p.prefix = prefix
	// The provider is responsible for emitting the prefix; mimic the real client.
	return eventStream(endTurnEvent(prefix + "## Release v0.1.51\n- tag and release\n</proposed_plan>"))
}

func TestPlanFinalizationRecoversViaPrefill(t *testing.T) {
	store := NewInMemoryStore()
	prov := &prefillPlanProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-prefill", "release the next version")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var planContent string
	var sawPlanCompleted bool
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			sawPlanCompleted = true
			planContent = ev.Content
		}
	}
	if prov.prefixCalls != 1 {
		t.Fatalf("expected exactly one prefill (StreamResponseWithPrefix) call, got %d", prov.prefixCalls)
	}
	if prov.prefix != planFinalizationPrefix {
		t.Fatalf("expected prefill with %q, got %q", planFinalizationPrefix, prov.prefix)
	}
	if !sawPlanCompleted {
		t.Fatal("expected a plan_completed event after prefill recovery")
	}
	if !strings.Contains(planContent, "Release v0.1.51") {
		t.Fatalf("recovered plan missing expected content: %q", planContent)
	}
}

// emptyThenPrefillProvider reproduces the reasoning-only / empty-completion
// terminal error: recovery must escalate to prefill rather than aborting.
type emptyThenPrefillProvider struct {
	calls       int
	prefixCalls int
}

func (p *emptyThenPrefillProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(ProviderEvent{Type: EventError, Err: llm.ErrEmptyCompletion})
}

func (p *emptyThenPrefillProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, prefix string, _ []string) <-chan ProviderEvent {
	p.prefixCalls++
	return eventStream(endTurnEvent(prefix + "## Release plan\n- ship it\n</proposed_plan>"))
}

func TestPlanFinalizationRecoversFromEmptyCompletion(t *testing.T) {
	store := NewInMemoryStore()
	prov := &emptyThenPrefillProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-empty", "release")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawError, sawPlanCompleted bool
	var planContent string
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			sawError = true
		}
		if ev.Type == AgentEventTypePlanCompleted {
			sawPlanCompleted = true
			planContent = ev.Content
		}
	}
	if sawError {
		t.Fatal("empty completion in plan mode should be recovered, not surfaced as an error")
	}
	if prov.prefixCalls != 1 {
		t.Fatalf("expected one prefill call after empty completion, got %d", prov.prefixCalls)
	}
	if !sawPlanCompleted || !strings.Contains(planContent, "Release plan") {
		t.Fatalf("expected recovered plan, completed=%v content=%q", sawPlanCompleted, planContent)
	}
	// The empty assistant turn that the provider failed on must not linger as a
	// visible failed turn in the store (it would pollute replay/resume).
	msgs, _ := store.List(context.Background(), "s-plan-empty")
	for _, m := range msgs {
		if m.Role == core.RoleAssistant && strings.TrimSpace(m.Text) == "" && len(m.Parts) == 0 && !m.Hidden {
			t.Fatalf("empty recovered assistant turn should be hidden, got visible: %+v", m)
		}
	}
}

// inlineTagMentionProvider returns visible text that merely *mentions* the tag
// (not a real block on its own line), so no plan part is produced. Recovery must
// still fire — the raw mention must not be mistaken for a real plan.
type inlineTagMentionProvider struct {
	calls       int
	prefixCalls int
}

func (p *inlineTagMentionProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(endTurnEvent("I will now output the final plan in a <proposed_plan> block."))
}

func (p *inlineTagMentionProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, prefix string, _ []string) <-chan ProviderEvent {
	p.prefixCalls++
	return eventStream(endTurnEvent(prefix + "## Release plan\n- ship it\n</proposed_plan>"))
}

func TestPlanFinalizationFiresOnInlineTagMention(t *testing.T) {
	store := NewInMemoryStore()
	prov := &inlineTagMentionProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-inline", "release")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawPlanCompleted bool
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			sawPlanCompleted = true
		}
	}
	if prov.prefixCalls != 1 {
		t.Fatalf("expected recovery to fire despite inline tag mention, prefixCalls=%d", prov.prefixCalls)
	}
	if !sawPlanCompleted {
		t.Fatal("expected a plan_completed event after recovery")
	}
}

// stubbornProvider never emits a plan and is not prefix-capable, to prove the
// recovery is bounded: one original turn + one (tool-suppressed) recovery turn.
type stubbornProvider struct{ calls int }

func (p *stubbornProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(endTurnEvent("analysis only, no plan block"))
}

func TestPlanFinalizationRecoveryIsBounded(t *testing.T) {
	store := NewInMemoryStore()
	prov := &stubbornProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-bounded", "release")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawDone bool
	for ev := range events {
		if ev.Type == AgentEventTypeDone {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("expected the turn to terminate with a done event")
	}
	// Bounded: original turn + exactly one recovery turn.
	if prov.calls != 2 {
		t.Fatalf("expected exactly 2 provider calls (bounded), got %d", prov.calls)
	}
}

type taggedFirstProvider struct{ calls int }

func (p *taggedFirstProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(endTurnEvent("<proposed_plan>\nalready a plan\n</proposed_plan>"))
}

func TestPlanFinalizationSkippedWhenPlanPresent(t *testing.T) {
	store := NewInMemoryStore()
	prov := &taggedFirstProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-ok", "release")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	if prov.calls != 1 {
		t.Fatalf("expected a single provider call when the plan is already tagged, got %d", prov.calls)
	}
}
