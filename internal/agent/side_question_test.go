package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

type sideQuestionCaptureProvider struct {
	history []Message
	tools   []Tool
	events  []ProviderEvent
}

func (p *sideQuestionCaptureProvider) StreamResponse(_ context.Context, history []Message, tools []Tool) <-chan ProviderEvent {
	p.history = append([]Message(nil), history...)
	p.tools = append([]Tool(nil), tools...)
	return eventStream(p.events...)
}

func TestRunSideQuestionUsesContextWithoutToolsOrStoreWrites(t *testing.T) {
	st := store.NewInMemoryStore()
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleUser, Text: "main question"})
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleAssistant, Text: "finished", FinishReason: FinishReasonEndTurn})
	provider := &sideQuestionCaptureProvider{events: []ProviderEvent{
		{Type: EventContentDelta, Content: "partial"},
		endTurnEvent("final answer"),
	}}
	ag := NewAgent(provider, st, []Tool{echoTool{}})

	events, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err != nil {
		t.Fatalf("RunSideQuestion: %v", err)
	}
	var done string
	for ev := range events {
		if ev.Type == SideQuestionEventDone {
			done = ev.Content
		}
	}
	if done != "final answer" {
		t.Fatalf("done = %q", done)
	}
	if len(provider.tools) != 0 {
		t.Fatalf("expected no tools, got %d", len(provider.tools))
	}
	if len(provider.history) == 0 || !strings.Contains(provider.history[len(provider.history)-1].Text, "This is a side question") || !strings.Contains(provider.history[len(provider.history)-1].Text, "side?") {
		t.Fatalf("missing side-question prompt in history: %+v", provider.history)
	}
	msgs, _ := st.List(context.Background(), "s-btw")
	if len(msgs) != 2 {
		t.Fatalf("side question should not write store messages, got %d", len(msgs))
	}
}

func TestRunSideQuestionStripsInProgressAssistant(t *testing.T) {
	st := store.NewInMemoryStore()
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleUser, Text: "main question"})
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleAssistant, Text: "streaming partial"})
	provider := &sideQuestionCaptureProvider{events: []ProviderEvent{endTurnEvent("ok")}}
	ag := NewAgent(provider, st, nil)
	ag.active.Store("s-btw", struct{}{})

	events, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err != nil {
		t.Fatalf("RunSideQuestion: %v", err)
	}
	for range events {
	}
	for _, msg := range provider.history {
		if msg.Role == RoleAssistant && msg.Text == "streaming partial" {
			t.Fatal("expected in-progress assistant message to be stripped")
		}
	}
}

func TestRunSideQuestionKeepsFinishedAssistantWithoutFinishReason(t *testing.T) {
	st := store.NewInMemoryStore()
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleUser, Text: "main question"})
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleAssistant, Text: "legacy answer"})
	provider := &sideQuestionCaptureProvider{events: []ProviderEvent{endTurnEvent("ok")}}
	ag := NewAgent(provider, st, nil)

	events, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err != nil {
		t.Fatalf("RunSideQuestion: %v", err)
	}
	for range events {
	}
	found := false
	for _, msg := range provider.history {
		if msg.Role == RoleAssistant && msg.Text == "legacy answer" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected resumed-session assistant message to be preserved")
	}
}

func TestRunSideQuestionStripsDanglingToolUseAssistant(t *testing.T) {
	st := store.NewInMemoryStore()
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleUser, Text: "main question"})
	_, _ = st.Create(context.Background(), Message{SessionID: "s-btw", Role: RoleAssistant, Text: "calling tool", FinishReason: FinishReasonToolUse})
	provider := &sideQuestionCaptureProvider{events: []ProviderEvent{endTurnEvent("ok")}}
	ag := NewAgent(provider, st, nil)

	events, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err != nil {
		t.Fatalf("RunSideQuestion: %v", err)
	}
	for range events {
	}
	for _, msg := range provider.history {
		if msg.Role == RoleAssistant && msg.Text == "calling tool" {
			t.Fatal("expected dangling tool-use assistant message to be stripped")
		}
	}
}

func TestRunSideQuestionToolUseWithoutTextReturnsWarning(t *testing.T) {
	st := store.NewInMemoryStore()
	provider := &sideQuestionCaptureProvider{events: []ProviderEvent{toolUseEvent(toolCall("tc", "read_file", "{}"))}}
	ag := NewAgent(provider, st, nil)

	events, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err != nil {
		t.Fatalf("RunSideQuestion: %v", err)
	}
	var done string
	for ev := range events {
		if ev.Type == SideQuestionEventDone {
			done = ev.Content
		}
	}
	if !strings.Contains(done, "tried to call a tool") {
		t.Fatalf("expected tool warning, got %q", done)
	}
}

func TestRunSideQuestionRecordsUsageCost(t *testing.T) {
	st := store.NewInMemoryStore()
	usagePath := filepath.Join(t.TempDir(), "usage.jsonl")
	provider := &sideQuestionCaptureProvider{events: []ProviderEvent{{
		Type: EventComplete,
		Response: &ProviderResponse{
			Content:      "ok",
			Usage:        Usage{PromptTokens: 100, CompletionTokens: 50, PromptCacheHitTokens: 20, PromptCacheMissTokens: 80},
			Model:        "deepseek-v4-flash",
			FinishReason: FinishReasonEndTurn,
		},
	}}}
	ag := NewAgentWithRegistry(provider, st, nil, WithUsageLogPath(usagePath))

	events, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err != nil {
		t.Fatalf("RunSideQuestion: %v", err)
	}
	for range events {
	}
	if _, err := os.Stat(usagePath); err != nil {
		t.Fatalf("usage log missing: %v", err)
	}
}

func TestRunSideQuestionBlocksWhenBudgetExceeded(t *testing.T) {
	st := store.NewInMemoryStore()
	sessionsDir := t.TempDir()
	if err := session.SaveSessionMeta(sessionsDir, "s-btw", session.SessionMeta{TotalCostUSD: 1.2}); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	ag := NewAgentWithRegistry(&sideQuestionCaptureProvider{}, st, nil,
		WithSessionsDir(sessionsDir),
		WithBudgetWarningUSD(1.0),
	)

	_, err := ag.RunSideQuestion(context.Background(), "s-btw", "side?")
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}
