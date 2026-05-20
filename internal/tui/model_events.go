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

func (m *model) handleServiceEvent(ev service.Event) (tea.Cmd, bool, bool) {
	var eventCmd tea.Cmd
	switch ev.Kind {
	case service.EventAssistantDelta:
		m.clearProviderRetryStatus()
		m.append("assistant", ev.Text)
		m.recordAssistantDelta(ev.Text)
		m.addLog(logEntry{Kind: "assistant_delta", Source: "assistant", Summary: ev.Text, Raw: ev.Text})
		if strings.TrimSpace(ev.Text) != "" {
			m.sawAssistantThisTurn = true
		}
		m.startBusy()
	case service.EventReasoningDelta:
		m.clearProviderRetryStatus()
		m.append("think", ev.Text)
		m.addLog(logEntry{Kind: "reasoning_delta", Source: "reasoning", Summary: ev.Text, Raw: ev.Text})
		if strings.TrimSpace(ev.Text) != "" {
			m.sawReasoningThisTurn = true
		}
	case service.EventPlanDelta:
		m.clearProviderRetryStatus()
		m.appendPlanDelta(ev.Text)
		m.addLog(logEntry{Kind: "plan_delta", Source: "plan", Summary: ev.Text, Raw: ev.Text})
		if strings.TrimSpace(ev.Text) != "" {
			m.sawPlanThisTurn = true
		}
	case service.EventPlanCompleted:
		m.clearProviderRetryStatus()
		if strings.TrimSpace(ev.Text) != "" {
			m.lastProposedPlan = strings.TrimSpace(ev.Text)
			if m.assembler == nil {
				m.assembler = tuirender.NewAssembler()
			}
			m.assembler.SetPlan(ev.Text)
			m.commitLiveTranscript(false)
			m.sawPlanThisTurn = true
		}
		m.addLog(logEntry{Kind: "plan_completed", Source: "plan", Summary: truncateLine(ev.Text, 120), Raw: ev.Text})
	case service.EventPlanUpdate:
		m.clearProviderRetryStatus()
		if strings.TrimSpace(ev.Text) != "" {
			if m.assembler == nil {
				m.assembler = tuirender.NewAssembler()
			}
			m.assembler.AddPlanUpdate(ev.Text)
			m.refreshLiveViewportContent()
		}
		m.addLog(logEntry{Kind: "plan_update", Source: "plan", Summary: truncateLine(ev.Text, 120), Raw: ev.Text})
	case service.EventProviderRetry:
		m.setProviderRetryStatus(ev)
		m.addLog(logEntry{Kind: "api_retry", Source: "provider", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventInfo:
		m.clearProviderRetryStatus()
		if !isEnvironmentInventoryBlock(ev.Text) {
			if isSessionNotice(ev.Text) {
				m.appendTranscript("notice", tuirender.KindNotice, ev.Text)
			} else {
				m.append("info", ev.Text)
			}
		} else {
			m.addLog(logEntry{
				Kind:    "env_summary",
				Source:  "system",
				Summary: "environment summary captured",
				Raw:     ev.Text,
			})
		}
		m.addLog(logEntry{Kind: "info", Source: "system", Summary: ev.Text, Raw: ev.Text})
		m.status = "ready"
		m.syncModelEffortFromInfo(ev.Text)
		m.refreshViewportContentFollow(true)
	case service.EventError:
		m.clearProviderRetryStatus()
		m.append("error", ev.Text)
		m.addLog(logEntry{Kind: "error", Source: "system", Summary: ev.Text, Raw: ev.Text})
		m.status = "error"
	case service.EventLocalSubmitResult:
		m.clearProviderRetryStatus()
		role := ev.Status
		if role == "" {
			role = "info"
		}
		if !isEnvironmentInventoryBlock(ev.Text) {
			m.appendLocalCommandEcho(m.popLocalSubmitCommand())
			m.appendLocalSubmitResult(role, ev.Text)
		} else {
			m.addLog(logEntry{
				Kind:    "env_summary",
				Source:  "system",
				Summary: "environment summary captured",
				Raw:     ev.Text,
			})
		}
		m.addLog(logEntry{Kind: role, Source: "system", Summary: ev.Text, Raw: ev.Text})
		if role == "error" {
			m.status = "error"
		}
		if role == "info" {
			m.syncModelEffortFromInfo(ev.Text)
			m.refreshViewportContentFollow(true)
		}
	case service.EventToolCall:
		m.clearProviderRetryStatus()
		if ev.ToolName != "update_plan" {
			m.appendToolCall(ev.ToolCallID, ev.ToolName, ev.Text)
		}
		m.addLog(logEntry{
			Kind:    "tool_call",
			Source:  ev.ToolName,
			Summary: fmt.Sprintf("%s (id=%s)", ev.Text, ev.ToolCallID),
			Raw:     fmt.Sprintf("id=%s\ninput=%s", ev.ToolCallID, ev.Text),
		})
	case service.EventToolResult:
		m.clearProviderRetryStatus()
		role, text := summarizeToolResultForChat(ev.ToolName, ev.Text)
		if suppressesNoFinalAnswer(role) {
			m.sawTerminalToolOutcomeThisTurn = true
		}
		if !m.updateToolCallFromResult(ev.ToolCallID, ev.ToolName, ev.Text, role, text, ev.Metadata) {
			m.markToolCallResolved(ev.ToolCallID)
			if shouldShowUnmatchedToolResult(ev.ToolName, role, text) {
				m.appendLiveToolResult(text, role)
			}
		}
		m.addLog(logEntry{Kind: "tool_result", Source: ev.ToolName, Summary: truncateLine(ev.Text, 120), Raw: ev.Text})
		m.captureDiffMetadata(ev.ToolName, ev.Metadata)
		m.captureDiff(ev.ToolName, ev.Text)
		if !m.hasPendingToolCalls() {
			m.commitLiveTranscript(false)
		}
		if toolResultMayChangeGitBranch(ev.ToolName) {
			eventCmd = tea.Batch(eventCmd, detectGitBranchCmd(m.cwdPath))
		}
	case service.EventTaskStarted:
		m.clearProviderRetryStatus()
		m.status = ev.Text
		m.addLog(logEntry{Kind: "task_started", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventTaskProgress:
		m.clearProviderRetryStatus()
		m.status = ev.Text
		m.updateTaskProgress(ev.ToolCallID, ev.ToolName, ev.Text)
		m.addLog(logEntry{Kind: "task_progress", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventTaskCompleted:
		m.clearProviderRetryStatus()
		m.status = ev.Text
		m.addLog(logEntry{Kind: "task_completed", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventMCPStatus:
		m.clearProviderRetryStatus()
		m.status = ev.Text
		if ev.Status == "failed" || ev.Status == "cancelled" {
			m.append("error", ev.Text)
		}
		m.addLog(logEntry{Kind: "mcp_status", Source: "mcp", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventMCPComplete:
		m.clearProviderRetryStatus()
		m.status = ev.Text
		m.addLog(logEntry{Kind: "mcp_complete", Source: "mcp", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventApprovalRequired:
		m.clearProviderRetryStatus()
		if m.stopping {
			if ev.ToolCallID != "" {
				m.dispatchIntent(service.Intent{Kind: service.IntentCancelToolApproval, ToolCallID: ev.ToolCallID})
			}
			m.addLog(logEntry{Kind: "approval_required_stale", Source: ev.ToolName, Summary: ev.Text, Raw: ev.Text})
			break
		}
		m.mode = modeApproval
		m.approval.toolCallID = ev.ToolCallID
		m.approval.toolName = ev.ToolName
		m.approval.reason = ev.Text
		m.approval.metadata = ev.Metadata
		m.approval.selected = 0
		m.addLog(logEntry{Kind: "approval_required", Source: ev.ToolName, Summary: ev.Text, Raw: ev.Text})
		m.status = "approval required"
	case service.EventUserInputRequired:
		m.clearProviderRetryStatus()
		if m.stopping {
			if ev.ToolCallID != "" {
				m.dispatchIntent(service.Intent{Kind: service.IntentCancelUserInput, ToolCallID: ev.ToolCallID})
			}
			m.addLog(logEntry{Kind: "user_input_required_stale", Source: ev.ToolName, Summary: fmt.Sprintf("%d questions", len(ev.Questions)), Raw: fmt.Sprintf("%+v", ev.Questions)})
			break
		}
		m.mode = modeUserInput
		m.userInput.toolCallID = ev.ToolCallID
		m.userInput.toolName = ev.ToolName
		m.userInput.questions = ev.Questions
		m.userInput.index = 0
		m.userInput.selectedOption = 0
		m.userInput.answers = nil
		m.addLog(logEntry{Kind: "user_input_required", Source: ev.ToolName, Summary: fmt.Sprintf("%d questions", len(ev.Questions)), Raw: fmt.Sprintf("%+v", ev.Questions)})
		m.status = "user input required"
	case service.EventSessionsListed:
		m.clearProviderRetryStatus()
		m.mode = modeSessionPicker
		m.sessionChoices = ev.Choices
		m.sessionIndex = firstSessionChoiceIndex(ev.Choices)
		m.addLog(logEntry{Kind: "sessions_listed", Source: "session", Summary: fmt.Sprintf("%d sessions", len(ev.Choices)), Raw: strings.Join(ev.Choices, "\n")})
		m.status = "session picker"
	case service.EventLocalSubmitDone:
		m.clearProviderRetryStatus()
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
				eventCmd = m.submitPromptWithBinding(next.Text, next.SkillBinding)
				m.restoreWindowsBusyInput(pendingWindowsInput)
			} else {
				m.restoreWindowsBusyInput(pendingWindowsInput)
				if m.deferredPlanPicker && m.mode == modeChat {
					if m.hasPendingWindowsBusyInput() {
						m.deferredPlanPicker = false
					} else {
						m.openPlanImplementationPicker()
					}
				}
			}
		}
	case service.EventTurnDone:
		m.clearProviderRetryStatus()
		eventCmd = m.handleTurnDone(ev)
	case service.EventModelPicker:
		m.clearProviderRetryStatus()
		m.stopBusy()
		m.stopping = false
		m.mode = modeModelPicker
		m.modelPicker.stage = 0
		m.modelPicker.models = ev.ModelChoices
		m.modelPicker.efforts = ev.EffortChoices
		m.modelPicker.thinkings = ev.ThinkingChoices
		m.modelPicker.modelIx = indexOf(ev.ModelChoices, ev.CurrentModel)
		m.modelPicker.effIx = indexOf(ev.EffortChoices, ev.CurrentEffort)
		m.modelPicker.thinkIx = indexOf(ev.ThinkingChoices, ev.CurrentThinking)
	case service.EventPermissionsPicker:
		m.clearProviderRetryStatus()
		m.stopBusy()
		m.stopping = false
		m.mode = modePermissionsPicker
		m.permissionsPicker.choices = ev.ApprovalChoices
		m.permissionsPicker.index = indexOf(ev.ApprovalChoices, ev.CurrentApproval)
	case service.EventSkillLoaded:
		m.clearProviderRetryStatus()
		m.addLog(logEntry{Kind: "skill_loaded", Source: "skills", Summary: ev.Text, Raw: ev.Text})
		m.status = ev.Text
	case service.EventSkillsMenu:
		m.clearProviderRetryStatus()
		m.stopBusy()
		m.stopping = false
		m.mode = modeSkillsMenu
		m.skillsMenu.selected = 0
		m.slash.matches = nil
		m.slash.selected = 0
		m.skills.matches = nil
		m.skills.selected = 0
		m.status = "skills"
	case service.EventSkillsManager:
		m.clearProviderRetryStatus()
		m.stopBusy()
		m.stopping = false
		m.mode = modeSkillsManager
		m.slash.matches = nil
		m.slash.selected = 0
		m.skills.matches = nil
		m.skills.selected = 0
		m.setSkillsManagerItems(ev.Skills)
		m.status = "skills"
	case service.EventPluginsManager:
		m.clearProviderRetryStatus()
		m.stopBusy()
		m.stopping = false
		m.mode = modePluginsManager
		m.slash.matches = nil
		m.slash.selected = 0
		m.skills.matches = nil
		m.skills.selected = 0
		m.setPluginsManagerItems(ev.Plugins)
		m.status = "plugins"
	case service.EventReviewMenu:
		m.clearProviderRetryStatus()
		m.stopBusy()
		m.stopping = false
		m.mode = modeReviewMenu
		m.reviewMenu.selected = 0
		m.slash.matches = nil
		m.slash.selected = 0
		m.skills.matches = nil
		m.skills.selected = 0
		m.status = "review"
	case service.EventViewModeChanged:
		m.clearProviderRetryStatus()
		mode := strings.TrimSpace(ev.ViewMode)
		if mode == "" {
			mode = strings.TrimSpace(ev.Text)
			switch mode {
			case app.ViewModeToggleMessage(app.ViewModeFocus):
				mode = app.ViewModeFocus
			case app.ViewModeToggleMessage(app.ViewModeDefault):
				mode = app.ViewModeDefault
			default:
				mode = strings.TrimPrefix(mode, "view:")
				mode = strings.TrimSpace(mode)
			}
		}
		if mode == "" {
			mode = app.ViewModeDefault
		}
		m.viewMode = mode
		if strings.TrimSpace(ev.Text) != "" {
			m.setEphemeralInfo(ev.Text)
		} else {
			m.refreshViewportContentFollow(true)
		}
		m.status = "ready"
	case service.EventClearScreen:
		m.clearProviderRetryStatus()
		m.assembler.Reset()
		m.clearPendingToolCalls()
		m.ephemeralMessages = nil
		m.resetTranscript()
		m.resetTurnVisibility()
		m.logs = nil
		m.diffs = nil
		m.status = "terminal cleared"
		return tea.Sequence(clearScreenCmd(), m.startupHeaderPrintCmd(), waitEventCmd(m.svc)), false, true
	case service.EventSessionHydrated:
		prevSessionID := m.sessionID
		if strings.TrimSpace(ev.SessionID) != "" {
			m.sessionID = strings.TrimSpace(ev.SessionID)
		}
		sessionChanged := prevSessionID != "" && m.sessionID != "" && m.sessionID != prevSessionID
		hadStartupHeaderPrinted := m.startupHeaderPrinted || (m.startupHeaderOnce != nil && *m.startupHeaderOnce)
		m.clearProviderRetryStatus()
		m.assembler.Reset()
		m.clearPendingToolCalls()
		m.ephemeralMessages = nil
		m.resetTranscript()
		m.resetTurnVisibility()
		m.logs = nil
		m.diffs = nil
		m.hydrateSessionMessages(ev.Messages)
		m.commitLiveTranscript(true)
		m.trimHydratedTranscriptForDisplay(maxHydratedTranscriptLines)
		if sessionChanged {
			hadStartupHeaderPrinted = false
			eventCmd = clearScreenCmd()
		}
		if len(m.transcript) > 0 || hadStartupHeaderPrinted {
			m.startupHeaderPrinted = true
			if m.startupHeaderOnce == nil {
				m.startupHeaderOnce = new(bool)
			}
			*m.startupHeaderOnce = true
		}
		m.status = "ready"
	case service.EventExitRequested:
		m.clearProviderRetryStatus()
		m.dispatchIntent(service.Intent{Kind: service.IntentShutdown})
		return nil, true, false
	}
	return eventCmd, false, false
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
	m.markNoFinalAnswerIfNeeded()
	m.commitLiveTranscript(reconciledAssistant && !wasFrozen)
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
		queuedRestored = m.restoreQueuedPromptsToComposerWithWindowsInput(pendingWindowsInput)
	} else if m.localSubmitPending > 0 {
		if shouldOpenPlanPicker {
			m.deferredPlanPicker = true
		}
		m.status = "wait for command to finish"
	} else if next, ok := m.popQueuedPrompt(); ok {
		m.deferredPlanPicker = false
		eventCmd = m.submitPromptWithBinding(next.Text, next.SkillBinding)
		m.restoreWindowsBusyInput(pendingWindowsInput)
		queuedTurnStarted = true
	}
	if !queuedTurnStarted && !queuedRestored && m.localSubmitPending == 0 && !m.hasPendingWindowsBusyInput() && shouldOpenPlanPicker {
		m.openPlanImplementationPicker()
	}
	m.resetTurnVisibility()
	return eventCmd
}

func (m *model) openPlanImplementationPicker() {
	m.deferredPlanPicker = false
	m.mode = modePlanImplementation
	m.planImplementation.index = 0
}

func (m *model) appendLocalSubmitResult(role, text string) {
	if m.busy {
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
	m.turnTranscriptStart = len(m.transcript)
}

func isAgentTurnDone(ev service.Event) bool {
	if ev.Metadata == nil {
		return false
	}
	agentTurn, ok := ev.Metadata[service.EventMetadataAgentTurn].(bool)
	return ok && agentTurn
}
