package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func TestRenderChatItemLinesPreservesStyledUserPromptPadding(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	lines := renderChatItemLines(tuirender.UIMessage{
		Role: "you",
		Kind: tuirender.KindText,
		Text: "hello",
	}, 80)
	if len(lines) < 3 {
		t.Fatalf("expected styled user prompt padding, got: %q", strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(xansi.Strip(lines[0])) != "" || strings.TrimSpace(xansi.Strip(lines[len(lines)-1])) != "" {
		t.Fatalf("expected first and last item lines to be padding, got: %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "\x1b[48;5;236m") || !strings.Contains(lines[len(lines)-1], "\x1b[48;5;236m") {
		t.Fatalf("expected styled padding to survive item trimming, got: %q", strings.Join(lines, "\n"))
	}
}

func TestChatListInsertsWorkSeparatorBetweenToolAndAssistant(t *testing.T) {
	var l chatList
	l.SetSize(80, 24)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\n(no output)"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Done."},
	}, 80)

	plain := xansi.Strip(l.FullContent())
	separator := strings.Repeat("─", 80)
	if !strings.Contains(plain, separator) {
		t.Fatalf("expected separator between work event and assistant:\n%s", plain)
	}
	if strings.Index(plain, "Ran make test") > strings.Index(plain, separator) ||
		strings.Index(plain, separator) > strings.Index(plain, "Done.") {
		t.Fatalf("expected separator between tool event and assistant answer:\n%s", plain)
	}
}

func TestChatListPreservesWorkSeparatorAcrossThinking(t *testing.T) {
	var l chatList
	l.SetSize(80, 24)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\n(no output)"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "checking result"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Done."},
	}, 80)

	plain := xansi.Strip(l.FullContent())
	separator := strings.Repeat("─", 80)
	if !strings.Contains(plain, separator) {
		t.Fatalf("expected separator between work event and final assistant across thinking:\n%s", plain)
	}
	if strings.Index(plain, "Ran make test") > strings.Index(plain, separator) ||
		strings.Index(plain, separator) > strings.Index(plain, "Done.") {
		t.Fatalf("expected separator before final assistant answer:\n%s", plain)
	}
}
