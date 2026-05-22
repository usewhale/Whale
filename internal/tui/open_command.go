package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
)

type openCommandFinishedMsg struct {
	path string
	err  error
}

func (m *model) startOpenCommand(line string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return openCommandFinishedMsg{err: fmt.Errorf("open command is unavailable")}
		}
	}
	path, cmd, err := m.svc.PrepareOpenCommand(line)
	if err != nil {
		return func() tea.Msg {
			return openCommandFinishedMsg{err: err}
		}
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	m.refreshViewportContent()
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			err = fmt.Errorf("open editor: %w", err)
		}
		return openCommandFinishedMsg{path: path, err: err}
	})
}

func (m *model) handleOpenCommandFinished(msg openCommandFinishedMsg) tea.Cmd {
	m.clearProviderRetryStatus()
	m.appendLocalCommandEcho(m.popLocalSubmitCommand())
	role := "info"
	text := app.OpenCommandSuccessText(msg.path)
	if msg.err != nil {
		role = "error"
		text = msg.err.Error()
	}
	m.appendLocalSubmitResult(role, text)
	m.addLog(logEntry{Kind: role, Source: "system", Summary: text, Raw: text})
	if role == "error" {
		m.status = "error"
	} else {
		m.status = "ready"
	}
	return m.finishLocalSubmit()
}

func (m *model) finishLocalSubmit() tea.Cmd {
	if m.localSubmitPending > 0 {
		m.localSubmitPending--
	}
	if len(m.localSubmitCommands) > m.localSubmitPending {
		m.localSubmitCommands = m.localSubmitCommands[len(m.localSubmitCommands)-m.localSubmitPending:]
	}
	if !m.busy && m.localSubmitPending > 0 {
		m.status = "wait for command to finish"
	}
	if !m.busy && m.localSubmitPending == 0 {
		if m.status == "command pending" || m.status == "wait for command to finish" {
			m.status = "ready"
		}
		pendingWindowsInput := m.snapshotWindowsBusyInput()
		if next, ok := m.popQueuedPrompt(); ok {
			m.deferredPlanPicker = false
			eventCmd := m.submitPromptWithBinding(next.Text, next.SkillBinding)
			m.restoreWindowsBusyInput(pendingWindowsInput)
			return eventCmd
		}
		m.restoreWindowsBusyInput(pendingWindowsInput)
		if m.deferredPlanPicker && m.mode == modeChat {
			if m.hasPendingWindowsBusyInput() {
				m.deferredPlanPicker = false
			} else {
				m.openPlanImplementationPicker()
			}
		}
	}
	return nil
}
