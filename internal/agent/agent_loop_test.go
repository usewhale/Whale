package agent

import (
	"context"
	"testing"
)

type mockProvider struct {
	calls int
}

func (m *mockProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	m.calls++
	if m.calls == 1 {
		return eventStream(toolUseEvent(toolCall("tc-1", "echo", "hi")))
	}
	return eventStream(endTurnEvent("done"))
}

func TestAgentLoopWithToolRoundTrip(t *testing.T) {
	store := NewInMemoryStore()
	prov := &mockProvider{}
	a := NewAgent(prov, store, []Tool{echoTool{}})

	msg, err := a.Run(context.Background(), "s1", "hello")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.FinishReason != FinishReasonEndTurn {
		t.Fatalf("unexpected finish: %s", msg.FinishReason)
	}

	all, _ := store.List(context.Background(), "s1")
	if len(all) != 4 {
		t.Fatalf("expected 4 messages (user,assistant,tool,assistant), got %d", len(all))
	}
}

// deltasNoTerminalProvider emits content deltas then closes the channel
// without an EventComplete/EventError. This models the SSE-EOF-before-[DONE]
// case noted in issue #22 review.
type deltasNoTerminalProvider struct{}

func (p *deltasNoTerminalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 4)
	out <- ProviderEvent{Type: EventContentDelta, Content: "partial-"}
	out <- ProviderEvent{Type: EventContentDelta, Content: "answer"}
	close(out)
	return out
}

func TestStreamFallthroughPersistsPartialAssistant(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&deltasNoTerminalProvider{}, store, nil)

	events, err := a.RunStream(context.Background(), "s-partial", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}

	all, err := store.List(context.Background(), "s-partial")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected user+assistant, got %d", len(all))
	}
	asst := all[len(all)-1]
	if asst.Role != RoleAssistant {
		t.Fatalf("expected last to be assistant, got %s", asst.Role)
	}
	if asst.Text != "partial-answer" {
		t.Fatalf("expected persisted partial text %q, got %q", "partial-answer", asst.Text)
	}
}

func TestRunStreamWithInjectedInputStoresVisibleAndHiddenMessages(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&mockProviderWithDeltas{}, store, nil)

	events, err := a.RunStreamWithInjectedInput(context.Background(), "s-skill", "$demo do it", "<skill>demo</skill>")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	all, err := store.List(context.Background(), "s-skill")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected at least two user messages, got %d", len(all))
	}
	if all[0].Role != RoleUser || all[0].Hidden || all[0].Text != "$demo do it" {
		t.Fatalf("unexpected visible message: %+v", all[0])
	}
	if all[1].Role != RoleUser || !all[1].Hidden || all[1].Text != "<skill>demo</skill>" {
		t.Fatalf("unexpected hidden message: %+v", all[1])
	}
}

type mockProviderWithDeltas struct{}

func (m *mockProviderWithDeltas) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 16)
	out <- ProviderEvent{Type: EventReasoningDelta, ReasoningDelta: "think-1"}
	out <- ProviderEvent{Type: EventContentDelta, Content: "hello"}
	out <- ProviderEvent{
		Type: EventToolArgsDelta,
		ToolArgsDelta: &ToolArgsDelta{
			ToolCallIndex: 0,
			ToolName:      "echo",
			ArgsDelta:     "{",
			ArgsChars:     1,
			ReadyCount:    0,
		},
	}
	out <- ProviderEvent{
		Type: EventToolArgsDelta,
		ToolArgsDelta: &ToolArgsDelta{
			ToolCallIndex: 0,
			ToolName:      "echo",
			ArgsDelta:     "\"x\":1}",
			ArgsChars:     7,
			ReadyCount:    1,
		},
	}
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "hello", Reasoning: "think-1"}}
	close(out)
	return out
}

func TestRunStreamEmitsReasoningAndToolArgsDeltas(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&mockProviderWithDeltas{}, store, nil)

	events, err := a.RunStream(context.Background(), "s-delta", "hi")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}

	var seenReasoning bool
	var seenToolArgs bool
	var gotDone *Message
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeReasoningDelta:
			if ev.ReasoningDelta == "think-1" {
				seenReasoning = true
			}
		case AgentEventTypeToolArgsDelta:
			if ev.ToolArgs != nil && ev.ToolArgs.ToolName == "echo" && ev.ToolArgs.ReadyCount == 1 {
				seenToolArgs = true
			}
		case AgentEventTypeDone:
			gotDone = ev.Message
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if !seenReasoning {
		t.Fatal("expected reasoning delta event")
	}
	if !seenToolArgs {
		t.Fatal("expected tool args delta event")
	}
	if gotDone == nil || gotDone.Text != "hello" || gotDone.Reasoning != "think-1" {
		t.Fatalf("unexpected done message: %+v", gotDone)
	}
}

type historyCaptureProvider struct {
	histories [][]Message
}

func (p *historyCaptureProvider) StreamResponse(_ context.Context, history []Message, _ []Tool) <-chan ProviderEvent {
	copied := append([]Message(nil), history...)
	p.histories = append(p.histories, copied)

	return eventStream(endTurnEvent("ok"))
}

func TestResumeSessionHistoryAfterSwitchingSessions(t *testing.T) {
	store := NewInMemoryStore()
	prov := &historyCaptureProvider{}
	a := NewAgent(prov, store, nil)

	if _, err := a.Run(context.Background(), "s1", "first-s1"); err != nil {
		t.Fatalf("run s1 first turn failed: %v", err)
	}
	if _, err := a.Run(context.Background(), "s2", "first-s2"); err != nil {
		t.Fatalf("run s2 first turn failed: %v", err)
	}
	if _, err := a.Run(context.Background(), "s1", "second-s1"); err != nil {
		t.Fatalf("run s1 second turn failed: %v", err)
	}

	if len(prov.histories) != 3 {
		t.Fatalf("expected 3 provider calls, got %d", len(prov.histories))
	}

	third := prov.histories[2]
	if len(third) < 3 {
		t.Fatalf("expected resumed s1 history len>=3, got %d", len(third))
	}
	userAssistantUser := make([]Message, 0, 3)
	for _, m := range third {
		if m.Role == RoleUser || m.Role == RoleAssistant {
			userAssistantUser = append(userAssistantUser, m)
		}
	}
	if len(userAssistantUser) < 3 {
		t.Fatalf("expected at least 3 user/assistant messages, got %d", len(userAssistantUser))
	}
	if userAssistantUser[0].Role != RoleUser || userAssistantUser[0].Text != "first-s1" {
		t.Fatalf("unexpected third history[0]: %+v", userAssistantUser[0])
	}
	if userAssistantUser[1].Role != RoleAssistant || userAssistantUser[1].Text != "ok" {
		t.Fatalf("unexpected third history[1]: %+v", userAssistantUser[1])
	}
	if userAssistantUser[2].Role != RoleUser || userAssistantUser[2].Text != "second-s1" {
		t.Fatalf("unexpected third history[2]: %+v", userAssistantUser[2])
	}
}
