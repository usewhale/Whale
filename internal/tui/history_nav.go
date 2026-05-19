package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) shouldHandleHistoryNavigation() bool {
	if len(m.promptHistory) == 0 {
		return false
	}
	text := m.input.Value()
	if text == "" {
		return true
	}
	if !m.inHistoryNav || text != m.lastHistoryText {
		return false
	}
	return m.input.AtStart() || m.input.AtEnd()
}

func (m *model) historyPrev() bool {
	if len(m.promptHistory) == 0 {
		return false
	}
	if m.historyIndex == -1 {
		m.historyDraft = m.input.Value()
	}
	next := m.historyIndex + 1
	if next >= len(m.promptHistory) {
		return false
	}
	m.historyIndex = next
	idx := len(m.promptHistory) - 1 - m.historyIndex
	entry := m.promptHistory[idx]
	m.input.SetValue(entry)
	m.input.SetCursorEnd()
	m.lastHistoryText = entry
	m.inHistoryNav = true
	m.updateSlashMatches()
	return true
}

func (m *model) historyNext() bool {
	if m.historyIndex < 0 {
		return false
	}
	next := m.historyIndex - 1
	if next < 0 {
		m.input.SetValue(m.historyDraft)
		m.input.SetCursorEnd()
		m.historyIndex = -1
		m.lastHistoryText = ""
		m.inHistoryNav = false
		m.updateSlashMatches()
		return true
	}
	m.historyIndex = next
	idx := len(m.promptHistory) - 1 - m.historyIndex
	entry := m.promptHistory[idx]
	m.input.SetValue(entry)
	m.input.SetCursorEnd()
	m.lastHistoryText = entry
	m.inHistoryNav = true
	m.updateSlashMatches()
	return true
}

func (m *model) resetHistoryNavigation() {
	m.historyIndex = -1
	m.historyDraft = ""
	m.lastHistoryText = ""
	m.inHistoryNav = false
}

func (m *model) recordPromptHistory(value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if n := len(m.promptHistory); n > 0 && m.promptHistory[n-1] == value {
		return
	}
	m.promptHistory = append(m.promptHistory, value)
}

func (m *model) handleViewportScrollKey(key string) tea.Cmd {
	if m.page != pageChat {
		m.refreshViewportContent()
		switch key {
		case "pgup":
			m.viewport.ViewUp()
		case "pgdown":
			m.viewport.ViewDown()
		case "ctrl+u":
			m.viewport.HalfViewUp()
		case "ctrl+d":
			m.viewport.HalfViewDown()
		case "home":
			m.viewport.GotoTop()
		case "end":
			m.viewport.GotoBottom()
		}
		return nil
	}
	switch key {
	case "pgup", "ctrl+u", "home":
		if m.followTail && !m.viewportFrozen {
			m.refreshViewportContentFollow(true)
		} else {
			m.refreshViewportContent()
		}
	default:
		m.refreshViewportContent()
	}
	switch key {
	case "pgup":
		if m.busy {
			wasFollowingLiveTail := m.followTail && !m.viewportFrozen
			m.freezeChatViewport()
			if wasFollowingLiveTail {
				m.followTail = false
				break
			}
		}
		m.chat.PageUp()
		m.followTail = false
	case "pgdown":
		m.chat.PageDown()
		m.followTail = m.chat.AtBottom()
		if m.followTail {
			return m.resumeChatTail()
		}
	case "ctrl+u":
		if m.busy {
			wasFollowingLiveTail := m.followTail && !m.viewportFrozen
			m.freezeChatViewport()
			if wasFollowingLiveTail {
				m.followTail = false
				break
			}
		}
		m.chat.HalfPageUp()
		m.followTail = false
	case "ctrl+d":
		m.chat.HalfPageDown()
		m.followTail = m.chat.AtBottom()
		if m.followTail {
			return m.resumeChatTail()
		}
	case "home":
		if m.busy {
			m.freezeChatViewport()
		}
		m.chat.ScrollToTop()
		m.followTail = false
	case "end":
		return m.resumeChatTail()
	}
	m.syncViewportFromChat()
	return nil
}

func (m *model) resumeChatTail() tea.Cmd {
	if m.page != pageChat {
		m.viewport.GotoBottom()
		return nil
	}
	m.unfreezeChatViewport()
	m.followTail = true
	m.refreshViewportContentFollow(true)
	return m.flushNativeScrollbackCmd()
}
