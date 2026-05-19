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
		m.submitLocalNoTurn(submit)
		return nil
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
		m.submitLocalNoTurn(submit)
		return
	}
	if appcommands.LooksLikeSlashCommand(submit.Line) {
		m.append("error", busySlashBlockedMessage(submit.Line, m.stopping))
		if m.stopping {
			m.status = "command disabled while stopping"
		} else {
			m.status = "command disabled while working"
		}
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
		m.submitLocalNoTurn(submit)
		return nil
	}
	stopping := m.stopping || wasStopping
	if appcommands.LooksLikeSlashCommand(submit.Line) {
		m.append("error", busySlashBlockedMessage(submit.Line, stopping))
		if stopping {
			m.status = "command disabled while stopping"
		} else {
			m.status = "command disabled while working"
		}
		m.refreshViewportContent()
		return nil
	}
	m.enqueuePrompt(value)
	return nil
}

func busySlashBlockedMessage(line string, stopping bool) string {
	fields := strings.Fields(line)
	cmd := strings.TrimSpace(line)
	if len(fields) > 0 {
		cmd = fields[0]
	}
	state := "a turn is in progress"
	if stopping {
		state = "the current turn is stopping"
	}
	return fmt.Sprintf("%s is disabled while %s. Press Esc/Ctrl+C to interrupt or wait.", cmd, state)
}

func (m *model) submitLocalNoTurn(submit appcommands.SubmitClassification) {
	cmd := submit.Line
	m.clearEphemeralMessages()
	m.recordPromptHistory(cmd)
	m.resetHistoryNavigation()
	m.input.SetValue("")
	m.skillBinding = nil
	m.resetWindowsPasteFallbackInputState()
	m.slash.matches = nil
	m.slash.selected = 0
	m.localSubmitPending++
	if !m.busy || submit.SubmitBarrier() {
		m.status = "command pending"
	}
	m.dispatchIntent(service.Intent{Kind: service.IntentSubmitLocal, Input: cmd})
	m.refreshViewportContent()
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

func (m *model) restoreQueuedPromptsToComposer() bool {
	return m.restoreQueuedPromptsToComposerWithCurrent(m.input.Value())
}

func (m *model) restoreQueuedPromptsToComposerWithWindowsInput(snapshot windowsBusyInputSnapshot) bool {
	current := m.input.Value()
	if snapshot.ok {
		current = snapshot.composerValue()
	}
	restored := m.restoreQueuedPromptsToComposerWithCurrent(current)
	if restored && snapshot.ok {
		m.resetWindowsPasteFallbackInputState()
	}
	return restored
}

func (m *model) restoreQueuedPromptsToComposerWithCurrent(currentValue string) bool {
	if len(m.queuedPrompts) == 0 {
		return false
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
	m.updateSlashMatches()
	m.refreshViewportContent()
	return true
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
	if s.windowsPaste.buffer == "" {
		return s.value
	}
	return s.value + s.windowsPaste.buffer
}

func (m *model) restoreWindowsBusyInput(snapshot windowsBusyInputSnapshot) {
	if !snapshot.ok {
		return
	}
	m.input.SetValue(snapshot.value)
	m.skillBinding = snapshot.skillBinding
	m.windowsPaste = snapshot.windowsPaste
	m.updateSlashMatches()
}
