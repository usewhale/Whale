package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) flushNativeScrollbackCmd() tea.Cmd {
	if m.nativeScrollbackPrinted < 0 || m.nativeScrollbackPrinted > len(m.transcript) {
		m.nativeScrollbackPrinted = len(m.transcript)
		return nil
	}
	if m.nativeScrollbackPrinted == len(m.transcript) {
		return nil
	}
	if !m.shouldFlushNativeScrollback() {
		return nil
	}
	messages := append([]tuirender.UIMessage(nil), m.transcript[m.nativeScrollbackPrinted:]...)
	m.nativeScrollbackPrinted = len(m.transcript)
	messages = m.focusMessages(messages)
	return nativeScrollbackPrintCmd(messages, m.chatRenderWidth())
}

func (m *model) shouldFlushNativeScrollback() bool {
	if m.page == pageChat && (m.viewportFrozen || !m.followTail) {
		return false
	}
	return true
}

func nativeScrollbackPrintCmd(messages []tuirender.UIMessage, width int) tea.Cmd {
	lines := tuirender.ChatLines(messages, max(20, width))
	text := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return tea.Println(text)
}
