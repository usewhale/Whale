package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	appcommands "github.com/usewhale/whale/internal/runtime/commands"
	"github.com/usewhale/whale/internal/runtime/protocol"
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
	m.resetBusyTokenEstimate()
}

func (m *model) submitPrompt(value string) tea.Cmd {
	return m.submitPromptWithBinding(value, m.currentSkillBinding(value))
}

func (m *model) submitPromptWithBinding(value string, binding *protocol.SkillBinding) tea.Cmd {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	submit := m.classifySubmit(value)
	if submit.LocalNoTurn() {
		return m.submitLocalNoTurn(submit)
	}
	m.clearEphemeralMessages()
	if m.assembler != nil && m.assembler.Len() > 0 {
		m.commitLiveTranscript(false)
	}
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
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSubmit, Input: value, SkillBinding: binding})
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
	submit := m.classifySubmit(value)
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
	submit := m.classifySubmit(value)
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

func (m model) classifySubmit(value string) appcommands.SubmitClassification {
	line := m.expandSubmitSlashPrefix(value)
	submit := appcommands.ClassifySubmit(line, appcommands.CommandsHelp(), "/mcp")
	if submit.Class != appcommands.SubmitUsageError {
		return submit
	}
	if class, ok := m.pluginSubmitClass(submit.Line); ok {
		return appcommands.SubmitClassification{Line: submit.Line, Class: class}
	}
	return submit
}

func (m model) expandSubmitSlashPrefix(value string) string {
	line := strings.TrimSpace(value)
	if !appcommands.LooksLikeSlashCommand(line) || strings.ContainsAny(line, " \t") {
		return line
	}
	var names []string
	for _, spec := range m.slash.all {
		name := strings.TrimSpace(spec.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	matches := make([]string, 0, 1)
	for _, name := range names {
		if strings.HasPrefix(name, line) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return line
}

func (m model) pluginSubmitClass(line string) (appcommands.SubmitClass, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || m.slash.commandClasses == nil {
		return appcommands.SubmitUsageError, false
	}
	class, ok := m.slash.commandClasses[fields[0]]
	return class, ok
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
	if appcommands.IsOpenCommandLine(cmd) {
		return m.startOpenCommand(cmd)
	}
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSubmitLocal, Input: cmd})
	m.refreshViewportContent()
	return nil
}

func (m *model) submitSteeringPrompt(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	binding := m.currentSkillBinding(value)
	clientInputID := m.nextPendingInputID()
	m.clearEphemeralMessages()
	if m.assembler != nil && m.assembler.Len() > 0 {
		m.commitLiveTranscript(false)
	}
	m.recordPromptHistory(value)
	m.resetHistoryNavigation()
	m.appendTranscript("you", tuirender.KindText, visibleSubmittedText(value))
	m.input.SetValue("")
	m.skillBinding = nil
	m.resetWindowsPasteFallbackInputState()
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	clearFileSuggestions(m)
	m.pendingSteers = append(m.pendingSteers, pendingSteer{
		ID:           clientInputID,
		Text:         value,
		SkillBinding: binding,
	})
	m.status = "sent"
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSubmit, Input: value, ClientInputID: clientInputID, SkillBinding: binding})
	m.refreshViewportContentFollow(true)
}

func (m *model) nextPendingInputID() string {
	m.nextClientInputID++
	return fmt.Sprintf("pending-%d", m.nextClientInputID)
}

func (m *model) markPendingInputAccepted(clientInputID string) {
	if clientInputID == "" {
		return
	}
	for i := range m.pendingSteers {
		if m.pendingSteers[i].ID == clientInputID {
			m.pendingSteers[i].Accepted = true
			m.refreshViewportContent()
			return
		}
	}
}

func (m *model) rejectPendingInput(clientInputID, text string) tea.Cmd {
	if clientInputID == "" {
		return nil
	}
	for i, steer := range m.pendingSteers {
		if steer.ID != clientInputID {
			continue
		}
		if strings.TrimSpace(text) == "" {
			text = steer.Text
		}
		m.pendingSteers = append(m.pendingSteers[:i], m.pendingSteers[i+1:]...)
		return m.restoreTextToComposer(text)
	}
	return nil
}

func (m *model) clearAcceptedPendingSteers() {
	if len(m.pendingSteers) == 0 {
		return
	}
	m.pendingSteers = nil
}

func (m *model) restoreTextToComposer(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	current := strings.TrimSpace(m.input.Value())
	if current != "" {
		text += "\n" + current
	}
	m.input.SetValue(text)
	m.skillBinding = nil
	m.resetHistoryNavigation()
	cmd := m.updateSlashMatches()
	m.refreshViewportContent()
	return cmd
}

func (m *model) submitPendingSteersNow() tea.Cmd {
	if len(m.pendingSteers) == 0 {
		return nil
	}
	parts := make([]string, 0, len(m.pendingSteers))
	for _, steer := range m.pendingSteers {
		if text := strings.TrimSpace(steer.Text); text != "" {
			parts = append(parts, text)
		}
	}
	m.pendingSteers = nil
	value := strings.TrimSpace(strings.Join(parts, "\n"))
	if value == "" {
		return nil
	}
	m.startBusy()
	m.status = "running"
	m.clearEphemeralMessages()
	m.beginTurnTranscript()
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSubmit, Input: value})
	m.refreshViewportContentFollow(true)
	return busyTickCmd()
}

func (m *model) prepareQueuedPromptAfterInterrupt() {
	value := strings.TrimSpace(m.input.Value())
	if value != "" {
		submit := m.classifySubmit(value)
		if !submit.BusyImmediate() && !appcommands.LooksLikeSlashCommand(submit.Line) {
			m.enqueuePrompt(value)
		}
	}
	if len(m.queuedPrompts) > 0 {
		m.submitQueuedPromptAfterInterrupt = true
	}
}

func (m *model) submitQueuedPromptAfterInterruptNow(snapshot windowsBusyInputSnapshot) tea.Cmd {
	next, ok := m.popQueuedPrompt()
	m.submitQueuedPromptAfterInterrupt = false
	if !ok {
		return nil
	}
	return tea.Batch(m.submitPromptWithBinding(next.Text, next.SkillBinding), m.restoreWindowsBusyInput(snapshot))
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
	if len(m.queuedPrompts) == 0 && len(m.pendingSteers) == 0 {
		return false, nil
	}
	parts := make([]string, 0, len(m.pendingSteers)+len(m.queuedPrompts)+1)
	for _, steer := range m.pendingSteers {
		if text := strings.TrimSpace(steer.Text); text != "" {
			parts = append(parts, text)
		}
	}
	for _, prompt := range m.queuedPrompts {
		if text := strings.TrimSpace(prompt.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if current := strings.TrimSpace(currentValue); current != "" {
		parts = append(parts, current)
	}
	m.pendingSteers = nil
	m.submitQueuedPromptAfterInterrupt = false
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
	skillBinding *protocol.SkillBinding
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
