package tui

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

const mouseWheelScrollLines = 3

var (
	sgrMouseFragmentRE     = regexp.MustCompile(`^(?:\x1b?\[<\d+;\d+;\d+[Mm])+$`)
	sgrMouseTailFragmentRE = regexp.MustCompile(`^<\d+;\d+;\d+[Mm](?:\x1b?\[<\d+;\d+;\d+[Mm])*$`)
)

func (m *model) handleMouseMsg(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.mode != modeChat || m.page != pageChat || !m.busy {
		return nil, false
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.refreshViewportContent()
		wasFollowingLiveTail := m.followTail && !m.viewportFrozen
		m.freezeChatViewport()
		if !wasFollowingLiveTail {
			m.chat.ScrollBy(-mouseWheelScrollLines)
		}
		m.followTail = false
		m.syncViewportFromChat()
		return nil, true
	case tea.MouseButtonWheelDown:
		m.refreshViewportContent()
		m.chat.ScrollBy(mouseWheelScrollLines)
		m.followTail = m.chat.AtBottom()
		if m.followTail {
			return m.resumeChatTail(), true
		}
		m.syncViewportFromChat()
		return nil, true
	default:
		return nil, false
	}
}

func (m *model) consumeMouseCSIFragment(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes {
		return false
	}
	text := string(msg.Runes)
	if text == "" {
		return false
	}
	if !m.busy && !m.mouseCapture {
		m.pendingMouseCSIFragment = false
		return false
	}
	if msg.Alt && text == "[" {
		m.pendingMouseCSIFragment = true
		return true
	}
	if sgrMouseFragmentRE.MatchString(text) {
		m.pendingMouseCSIFragment = false
		return true
	}
	if m.pendingMouseCSIFragment {
		m.pendingMouseCSIFragment = false
		return sgrMouseTailFragmentRE.MatchString(text)
	}
	return false
}
