package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) startupHeaderPrintCmd() tea.Cmd {
	if m.startupHeaderOnce == nil {
		m.startupHeaderOnce = new(bool)
	}
	if *m.startupHeaderOnce || m.page != pageChat || m.width <= 0 || m.height <= 0 || m.resumeMenu {
		return nil
	}
	header := m.startupHeaderText()
	if header == "" {
		return nil
	}
	m.startupHeaderPrinted = true
	*m.startupHeaderOnce = true
	m.viewportLayoutReady = false
	return tea.Println(header)
}

func (m model) startupHeaderText() string {
	mainWidth, _ := m.layoutDims()
	bodyHeight := m.viewportBodyHeight(mainWidth)
	if bodyHeight <= 0 {
		return ""
	}
	header := strings.TrimRight(buildHeaderBanner(m.model, m.effort, m.thinking, m.cwd, m.version, max(20, mainWidth), bodyHeight), "\n")
	if strings.TrimSpace(header) == "" {
		return ""
	}
	return header
}

// replayNativeScrollbackCmd is the resize-driven cousin of
// flushNativeScrollbackCmd: it emits the header and the full transcript into
// native scrollback regardless of follow-tail or viewport-freeze state. After
// we wipe the terminal scrollback on resize, those normal gates would
// otherwise drop the replay and leave the user with no accessible history
// until they returned to the tail.
func (m *model) replayNativeScrollbackCmd() tea.Cmd {
	if m.page != pageChat {
		return nil
	}
	start := min(max(m.nativeScrollbackPrinted, 0), len(m.transcript))
	text := ""
	if start < len(m.transcript) {
		text = m.scrollbackText(m.transcript[start:])
	}
	if start == 0 && (m.startupHeaderOnce == nil || !*m.startupHeaderOnce) {
		if header := strings.TrimSpace(m.startupHeaderText()); header != "" {
			if text != "" {
				text = header + "\n\n" + text
			} else {
				text = header
			}
			m.startupHeaderPrinted = true
			if m.startupHeaderOnce == nil {
				m.startupHeaderOnce = new(bool)
			}
			*m.startupHeaderOnce = true
		}
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return tea.Println(text)
}

func (m *model) flushNativeScrollbackCmd() tea.Cmd {
	if m.viewportFrozen || !m.followTail {
		return nil
	}
	return m.emitNativeScrollbackCmd()
}

// flushCompletedTurnToNativeScrollbackCmd emits the just-finished turn into
// native scrollback regardless of follow-tail or viewport-freeze state. Unlike
// the normal flushNativeScrollbackCmd gate, a turn that completes while the
// user is scrolled up must still reach real scrollback so the final answer is
// immediately reachable by scrolling the terminal, rather than being deferred
// (and hidden) until the user returns to the tail.
func (m *model) flushCompletedTurnToNativeScrollbackCmd() tea.Cmd {
	return m.emitNativeScrollbackCmd()
}

// emitNativeScrollbackCmd prints transcript[nativeScrollbackPrinted:] to the
// terminal's native scrollback and advances the printed cursor. Callers own
// the follow-tail/freeze gating decision.
func (m *model) emitNativeScrollbackCmd() tea.Cmd {
	if m.page != pageChat {
		return nil
	}
	start := min(max(m.nativeScrollbackPrinted, 0), len(m.transcript))
	if start >= len(m.transcript) {
		return nil
	}
	if m.focusEnabled() {
		projected := m.focusMessages(m.transcript[start:])
		if focusMessagesAreOnlyDeferredToolSummary(projected) {
			return nil
		}
	}
	text := m.scrollbackText(m.transcript[start:])
	if start == 0 && (m.startupHeaderOnce == nil || !*m.startupHeaderOnce) {
		if header := strings.TrimSpace(m.startupHeaderText()); header != "" {
			text = header + "\n\n" + text
			m.startupHeaderPrinted = true
			if m.startupHeaderOnce == nil {
				m.startupHeaderOnce = new(bool)
			}
			*m.startupHeaderOnce = true
		}
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return tea.Println(text)
}

func focusMessagesAreOnlyDeferredToolSummary(messages []tuirender.UIMessage) bool {
	if len(messages) == 0 {
		return false
	}
	for _, msg := range messages {
		if msg.Kind != tuirender.KindToolSummary {
			return false
		}
	}
	return true
}
