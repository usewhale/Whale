package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) startupHeaderPrintCmd() tea.Cmd {
	if m.startupHeaderOnce == nil {
		m.startupHeaderOnce = new(bool)
	}
	if *m.startupHeaderOnce || m.page != pageChat || m.width <= 0 || m.height <= 0 {
		return nil
	}
	header := m.startupHeaderText()
	if header == "" {
		return nil
	}
	m.startupHeaderPrinted = true
	*m.startupHeaderOnce = true
	m.viewportLayoutReady = false
	return nil
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

func (m *model) flushNativeScrollbackCmd() tea.Cmd {
	if m.page != pageChat || m.viewportFrozen || !m.followTail {
		return nil
	}
	start := min(max(m.nativeScrollbackPrinted, 0), len(m.transcript))
	if start >= len(m.transcript) {
		return nil
	}
	text := m.scrollbackText(m.transcript[start:])
	if start == 0 {
		if header := strings.TrimSpace(m.startupHeaderText()); header != "" {
			text = header + "\n\n" + text
		}
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return tea.Println(text)
}
