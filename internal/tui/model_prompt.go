package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) startBusy() {
	if m.busySince.IsZero() {
		m.busySince = time.Now()
	}
	m.busy = true
}

func (m *model) stopBusy() {
	m.busy = false
	m.busySince = time.Time{}
}

func (m *model) submitPrompt(value string) tea.Cmd {
	return m.submitPromptWithBinding(value, m.currentSkillBinding(value))
}

func (m *model) submitPromptWithBinding(value string, binding *app.SkillBinding) tea.Cmd {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	submit := appcommands.ClassifySubmit(value, app.CommandsHelp, "/mcp")
	if submit.LocalNoTurn() {
		return m.submitLocalNoTurn(submit)
	}
	m.clearEphemeralMessages()
	m.recordPromptHistory(value)
	m.resetHistoryNavigation()
	m.appendTranscript("you", tuirender.KindText, visibleSubmittedText(value))
	m.beginTurnTranscript()
	m.input.SetValue("")
	m.skillBinding = nil
	m.resetWindowsPasteFallbackInputState()
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	clearFileSuggestions(m)
	m.startBusy()
	m.status = "running"
	m.dispatchIntent(service.Intent{Kind: service.IntentSubmit, Input: value, SkillBinding: binding})
	m.refreshViewportContentFollow(true)
	return busyTickCmd()
}

func (m *model) submitPromptWhileBusy(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		if m.stopping {
			m.status = "stopping"
		}
		return
	}
	submit := appcommands.ClassifySubmit(value, app.CommandsHelp, "/mcp")
	if submit.BusyImmediate() {
		_ = m.submitLocalNoTurn(submit)
		return
	}
	if appcommands.LooksLikeSlashCommand(submit.Line) {
		m.status = busySlashBlockedStatus(submit.Line, m.stopping)
		m.refreshViewportContent()
		return
	}
	m.enqueuePrompt(value)
}

func (m *model) submitPromptFromDeferredBusyEnter(value string, wasStopping bool) tea.Cmd {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if wasStopping && !m.busy {
		return nil
	}
	submit := appcommands.ClassifySubmit(value, app.CommandsHelp, "/mcp")
	if submit.BusyImmediate() {
		_ = m.submitLocalNoTurn(submit)
		return nil
	}
	stopping := m.stopping || wasStopping
	if appcommands.LooksLikeSlashCommand(submit.Line) {
		m.status = busySlashBlockedStatus(submit.Line, stopping)
		m.refreshViewportContent()
		return nil
	}
	m.enqueuePrompt(value)
	return nil
}

func busySlashBlockedStatus(line string, stopping bool) string {
	fields := strings.Fields(line)
	cmd := strings.TrimSpace(line)
	if len(fields) > 0 {
		cmd = fields[0]
	}
	state := "working"
	if stopping {
		state = "stopping"
	}
	return fmt.Sprintf("%s disabled while %s", cmd, state)
}

func (m *model) submitLocalNoTurn(submit appcommands.SubmitClassification) tea.Cmd {
	cmd := submit.Line
	if strings.TrimSpace(cmd) == "/help" {
		m.openHelp()
		return nil
	}
	if m.btwPanel.loading && isBtwCommand(cmd) {
		m.status = "/btw is already answering"
		return nil
	}
	m.clearEphemeralMessages()
	m.recordPromptHistory(cmd)
	m.resetHistoryNavigation()
	m.input.SetValue("")
	m.skillBinding = nil
	m.resetWindowsPasteFallbackInputState()
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.localSubmitPending++
	m.localSubmitCommands = append(m.localSubmitCommands, cmd)
	if !m.busy || submit.SubmitBarrier() {
		m.status = "command pending"
	}
	if app.IsOpenCommandLine(cmd) {
		return m.startOpenCommand(cmd)
	}
	m.dispatchIntent(service.Intent{Kind: service.IntentSubmitLocal, Input: cmd})
	m.refreshViewportContent()
	return nil
}

func isBtwCommand(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) > 0 && fields[0] == "/btw"
}

func (m *model) enqueuePrompt(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	m.queuedPrompts = append(m.queuedPrompts, queuedPrompt{Text: value, SkillBinding: m.currentSkillBinding(value)})
	m.input.SetValue("")
	m.skillBinding = nil
	m.resetWindowsPasteFallbackInputState()
	m.resetHistoryNavigation()
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.status = fmt.Sprintf("queued (%d)", len(m.queuedPrompts))
	m.refreshViewportContent()
	return true
}

func (m *model) popQueuedPrompt() (queuedPrompt, bool) {
	if len(m.queuedPrompts) == 0 {
		return queuedPrompt{}, false
	}
	next := m.queuedPrompts[0]
	copy(m.queuedPrompts, m.queuedPrompts[1:])
	m.queuedPrompts = m.queuedPrompts[:len(m.queuedPrompts)-1]
	return next, true
}

func (m *model) restoreQueuedPromptsToComposer() (bool, tea.Cmd) {
	return m.restoreQueuedPromptsToComposerWithCurrent(m.input.Value())
}

func (m *model) restoreQueuedPromptsToComposerWithWindowsInput(snapshot windowsBusyInputSnapshot) (bool, tea.Cmd) {
	current := m.input.Value()
	if snapshot.ok {
		current = snapshot.composerValue()
	}
	restored, cmd := m.restoreQueuedPromptsToComposerWithCurrent(current)
	if restored && snapshot.ok {
		m.resetWindowsPasteFallbackInputState()
	}
	return restored, cmd
}

func (m *model) restoreQueuedPromptsToComposerWithCurrent(currentValue string) (bool, tea.Cmd) {
	if len(m.queuedPrompts) == 0 {
		return false, nil
	}
	parts := make([]string, 0, len(m.queuedPrompts)+1)
	for _, prompt := range m.queuedPrompts {
		if text := strings.TrimSpace(prompt.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if current := strings.TrimSpace(currentValue); current != "" {
		parts = append(parts, current)
	}
	m.queuedPrompts = nil
	m.skillBinding = nil
	m.input.SetValue(strings.Join(parts, "\n"))
	m.resetHistoryNavigation()
	cmd := m.updateSlashMatches()
	m.refreshViewportContent()
	return true, cmd
}

type windowsBusyInputSnapshot struct {
	ok           bool
	value        string
	skillBinding *app.SkillBinding
	windowsPaste windowsPasteFallbackState
}

func (m model) snapshotWindowsBusyInput() windowsBusyInputSnapshot {
	if !m.hasPendingWindowsBusyInput() {
		return windowsBusyInputSnapshot{}
	}
	return windowsBusyInputSnapshot{
		ok:           true,
		value:        m.input.Value(),
		skillBinding: m.skillBinding,
		windowsPaste: m.windowsPaste,
	}
}

func (s windowsBusyInputSnapshot) composerValue() string {
	if !s.ok {
		return ""
	}
	if s.windowsPaste.bufferLen == 0 {
		return s.value
	}
	return s.value + model{windowsPaste: s.windowsPaste}.windowsPasteBuffer()
}

func (m *model) restoreWindowsBusyInput(snapshot windowsBusyInputSnapshot) tea.Cmd {
	if !snapshot.ok {
		return nil
	}
	m.input.SetValue(snapshot.value)
	m.skillBinding = snapshot.skillBinding
	m.windowsPaste = snapshot.windowsPaste
	return m.updateSlashMatches()
}
