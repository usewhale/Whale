package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

func TestHooksStartupReviewDispatchesReviewAndContinueActions(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksStartupReviewRequested,
		Hooks: testHooksManagerState(),
	}))
	if m.mode != modeHooksStartupReview {
		t.Fatalf("expected startup review mode, got %v", m.mode)
	}
	rendered := xansi.Strip(m.renderHooksStartupReview())
	if !strings.Contains(rendered, "Hooks need review") || !strings.Contains(rendered, "Review hooks") {
		t.Fatalf("unexpected startup review render:\n%s", rendered)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeHooksManager {
		t.Fatalf("expected hooks manager after review choice, got %v", m.mode)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentResolveHooksStartupReview || (*intents)[0].HooksReviewAction != "review" {
		t.Fatalf("unexpected review intent: %+v", *intents)
	}

	m, intents = newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksStartupReviewRequested,
		Hooks: testHooksManagerState(),
	}))
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentResolveHooksStartupReview || (*intents)[0].HooksReviewAction != "trust_all" {
		t.Fatalf("unexpected trust-all intent: %+v", *intents)
	}
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksManagerUpdated,
		Hooks: testTrustedHooksManagerState(),
	}))
	if m.mode != modeChat {
		t.Fatalf("trust all and continue should not open hooks manager on refresh, got %v", m.mode)
	}

	m, intents = newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksStartupReviewRequested,
		Hooks: testHooksManagerState(),
	}))
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentResolveHooksStartupReview || (*intents)[0].HooksReviewAction != "continue" {
		t.Fatalf("unexpected continue intent: %+v", *intents)
	}
}

func TestHooksStartupReviewSurvivesSessionHydration(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksStartupReviewRequested,
		Hooks: testHooksManagerState(),
	}))
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:      protocol.EventSessionHydrated,
		SessionID: "session-1",
	}))
	if m.mode != modeHooksStartupReview {
		t.Fatalf("expected startup review to remain visible after hydration, got %v", m.mode)
	}
	rendered := xansi.Strip(m.View())
	if !strings.Contains(rendered, "Hooks need review") {
		t.Fatalf("startup review disappeared after hydration:\n%s", rendered)
	}
}

func TestHooksManagerDispatchesTrustAndToggleIntents(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksManagerUpdated,
		Hooks: testHooksManagerState(),
	}))
	if m.mode != modeHooksManager {
		t.Fatalf("expected hooks manager mode, got %v", m.mode)
	}
	rendered := xansi.Strip(m.renderHooksManager())
	for _, want := range []string{"Hooks", "PreToolUse", "Installed", "Review"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected hooks manager to contain %q:\n%s", want, rendered)
		}
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.hooksManager.page != hooksPageHandlers {
		t.Fatalf("expected handlers page, got %v", m.hooksManager.page)
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentTrustHook || (*intents)[0].HookKey != "project:PreToolUse:0" {
		t.Fatalf("unexpected trust selected intent: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(*intents) != 2 || (*intents)[1].Kind != protocol.IntentSetHookEnabled || (*intents)[1].HookKey != "project:PreToolUse:1" || (*intents)[1].HookEnabled {
		t.Fatalf("unexpected toggle intent: %+v", *intents)
	}
}

func TestHooksManagerHintsOnlyShowAvailableActions(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksManagerUpdated,
		Hooks: testTrustedHooksManagerState(),
	}))
	rendered := xansi.Strip(m.renderHooksManager())
	if strings.Contains(rendered, "trust all") {
		t.Fatalf("trusted event page should not advertise trust all:\n%s", rendered)
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if len(*intents) != 0 {
		t.Fatalf("t should be a no-op when no hooks need review, got %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	rendered = xansi.Strip(m.renderHooksManager())
	if strings.Contains(rendered, "trust selected") || strings.Contains(rendered, "t trust") {
		t.Fatalf("trusted handler page should not advertise trust:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Space/Enter toggle") {
		t.Fatalf("trusted handler page should advertise toggle:\n%s", rendered)
	}

	m.hooksManager.selectedHandler = 0
	m.hooksManager.state.Entries[0].Trust = "Untrusted"
	m.hooksManager.state.Entries[0].Active = false
	rendered = xansi.Strip(m.renderHooksManager())
	if !strings.Contains(rendered, "t trust") || strings.Contains(rendered, "Space/Enter toggle") {
		t.Fatalf("review handler page should only advertise trust:\n%s", rendered)
	}
}

func TestHooksManagerExitAfterStartupReviewContinuesSession(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m, _ = updateTestModel(t, m, svcMsg(protocol.Event{
		Kind:  protocol.EventHooksStartupReviewRequested,
		Hooks: testHooksManagerState(),
	}))
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after closing startup review browser, got %v", m.mode)
	}
	if len(*intents) != 2 || (*intents)[1].Kind != protocol.IntentResolveHooksStartupReview || (*intents)[1].HooksReviewAction != "continue" {
		t.Fatalf("expected continue intent after closing browser, got %+v", *intents)
	}
}

func testHooksManagerState() *protocol.HooksManagerState {
	return &protocol.HooksManagerState{
		ReviewNeededCount: 1,
		Events: []protocol.HookEventSummary{{
			Event:       "PreToolUse",
			Installed:   2,
			Active:      1,
			Review:      1,
			Description: "Before a tool executes",
		}},
		Entries: []protocol.HookEntry{
			{
				Key:     "project:PreToolUse:0",
				Event:   "PreToolUse",
				Name:    "review me",
				Source:  ".whale/config.toml",
				Command: "echo review",
				Enabled: true,
				Trust:   "Untrusted",
			},
			{
				Key:     "project:PreToolUse:1",
				Event:   "PreToolUse",
				Name:    "trusted hook",
				Source:  ".whale/config.toml",
				Command: "echo trusted",
				Enabled: true,
				Active:  true,
				Trust:   "Trusted",
			},
		},
	}
}

func testTrustedHooksManagerState() *protocol.HooksManagerState {
	state := testHooksManagerState()
	state.ReviewNeededCount = 0
	state.Events[0].Active = 2
	state.Events[0].Review = 0
	for i := range state.Entries {
		state.Entries[i].Active = true
		state.Entries[i].Trust = "Trusted"
	}
	return state
}
