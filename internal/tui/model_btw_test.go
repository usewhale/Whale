package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/app/service"
	"strings"
	"testing"
)

func TestBtwSecondSubmitWhileLoadingIsBlocked(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.btwPanel = btwPanelState{visible: true, id: 1, question: "first?", loading: true}
	m.input.SetValue("/btw second?")

	m.submitLocalNoTurn(appcommands.SubmitClassification{Line: "/btw second?", Class: appcommands.SubmitLocalReadOnly})

	if len(*intents) != 0 {
		t.Fatalf("expected no second /btw intent while loading, got %+v", *intents)
	}
	if got := m.input.Value(); got != "/btw second?" {
		t.Fatalf("expected input to remain editable, got %q", got)
	}
	if m.localSubmitPending != 0 {
		t.Fatalf("expected no pending local submit, got %d", m.localSubmitPending)
	}
	if m.status != "/btw is already answering" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}
func TestBtwDeltaEventsAreNotBatchableAcrossRequests(t *testing.T) {
	first := service.Event{Kind: service.EventBtwDelta, Text: "first", Count: 1}
	second := service.Event{Kind: service.EventBtwDelta, Text: "second", Count: 2}
	if shouldBatchServiceEvent(first) {
		t.Fatal("btw deltas should not be batched because request ids can differ")
	}
	events := appendBatchedServiceEvent(nil, first)
	events = appendBatchedServiceEvent(events, second)
	if len(events) != 2 {
		t.Fatalf("expected separate btw delta events, got %d: %+v", len(events), events)
	}
	if events[0].Text != "first" || events[0].Count != 1 || events[1].Text != "second" || events[1].Count != 2 {
		t.Fatalf("unexpected btw delta events: %+v", events)
	}
}
func TestBtwPanelRendersAndDoesNotAppendTranscript(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 24
	before := len(m.transcript)
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventBtwStarted, Text: "quick?", Count: 1}))
	if !m.btwPanel.visible || !m.btwPanel.loading {
		t.Fatalf("expected loading btw panel: %+v", m.btwPanel)
	}
	view := m.View()
	if !strings.Contains(view, "/btw") || !strings.Contains(view, "Answering...") {
		t.Fatalf("expected btw loading panel in view:\n%s", view)
	}
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventBtwDone, Text: "**answer**", Count: 1}))
	view = m.View()
	if !strings.Contains(view, "answer") || !strings.Contains(view, "Ctrl+P/Ctrl+N") {
		t.Fatalf("expected btw answer panel in view:\n%s", view)
	}
	if len(m.transcript) != before {
		t.Fatalf("btw answer should not append transcript, before=%d after=%d", before, len(m.transcript))
	}
}
func TestBtwPanelDoesNotConsumeChatInputKeys(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.btwPanel = btwPanelState{visible: true, id: 1, question: "quick?", response: "answer"}
	m.input.SetValue("hello")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if got := m.input.Value(); got != "hello " {
		t.Fatalf("expected space to reach composer, got %q", got)
	}

	m.input.SetValue("follow up")
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(*intents) != 1 {
		t.Fatalf("expected enter to submit prompt, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "follow up" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
	if !m.btwPanel.visible {
		t.Fatal("btw panel should remain visible after chat input submit")
	}
}
func TestBtwPanelScrollKeys(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.height = 9
	m.width = 80
	m.btwPanel = btwPanelState{
		visible:  true,
		id:       1,
		question: "quick?",
		response: strings.Repeat("line\n\n", 20),
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.btwPanel.scroll == 0 {
		t.Fatal("expected ctrl+n to scroll btw panel")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.btwPanel.scroll != 0 {
		t.Fatalf("expected ctrl+p to scroll back to top, got %d", m.btwPanel.scroll)
	}
}
