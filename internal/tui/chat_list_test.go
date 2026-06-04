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

func TestChatListRenderCacheTracksCurrentMessagesOnly(t *testing.T) {
	var l chatList
	l.SetSize(80, 24)

	base := tuirender.UIMessage{ID: "base", Role: "assistant", Kind: tuirender.KindText, Text: "stable"}
	live := tuirender.UIMessage{ID: "live", Role: "assistant", Kind: tuirender.KindText, Text: "first live"}
	l.SetMessages([]tuirender.UIMessage{base, live}, 80)

	live.Text = "second live"
	l.SetMessages([]tuirender.UIMessage{base, live}, 80)

	plain := xansi.Strip(l.FullContent())
	if strings.Contains(plain, "first live") {
		t.Fatalf("expected changed message to be re-rendered, got:\n%s", plain)
	}
	if !strings.Contains(plain, "second live") {
		t.Fatalf("expected changed message to be visible, got:\n%s", plain)
	}
	if got := len(l.renderCache); got != 2 {
		t.Fatalf("expected cache to retain only current messages, got %d entries", got)
	}
}

func TestChatListConversationGapAfterUserAndAssistant(t *testing.T) {
	user := tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "who are you"}
	assistant := tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: "I'm Whale."}

	if got := chatListGapAfter(user, assistant); got != chatListGap {
		t.Fatalf("expected user to assistant gap %d, got %d", chatListGap, got)
	}
	if got := chatListGapAfter(assistant, user); got != 2 {
		t.Fatalf("expected assistant text to user gap 2, got %d", got)
	}
}

func TestChatListToolBoundariesKeepDefaultGap(t *testing.T) {
	user := tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "run tests"}
	assistant := tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: "Done."}
	tool := tuirender.UIMessage{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\nok"}
	notice := tuirender.UIMessage{Role: "notice", Kind: tuirender.KindNotice, Text: "You approved whale"}
	status := tuirender.UIMessage{Role: "status", Kind: tuirender.KindStatus, Text: "Working"}
	local := tuirender.UIMessage{Role: "local", Kind: tuirender.KindLocalStatus, Text: "local update"}
	thinking := tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindThinking, Text: "checking"}

	for _, tc := range []struct {
		name string
		prev tuirender.UIMessage
		next tuirender.UIMessage
	}{
		{name: "user to tool", prev: user, next: tool},
		{name: "tool to assistant", prev: tool, next: assistant},
		{name: "assistant to tool", prev: assistant, next: tool},
		{name: "assistant notice to user", prev: notice, next: user},
		{name: "assistant status to user", prev: status, next: user},
		{name: "assistant local to user", prev: local, next: user},
		{name: "assistant thinking to user", prev: thinking, next: user},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatListGapAfter(tc.prev, tc.next); got != chatListGap {
				t.Fatalf("expected default gap %d, got %d", chatListGap, got)
			}
		})
	}
}

func TestChatListViewAndFullContentUseSameConversationGap(t *testing.T) {
	var l chatList
	l.SetSize(80, 200)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "who are you"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "I'm Whale."},
		{Role: "you", Kind: tuirender.KindText, Text: "next task"},
	}, 80)

	full := l.FullContent()
	view := l.View()
	if view != full {
		t.Fatalf("expected View and FullContent to match when height includes all content\nview:\n%s\nfull:\n%s", xansi.Strip(view), xansi.Strip(full))
	}
	if got, want := l.TotalLineCount(), renderedLineCount(full); got != want {
		t.Fatalf("expected TotalLineCount to match FullContent lines, got %d want %d", got, want)
	}
	if got := countPlainBlankLinesAfterItem(l.items, 0, full); got != chatListGap {
		t.Fatalf("expected %d plain gap lines after user item, got %d:\n%s", chatListGap, got, xansi.Strip(full))
	}
	if got := countPlainBlankLinesAfterItem(l.items, 1, full); got != 2 {
		t.Fatalf("expected 2 plain gap lines after assistant item before user prompt, got %d:\n%s", got, xansi.Strip(full))
	}
}

func TestChatListLeadingGapParticipatesInFullContentAndView(t *testing.T) {
	var l chatList
	l.SetSize(80, 200)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "next task"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Done."},
	}, 80)
	l.SetLeadingGap(2)

	full := l.FullContent()
	view := l.View()
	if view != full {
		t.Fatalf("expected View and FullContent to match when height includes all content\nview:\n%s\nfull:\n%s", xansi.Strip(view), xansi.Strip(full))
	}
	if got, want := l.TotalLineCount(), renderedLineCount(full); got != want {
		t.Fatalf("expected TotalLineCount to include leading gap, got %d want %d", got, want)
	}
	if got := leadingNewlines(full); got != 2 {
		t.Fatalf("expected leading gap to render as 2 leading blank lines, got %d:\n%q", got, xansi.Strip(full))
	}
}

func TestChatListScrollToBottomWithLeadingGapShowsTail(t *testing.T) {
	var l chatList
	l.SetSize(80, 5)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "first prompt"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "first answer"},
		{Role: "you", Kind: tuirender.KindText, Text: "second prompt"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "second answer"},
	}, 80)
	l.SetLeadingGap(2)

	l.ScrollToBottom()

	fullLines := strings.Split(l.FullContent(), "\n")
	want := strings.Join(fullLines[len(fullLines)-5:], "\n")
	if got := l.View(); got != want {
		t.Fatalf("expected bottom view to match full content tail with leading gap\ngot:\n%s\nwant:\n%s", xansi.Strip(got), xansi.Strip(want))
	}
	if !l.AtBottom() {
		t.Fatalf("expected list to report bottom after ScrollToBottom")
	}
}

func TestChatListTotalLineCountMatchesFullContent(t *testing.T) {
	var l chatList
	l.SetSize(80, 24)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "run tests"},
		{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\nok"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Done."},
		{Role: "you", Kind: tuirender.KindText, Text: "next task"},
	}, 80)

	full := l.FullContent()
	if got, want := l.TotalLineCount(), renderedLineCount(full); got != want {
		t.Fatalf("expected TotalLineCount to match FullContent lines, got %d want %d\n%s", got, want, xansi.Strip(full))
	}
}

func TestChatListScrollToBottomWithConversationGapShowsTail(t *testing.T) {
	var l chatList
	l.SetSize(80, 5)
	l.SetMessages([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "first prompt"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "first answer"},
		{Role: "you", Kind: tuirender.KindText, Text: "second prompt"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "second answer"},
	}, 80)

	l.ScrollToBottom()

	fullLines := strings.Split(l.FullContent(), "\n")
	want := strings.Join(fullLines[len(fullLines)-5:], "\n")
	if got := l.View(); got != want {
		t.Fatalf("expected bottom view to match full content tail\ngot:\n%s\nwant:\n%s", xansi.Strip(got), xansi.Strip(want))
	}
	if !l.AtBottom() {
		t.Fatalf("expected list to report bottom after ScrollToBottom")
	}
}

func TestChatTailMessagesForViewIncludesConversationGapInLineEstimate(t *testing.T) {
	var m model
	messages := make([]tuirender.UIMessage, 0, 20)
	for range 10 {
		messages = append(messages,
			tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "next prompt"},
			tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: strings.Repeat("answer segment ", 80)},
		)
	}
	tail := m.chatTailMessagesForView(messages, 80, 1)
	if len(tail) == len(messages) {
		t.Fatalf("expected tail pruning for small viewport")
	}

	var l chatList
	l.SetSize(80, 200)
	l.SetMessages(tail, 80)
	lineLimit := max(chatTailRenderLineFloor, 1*4)
	if got := l.TotalLineCount(); got > lineLimit {
		t.Fatalf("expected tail line count to respect limit including conversation gaps, got %d limit %d", got, lineLimit)
	}
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func countPlainBlankLinesAfterItem(items []chatItem, idx int, content string) int {
	itemLineCount := 0
	for i := 0; i <= idx && i < len(items); i++ {
		itemLineCount += len(items[i].lines)
		if i < idx {
			itemLineCount += chatListGapAfterItems(items, i)
		}
	}
	lines := strings.Split(content, "\n")
	count := 0
	for _, line := range lines[itemLineCount : itemLineCount+chatListGapAfterItems(items, idx)] {
		if strings.TrimSpace(xansi.Strip(line)) == "" && line == xansi.Strip(line) {
			count++
		}
	}
	return count
}
