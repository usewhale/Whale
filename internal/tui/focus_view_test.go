package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func TestProjectFocusMessagesHidesThinkingAndToolDetails(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "inspect this"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "private reasoning"},
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran go test\nok"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "done"},
	}

	rendered := strings.Join(tuirender.ChatLines(projectFocusMessages(messages), 100), "\n")
	for _, hidden := range []string{"private reasoning", "Ran go test", "Read internal/tui/model.go"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("focus view leaked %q:\n%s", hidden, rendered)
		}
	}
	for _, want := range []string{"inspect this", "Ran shell: go test, 1 file/search read", "(ctrl+o to expand)", "done"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing %q:\n%s", want, rendered)
		}
	}
}

func TestProjectFocusMessagesKeepsSingleShellCommandVisible(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran git status\nOn branch main"},
	}

	rendered := strings.Join(tuirender.ChatLines(projectFocusMessages(messages), 100), "\n")
	for _, want := range []string{"Ran shell: git status", "(ctrl+o to expand)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing shell command %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "On branch main") {
		t.Fatalf("focus view should still hide shell output:\n%s", rendered)
	}
}

func TestProjectFocusMessagesExpandedShowsHiddenDetailsWithCollapseHint(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "inspect this"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "private reasoning"},
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran go test\nok"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "done"},
	}

	rendered := strings.Join(tuirender.ChatLines(projectExpandedFocusMessages(messages), 100), "\n")
	for _, want := range []string{"private reasoning", "Ran go test", "ok", "(ctrl+o to collapse)", "done"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expanded focus view missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "(ctrl+o to expand)") {
		t.Fatalf("expanded focus view should not show expand hint:\n%s", rendered)
	}
}

func TestModelChatMessagesApplyFocusView(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.viewMode = app.ViewModeFocus
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "question"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "hidden thought"},
		{Role: "tool", Kind: tuirender.KindToolCall, ToolName: "edit_file", Text: "Edited internal/tui/focus_view.go"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "answer"},
	}

	got := strings.Join(tuirender.ChatLines(m.chatMessages(), 100), "\n")
	if strings.Contains(got, "hidden thought") || strings.Contains(got, "Edited internal/tui/focus_view.go") {
		t.Fatalf("focus chat leaked hidden entries:\n%s", got)
	}
	if !strings.Contains(got, "Ran 1 edit (1 running)") || !strings.Contains(got, "answer") {
		t.Fatalf("focus chat missing summary/final answer:\n%s", got)
	}
}

func TestViewModeChangedEventRefreshesFooter(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 20
	_, _, _ = m.handleServiceEvent(serviceViewModeChanged(app.ViewModeFocus))

	view := m.View()
	if !strings.Contains(view, "focus") {
		t.Fatalf("footer missing focus indicator:\n%s", view)
	}
	if !strings.Contains(view, "Focus view enabled") {
		t.Fatalf("view missing focus toggle message:\n%s", view)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n"); strings.Contains(got, "Focus view enabled") {
		t.Fatalf("focus toggle message should not enter transcript:\n%s", got)
	}
	m.commitLiveTranscript(true)
	if got := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n"); strings.Contains(got, "Focus view enabled") {
		t.Fatalf("focus toggle message should not be committed later:\n%s", got)
	}
}

func TestFocusFooterIndicatorSurvivesLongDirectory(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.viewMode = app.ViewModeFocus
	m.cwd = "/Users/goranka/Engineer/ai/dsk/whale-output-mouse-copy"
	m.width = 72
	m.height = 12

	lines := strings.Split(m.View(), "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "focus") {
		t.Fatalf("footer missing focus indicator:\n%s", footer)
	}
}

func TestFocusToggleMessageClearsOnNextSubmit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 20
	_, _, _ = m.handleServiceEvent(serviceViewModeChanged(app.ViewModeFocus))
	if !strings.Contains(m.View(), "Focus view enabled") {
		t.Fatalf("missing focus toggle message:\n%s", m.View())
	}

	m.input.SetValue("next prompt")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if strings.Contains(m.View(), "Focus view enabled") {
		t.Fatalf("focus toggle message should clear on next submit:\n%s", m.View())
	}
	if len(*intents) != 1 || (*intents)[0].Input != "next prompt" {
		t.Fatalf("expected next prompt dispatch, got %+v", *intents)
	}
}

func TestCtrlOTogglesFocusView(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 20
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "question"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "hidden thought"},
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran git status\nclean"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "answer"},
	}

	expanded := strings.Join(tuirender.ChatLines(m.chatMessages(), 100), "\n")
	if !strings.Contains(expanded, "hidden thought") || !strings.Contains(expanded, "(ctrl+o to collapse)") {
		t.Fatalf("expected default view with collapse hint:\n%s", expanded)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(model)
	collapsed := strings.Join(tuirender.ChatLines(m.chatMessages(), 100), "\n")
	if m.viewMode != app.ViewModeFocus || strings.Contains(collapsed, "hidden thought") || !strings.Contains(collapsed, "(ctrl+o to expand)") {
		t.Fatalf("expected ctrl+o to collapse into focus view:\n%s", collapsed)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected ctrl+o to stay local, got intents %+v", *intents)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(model)
	reexpanded := strings.Join(tuirender.ChatLines(m.chatMessages(), 100), "\n")
	if m.viewMode != app.ViewModeDefault || !strings.Contains(reexpanded, "hidden thought") || !strings.Contains(reexpanded, "(ctrl+o to collapse)") {
		t.Fatalf("expected second ctrl+o to expand into default view:\n%s", reexpanded)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected ctrl+o to stay local, got intents %+v", *intents)
	}
}

func TestCtrlODoesNotEndBusyTurn(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 20
	m.busy = true
	m.status = "running"
	m.sawReasoningThisTurn = true
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "old question"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "old hidden thought"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "old answer"},
	}
	m.nativeScrollbackPrinted = len(m.transcript)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected busy ctrl+o to redraw transcript")
	}
	if !m.busy || m.status != "running" {
		t.Fatalf("expected ctrl+o not to complete busy turn, busy=%v status=%q", m.busy, m.status)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected busy ctrl+o not to dispatch service intent, got %+v", *intents)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(rendered, "Reasoning only") || strings.Contains(rendered, "did not produce a visible answer") {
		t.Fatalf("busy ctrl+o should not append reasoning-only notice:\n%s", rendered)
	}
}

func TestCtrlORedrawsPreviouslyPrintedTranscript(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 100
	m.height = 20
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "old question"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "old hidden thought"},
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran git status\nclean"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "old answer"},
	}
	m.nativeScrollbackPrinted = len(m.transcript)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected ctrl+o to redraw printed transcript")
	}
	if m.viewMode != app.ViewModeFocus {
		t.Fatalf("expected focus view, got %q", m.viewMode)
	}
	if m.nativeScrollbackPrinted != len(m.transcript) {
		t.Fatalf("expected redrawn transcript to be marked printed, got %d of %d", m.nativeScrollbackPrinted, len(m.transcript))
	}
	collapsed := m.scrollbackText(m.transcript)
	for _, want := range []string{"old question", "Ran shell: git status", "(ctrl+o to expand)", "old answer"} {
		if !strings.Contains(collapsed, want) {
			t.Fatalf("expected collapsed redraw to include %q:\n%s", want, collapsed)
		}
	}
	for _, hidden := range []string{"old hidden thought", "clean"} {
		if strings.Contains(collapsed, hidden) {
			t.Fatalf("collapsed redraw leaked %q:\n%s", hidden, collapsed)
		}
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected second ctrl+o to redraw printed transcript")
	}
	expanded := m.scrollbackText(m.transcript)
	for _, want := range []string{"old hidden thought", "Ran git status", "clean", "(ctrl+o to collapse)"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expected expanded redraw to include %q:\n%s", want, expanded)
		}
	}
}

func serviceViewModeChanged(mode string) service.Event {
	return service.Event{Kind: service.EventViewModeChanged, ViewMode: mode, Text: app.ViewModeToggleMessage(mode)}
}
