package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const providerRetryStatusMinimumTTL = 250 * time.Millisecond

func (m *model) setProviderRetryStatus(ev service.Event) {
	m.providerRetryStatus = strings.TrimSpace(ev.Text)
	ttl := providerRetryStatusMinimumTTL
	if ev.Metadata != nil {
		if delayMS, ok := metadataInt64(ev.Metadata["delay_ms"]); ok && delayMS > 0 {
			ttl = time.Duration(delayMS) * time.Millisecond
		}
	}
	m.providerRetryUntil = time.Now().Add(ttl)
}

func (m *model) clearProviderRetryStatus() {
	m.providerRetryStatus = ""
	m.providerRetryUntil = time.Time{}
}

func metadataInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	default:
		return 0, false
	}
}

func metadataBool(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func (m *model) handleServiceEvent(ev service.Event) (tea.Cmd, bool, bool) {
	if ev.AutoAcceptKnown {
		m.autoAccept = ev.AutoAccept
	}
	switch ev.Kind {
	case service.EventAssistantDelta:
		m.handleAssistantDeltaEvent(ev)
	case service.EventReasoningDelta:
		m.handleReasoningDeltaEvent(ev)
	case service.EventPlanDelta:
		m.handlePlanDeltaEvent(ev)
	case service.EventPlanCompleted:
		m.handlePlanCompletedEvent(ev)
	case service.EventPlanUpdate:
		m.handlePlanUpdateEvent(ev)
	case service.EventProviderRetry:
		m.handleProviderRetryEvent(ev)
	case service.EventInfo:
		m.handleInfoEvent(ev)
	case service.EventError:
		m.handleErrorEvent(ev)
	case service.EventLocalSubmitResult:
		m.handleLocalSubmitResultEvent(ev)
	case service.EventDiffResult:
		m.handleDiffResultEvent(ev)
	case service.EventBtwStarted:
		m.handleBtwStartedEvent(ev)
	case service.EventBtwDelta:
		m.handleBtwDeltaEvent(ev)
	case service.EventBtwDone:
		m.handleBtwDoneEvent(ev)
	case service.EventBtwError:
		m.handleBtwErrorEvent(ev)
	case service.EventToolCall:
		m.handleToolCallEvent(ev)
	case service.EventToolResult:
		return m.handleToolResultEvent(ev), false, false
	case service.EventTaskStarted:
		m.handleTaskStartedEvent(ev)
	case service.EventTaskProgress:
		m.handleTaskProgressEvent(ev)
	case service.EventTaskCompleted:
		m.handleTaskCompletedEvent(ev)
	case service.EventMCPStatus:
		m.handleMCPStatusEvent(ev)
	case service.EventMCPComplete:
		m.handleMCPCompleteEvent(ev)
	case service.EventApprovalRequired:
		m.handleApprovalRequiredEvent(ev)
	case service.EventUserInputRequired:
		m.handleUserInputRequiredEvent(ev)
	case service.EventSessionsListed:
		m.handleSessionsListedEvent(ev)
	case service.EventLocalSubmitDone:
		m.clearProviderRetryStatus()
		return m.finishLocalSubmit(), false, false
	case service.EventTurnDone:
		m.clearProviderRetryStatus()
		return m.handleTurnDone(ev), false, false
	case service.EventModelPicker:
		m.handleModelPickerEvent(ev)
	case service.EventPermissionsMenu:
		m.handlePermissionsMenuEvent(ev)
	case service.EventSkillLoaded:
		m.handleSkillLoadedEvent(ev)
	case service.EventSkillsMenu:
		m.handleSkillsMenuEvent(ev)
	case service.EventSkillsManager:
		m.handleSkillsManagerEvent(ev)
	case service.EventPluginsManager:
		m.handlePluginsManagerEvent(ev)
	case service.EventReviewMenu:
		m.handleReviewMenuEvent(ev)
	case service.EventViewModeChanged:
		return m.handleViewModeChangedEvent(ev), false, false
	case service.EventWorktreeExitPrompt:
		m.handleWorktreeExitPromptEvent(ev)
	case service.EventClearScreen:
		return m.handleClearScreenEvent(), false, true
	case service.EventSessionHydrated:
		return m.handleSessionHydratedEvent(ev), false, false
	case service.EventExitRequested:
		m.handleExitRequestedEvent()
		return nil, true, false
	}
	return nil, false, false
}

func (m *model) handleServiceEvents(events []service.Event) (tea.Cmd, bool, bool) {
	cmds := make([]tea.Cmd, 0, len(events))
	for _, ev := range events {
		cmd, quit, direct := m.handleServiceEvent(ev)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if quit || direct {
			return tea.Sequence(cmds...), quit, direct
		}
	}
	return tea.Sequence(cmds...), false, false
}

func (m *model) handleTurnDone(ev service.Event) tea.Cmd {
	wasBusy := m.busy
	wasStopping := m.stopping
	wasFrozen := m.viewportFrozen
	wasBlockingModal := m.mode == modeApproval || m.mode == modeUserInput
	turnDuration := m.completedTurnDuration(wasBusy)
	m.stopBusy()
	m.stopping = false
	if wasBlockingModal {
		m.mode = modeChat
	}
	reconciledAssistant := false
	if isAgentTurnDone(ev) {
		reconciledAssistant = m.reconcileFinalAssistant(ev.LastResponse)
	}
	m.markMissingProposedPlanIfNeeded(wasBusy)
	if !wasStopping {
		m.markNoFinalAnswerIfNeeded()
	}
	m.commitLiveTranscript(reconciledAssistant && !wasFrozen)
	m.appendTurnDurationNotice(wasBusy, wasStopping, turnDuration)
	if wasFrozen {
		m.unfreezeChatViewport()
		m.refreshViewportContentFollow(false)
	}
	m.addLog(logEntry{Kind: "turn_done", Source: "assistant", Summary: truncateLine(ev.LastResponse, 120), Raw: ev.LastResponse})
	m.status = "ready"
	queuedTurnStarted := false
	queuedRestored := false
	shouldOpenPlanPicker := wasBusy && !wasBlockingModal && m.chatMode == "plan" && m.sawPlanThisTurn && m.mode == modeChat
	var eventCmd tea.Cmd
	pendingWindowsInput := m.snapshotWindowsBusyInput()
	if wasStopping {
		m.deferredPlanPicker = false
		var restoreCmd tea.Cmd
		queuedRestored, restoreCmd = m.restoreQueuedPromptsToComposerWithWindowsInput(pendingWindowsInput)
		eventCmd = tea.Batch(eventCmd, restoreCmd)
	} else if m.localSubmitPending > 0 {
		if shouldOpenPlanPicker {
			m.deferredPlanPicker = true
		}
		m.status = "wait for command to finish"
	} else if next, ok := m.popQueuedPrompt(); ok {
		m.deferredPlanPicker = false
		eventCmd = tea.Batch(m.submitPromptWithBinding(next.Text, next.SkillBinding), m.restoreWindowsBusyInput(pendingWindowsInput))
		queuedTurnStarted = true
	}
	if !queuedTurnStarted && !queuedRestored && m.localSubmitPending == 0 && !m.hasPendingWindowsBusyInput() && shouldOpenPlanPicker {
		m.openPlanImplementationPicker()
	}
	m.resetTurnVisibility()
	return eventCmd
}

const turnDurationNoticeThreshold = 30 * time.Second

func (m *model) completedTurnDuration(wasBusy bool) time.Duration {
	if !wasBusy || m.busySince.IsZero() {
		return 0
	}
	return time.Since(m.busySince)
}

func (m *model) appendTurnDurationNotice(wasBusy, wasStopping bool, duration time.Duration) {
	if !wasBusy || wasStopping || duration < turnDurationNoticeThreshold {
		return
	}
	m.appendTranscriptMessages([]tuirender.UIMessage{{
		Role: "notice",
		Kind: tuirender.KindNotice,
		Text: "✻ Worked for " + formatTurnDuration(duration),
	}})
	if !m.viewportFrozen {
		m.refreshViewportContentFollow(true)
	}
}

func formatTurnDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	totalSeconds := int(duration / time.Second)
	if totalSeconds < 60 {
		return fmt.Sprintf("%ds", totalSeconds)
	}
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes %= 60
	if hours < 24 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	days := hours / 24
	hours %= 24
	return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
}

func (m *model) openPlanImplementationPicker() {
	m.deferredPlanPicker = false
	m.mode = modePlanImplementation
	m.planImplementation.index = 0
}

func (m *model) appendLocalSubmitResult(role, text string, localResult *app.LocalResult) {
	if m.busy {
		if localResult != nil {
			m.appendLiveLocalResult(localResult)
			return
		}
		m.append(role, text)
		return
	}
	if m.assembler != nil && m.assembler.Len() > 0 {
		m.commitLiveTranscript(false)
	}
	if isSessionNotice(text) {
		m.appendTranscript("notice", tuirender.KindNotice, text)
		return
	}
	if localResult != nil {
		m.appendLocalResult(localResult)
		return
	}
	m.appendTranscript(role, tuirender.KindText, text)
}

func (m *model) popLocalSubmitCommand() string {
	if len(m.localSubmitCommands) == 0 {
		return ""
	}
	cmd := m.localSubmitCommands[0]
	copy(m.localSubmitCommands, m.localSubmitCommands[1:])
	m.localSubmitCommands = m.localSubmitCommands[:len(m.localSubmitCommands)-1]
	return cmd
}

func (m *model) appendLocalCommandEcho(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	if m.busy {
		m.append("you", cmd)
		return
	}
	if m.assembler != nil && m.assembler.Len() > 0 {
		m.commitLiveTranscript(false)
	}
	m.appendTranscript("you", tuirender.KindText, cmd)
}

func isSessionNotice(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "New session\n")
}

func (m *model) resetTurnVisibility() {
	m.sawPlanThisTurn = false
	m.sawAssistantThisTurn = false
	m.sawReasoningThisTurn = false
	m.sawTerminalToolOutcomeThisTurn = false
	m.visibleAssistantThisTurn = ""
	m.resetBusyTokenEstimate()
	m.turnTranscriptStart = len(m.transcript)
}

func (m *model) resetLiveAttemptForProviderRetry() {
	if m.assembler != nil {
		m.assembler.Reset()
	}
	m.resetTurnVisibility()
	m.refreshLiveViewportContent()
}

func isAgentTurnDone(ev service.Event) bool {
	if ev.Metadata == nil {
		return false
	}
	agentTurn, ok := ev.Metadata[service.EventMetadataAgentTurn].(bool)
	return ok && agentTurn
}
