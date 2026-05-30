package tui

import (
	"fmt"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) handleAssistantDeltaEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.append("assistant", ev.Text)
	m.recordAssistantDelta(ev.Text)
	m.addLog(logEntry{Kind: "assistant_delta", Source: "assistant", Summary: ev.Text, Raw: ev.Text})
	if strings.TrimSpace(ev.Text) != "" {
		m.sawAssistantThisTurn = true
	}
	m.startBusy()
}

func (m *model) handleReasoningDeltaEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.append("think", ev.Text)
	m.recordModelOutputDelta(ev.Text)
	m.addLog(logEntry{Kind: "reasoning_delta", Source: "reasoning", Summary: ev.Text, Raw: ev.Text})
	if strings.TrimSpace(ev.Text) != "" {
		m.sawReasoningThisTurn = true
	}
}

func (m *model) handlePlanDeltaEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.appendPlanDelta(ev.Text)
	m.recordModelOutputDelta(ev.Text)
	m.addLog(logEntry{Kind: "plan_delta", Source: "plan", Summary: ev.Text, Raw: ev.Text})
	if strings.TrimSpace(ev.Text) != "" {
		m.sawPlanThisTurn = true
	}
}

func (m *model) handlePlanCompletedEvent(ev protocol.Event) {
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
}

func (m *model) handlePlanUpdateEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	if strings.TrimSpace(ev.Text) != "" {
		if m.assembler == nil {
			m.assembler = tuirender.NewAssembler()
		}
		m.assembler.AddPlanUpdate(ev.Text)
		m.refreshLiveViewportContent()
	}
	m.addLog(logEntry{Kind: "plan_update", Source: "plan", Summary: truncateLine(ev.Text, 120), Raw: ev.Text})
}

func (m *model) handleProviderRetryEvent(ev protocol.Event) {
	if ev.Metadata != nil && metadataBool(ev.Metadata["stream_reset"]) {
		m.resetLiveAttemptForProviderRetry()
	}
	m.setProviderRetryStatus(ev)
	m.addLog(logEntry{Kind: "api_retry", Source: "provider", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
}

func (m *model) handleInfoEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	if !isEnvironmentInventoryBlock(ev.Text) {
		if ev.LocalResult != nil {
			m.appendLocalResult(ev.LocalResult)
		} else if notice := permissionNoticeFromInfo(ev.Text); notice != nil {
			m.appendSystemNotice(notice)
		} else if isSessionNotice(ev.Text) {
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
}

func (m *model) handleErrorEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.append("error", ev.Text)
	m.addLog(logEntry{Kind: "error", Source: "system", Summary: ev.Text, Raw: ev.Text})
	m.status = "error"
}

func (m *model) handleLocalSubmitResultEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	role := ev.Status
	if role == "" {
		role = "info"
	}
	if !isEnvironmentInventoryBlock(ev.Text) {
		m.appendLocalCommandEcho(m.popLocalSubmitCommand())
		m.appendLocalSubmitResult(role, ev.Text, ev.LocalResult)
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
}

func (m *model) handleDiffResultEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.appendLocalCommandEcho(m.popLocalSubmitCommand())
	m.setDiffText(ev.Text)
	m.page = pageDiff
	m.status = "diff"
	m.refreshViewportContentFollow(true)
	m.viewport.GotoTop()
}

func (m *model) handleBtwStartedEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.startBtwPanel(ev.Count, ev.Text)
	m.refreshViewportContent()
}

func (m *model) handleBtwDeltaEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.appendBtwDelta(ev.Count, ev.Text)
	m.refreshViewportContent()
}

func (m *model) handleBtwDoneEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.finishBtwPanel(ev.Count, ev.Text)
	m.refreshViewportContent()
}

func (m *model) handleBtwErrorEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.failBtwPanel(ev.Count, ev.Text)
	m.refreshViewportContent()
}

func (m *model) handleToolCallEvent(ev protocol.Event) {
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
}

func (m *model) handleToolResultEvent(ev protocol.Event) tea.Cmd {
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
		return detectGitBranchCmd(m.cwdPath)
	}
	return nil
}

func (m *model) handleTaskStartedEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.status = ev.Text
	m.addLog(logEntry{Kind: "task_started", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
}

func (m *model) handleTaskProgressEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.status = ev.Text
	m.updateTaskProgressWithSteps(ev.ToolCallID, ev.ToolName, ev.Text, ev.Status, ev.Metadata, ev.ProgressMessages)
	m.addLog(logEntry{Kind: "task_progress", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
}

func (m *model) handleTaskCompletedEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.status = ev.Text
	m.addLog(logEntry{Kind: "task_completed", Source: ev.ToolName, Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
}

func (m *model) handleMCPStatusEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.status = ev.Text
	m.addLog(logEntry{Kind: "mcp_status", Source: "mcp", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
}

func (m *model) handleMCPCompleteEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.status = ev.Text
	m.addLog(logEntry{Kind: "mcp_complete", Source: "mcp", Summary: ev.Text, Raw: fmt.Sprintf("%+v", ev.Metadata)})
}

func (m *model) handleApprovalRequiredEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	if m.stopping {
		if ev.ToolCallID != "" {
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentCancelToolApproval, ToolCallID: ev.ToolCallID})
		}
		m.addLog(logEntry{Kind: "approval_required_stale", Source: ev.ToolName, Summary: ev.Text, Raw: ev.Text})
		return
	}
	m.mode = modeApproval
	m.approval.toolCallID = ev.ToolCallID
	m.approval.toolName = ev.ToolName
	m.approval.reason = ev.Text
	m.approval.metadata = ev.Metadata
	m.approval.selected = 0
	m.addLog(logEntry{Kind: "approval_required", Source: ev.ToolName, Summary: ev.Text, Raw: ev.Text})
	m.status = "approval required"
}

func (m *model) handleUserInputRequiredEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	if m.stopping {
		if ev.ToolCallID != "" {
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentCancelUserInput, ToolCallID: ev.ToolCallID})
		}
		m.addLog(logEntry{Kind: "user_input_required_stale", Source: ev.ToolName, Summary: fmt.Sprintf("%d questions", len(ev.Questions)), Raw: fmt.Sprintf("%+v", ev.Questions)})
		return
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
}

func (m *model) handleSessionsListedEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.mode = modeSessionPicker
	m.sessionChoices = ev.Choices
	m.sessionIndex = firstSessionChoiceIndex(ev.Choices)
	m.addLog(logEntry{Kind: "sessions_listed", Source: "session", Summary: fmt.Sprintf("%d sessions", len(ev.Choices)), Raw: strings.Join(ev.Choices, "\n")})
	m.status = "session picker"
}

func (m *model) handleModelPickerEvent(ev protocol.Event) {
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
}

func (m *model) handlePermissionsMenuEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.stopBusy()
	m.stopping = false
	m.mode = modePermissionsMenu
	m.permissionsMenu.autoAccept = ev.AutoAccept
	m.permissionsMenu.selected = 0
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.skills.matches = nil
	m.skills.selected = 0
	m.status = "permissions"
}

func (m *model) handleSkillLoadedEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.addLog(logEntry{Kind: "skill_loaded", Source: "skills", Summary: ev.Text, Raw: ev.Text})
	m.status = ev.Text
}

func (m *model) handleSkillsMenuEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.stopBusy()
	m.stopping = false
	m.mode = modeSkillsMenu
	m.skillsMenu.selected = 0
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.skills.matches = nil
	m.skills.selected = 0
	m.status = "skills"
}

func (m *model) handleSkillsManagerEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.stopBusy()
	m.stopping = false
	m.mode = modeSkillsManager
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.skills.matches = nil
	m.skills.selected = 0
	m.setSkillsManagerItems(ev.Skills)
	m.status = "skills"
}

func (m *model) handlePluginsManagerEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.stopBusy()
	m.stopping = false
	m.mode = modePluginsManager
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.skills.matches = nil
	m.skills.selected = 0
	m.setPluginsManagerItems(ev.Plugins)
	m.status = "plugins"
}

func (m *model) handleReviewMenuEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	m.stopBusy()
	m.stopping = false
	m.mode = modeReviewMenu
	m.reviewMenu.selected = 0
	m.reviewTargetPicker = reviewTargetPickerState{}
	m.slash.matches = nil
	m.slash.selected = 0
	m.slash.argumentHint = ""
	m.skills.matches = nil
	m.skills.selected = 0
	m.status = "review"
}

func (m *model) handleViewModeChangedEvent(ev protocol.Event) tea.Cmd {
	m.clearProviderRetryStatus()
	mode := strings.TrimSpace(ev.ViewMode)
	if mode == "" {
		mode = strings.TrimSpace(ev.Text)
		switch mode {
		case protocol.ViewModeToggleMessage(protocol.ViewModeFocus):
			mode = protocol.ViewModeFocus
		case protocol.ViewModeToggleMessage(protocol.ViewModeDefault):
			mode = protocol.ViewModeDefault
		default:
			mode = strings.TrimPrefix(mode, "view:")
			mode = strings.TrimSpace(mode)
		}
	}
	if mode == "" {
		mode = protocol.ViewModeDefault
	}
	m.viewMode = mode
	if strings.TrimSpace(ev.Text) != "" {
		m.setEphemeralInfo(ev.Text)
	}
	m.status = "ready"
	return m.redrawTranscriptForFocusToggleCmd()
}

func (m *model) handleWorktreeExitPromptEvent(ev protocol.Event) {
	m.clearProviderRetryStatus()
	if ev.WorktreeExit != nil {
		m.worktreeExit.summary = *ev.WorktreeExit
		m.worktreeExit.selected = 0
		m.mode = modeWorktreeExit
		m.status = "worktree exit"
	}
}

func (m *model) handleClearScreenEvent() tea.Cmd {
	m.clearProviderRetryStatus()
	m.assembler.Reset()
	m.clearPendingToolCalls()
	m.ephemeralMessages = nil
	m.resetTranscript()
	m.resetTurnVisibility()
	m.logs = nil
	m.diffs = nil
	m.status = "terminal cleared"
	return tea.Sequence(clearScreenCmd(), m.startupHeaderPrintCmd(), waitEventCmd(m.runtime))
}

func (m *model) handleSessionHydratedEvent(ev protocol.Event) tea.Cmd {
	m.mode = modeChat
	m.resumeMenu = false
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
	var eventCmd tea.Cmd
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
	return eventCmd
}

func (m *model) handleExitRequestedEvent() {
	m.clearProviderRetryStatus()
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentShutdown})
}
