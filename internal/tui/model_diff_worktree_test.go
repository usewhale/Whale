package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
	"testing"
)

func TestDiffResultOpensDiffPageAndEscReturnsToChat(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 20
	m.input.SetValue("draft")
	m.localSubmitCommands = []string{"/diff"}

	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventDiffResult,
		Text: "diff --git a/README.md b/README.md\n@@ -1 +1 @@\n-old\n+new\n",
	}))
	m = next.(model)

	if m.page != pageDiff {
		t.Fatalf("expected diff page, got %v", m.page)
	}
	if m.shouldRenderComposer() {
		t.Fatal("diff page should hide the composer")
	}
	if view := m.View(); strings.Contains(view, "draft") || !strings.Contains(view, "q/Esc close") || strings.Contains(view, "Ctrl+C") || strings.Contains(view, "Space") || strings.Contains(view, "-old\n\n+new") {
		t.Fatalf("diff page should render pager hints without composer:\n%s", view)
	}
	if got := strings.Join(m.renderDiffs(), "\n"); !strings.Contains(got, "+new") || strings.Contains(got, "[") {
		t.Fatalf("unexpected diff page content:\n%s", got)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "/diff") || strings.Contains(rendered, "+new") {
		t.Fatalf("expected transcript to contain only command echo, got:\n%s", rendered)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(model)
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("diff page should ignore text input, got %q", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.page != pageChat {
		t.Fatalf("expected esc to return to chat, got %v", m.page)
	}
}
func TestDiffPageUsesPagerKeys(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 12
	lines := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}
	m.setDiffText(strings.Join(lines, "\n"))
	m.page = pageDiff
	m.refreshViewportContent()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("expected j to scroll diff down")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected k to scroll diff up, offset=%d", m.viewport.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("expected pgdown to page diff down")
	}
	pagedOffset := m.viewport.YOffset

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	if m.viewport.YOffset != pagedOffset {
		t.Fatalf("space should not page diff, offset=%d want %d", m.viewport.YOffset, pagedOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(model)
	if m.viewport.YOffset != pagedOffset {
		t.Fatalf("ctrl+d should not half-page diff, offset=%d want %d", m.viewport.YOffset, pagedOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected home to jump to top, offset=%d", m.viewport.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if m.page != pageChat {
		t.Fatalf("expected q to close diff page, got %v", m.page)
	}

	m.page = pageDiff
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if m.page != pageDiff {
		t.Fatalf("ctrl+c should not close diff page, got %v", m.page)
	}
	if m.status != "Press Ctrl+C again to quit" {
		t.Fatalf("ctrl+c on idle diff page should arm global quit, got status %q", m.status)
	}
}
func TestDiffResultResetsPagerToTop(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 12

	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}
	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventDiffResult,
		Text: strings.Join(lines, "\n"),
	}))
	m = next.(model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("expected End to scroll the diff away from the top")
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind: service.EventDiffResult,
		Text: strings.Join(lines, "\n"),
	}))
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected a fresh diff to reset the pager to the top, offset=%d", m.viewport.YOffset)
	}
}
func TestWorktreeExitPromptEnterDispatchesSelectedAction(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modeWorktreeExit
	m.worktreeExit.summary = app.WorktreeExitSummary{
		Session:      app.WorktreeSession{Name: "feature", Path: "/tmp/repo/.whale/worktrees/feature", Branch: "worktree-feature"},
		ChangedFiles: 1,
	}
	m.worktreeExit.selected = 1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentWorktreeExitChoice || (*intents)[0].WorktreeAction != "remove" {
		t.Fatalf("unexpected intent: %+v", (*intents)[0])
	}
	if m.mode != modeChat || m.status != "removing worktree" {
		t.Fatalf("unexpected mode/status: %v %q", m.mode, m.status)
	}
}
func TestWorktreeExitSummaryIncludesIgnoredFiles(t *testing.T) {
	got := worktreeExitSummaryText(0, 1, 0)
	if !strings.Contains(got, "1 ignored file") {
		t.Fatalf("expected ignored file warning, got %q", got)
	}
}
func TestWorktreeExitPromptEscCancelsExit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modeWorktreeExit
	m.worktreeExit.summary = app.WorktreeExitSummary{Session: app.WorktreeSession{Name: "feature"}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentWorktreeExitChoice || (*intents)[0].WorktreeAction != "cancel" {
		t.Fatalf("unexpected intent: %+v", (*intents)[0])
	}
	if m.mode != modeChat || m.status != "exit canceled" {
		t.Fatalf("unexpected mode/status: %v %q", m.mode, m.status)
	}
}
func largeTranslationDiffMetadata(deletions, additions int) map[string]any {
	lines := []string{
		"--- a/roadmap.md",
		"+++ b/roadmap.md",
		fmt.Sprintf("@@ -1,%d +1,%d @@", deletions, additions),
	}
	for i := 0; i < deletions; i++ {
		lines = append(lines, fmt.Sprintf("-中文 %03d", i))
	}
	for i := 0; i < additions; i++ {
		lines = append(lines, fmt.Sprintf("+English %03d", i))
	}
	return map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "roadmap.md",
				"unified_diff": strings.Join(lines, "\n"),
				"additions":    additions,
				"deletions":    deletions,
			},
		},
	}
}
func testFileDiffMetadata() map[string]any {
	return map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-world\n+whale",
				"additions":    1,
				"deletions":    1,
			},
		},
	}
}
