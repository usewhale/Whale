package tui

import (
	"fmt"
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
	for _, hidden := range []string{"private reasoning", "Ran go test", "\nok"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("focus view leaked %q:\n%s", hidden, rendered)
		}
	}
	for _, want := range []string{"inspect this", "Read 1 file, Ran shell: go test", "(ctrl+o to expand)", "done"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing %q:\n%s", want, rendered)
		}
	}
}

func TestProjectFocusMessagesKeepsSingleShellCommandVisible(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran git status\nOn branch main"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	rendered := projected[0].Text
	for _, want := range []string{"Ran shell: git status", "(ctrl+o to expand)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing shell command %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "On branch main") {
		t.Fatalf("focus view should still hide shell output:\n%s", rendered)
	}
}

func TestProjectFocusMessagesCollapsesSubagentCells(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "inspect this"},
		{Role: "result_running", Kind: tuirender.KindSubagent, ToolName: "spawn_subagent", Text: "Subagent review running\nsession: child-123\ncurrent: read_file\ndetail: reading internal/tasks/runner.go"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "done"},
	}

	rendered := strings.Join(tuirender.ChatLines(projectFocusMessages(messages), 100), "\n")
	for _, hidden := range []string{"child-123", "read_file", "reading internal/tasks/runner.go"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("focus view leaked subagent detail %q:\n%s", hidden, rendered)
		}
	}
	for _, want := range []string{"inspect this", "Subagent review running (1 running)", "(ctrl+o to expand)", "done"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing %q:\n%s", want, rendered)
		}
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
	if strings.Contains(got, "hidden thought") || strings.Contains(got, "\nEdited internal/tui/focus_view.go") {
		t.Fatalf("focus chat leaked hidden entries:\n%s", got)
	}
	if !strings.Contains(got, "Editing 1 file: internal/tui/focus_view.go (1 running)") || !strings.Contains(got, "answer") {
		t.Fatalf("focus chat missing summary/final answer:\n%s", got)
	}
}

func TestProjectFocusMessagesKeepsRunningHintVisible(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_running", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/focus_view.go"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Reading 1 file: internal/tui/focus_view.go (1 running) (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected running focus summary:\nwant: %q\n got: %q", want, got)
	}
}

func TestProjectFocusMessagesOmitsCompletedHints(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "grep", Text: "Explored\nSearch focus"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "list_dir", Text: "Explored\nList internal/tui"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Searched for 1 pattern, Read 1 file, Listed 1 directory (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected completed focus summary:\nwant: %q\n got: %q", want, got)
	}
}

func TestProjectFocusMessagesUsesStableSemanticSummaryOrder(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "edit_file", Text: "Edited internal/tui/focus_view.go"},
		{Role: "shell_result_ok", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran go test ./internal/tui\nok"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Read 1 file, Ran shell: go test ./internal/tui, Edited 1 file (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected ordered semantic summary:\nwant: %q\n got: %q", want, got)
	}
}

func TestProjectFocusMessagesDoesNotSplitToolSummaryOnHiddenThinking(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "consider next file"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "grep", Text: "Explored\nSearch focus in internal/tui"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "consider final file"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/render/chat.go"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one merged focus summary, got %d: %+v", len(projected), projected)
	}
	want := `Searched for 1 pattern, Read 2 files (ctrl+o to expand)`
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected merged focus summary: %q", got)
	}
}

func TestProjectFocusMessagesMergesHydratedToolDenseSequence(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "think", Kind: tuirender.KindThinking, Text: "decide first file"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/focus_view.go"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "decide second file"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "grep", Text: "Explored\nSearch projectFocusMessages"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "decide third file"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/hydration.go"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "analysis complete"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 2 {
		t.Fatalf("expected merged summary plus answer, got %d: %+v", len(projected), projected)
	}
	want := `Searched for 1 pattern, Read 2 files (ctrl+o to expand)`
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected merged focus summary: %q", got)
	}
}

func TestProjectFocusMessagesSeparatesSearchReadAndList(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "grep", Text: "Explored\nSearch focus"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "list_dir", Text: "Explored\nList internal/tui"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := `Searched for 1 pattern, Read 1 file, Listed 1 directory (ctrl+o to expand)`
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected search/read/list summary:\nwant: %q\n got: %q", want, got)
	}
}

func TestProjectFocusMessagesClassifiesMCPFilesystemTools(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_denied", Kind: tuirender.KindToolCall, ToolName: "mcp__fs__read_text_file", Text: "Ran mcp__fs__read_text_file"},
		{Role: "result_denied", Kind: tuirender.KindToolCall, ToolName: "mcp__fs__read_text_file", Text: "Ran mcp__fs__read_text_file"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Denied 2 files (2 denied/canceled) (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected MCP read summary:\nwant: %q\n got: %q", want, got)
	}
	if strings.Contains(projected[0].Text, "shell") || strings.Contains(projected[0].Text, "mcp__fs__read_text_file") {
		t.Fatalf("MCP read tool should not render as shell/tool-name noise:\n%s", projected[0].Text)
	}
}

func TestProjectFocusMessagesDoesNotUseMCPToolNameAsRunningHint(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_running", Kind: tuirender.KindToolCall, ToolName: "mcp__fs__read_text_file", Text: "Running read_text_file"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Reading 1 file (1 running) (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected MCP running summary:\nwant: %q\n got: %q", want, got)
	}
	if strings.Contains(projected[0].Text, "read_text_file") || strings.Contains(projected[0].Text, "mcp__fs__read_text_file") {
		t.Fatalf("MCP tool name should not render as a running hint:\n%s", projected[0].Text)
	}
}

func TestProjectFocusMessagesKeepsRealMCPRunningDetail(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_running", Kind: tuirender.KindToolCall, ToolName: "mcp__fs__read_text_file", Text: "Running internal/tui/focus_view.go"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Reading 1 file: internal/tui/focus_view.go (1 running) (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected MCP running detail summary:\nwant: %q\n got: %q", want, got)
	}
}

func TestProjectFocusMessagesKeepsAssistantTextAsGroupBreaker(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "checkpoint"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "grep", Text: "Explored\nSearch focus"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 3 {
		t.Fatalf("expected read summary, assistant text, and search summary, got %d: %+v", len(projected), projected)
	}
	want := []string{
		"Read 1 file (ctrl+o to expand)",
		"checkpoint",
		`Searched for 1 pattern (ctrl+o to expand)`,
	}
	for i := range want {
		if projected[i].Text != want[i] {
			t.Fatalf("unexpected projected message %d:\nwant: %q\n got: %q", i, want[i], projected[i].Text)
		}
	}
}

func TestProjectFocusMessagesKeepsStatusAndTruncatesLongShellDetail(t *testing.T) {
	longCommand := "go test ./internal/tui -run TestProjectFocusMessagesKeepsStatusAndTruncatesLongShellDetail -count=1 -v"
	messages := []tuirender.UIMessage{
		{Role: "shell_result_failed", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran " + longCommand + "\nFAIL"},
		{Role: "result_denied", Kind: tuirender.KindToolCall, ToolName: "edit_file", Text: "Edited internal/tui/focus_view.go"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	rendered := projected[0].Text
	for _, want := range []string{"Ran shell: go test ./internal/tui -run", "...", "Denied 1 file", "(1 failed)", "(1 denied/canceled)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("focus view missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, " -count=1 -v") {
		t.Fatalf("focus view should truncate long shell detail:\n%s", rendered)
	}
}

func TestProjectFocusMessagesSummarizesDeniedShellWithoutPlaceholderDetails(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "shell_result_denied", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran shell command"},
		{Role: "shell_result_denied", Kind: tuirender.KindToolCall, ToolName: "shell_run", Text: "Ran shell command"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Denied 2 shell commands (2 denied/canceled) (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected denied shell summary:\nwant: %q\n got: %q", want, got)
	}
	if strings.Contains(projected[0].Text, "shell command; shell command") {
		t.Fatalf("denied shell summary should not repeat placeholder details:\n%s", projected[0].Text)
	}
}

func TestProjectFocusMessagesSummarizesMultipleSimpleUpdates(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_running", Kind: tuirender.KindToolCall, ToolName: "update_plan", Text: "Updating plan"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "update_plan", Text: "Updated plan"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "todo_update", Text: "Updated todos"},
		{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "todo_update", Text: "Updated todos"},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	want := "Updating plan: 2 plan updates (1 running), Updated todos: 2 todo updates (ctrl+o to expand)"
	if got := projected[0].Text; got != want {
		t.Fatalf("unexpected simple update summary:\nwant: %q\n got: %q", want, got)
	}
}

func TestProjectFocusMessagesFallsBackWhenTaskHasNoDetail(t *testing.T) {
	messages := []tuirender.UIMessage{
		{Role: "result_running", Kind: tuirender.KindSubagent, ToolName: "spawn_subagent", Text: "   "},
	}

	projected := projectFocusMessages(messages)
	if len(projected) != 1 {
		t.Fatalf("expected one focus summary, got %d: %+v", len(projected), projected)
	}
	if got := projected[0].Text; got != "Running 1 subagent task (1 running) (ctrl+o to expand)" {
		t.Fatalf("unexpected task fallback summary: %q", got)
	}
}

func TestFocusNativeScrollbackDefersToolOnlySummaries(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.viewMode = app.ViewModeFocus
	m.width = 80
	m.height = 24
	m.transcript = []tuirender.UIMessage{{Role: "you", Kind: tuirender.KindText, Text: "inspect these changes"}}
	m.nativeScrollbackPrinted = len(m.transcript)

	m.transcript = append(m.transcript,
		tuirender.UIMessage{Role: "think", Kind: tuirender.KindThinking, Text: "choose first file"},
		tuirender.UIMessage{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/focus_view.go"},
	)
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatalf("expected tool-only focus summary to stay in live viewport, got print command %#v", cmd())
	}
	if m.nativeScrollbackPrinted != 1 {
		t.Fatalf("expected native scrollback cursor to stay at user prompt, got %d", m.nativeScrollbackPrinted)
	}

	m.transcript = append(m.transcript,
		tuirender.UIMessage{Role: "think", Kind: tuirender.KindThinking, Text: "choose second file"},
		tuirender.UIMessage{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "grep", Text: "Explored\nSearch focus"},
		tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: "analysis complete"},
	)
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected visible answer to flush delayed focus summaries")
	}
	printed := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(printed, `Searched for 1 pattern, Read 1 file`) || strings.Contains(printed, "Read 1 file (ctrl+o to expand)\\n\\n┃") {
		t.Fatalf("expected delayed native scrollback to print one merged summary, got %s", printed)
	}
}

func TestFocusNativeScrollbackDoesNotLeakHiddenOnlyMessages(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.viewMode = app.ViewModeFocus
	m.width = 80
	m.height = 24
	m.transcript = []tuirender.UIMessage{{Role: "you", Kind: tuirender.KindText, Text: "question"}}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.transcript = append(m.transcript, tuirender.UIMessage{Role: "think", Kind: tuirender.KindThinking, Text: "private reasoning"})

	cmd := m.flushNativeScrollbackCmd()
	if cmd != nil {
		t.Fatalf("expected hidden-only focus messages to produce no print command, got %#v", cmd())
	}
	if m.nativeScrollbackPrinted != len(m.transcript) {
		t.Fatalf("expected hidden-only messages to be marked printed, got %d of %d", m.nativeScrollbackPrinted, len(m.transcript))
	}
}

func TestFocusNativeScrollbackFlushesDeferredToolSummaryBeforeNextVisibleMessage(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.viewMode = app.ViewModeFocus
	m.width = 80
	m.height = 24
	m.transcript = []tuirender.UIMessage{{Role: "you", Kind: tuirender.KindText, Text: "first prompt"}}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.transcript = append(m.transcript, tuirender.UIMessage{Role: "result_ok", Kind: tuirender.KindToolCall, ToolName: "read_file", Text: "Explored\nRead internal/tui/focus_view.go"})
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatalf("expected tool-only focus summary to be deferred, got %#v", cmd())
	}

	m.transcript = append(m.transcript, tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "next prompt"})
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected next visible message to flush delayed tool summary")
	}
	printed := fmt.Sprintf("%#v", cmd())
	for _, want := range []string{"Read 1 file", "next prompt"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("expected delayed flush to include %q, got %s", want, printed)
		}
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
