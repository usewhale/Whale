package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type cancelThenSummaryProvider struct {
	calls int
}

func (p *cancelThenSummaryProvider) StreamResponse(ctx context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		go func() {
			defer close(out)
			<-ctx.Done()
			out <- ProviderEvent{Type: EventError, Err: ctx.Err()}
		}()
		return out
	}
	return eventStream(endTurnEvent("forced summary"))
}

func TestRunStreamCancelCurrentTurn(t *testing.T) {
	store := NewInMemoryStore()
	prov := &cancelThenSummaryProvider{}
	a := NewAgent(prov, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	events, err := a.RunStream(ctx, "s-cancel", "hi")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	time.AfterFunc(10*time.Millisecond, cancel)

	seenCancelled := false
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeTurnCancelled:
			seenCancelled = true
		case AgentEventTypeForcedSummaryStarted, AgentEventTypeForcedSummaryDone, AgentEventTypeDone:
			t.Fatalf("unexpected event after cancel: %s", ev.Type)
		}
	}

	if !seenCancelled {
		t.Fatal("expected turn_cancelled event")
	}
	if got := prov.calls; got != 1 {
		t.Fatalf("expected cancel path not to trigger extra summary request, got provider calls=%d", got)
	}
	msgs, _ := store.List(context.Background(), "s-cancel")
	if len(msgs) == 0 {
		t.Fatal("expected persisted messages")
	}
	last := msgs[len(msgs)-1]
	if last.Role != RoleUser || !last.Hidden || last.FinishReason != FinishReasonCanceled || !strings.Contains(last.Text, "<turn_aborted>") {
		t.Fatalf("expected hidden interrupt marker, got: %+v", last)
	}
}

func TestRunCancelCurrentTurnReturnsContextCanceled(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&cancelThenSummaryProvider{}, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	_, err := a.RunSession(ctx, "s-cancel-run", "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

type blockingCancelTool struct{}

func (b blockingCancelTool) Name() string { return "blocking_cancel" }
func (b blockingCancelTool) Run(ctx context.Context, call ToolCall) (ToolResult, error) {
	<-ctx.Done()
	return ToolResult{}, ctx.Err()
}

func TestRunStreamCancelDuringToolSkipsRecovery(t *testing.T) {
	store := NewInMemoryStore()
	prov := &oneToolProvider{tool: "blocking_cancel", input: `{}`}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{blockingCancelTool{}}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
		WithRecoveryPolicy(DefaultRecoveryPolicy()),
	)
	ctx, cancel := context.WithCancel(context.Background())
	events, err := a.RunStream(ctx, "s-tool-cancel", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	time.AfterFunc(10*time.Millisecond, cancel)

	var seenCancelled bool
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeTurnCancelled:
			seenCancelled = true
		case AgentEventTypeToolRecoveryScheduled, AgentEventTypeToolRecoveryAttempt, AgentEventTypeToolRecoveryExhausted, AgentEventTypeReplanRequiredSet:
			t.Fatalf("unexpected recovery event after cancel: %s", ev.Type)
		case AgentEventTypeToolResult:
			if ev.Result != nil && strings.Contains(ev.Result.Content, "request_replan") {
				t.Fatalf("unexpected replan result after cancel: %s", ev.Result.Content)
			}
		}
	}
	if !seenCancelled {
		t.Fatal("expected turn_cancelled event")
	}
	msgs, _ := store.List(context.Background(), "s-tool-cancel")
	last := msgs[len(msgs)-1]
	if last.Role != RoleUser || !last.Hidden || last.FinishReason != FinishReasonCanceled || !strings.Contains(last.Text, "<turn_aborted>") {
		t.Fatalf("expected hidden interrupt marker, got: %+v", last)
	}
}

type repairAndStormProvider struct {
	calls int
}

func (p *repairAndStormProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls == 1 {
		return eventStream(toolUseEvent(
			toolCall("tc-1", "echo_json", `{"x":1`),
			toolCall("tc-2", "echo_json", `{"x":1`),
			toolCall("tc-3", "echo_json", `{"x":1`),
			toolCall("tc-4", "echo_json", `{"x":1`),
		))
	}
	return eventStream(endTurnEvent("ok"))
}

type echoJSONTool struct{}

func (e echoJSONTool) Name() string { return "echo_json" }
func (e echoJSONTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(call.Input), &v); err != nil {
		return ToolResult{}, err
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}

func TestRunStreamEmitsRepairAndBlockedEvents(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&repairAndStormProvider{}, store, []Tool{echoJSONTool{}})

	events, err := a.RunStream(context.Background(), "s-repair", "hi")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}

	var repaired int
	var blocked int
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeToolArgsRepaired:
			repaired++
		case AgentEventTypeToolCallBlocked:
			blocked++
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if repaired != 4 {
		t.Fatalf("expected 4 repaired events, got %d", repaired)
	}
	if blocked != 1 {
		t.Fatalf("expected 1 blocked event, got %d", blocked)
	}
}
