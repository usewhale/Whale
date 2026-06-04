package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const providerRetryStatusMinimumTTL = 250 * time.Millisecond

func (m *model) setProviderRetryStatus(ev protocol.Event) {
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

func metadataString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func (m *model) handleServiceEvent(ev protocol.Event) (tea.Cmd, bool, bool) {
	if ev.AutoAcceptKnown {
		m.autoAccept = ev.AutoAccept
	}
	if action, ok := uiActionFromServiceEvent(ev); ok {
		return m.handleUIAction(action)
	}
	switch ev.Kind {
	case protocol.EventAssistantDelta:
		m.handleAssistantDeltaEvent(ev)
	case protocol.EventReasoningDelta:
		m.handleReasoningDeltaEvent(ev)
	case protocol.EventPlanDelta:
		m.handlePlanDeltaEvent(ev)
	case protocol.EventPlanCompleted:
		m.handlePlanCompletedEvent(ev)
	case protocol.EventPlanUpdate:
		m.handlePlanUpdateEvent(ev)
	case protocol.EventProviderRetry:
		m.handleProviderRetryEvent(ev)
	case protocol.EventResponseReset:
		m.handleResponseResetEvent(ev)
	case protocol.EventInfo:
		m.handleInfoEvent(ev)
	case protocol.EventError:
		m.handleErrorEvent(ev)
	case protocol.EventLocalSubmitResult:
		return m.handleLocalSubmitResultEvent(ev), false, false
	case protocol.EventWorkflowPanel:
		return m.handleWorkflowPanelEvent(ev.LocalResult), false, false
	case protocol.EventWorkflowSnapshot:
		m.handleWorkflowSnapshotEvent(ev)
	case protocol.EventWorkflowTerminal:
		m.handleWorkflowTerminalEvent(ev)
	case protocol.EventDiffResult:
		m.handleDiffResultEvent(ev)
	case protocol.EventBtwStarted:
		m.handleBtwStartedEvent(ev)
	case protocol.EventBtwDelta:
		m.handleBtwDeltaEvent(ev)
	case protocol.EventBtwDone:
		m.handleBtwDoneEvent(ev)
	case protocol.EventBtwError:
		m.handleBtwErrorEvent(ev)
	case protocol.EventPendingInputAccepted:
		m.markPendingInputAccepted(ev.ClientInputID)
	case protocol.EventPendingInputRejected:
		return m.rejectPendingInput(ev.ClientInputID, ev.Text), false, false
	case protocol.EventToolCall:
		m.handleToolCallEvent(ev)
	case protocol.EventToolResult:
		return m.handleToolResultEvent(ev), false, false
	case protocol.EventHookStarted:
		m.handleHookStartedEvent(ev)
	case protocol.EventHookCompleted:
		m.handleHookCompletedEvent(ev)
	case protocol.EventTaskStarted:
		m.handleTaskStartedEvent(ev)
	case protocol.EventTaskProgress:
		m.handleTaskProgressEvent(ev)
	case protocol.EventTaskCompleted:
		m.handleTaskCompletedEvent(ev)
	case protocol.EventMCPStatus:
		m.handleMCPStatusEvent(ev)
	case protocol.EventMCPComplete:
		m.handleMCPCompleteEvent(ev)
	case protocol.EventApprovalRequired:
		m.handleApprovalRequiredEvent(ev)
	case protocol.EventApprovalDecision:
		m.handleApprovalDecisionEvent(ev)
	case protocol.EventUserInputRequired:
		m.handleUserInputRequiredEvent(ev)
	case protocol.EventUserInputDone:
		m.handleUserInputDoneEvent(ev)
	case protocol.EventSessionsListed:
		m.handleSessionsListedEvent(ev)
	case protocol.EventRewindMessagesListed:
		m.handleRewindMessagesListedEvent(ev)
	case protocol.EventLocalSubmitDone:
		m.clearProviderRetryStatus()
		return m.finishLocalSubmit(), false, false
	case protocol.EventTurnDone:
		m.clearProviderRetryStatus()
		return m.handleTurnDone(ev), false, false
	case protocol.EventSkillLoaded:
		m.handleSkillLoadedEvent(ev)
	case protocol.EventViewModeChanged:
		return m.handleViewModeChangedEvent(ev), false, false
	case protocol.EventWorktreeExitPrompt:
		m.handleWorktreeExitPromptEvent(ev)
	case protocol.EventSessionHydrated:
		return m.handleSessionHydratedEvent(ev), false, false
	case protocol.EventExitRequested:
		m.handleExitRequestedEvent()
		return nil, true, false
	}
	return nil, false, false
}

func (m *model) handleServiceEvents(events []protocol.Event) (tea.Cmd, bool, bool) {
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

func (m *model) handleTurnDone(ev protocol.Event) tea.Cmd {
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
	// Preserve the user's scroll position: if they scrolled up mid-turn (frozen
	// viewport, followTail cleared) we deliberately do NOT yank them back to the
	// tail when the turn completes.
	m.commitLiveTranscript(reconciledAssistant && !wasFrozen)
	m.appendTurnDurationNotice(wasBusy, wasStopping, turnDuration)
	if wasFrozen {
		m.unfreezeChatViewport()
		m.refreshViewportContentFollow(false)
	}
	// ...but always emit the completed turn into the terminal's native
	// scrollback, even when scrolled up. The normal flush in handleServiceUpdate
	// is gated on followTail/!frozen and would otherwise defer this until the
	// next keystroke, leaving the final answer hidden (the "看起来突然停下来"
	// symptom). Flushing here keeps the in-app position untouched while making
	// the finished answer immediately reachable via terminal scroll.
	turnScrollbackCmd := m.flushCompletedTurnToNativeScrollbackCmd()
	m.addLog(logEntry{Kind: "turn_done", Source: "assistant", Summary: truncateLine(ev.LastResponse, 120), Raw: ev.LastResponse})
	m.status = "ready"
	queuedTurnStarted := false
	queuedRestored := false
	shouldOpenPlanPicker := wasBusy && !wasBlockingModal && m.chatMode == "plan" && m.sawPlanThisTurn && m.mode == modeChat
	eventCmd := turnScrollbackCmd
	pendingWindowsInput := m.snapshotWindowsBusyInput()
	if wasStopping {
		m.deferredPlanPicker = false
		if m.submitQueuedPromptAfterInterrupt && len(m.queuedPrompts) > 0 {
			eventCmd = tea.Batch(eventCmd, m.submitQueuedPromptAfterInterruptNow(pendingWindowsInput))
			queuedTurnStarted = true
			queuedRestored = true
		} else {
			m.submitQueuedPromptAfterInterrupt = false
			var restoreCmd tea.Cmd
			queuedRestored, restoreCmd = m.restoreQueuedPromptsToComposerWithWindowsInput(pendingWindowsInput)
			eventCmd = tea.Batch(eventCmd, restoreCmd)
		}
	} else {
		m.clearAcceptedPendingSteers()
	}
	if !wasStopping && m.localSubmitPending > 0 {
		if shouldOpenPlanPicker {
			m.deferredPlanPicker = true
		}
		m.status = "wait for command to finish"
	} else if !wasStopping {
		if next, ok := m.popQueuedPrompt(); ok {
			m.deferredPlanPicker = false
			eventCmd = tea.Batch(eventCmd, m.submitPromptWithBindingAndAttachments(next.Text, next.SkillBinding, attachmentInputsFromComposerAttachments(next.Attachments)), m.restoreWindowsBusyInput(pendingWindowsInput))
			queuedTurnStarted = true
		}
	}
	if !queuedTurnStarted && !queuedRestored && m.localSubmitPending == 0 && !m.hasPendingWindowsBusyInput() && shouldOpenPlanPicker {
		m.openPlanImplementationPicker()
	}
	// Desktop notification: only if user has been idle for 6+ seconds.
	m.maybeNotifyTurnDone(ev.LastResponse)
	m.resetTurnVisibility()
	return eventCmd
}

// maybeNotifyTurnDone sends a desktop notification when a turn completes.
func (m *model) maybeNotifyTurnDone(lastResponse string) {
	if m.notifier == nil {
		return
	}
	summary := truncateLine(lastResponse, 120)
	m.notifier.SendTurnDone(summary)
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

func (m *model) appendLocalSubmitResult(role, text string, localResult *protocol.LocalResult) {
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
	m.resetLiveAttempt()
}

func (m *model) resetLiveAttemptForResponseReset() {
	if m.assembler != nil {
		m.assembler.Reset()
	}
	m.discardCurrentTurnModelOutput()
	m.resetTurnVisibility()
	m.refreshLiveViewportContent()
}

func (m *model) resetLiveAttempt() {
	if m.assembler != nil {
		m.assembler.Reset()
	}
	m.resetTimeline()
	m.resetTurnVisibility()
	m.refreshLiveViewportContent()
}

func isAgentTurnDone(ev protocol.Event) bool {
	if ev.Metadata == nil {
		return false
	}
	agentTurn, ok := ev.Metadata[protocol.EventMetadataAgentTurn].(bool)
	return ok && agentTurn
}
