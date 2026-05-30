package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func TestHistoryNavigationContinuesAcrossSlashCommandEntry(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.promptHistory = []string{"a", "b", "c", "/status"}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "/status" {
		t.Fatalf("expected first Up to recall slash command, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected recalled slash command not to show suggestions during history navigation")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "c" {
		t.Fatalf("expected second Up to continue history navigation, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected non-slash history entry to clear slash suggestions")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.input.Value(); got != "/status" {
		t.Fatalf("expected Down to return to slash history entry, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected slash suggestions to stay hidden after returning to slash history entry")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Down from newest history entry to restore draft, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected empty draft to clear slash suggestions")
	}
}
func TestHistoryNavigationSuppressesSkillSuggestions(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{{Name: "code-review"}}
	m.promptHistory = []string{"a", "$code-review"}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "$code-review" {
		t.Fatalf("expected first Up to recall skill trigger, got %q", got)
	}
	if m.hasSkillSuggestions() {
		t.Fatal("expected recalled skill trigger not to show suggestions during history navigation")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "a" {
		t.Fatalf("expected second Up to continue history navigation, got %q", got)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.input.Value(); got != "$code-review" {
		t.Fatalf("expected Down to return to skill history entry, got %q", got)
	}
	if m.hasSkillSuggestions() {
		t.Fatal("expected skill suggestions to stay hidden after returning to skill history entry")
	}
}
