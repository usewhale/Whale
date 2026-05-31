package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type rewindPickerState struct {
	messages        []protocol.Message
	selected        int
	confirming      bool
	confirmSelected int
}

func (m *model) handleRewindMessagesListedEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.stopBusy()
	m.stopping = false
	m.mode = modeRewindPicker
	m.rewindPicker.messages = ev.Messages
	m.rewindPicker.selected = 0
	m.rewindPicker.confirming = false
	m.rewindPicker.confirmSelected = 0
	m.status = "rewind"
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.skills.matches = nil
	m.skills.selected = 0
}

func (m *model) handleRewindPickerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.rewindPicker.confirming {
			m.rewindPicker.confirming = false
			m.rewindPicker.confirmSelected = 0
		} else {
			m.mode = modeChat
			m.status = "ready"
		}
	case "up", "k":
		if m.rewindPicker.confirming {
			if m.rewindPicker.confirmSelected > 0 {
				m.rewindPicker.confirmSelected--
			}
		} else if m.rewindPicker.selected > 0 {
			m.rewindPicker.selected--
		}
	case "down", "j":
		if m.rewindPicker.confirming {
			if m.rewindPicker.confirmSelected < 1 {
				m.rewindPicker.confirmSelected++
			}
		} else if m.rewindPicker.selected < len(m.rewindPicker.messages)-1 {
			m.rewindPicker.selected++
		}
	case "enter":
		if !m.rewindPicker.confirming {
			if m.rewindPicker.selected >= 0 && m.rewindPicker.selected < len(m.rewindPicker.messages) {
				m.rewindPicker.confirming = true
				m.rewindPicker.confirmSelected = 0
			}
			return nil
		}
		if m.rewindPicker.confirmSelected == 1 {
			m.rewindPicker.confirming = false
			return nil
		}
		msg := m.selectedRewindMessage()
		if strings.TrimSpace(msg.ID) != "" {
			m.mode = modeChat
			m.status = "rewinding"
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSelectRewindMessage, MessageID: msg.ID})
		}
	}
	return nil
}

func (m model) selectedRewindMessage() protocol.Message {
	if m.rewindPicker.selected < 0 || m.rewindPicker.selected >= len(m.rewindPicker.messages) {
		return protocol.Message{}
	}
	return m.rewindPicker.messages[m.rewindPicker.selected]
}

func (m model) renderRewindPicker() string {
	if m.rewindPicker.confirming {
		return m.renderRewindConfirm()
	}
	return m.renderRewindMessageList()
}

func (m model) renderRewindConfirm() string {
	msg := m.selectedRewindMessage()
	rows := []string{
		pickerTitle("Rewind"),
		pickerHint("Confirm you want to restore to the point before you sent this message:"),
		"",
		pickerHint("> " + truncateLine(singleLine(msg.Text), 88)),
		"",
		pickerHint("This restores the conversation and Whale-tracked file edits."),
	}
	options := []string{"Restore", "Cancel"}
	for i, option := range options {
		rows = append(rows, pickerRow(option, i == m.rewindPicker.confirmSelected, option == "Cancel"))
	}
	rows = append(rows, "", pickerHint("(up/down choose, enter confirm, esc back)"))
	return strings.Join(rows, "\n")
}

func (m model) renderRewindMessageList() string {
	rows := []string{
		pickerTitle("Rewind"),
		pickerHint("Restore code and conversation to the point before..."),
		pickerHint("(up/down choose, enter select, esc cancel)"),
	}
	if len(m.rewindPicker.messages) == 0 {
		rows = append(rows, "", pickerHint("Nothing to rewind to yet."))
		return strings.Join(rows, "\n")
	}
	start, end := visibleRewindRange(len(m.rewindPicker.messages), m.rewindPicker.selected, 6)
	for i := start; i < end; i++ {
		msg := m.rewindPicker.messages[i]
		label := fmt.Sprintf("%d) %s", i+1, truncateLine(singleLine(msg.Text), 88))
		rows = append(rows, pickerRow(label, i == m.rewindPicker.selected, false))
	}
	return strings.Join(rows, "\n")
}

func visibleRewindRange(total, selected, maxRows int) (int, int) {
	if total <= maxRows {
		return 0, total
	}
	start := selected - maxRows/2
	if start < 0 {
		start = 0
	}
	if start > total-maxRows {
		start = total - maxRows
	}
	return start, start + maxRows
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
