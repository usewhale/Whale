package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) handleServiceEvent(ev service.Event) (tea.Cmd, bool, bool) {
	var eventCmd tea.Cmd
	switch ev.Kind {
	case service.EventAssistantDelta:
		m.append("assistant", ev.Text)
		m.recordAssistantDelta(ev.Text)
		m.addLog(logEntry{Kind: "assistant_delta", Source: "assistant", Summary: ev.Text, Raw: ev.Text})
		if strings.TrimSpace(ev.Text) != "" {
			m.sawAssistantThisTurn = true
		}
		m.startBusy()
	case service.EventReasoningDelta:
		m.append("think", ev.Text)
		m.addLog(logEntry{Kind: "reasoning_delta", Source: "reasoning", Summary: ev.Text, Raw: ev.Text})
		if strings.TrimSpace(ev.Text) != "" {
			m.sawReasoningThisTurn = true
		}
	case service.EventPlanDelta:
		m.appendPlanDelta(ev.Text)
		m.addLog(logEntry{Kind: "plan_delta", Source: "plan", Summary: ev.Text, Raw: ev.Text})
		if strings.TrimSpace(ev.Text) != "" {
			m.sawPlanThisTurn = true
		}
	case service.EventPlanCompleted:
		if strings.TrimSpace(ev.Text) != "" {
			if m.assembler == nil {
				m.assembler = tuirender.NewAssembler()
			}
			m.assembler.SetPlan(ev.Text)
			m.commitLiveTranscript(false)
			m.sawPlanThisTurn = true
		}
		m.addLog(logEntry{Kind: "plan_completed", Source: "plan", Summary: truncateLine(ev.Text, 120), Raw: ev.Text})
	case service.EventInfo:
		if !isEnvironmentInventoryBlock(ev.Text) {
			m.append("info", ev.Text)
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
	case service.EventError:
		m.append("error", ev.Text)
		m.addLog(logEntry{Kind: "error", Source: "system", Summary: ev.Text, Raw: ev.Text})
		m.status = "error"
	case service.EventLocalSubmitResult:
		role := ev.Status
		if role == "" {
			role = "info"
		}
		if !isEnvironmentInventoryBlock(ev.Text) {
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
		}
	case service.EventToolCall:
		m.appendToolCall(ev.ToolCallID, ev.ToolName, ev.Text)
		m.addLog(logEntry{
			Kind:    "tool_call",
			Source:  ev.ToolName,
			Summary: fmt.Sprintf("%s (id=%s)", ev.Text, ev.ToolCallID),
			Raw:     fmt.Sprintf("id=%s\ninput=%s", ev.ToolCallID, ev.Text),
		})
	case service.EventToolResult:
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
	case service.EventTaskStarted:
		m.status = ev.Text
		m.addLog(logEntry{Kind: "task_started", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventTaskProgress:
		m.status = ev.Text
		m.updateTaskProgress(ev.ToolCallID, ev.ToolName, ev.Text)
		m.addLog(logEntry{Kind: "task_progress", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventTaskCompleted:
		m.status = ev.Text
		m.addLog(logEntry{Kind: "task_completed", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventMCPStatus:
		m.status = ev.Text
		if ev.Status == "failed" || ev.Status == "cancelled" {
			m.append("error", ev.Text)
		}
		m.addLog(logEntry{Kind: "mcp_status", Source: "mcp", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventMCPComplete:
		m.status = ev.Text
		m.addLog(logEntry{Kind: "mcp_complete", Source: "mcp", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
	case service.EventApprovalRequired:
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
		m.mode = modeSessionPicker
		m.sessionChoices = ev.Choices
		m.sessionIndex = firstSessionChoiceIndex(ev.Choices)
		m.addLog(logEntry{Kind: "sessions_listed", Source: "session", Summary: fmt.Sprintf("%d sessions", len(ev.Choices)), Raw: strings.Join(ev.Choices, "\n")})
		m.status = "session picker"
	case service.EventLocalSubmitDone:
		if m.localSubmitPending > 0 {
			m.localSubmitPending--
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
		eventCmd = m.handleTurnDone(ev)
	case service.EventModelPicker:
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
		m.stopBusy()
		m.stopping = false
		m.mode = modePermissionsPicker
		m.permissionsPicker.choices = ev.ApprovalChoices
		m.permissionsPicker.index = indexOf(ev.ApprovalChoices, ev.CurrentApproval)
	case service.EventSkillLoaded:
		m.addLog(logEntry{Kind: "skill_loaded", Source: "skills", Summary: ev.Text, Raw: ev.Text})
		m.status = ev.Text
	case service.EventSkillsMenu:
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
		m.stopBusy()
		m.stopping = false
		m.mode = modeSkillsManager
		m.slash.matches = nil
		m.slash.selected = 0
		m.skills.matches = nil
		m.skills.selected = 0
		m.setSkillsManagerItems(ev.Skills)
		m.status = "skills"
	case service.EventClearScreen:
		m.assembler.Reset()
		m.clearPendingToolCalls()
		m.resetTranscriptWithHeader()
		m.resetTurnVisibility()
		m.logs = nil
		m.diffs = nil
		m.status = "terminal cleared"
		return tea.Sequence(clearScreenCmd(), waitEventCmd(m.svc)), false, true
	case service.EventSessionHydrated:
		m.assembler.Reset()
		m.clearPendingToolCalls()
		m.resetTranscriptWithHeader()
		m.resetTurnVisibility()
		m.logs = nil
		m.diffs = nil
		m.hydrateSessionMessages(ev.Messages)
		m.commitLiveTranscript(true)
		m.trimHydratedTranscriptForDisplay(maxHydratedTranscriptLines)
		m.status = "ready"
	case service.EventExitRequested:
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
	m.appendTranscript(role, tuirender.KindText, text)
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
