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
	for _, want := range []string{"inspect this", "Ran 1 shell command, 1 file/search read", "done"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing %q:\n%s", want, rendered)
		}
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

func serviceViewModeChanged(mode string) service.Event {
	return service.Event{Kind: service.EventViewModeChanged, ViewMode: mode, Text: app.ViewModeToggleMessage(mode)}
}
