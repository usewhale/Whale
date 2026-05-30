package cmd

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/updatecheck"
)

func testUpdatePromptModel() updatePromptModel {
	return newUpdatePromptModel(updatecheck.Result{
		CurrentVersion:  "v0.1.15",
		LatestVersion:   "v0.1.16",
		ReleaseNotesURL: updatecheck.ReleaseNotesURL,
		UpdateAction:    updatecheck.Action{Cmd: "brew", Args: []string{"upgrade", "usewhale/tap/whale"}},
	})
}

func TestUpdatePromptEnterDefaultsToSkip(t *testing.T) {
	m := testUpdatePromptModel()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	got := next.(updatePromptModel)
	if !got.done || got.selection != updateSelectionSkip {
		t.Fatalf("selection=%v done=%v", got.selection, got.done)
	}
}

func TestUpdatePromptEnterAfterArrowSelectsUpdate(t *testing.T) {
	m := testUpdatePromptModel()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	next, cmd := next.(updatePromptModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	got := next.(updatePromptModel)
	if !got.done || got.selection != updateSelectionNow {
		t.Fatalf("selection=%v done=%v", got.selection, got.done)
	}
}

func TestUpdatePromptCtrlCInterrupts(t *testing.T) {
	m := testUpdatePromptModel()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := next.(updatePromptModel)
	if !got.done || got.selection != updateSelectionInterrupt {
		t.Fatalf("selection=%v done=%v", got.selection, got.done)
	}
}

func TestUpdatePromptEscSkips(t *testing.T) {
	m := testUpdatePromptModel()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(updatePromptModel)
	if !got.done || got.selection != updateSelectionSkip {
		t.Fatalf("selection=%v done=%v", got.selection, got.done)
	}
}

func TestUpdatePromptDismissSelection(t *testing.T) {
	m := testUpdatePromptModel()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	got := next.(updatePromptModel)
	if !got.done || got.selection != updateSelectionDismiss {
		t.Fatalf("selection=%v done=%v", got.selection, got.done)
	}
}

func TestUpdatePromptRenderIncludesVersionsAndCommand(t *testing.T) {
	m := testUpdatePromptModel()
	view := m.View()
	for _, want := range []string{
		"Update available! v0.1.15 -> v0.1.16",
		"Release notes: https://github.com/usewhale/DeepSeek-Code-",
		"Whale/releases/latest",
		"brew upgrade usewhale/tap/whale",
		"Skip until next version",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestUpdatePromptRenderManualAction(t *testing.T) {
	m := newUpdatePromptModel(updatecheck.Result{
		CurrentVersion:  "v0.1.15",
		LatestVersion:   "v0.1.16",
		ReleaseNotesURL: updatecheck.ReleaseNotesURL,
		UpdateAction: updatecheck.Action{
			Cmd:        "powershell",
			Args:       []string{"-NoProfile"},
			ManualOnly: true,
		},
	})
	view := m.View()
	for _, want := range []string{
		"Show update command",
		"run after Whale exits",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestRunUpdateActionManualOnlyPrintsCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	action := updatecheck.Action{
		Cmd:        "definitely-not-real-whale-updater",
		Args:       []string{"--bad"},
		ManualOnly: true,
	}
	err := runUpdateActionWithIO(action, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runUpdateActionWithIO: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"run after Whale exits",
		"definitely-not-real-whale-updater --bad",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
