package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/session"
)

// missingPlanPrefixCapableProvider reproduces the important DeepSeek shape:
// the model has already used tools earlier in the turn/history, then ends Plan
// mode with assistant text but no <proposed_plan> block. A missing plan is
// nudged (a normal re-prompt) to emit the block — but never via provider
// prefix-completion, the prefill repair removed in #307. This provider never
// emits a block, so after the nudges are exhausted the text is surfaced as-is.
type missingPlanPrefixCapableProvider struct {
	calls       int
	prefixCalls int
}

func (p *missingPlanPrefixCapableProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls == 1 {
		return eventStream(toolUseEvent(toolCall("call-read", "read_file", `{"file_path":"release.md"}`)))
	}
	return eventStream(ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      "Here's the situation. I'll present the plan.",
			Reasoning:    "The plan: tag, push, gh release create.",
		},
	})
}

func (p *missingPlanPrefixCapableProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, _ string, _ []string) <-chan ProviderEvent {
	p.prefixCalls++
	return eventStream(endTurnEvent("<proposed_plan>\nshould not be requested\n</proposed_plan>"))
}

func TestPlanFinalizationDoesNotRecoverViaProviderPrefix(t *testing.T) {
	store := NewInMemoryStore()
	prov := &missingPlanPrefixCapableProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{readOnlyViewTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-no-prefix", "release the next version")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawDone, sawPlanCompleted bool
	var done *core.Message
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			sawPlanCompleted = true
		}
		if ev.Type == AgentEventTypeDone {
			sawDone = true
			done = ev.Message
		}
	}
	// tool-use turn, then the planless turn plus maxPlanProposalNudges retries.
	if want := 2 + maxPlanProposalNudges; prov.calls != want {
		t.Fatalf("expected %d provider calls (tool turn + planless turn + %d nudges), got %d", want, maxPlanProposalNudges, prov.calls)
	}
	if prov.prefixCalls != 0 {
		t.Fatalf("missing plan must be nudged, never prefix-completed, got %d prefix calls", prov.prefixCalls)
	}
	if !sawDone {
		t.Fatal("expected the original assistant turn to complete")
	}
	if sawPlanCompleted {
		t.Fatal("did not expect a structured plan without a proposed_plan block")
	}
	if done == nil || !strings.Contains(done.Text, "present the plan") {
		t.Fatalf("expected original assistant text to remain visible, got %+v", done)
	}
}

type emptyPlanProvider struct {
	calls       int
	prefixCalls int
}

func (p *emptyPlanProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(ProviderEvent{Type: EventError, Err: llm.ErrEmptyCompletion})
}

func (p *emptyPlanProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, _ string, _ []string) <-chan ProviderEvent {
	p.prefixCalls++
	return eventStream(endTurnEvent("<proposed_plan>\nshould not be requested\n</proposed_plan>"))
}

func TestPlanFinalizationEmptyCompletionDoesNotRecoverViaProviderPrefix(t *testing.T) {
	store := NewInMemoryStore()
	prov := &emptyPlanProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-empty-no-prefix", "release")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var gotErr error
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			gotErr = ev.Err
		}
		if ev.Type == AgentEventTypePlanCompleted {
			t.Fatal("did not expect a structured plan after an empty provider error")
		}
	}
	if !errors.Is(gotErr, llm.ErrEmptyCompletion) {
		t.Fatalf("expected empty completion error event, got %v", gotErr)
	}
	if prov.prefixCalls != 0 {
		t.Fatalf("empty completion must not trigger provider prefix completion, got %d calls", prov.prefixCalls)
	}
}

type inlineTagMentionProvider struct {
	calls       int
	prefixCalls int
}

func (p *inlineTagMentionProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	return eventStream(endTurnEvent("I will now output the final plan in a <proposed_plan> block."))
}

func (p *inlineTagMentionProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, _ string, _ []string) <-chan ProviderEvent {
	p.prefixCalls++
	return eventStream(endTurnEvent("<proposed_plan>\nshould not be requested\n</proposed_plan>"))
}

func TestPlanFinalizationInlineTagMentionDoesNotTriggerPrefix(t *testing.T) {
	store := NewInMemoryStore()
	prov := &inlineTagMentionProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{viewLikeTool{}}), WithSessionMode(session.ModePlan))

	events, err := a.RunStream(context.Background(), "s-plan-inline-no-prefix", "release")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawDone, sawPlanCompleted bool
	for ev := range events {
		if ev.Type == AgentEventTypeDone {
			sawDone = true
		}
		if ev.Type == AgentEventTypePlanCompleted {
			sawPlanCompleted = true
		}
	}
	if prov.prefixCalls != 0 {
		t.Fatalf("inline tag mention must not trigger provider prefix completion, got %d calls", prov.prefixCalls)
	}
	if !sawDone {
		t.Fatal("expected original assistant turn to complete")
	}
	if sawPlanCompleted {
		t.Fatal("did not expect quoted/inline tag mention to become a structured plan")
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
	var sawPlanCompleted bool
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			sawPlanCompleted = true
		}
	}
	if prov.calls != 1 {
		t.Fatalf("expected a single provider call when the plan is already tagged, got %d", prov.calls)
	}
	if !sawPlanCompleted {
		t.Fatal("expected structured plan when proposed_plan block is present")
	}
}
