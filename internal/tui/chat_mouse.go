package tui

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	sgrMouseFragmentRE     = regexp.MustCompile(`^(?:\x1b?\[<\d+;\d+;\d+[Mm])+$`)
	sgrMouseTailFragmentRE = regexp.MustCompile(`^<\d+;\d+;\d+[Mm](?:\x1b?\[<\d+;\d+;\d+[Mm])*$`)
)

func (m *model) consumeMouseCSIFragment(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes {
		return false
	}
	text := string(msg.Runes)
	if text == "" {
		return false
	}
	if !m.busy {
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
