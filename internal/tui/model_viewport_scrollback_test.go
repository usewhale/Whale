package tui

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func TestClearScreenCmdPreservesUnixScrollbackClear(t *testing.T) {
	var out bytes.Buffer
	cmd := clearScreenCmdForOS("linux", &out)
	msg := cmd()
	if msg == nil {
		t.Fatal("unix clear should also return a Bubble Tea clear-screen message")
	}
	if got, want := out.String(), "\033[H\033[2J\033[3J"; got != want {
		t.Fatalf("unix clear sequence = %q, want %q", got, want)
	}
}
func TestScrollbackTextRendersUserMessage(t *testing.T) {
	m := model{width: 80, height: 24}
	got := m.scrollbackText([]tuirender.UIMessage{{
		Role: "you",
		Kind: tuirender.KindText,
		Text: "hello whale",
	}})
	if !strings.Contains(got, "hello whale") {
		t.Fatalf("expected user text in scrollback output, got %q", got)
	}
}
func TestScrollbackTextUsesChatListConversationGaps(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	got := xansi.Strip(m.scrollbackText([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "hi"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Hello! How can I help you today?"},
		{Role: "you", Kind: tuirender.KindText, Text: "who are you"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "I'm Whale."},
	}))

	if blanks := countBlankLinesBetweenText(t, got, "hi", "Hello! How can I help you today?"); blanks < chatListGap {
		t.Fatalf("expected native scrollback user-to-assistant gap, got %d blank lines:\n%s", blanks, got)
	}
	var l chatList
	l.SetMessages(m.focusMessages([]tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "hi"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Hello! How can I help you today?"},
		{Role: "you", Kind: tuirender.KindText, Text: "who are you"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "I'm Whale."},
	}), m.chatRenderWidth())
	if got := countPlainBlankLinesAfterItem(l.items, 1, l.FullContent()); got != 2 {
		t.Fatalf("expected native scrollback assistant-to-user inserted gap 2, got %d blank lines:\n%s", got, xansi.Strip(l.FullContent()))
	}
}

func TestNativeScrollbackFlushPreservesConversationGapAcrossFlushes(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "hi"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "hello"},
	}

	first := m.emitNativeScrollbackCmd()
	if first == nil {
		t.Fatal("expected first native scrollback flush")
	}
	firstPrinted := xansi.Strip(teaPrintLineMessageBody(first()))
	if blanks := countBlankLinesBetweenText(t, firstPrinted, "hi", "hello"); blanks < chatListGap {
		t.Fatalf("expected first flush user-to-assistant gap, got %d blank lines:\n%s", blanks, firstPrinted)
	}

	m.transcript = append(m.transcript, tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "next"})
	second := m.emitNativeScrollbackCmd()
	if second == nil {
		t.Fatal("expected second native scrollback flush")
	}
	secondPrinted := xansi.Strip(teaPrintLineMessageBody(second()))
	if got := leadingNewlines(secondPrinted); got != 2 {
		t.Fatalf("expected second flush to start with assistant-to-user gap 2, got %d blank lines:\n%q", got, secondPrinted)
	}
	if strings.Contains(secondPrinted, "hello") {
		t.Fatalf("second flush must not reprint previous assistant message:\n%s", secondPrinted)
	}

	m.transcript = append(m.transcript, tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: "answer"})
	third := m.emitNativeScrollbackCmd()
	if third == nil {
		t.Fatal("expected third native scrollback flush")
	}
	thirdPrinted := xansi.Strip(teaPrintLineMessageBody(third()))
	if got := leadingNewlines(thirdPrinted); got != chatListGapAfter(m.transcript[2], m.transcript[3]) {
		t.Fatalf("expected third flush to start with user-to-assistant gap, got %d blank lines:\n%q", got, thirdPrinted)
	}
}

func TestNativeScrollbackFlushDoesNotPersistComposerGap(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "off")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "hello"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Hello! How can I help you today?"},
	}

	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}
	printed := teaPrintLineMessageBody(cmd())
	if strings.HasSuffix(printed, "\n") {
		t.Fatalf("expected native scrollback flush not to persist composer gap, got %#v", printed)
	}
}

func TestChatBottomOnlyFrameLeavesTransientGapAboveComposer(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "off")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "hello"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Hello! How can I help you today?"},
	}
	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}

	view := xansi.Strip(m.View())
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	boundaryIdx := firstFullWidthBoundaryLine(lines, 80)
	if boundaryIdx != 1 {
		t.Fatalf("expected transient blank row above composer boundary, got boundary=%d in:\n%s", boundaryIdx, view)
	}
	if strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("expected first live frame row to be blank, got %q in:\n%s", lines[0], view)
	}
}

func TestChatBottomOnlyFrameDoesNotAddGapWhileBusyAfterUserPrompt(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "off")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{{Role: "you", Kind: tuirender.KindText, Text: "cool"}}
	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}
	m.startBusy()

	view := xansi.Strip(m.View())
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	workingIdx := firstLineContaining(lines, "Working (")
	boundaryIdx := firstFullWidthBoundaryLine(lines, 80)
	if workingIdx != 0 {
		t.Fatalf("expected busy bottom-only frame to start with working status, got working=%d in:\n%s", workingIdx, view)
	}
	if boundaryIdx != 1 {
		t.Fatalf("expected composer boundary immediately after working status, got boundary=%d in:\n%s", boundaryIdx, view)
	}
}

func TestChatLiveViewportKeepsAssistantToUserGapAfterNativeScrollbackTrim(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "off")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "hello"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "Hello again! What's on your mind?"},
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.transcript = append(m.transcript, tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "kaka"})
	m.startBusy()

	view := xansi.Strip(m.View())
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	promptIdx := firstLineContaining(lines, "kaka")
	if promptIdx < 0 {
		t.Fatalf("expected live user prompt in busy view:\n%s", view)
	}
	if promptIdx < 3 {
		t.Fatalf("expected assistant-to-user leading gap before trimmed live prompt, got prompt index %d in:\n%s", promptIdx, view)
	}
	for i := 0; i < promptIdx; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			t.Fatalf("expected leading line %d before prompt to be blank, got %q in:\n%s", i, lines[i], view)
		}
	}
}

func TestChatBottomOnlyFrameDoesNotAddGapAfterUserTail(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "off")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{{Role: "you", Kind: tuirender.KindText, Text: "cool"}}
	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}

	view := xansi.Strip(m.View())
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if boundaryIdx := firstFullWidthBoundaryLine(lines, 80); boundaryIdx != 0 {
		t.Fatalf("expected user-tail bottom-only frame to omit transient gap, got boundary=%d in:\n%s", boundaryIdx, view)
	}
}

func TestChatBottomOnlyFrameGapDoesNotOverflowConstrainedHeight(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "off")
	m.width = 80
	m.height = countVisibleLines(m.renderBottom(80))
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{{Role: "assistant", Kind: tuirender.KindText, Text: "tight response"}}

	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}
	view := xansi.Strip(m.View())
	if got := countVisibleLines(view); got > m.height {
		t.Fatalf("expected constrained bottom-only view not to exceed height %d, got %d:\n%s", m.height, got, view)
	}
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if boundaryIdx := firstFullWidthBoundaryLine(lines, 80); boundaryIdx != 0 {
		t.Fatalf("expected constrained bottom-only view to omit transient gap, got boundary=%d in:\n%s", boundaryIdx, view)
	}
}

func TestNativeScrollbackReplayPreservesConversationGapAcrossStartBoundary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{
		{Role: "you", Kind: tuirender.KindText, Text: "first"},
		{Role: "assistant", Kind: tuirender.KindText, Text: "reply"},
		{Role: "you", Kind: tuirender.KindText, Text: "second"},
	}
	m.nativeScrollbackPrinted = 2

	cmd := m.replayNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback replay")
	}
	printed := xansi.Strip(teaPrintLineMessageBody(cmd()))
	if got := leadingNewlines(printed); got != chatListGapAfter(m.transcript[1], m.transcript[2]) {
		t.Fatalf("expected replay to start with assistant-to-user gap, got %d blank lines:\n%q", got, printed)
	}
	if strings.Contains(printed, "reply") {
		t.Fatalf("replay from start boundary must not reprint previous assistant message:\n%s", printed)
	}
}

func TestNativeScrollbackUsesPreviousRenderedMessageAcrossHiddenFocusBoundary(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.viewMode = protocol.ViewModeFocus
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{
		{Role: "assistant", Kind: tuirender.KindText, Text: "previous answer"},
		{Role: "think", Kind: tuirender.KindThinking, Text: "hidden thought"},
		{Role: "you", Kind: tuirender.KindText, Text: "next prompt"},
	}
	m.nativeScrollbackPrinted = 2

	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}
	printed := xansi.Strip(teaPrintLineMessageBody(cmd()))
	if got := leadingNewlines(printed); got != chatListGapAfter(m.transcript[0], m.transcript[2]) {
		t.Fatalf("expected hidden focus boundary to use previous rendered assistant gap, got %d blank lines:\n%q", got, printed)
	}
	if strings.Contains(printed, "hidden thought") || strings.Contains(printed, "previous answer") {
		t.Fatalf("flush must not print hidden or previous context messages:\n%s", printed)
	}
}

func TestNativeScrollbackStartZeroDoesNotAddLeadingGap(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	*m.startupHeaderOnce = true
	m.transcript = []tuirender.UIMessage{{Role: "you", Kind: tuirender.KindText, Text: "first"}}

	cmd := m.emitNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected native scrollback flush")
	}
	printed := xansi.Strip(teaPrintLineMessageBody(cmd()))
	if strings.HasPrefix(printed, "\n") {
		t.Fatalf("expected start==0 flush not to add leading gap, got %q", printed)
	}
}

func TestNativeScrollbackCrossFlushToolBoundariesKeepDefaultGap(t *testing.T) {
	for _, tc := range []struct {
		name string
		prev tuirender.UIMessage
		next tuirender.UIMessage
	}{
		{
			name: "assistant to tool",
			prev: tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: "I'll run it."},
			next: tuirender.UIMessage{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\nok"},
		},
		{
			name: "tool to assistant",
			prev: tuirender.UIMessage{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\nok"},
			next: tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: "Done."},
		},
		{
			name: "you to tool",
			prev: tuirender.UIMessage{Role: "you", Kind: tuirender.KindText, Text: "run tests"},
			next: tuirender.UIMessage{Role: "shell_result_ok", Kind: tuirender.KindToolResult, Text: "Ran make test\nok"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(nil, "", "", "")
			m.width = 80
			m.height = 24
			*m.startupHeaderOnce = true
			m.transcript = []tuirender.UIMessage{tc.prev}
			m.nativeScrollbackPrinted = 1
			m.transcript = append(m.transcript, tc.next)

			cmd := m.emitNativeScrollbackCmd()
			if cmd == nil {
				t.Fatal("expected native scrollback flush")
			}
			printed := xansi.Strip(teaPrintLineMessageBody(cmd()))
			if got := leadingNewlines(printed); got != chatListGap {
				t.Fatalf("expected default cross-flush gap %d, got %d blank lines:\n%q", chatListGap, got, printed)
			}
		})
	}
}

func leadingNewlines(text string) int {
	for i, r := range text {
		if r != '\n' {
			return i
		}
	}
	return len(text)
}

func TestChatIdleViewDoesNotRenderEmptyViewportFrame(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	view := m.View()
	if strings.Contains(view, "┌") {
		t.Fatalf("idle chat view should not render an empty bordered viewport:\n%s", view)
	}
	if !strings.Contains(view, "Type message or command") {
		t.Fatalf("expected composer placeholder in idle view:\n%s", view)
	}
	if strings.Contains(view, "status: ready") {
		t.Fatalf("idle view should not render ready status in footer:\n%s", view)
	}
	if strings.Contains(view, "Working (") || strings.Contains(view, "Stopping (") {
		t.Fatalf("idle view should not render busy status line:\n%s", view)
	}
}
func TestChatStartupHeaderStaysOutOfViewportAfterFirstPrompt(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.model = "deepseek-v4-flash"
	m.effort = "max"
	m.thinking = "off"
	m.width = 80
	m.height = 24
	m.startupHeaderPrintCmd()
	m.input.SetValue("hi")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	view := m.View()
	if strings.Contains(view, "███████╗") {
		t.Fatalf("expected printed startup header not to repeat in live viewport after first prompt:\n%s", view)
	}
	if !strings.Contains(view, "hi") {
		t.Fatalf("expected first prompt in view:\n%s", view)
	}
}
func TestChatViewportScrollsTranscript(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 20; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	atBottom := m.View()
	if strings.Contains(atBottom, "entry-00") || !strings.Contains(atBottom, "entry-19") {
		t.Fatalf("expected bottom view to show tail only:\n%s", atBottom)
	}

	m.handleViewportScrollKey("home")
	atTop := m.View()
	if !strings.Contains(atTop, "WHALE") {
		t.Fatalf("expected home to scroll chat transcript to startup header:\n%s", atTop)
	}
}
func TestPgUpPgDownScrollTranscriptHomeEndStayOnComposer(t *testing.T) {
	// PgUp/PgDn remain the transcript-scroll keys. Home/End are now readline
	// line-start/end on the composer (they used to jump the transcript to
	// the extremes; that behavior moved to the explicit PgUp/PgDn flow).
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	bottomOffset := m.viewport.YOffset
	if bottomOffset == 0 {
		t.Fatal("expected transcript to be scrollable")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.viewport.YOffset >= bottomOffset {
		t.Fatalf("expected PageUp to scroll transcript up, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}
	scrolledOffset := m.viewport.YOffset

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewport.YOffset != bottomOffset {
		t.Fatalf("expected PageDown to return to bottom, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}

	// Scroll partway up so we can detect any accidental viewport movement
	// from Home/End — they should NOT touch the transcript anymore.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	preHomeOffset := m.viewport.YOffset
	preHomeFollow := m.followTail
	m.input.SetValue("draft")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if m.viewport.YOffset != preHomeOffset {
		t.Fatalf("expected Home not to scroll transcript, offset=%d want %d", m.viewport.YOffset, preHomeOffset)
	}
	if m.followTail != preHomeFollow {
		t.Fatalf("expected Home not to toggle followTail, got %v want %v", m.followTail, preHomeFollow)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if m.viewport.YOffset != preHomeOffset {
		t.Fatalf("expected End not to scroll transcript, offset=%d want %d", m.viewport.YOffset, preHomeOffset)
	}
	if m.followTail != preHomeFollow {
		t.Fatalf("expected End not to toggle followTail, got %v want %v", m.followTail, preHomeFollow)
	}
	if m.input.Value() != "draft" {
		t.Fatalf("expected composer value preserved across Home/End (cursor moves only), got %q", m.input.Value())
	}
	// Keep referencing scrolledOffset so the partially-scrolled state above
	// is acknowledged as the precondition for the no-scroll checks.
	_ = scrolledOffset
}
func TestMouseEventsDoNotDriveTerminalNativeTUI(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.input.SetValue("typed")
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	bottomOffset := m.viewport.YOffset
	if bottomOffset == 0 {
		t.Fatal("expected transcript to be scrollable")
	}

	next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = next.(model)
	if m.viewport.YOffset != bottomOffset {
		t.Fatalf("expected mouse wheel to be ignored by Whale, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}

	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 2, Y: 2})
	m = next.(model)
	if got := m.input.Value(); got != "typed" {
		t.Fatalf("expected mouse press not to mutate composer, got %q", got)
	}
}
func TestMouseCSIFragmentsDoNotEnterComposer(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.startBusy()

	for _, fragment := range []string{
		"[<65;69;14M",
		"[<65;54;25M[<65;54;25M",
		"\x1b[<64;10;10M",
	} {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(fragment)})
		m = next.(model)
		if got := m.input.Value(); got != "" {
			t.Fatalf("expected mouse CSI fragment %q not to enter composer, got %q", fragment, got)
		}
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	m = next.(model)
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("expected normal input to still enter composer, got %q", got)
	}
}
func TestSplitMouseCSIFragmentDoesNotEnterComposer(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.startBusy()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("["), Alt: true})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<65;69;14M")})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected split mouse CSI fragment not to enter composer, got %q", got)
	}
}
func TestChatViewportFrozenBatchDeltasDoNotRedrawKeyboardScrolledView(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if !m.viewportFrozen {
		t.Fatal("expected PageUp during busy output to freeze chat viewport")
	}
	frozenView := m.viewport.View()

	events := make([]protocol.Event, 0, 20)
	for i := 0; i < 20; i++ {
		events = append(events, protocol.Event{Kind: protocol.EventAssistantDelta, Text: fmt.Sprintf("batched-tail-%02d\n", i)})
	}
	next, _ = m.Update(svcBatchMsg(events))
	m = next.(model)
	if got := m.viewport.View(); got != frozenView {
		t.Fatalf("expected batched live deltas not to redraw scrolled viewport while frozen\nbefore:\n%s\n\nafter:\n%s", frozenView, got)
	}
	if strings.Contains(m.View(), "batched-tail-19") {
		t.Fatalf("expected hidden batched tail not to cover frozen viewport:\n%s", m.View())
	}
}
func TestAppendBatchedServiceEventMergesAdjacentDeltas(t *testing.T) {
	events := []protocol.Event{}
	events = appendBatchedServiceEvent(events, protocol.Event{Kind: protocol.EventAssistantDelta, Text: "a"})
	events = appendBatchedServiceEvent(events, protocol.Event{Kind: protocol.EventAssistantDelta, Text: "b"})
	events = appendBatchedServiceEvent(events, protocol.Event{Kind: protocol.EventReasoningDelta, Text: "c"})
	events = appendBatchedServiceEvent(events, protocol.Event{Kind: protocol.EventReasoningDelta, Text: "d"})
	events = appendBatchedServiceEvent(events, protocol.Event{Kind: protocol.EventTurnDone, Text: "done"})

	if len(events) != 3 {
		t.Fatalf("expected adjacent deltas to merge into 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Kind != protocol.EventAssistantDelta || events[0].Text != "ab" {
		t.Fatalf("unexpected merged assistant delta: %+v", events[0])
	}
	if events[1].Kind != protocol.EventReasoningDelta || events[1].Text != "cd" {
		t.Fatalf("unexpected merged reasoning delta: %+v", events[1])
	}
	if events[2].Kind != protocol.EventTurnDone {
		t.Fatalf("expected non-delta event to remain separate, got %+v", events[2])
	}
}
func TestChatViewportIdleFollowTailUsesTailRenderWindow(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.startupHeaderPrintCmd()
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.refreshViewportContentFollow(false)

	lineLimit := max(chatTailRenderLineFloor, m.viewportBodyHeight(m.width)*4)
	if lines := m.viewport.TotalLineCount(); lines > lineLimit {
		t.Fatalf("expected idle tail-follow render to keep a bounded line window, got %d lines", lines)
	}
	view := m.View()
	if strings.Contains(view, "entry-000") {
		t.Fatalf("expected old transcript lines to be outside the idle tail render window:\n%s", view)
	}
	if !strings.Contains(view, "entry-199") {
		t.Fatalf("expected latest transcript lines to remain visible in idle tail render window:\n%s", view)
	}
}
func TestChatViewportTailRenderBoundedWithAlternatingToolAndAssistant(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.startupHeaderPrintCmd()
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("tool", tuirender.KindToolCall, fmt.Sprintf("tool-call-%03d", i))
		m.appendTranscript("tool", tuirender.KindToolResult, fmt.Sprintf("tool-result-%03d", i))
		m.appendTranscript("assistant", tuirender.KindText, fmt.Sprintf("assistant-reply-%03d", i))
	}
	m.refreshViewportContentFollow(false)

	lineLimit := max(chatTailRenderLineFloor, m.viewportBodyHeight(m.width)*4)
	if lines := m.viewport.TotalLineCount(); lines > lineLimit {
		t.Fatalf("alternating tool/assistant tail render should stay within %d lines (work separators included), got %d", lineLimit, lines)
	}
}
func TestChatViewportHomeFromIdleTailRenderRestoresFullHistory(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.startupHeaderPrintCmd()
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.refreshViewportContentFollow(false)
	tailLines := m.viewport.TotalLineCount()

	// handleViewportScrollKey is still the underlying scroll-to-top primitive
	// (used directly by the diff page and intended for future programmatic
	// callers). The Home key no longer routes here in chat mode after the
	// readline alignment, but the function's invariants must hold.
	m.handleViewportScrollKey("home")
	if m.followTail {
		t.Fatal("expected scroll-to-top from idle tail render to disable tail following")
	}
	if fullLines := m.viewport.TotalLineCount(); fullLines <= tailLines {
		t.Fatalf("expected scroll-to-top from idle tail render to restore full scrollable content, tail=%d full=%d", tailLines, fullLines)
	}
	view := m.View()
	if !strings.Contains(view, "entry-000") {
		t.Fatalf("expected scroll-to-top from idle tail render to restore early history:\n%s", view)
	}
	if strings.Contains(view, "entry-199") {
		t.Fatalf("expected scroll-to-top from idle tail render to move away from the latest tail:\n%s", view)
	}
}
func TestChatViewportEndUnfreezesLiveOutputAndReturnsToTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	for i := 0; i < 30; i++ {
		m.append("assistant", fmt.Sprintf("live-tail-%02d\n", i))
	}

	// handleViewportScrollKey is still the unfreeze-and-resume-tail primitive
	// for the chat page. The End key no longer routes here in chat mode after
	// the readline alignment, but the underlying viewport invariants —
	// unfreezing the frozen state and restoring tail-follow — must hold.
	m.handleViewportScrollKey("end")
	if m.viewportFrozen {
		t.Fatal("expected scroll-to-tail to unfreeze chat viewport")
	}
	if !m.followTail {
		t.Fatal("expected scroll-to-tail to re-enable tail following")
	}
	view := m.View()
	if !strings.Contains(view, "live-tail-29") {
		t.Fatalf("expected scroll-to-tail to reveal latest live tail:\n%s", view)
	}
}

// When most of the transcript has been emitted to native scrollback
// (nativeScrollbackPrinted near len(transcript)), PgUp from the live tail
// must reveal the older transcript that is still semantically part of the
// session — not just the trimmed transcript[nativeScrollbackPrinted:]
// window. Previously PgUp scrolled within that trimmed view and landed at
// the startup banner (when start==0) or at very recent entries only.
func TestPageUpAtTailRevealsTranscriptAboveScrollbackBoundary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 20
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	// Simulate that all but the last two entries have already been written
	// to terminal native scrollback (the common state after a couple of
	// completed turns).
	m.nativeScrollbackPrinted = len(m.transcript) - 2
	m.followTail = true
	m.refreshViewportContentFollow(true)
	if !strings.Contains(m.View(), "entry-39") {
		t.Fatalf("precondition: expected tail view to show latest entry, got:\n%s", m.View())
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected PageUp to leave followTail mode")
	}
	view := m.View()
	// PageUp from the tail should expose entries above the scrollback
	// boundary (entries with index < 38), not just the two trimmed entries.
	if !regexp.MustCompile(`entry-3[0-7]`).MatchString(view) {
		t.Fatalf("expected PageUp from tail to expose transcript above the scrollback boundary, got:\n%s", view)
	}
	if strings.Contains(view, "WHALE") {
		t.Fatalf("expected PageUp from tail not to jump to the startup banner, got:\n%s", view)
	}
}
func TestChatViewportResizeKeepsTailWhenFollowing(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 18
	m.sizeMsgReceived = true
	m.transcript = nil
	for i := 0; i < 50; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	if !m.followTail || !m.viewport.AtBottom() {
		t.Fatalf("expected chat to start following tail, follow=%v bottom=%v", m.followTail, m.viewport.AtBottom())
	}

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	// Resize wipes terminal scrollback (so it can't accumulate ghost frames
	// from terminal-side reflow). The replay command re-emits the header and
	// the full transcript into the now-clean scrollback so the user still
	// sees their history; the live View itself only carries the composer and
	// footer in this state.
	if cmd == nil {
		t.Fatal("expected resize to schedule a scrollback replay")
	}
	replay := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(replay, "entry-49") {
		t.Fatalf("expected resize scrollback replay to include tail, got %s", replay)
	}
	if !strings.Contains(replay, "entry-00") {
		t.Fatalf("expected resize scrollback replay to include head, got %s", replay)
	}
	if !m.followTail {
		t.Fatal("expected resize to keep follow-tail mode")
	}
}
func TestChatViewportResizePreservesUserScrollPosition(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 18
	m.sizeMsgReceived = true
	m.transcript = nil
	for i := 0; i < 50; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)

	m.handleViewportScrollKey("home")
	if m.followTail {
		t.Fatal("expected Home to disable tail following")
	}
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	// Even though followTail is false (scrolled up), the resize-wiped
	// scrollback must be replayed with the entire transcript so the user
	// does not lose history accessibility. flushNativeScrollbackCmd would
	// short-circuit here; replayNativeScrollbackCmd is the right path.
	if cmd == nil {
		t.Fatal("expected resize while scrolled up to schedule a scrollback replay")
	}
	replay := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(replay, "entry-00") || !strings.Contains(replay, "entry-49") {
		t.Fatalf("expected scrollback replay to include entire transcript, got %s", replay)
	}
	view := m.View()
	if !strings.Contains(view, "entry-00") {
		t.Fatalf("expected resized scrolled-up view to preserve top position at first transcript entry:\n%s", view)
	}
	if strings.Contains(view, "entry-49") {
		t.Fatalf("expected resized scrolled-up view not to jump to tail:\n%s", view)
	}

	m.handleViewportScrollKey("end")
	if !m.followTail {
		t.Fatal("expected End to re-enable tail following")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = next.(model)
	view = m.View()
	if strings.Contains(view, "entry-49") {
		t.Fatalf("expected printed tail content not to repeat in viewport after End:\n%s", view)
	}
}
func TestNativeScrollbackSkipsHeaderAndPrintsNewTranscriptOnce(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10

	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected startup chrome header not to enter native scrollback")
	}

	m.appendTranscript("you", tuirender.KindText, "hello native scrollback")
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected new transcript entry to produce a native scrollback print command")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "hello native scrollback") || !strings.Contains(got, "WHALE") {
		t.Fatalf("expected printed message to include startup header and transcript text, got %s", got)
	}
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected transcript entry not to be printed twice")
	}
}
func TestPrintedNativeScrollbackIsNotRepeatedInTailViewport(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.width = 80
	m.height = 24
	m.startupHeaderPrintCmd()
	m.appendTranscript("you", tuirender.KindText, "hi")
	m.appendTranscript("think", tuirender.KindThinking, "thinking once")
	m.appendTranscript("assistant", tuirender.KindText, "hello once")
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected committed turn to print to native scrollback")
	}

	view := m.View()
	for _, repeated := range []string{"thinking once", "hello once"} {
		if strings.Contains(view, repeated) {
			t.Fatalf("expected printed transcript %q not to repeat in tail viewport:\n%s", repeated, view)
		}
	}
	if strings.Contains(view, "███████╗") || strings.Contains(view, "WHALE") {
		t.Fatalf("expected printed startup header not to repeat in tail viewport:\n%s", view)
	}

	m.startBusy()
	m.append("assistant", "live tail")
	view = m.View()
	if !strings.Contains(view, "live tail") {
		t.Fatalf("expected uncommitted live output to remain visible:\n%s", view)
	}
}
func TestFirstNativeScrollbackFlushKeepsStartupHeaderVisibleOutsideViewport(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.width = 80
	m.height = 24
	headerCmd := m.startupHeaderPrintCmd()
	if headerCmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}
	headerPrinted := fmt.Sprintf("%#v", headerCmd())
	for _, want := range []string{"███████", "version:"} {
		if !strings.Contains(headerPrinted, want) {
			t.Fatalf("expected startup header print to include %q, got %s", want, headerPrinted)
		}
	}
	m.appendTranscript("you", tuirender.KindText, "hi")
	m.appendTranscript("assistant", tuirender.KindText, "hello once")

	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected first committed turn to print to native scrollback")
	}
	printed := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(printed, "hello once") {
		t.Fatalf("expected first native scrollback flush to include transcript content, got %s", printed)
	}
	if strings.Contains(printed, "███████") {
		t.Fatalf("expected startup header not to be reprinted in the transcript flush, got %s", printed)
	}

	view := m.View()
	for _, repeated := range []string{"███████", "hello once"} {
		if strings.Contains(view, repeated) {
			t.Fatalf("expected first native scrollback content %q not to repeat in tail viewport:\n%s", repeated, view)
		}
	}
}
func TestNativeScrollbackWaitsWhileChatIsScrolledUpAndFlushesAtTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	printed := m.nativeScrollbackPrinted
	m.appendTranscript("assistant", tuirender.KindText, "pending native scrollback")
	m.followTail = false

	cmd := m.flushNativeScrollbackCmd()
	if cmd != nil {
		t.Fatal("expected scrolled-up chat viewport not to print native scrollback")
	}
	if m.nativeScrollbackPrinted != printed {
		t.Fatalf("expected native scrollback cursor to remain at %d, got %d", printed, m.nativeScrollbackPrinted)
	}

	cmd = m.resumeChatTail()
	if cmd == nil {
		t.Fatal("expected returning to tail to flush delayed native scrollback")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "pending native scrollback") {
		t.Fatalf("expected delayed native scrollback output, got %s", got)
	}
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected delayed native scrollback not to be printed twice")
	}
}
func TestNativeScrollbackWaitsWhileChatViewportFrozenAndFlushesAtTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	printed := m.nativeScrollbackPrinted
	m.appendTranscript("assistant", tuirender.KindText, "pending frozen scrollback")
	m.viewportFrozen = true

	cmd := m.flushNativeScrollbackCmd()
	if cmd != nil {
		t.Fatal("expected frozen chat viewport not to print native scrollback")
	}
	if m.nativeScrollbackPrinted != printed {
		t.Fatalf("expected native scrollback cursor to remain at %d, got %d", printed, m.nativeScrollbackPrinted)
	}

	cmd = m.resumeChatTail()
	if cmd == nil {
		t.Fatal("expected returning to tail to flush delayed frozen native scrollback")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "pending frozen scrollback") {
		t.Fatalf("expected delayed native scrollback output, got %s", got)
	}
}
func TestChatLiveViewRendersWithoutViewportFrame(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.append("assistant", "streamed answer")
	view := m.View()
	if !strings.Contains(view, "streamed answer") {
		t.Fatalf("expected live assistant text in view:\n%s", view)
	}
	if strings.Contains(view, "┌") {
		t.Fatalf("live chat view should not render bordered viewport:\n%s", view)
	}
}
func TestChatLiveViewUsesViewportForLongOutput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.append("assistant", strings.Repeat("line\n", 80))
	view := m.View()
	if !strings.Contains(view, "Type message or command") {
		t.Fatalf("expected composer to remain visible with long live output:\n%s", view)
	}
	if got := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; got != m.height {
		t.Fatalf("expected view to keep terminal height %d, got %d:\n%s", m.height, got, view)
	}
}
func TestUnmatchedToolResultRefreshesLiveViewportWhilePendingCallsRemain(t *testing.T) {
	m := model{
		assembler:  tuirender.NewAssembler(),
		mode:       modeChat,
		page:       pageChat,
		width:      100,
		height:     16,
		followTail: true,
	}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:       protocol.EventToolCall,
		ToolCallID: "tc-1",
		ToolName:   "shell_run",
		Text:       `shell_run: {"command":"echo first"}`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:       protocol.EventToolCall,
		ToolCallID: "tc-2",
		ToolName:   "shell_run",
		Text:       `shell_run: {"command":"echo second"}`,
	}))
	m = next.(model)

	beforeGeneration := m.chat.generation
	beforeView := m.View()

	raw := `{"success":true,"data":{"status":"ok","metrics":{"duration_ms":12},"payload":{"stdout":"visible output"}}}`
	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:       protocol.EventToolResult,
		ToolCallID: "missing-id",
		ToolName:   "shell_run",
		Text:       raw,
	}))
	m = next.(model)

	if m.chat.generation <= beforeGeneration {
		t.Fatalf("expected unmatched live tool result to refresh chat viewport, generation before=%d after=%d", beforeGeneration, m.chat.generation)
	}
	if got := m.View(); got == beforeView || !strings.Contains(got, "visible output") {
		t.Fatalf("expected unmatched live tool result to appear in chat viewport:\n%s", got)
	}
	if got := len(m.liveTranscriptMessages()); got != 3 {
		t.Fatalf("expected pending tool calls plus unmatched result to remain live, got %d", got)
	}
}
