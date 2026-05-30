package tui

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) handleApprovalKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "left", "h":
		m.approval.selected = (m.approval.selected + 2) % 3
		return nil
	case "right", "l", "tab":
		m.approval.selected = (m.approval.selected + 1) % 3
		return nil
	case "enter":
		switch m.approval.selected {
		case 0:
			return m.submitApprovalDecision(protocol.IntentAllowTool, "approval_allow", "allow", "approved", "allow")
		case 1:
			return m.submitApprovalDecision(protocol.IntentAllowToolForSession, "approval_allow_session", "allow for session", "approved for session", "allow_session")
		default:
			return m.submitApprovalDecision(protocol.IntentDenyTool, "approval_deny", "deny", "rejected", "deny")
		}
	case "a":
		return m.submitApprovalDecision(protocol.IntentAllowTool, "approval_allow", "allow", "approved", "allow")
	case "s":
		return m.submitApprovalDecision(protocol.IntentAllowToolForSession, "approval_allow_session", "allow for session", "approved for session", "allow_session")
	case "d":
		return m.submitApprovalDecision(protocol.IntentDenyTool, "approval_deny", "deny", "rejected", "deny")
	case "esc", "ctrl+c":
		return m.submitApprovalDecision(protocol.IntentCancelToolApproval, "approval_cancel", "cancel", "canceled", "cancel")
	}
	return nil
}

func (m *model) submitApprovalDecision(kind protocol.IntentKind, logKind, summary, status, notice string) tea.Cmd {
	toolCallID := m.approval.toolCallID
	if kind == protocol.IntentCancelToolApproval {
		m.removePendingApprovalToolCall(toolCallID)
		m.sawTerminalToolOutcomeThisTurn = true
	}
	m.dispatchIntent(protocol.Intent{Kind: kind, ToolCallID: m.approval.toolCallID})
	m.addLog(logEntry{Kind: logKind, Source: m.approval.toolName, Summary: summary, Raw: notice})
	m.mode = modeChat
	m.status = status
	m.appendSystemNotice(m.approvalNotice(notice))
	return m.flushNativeScrollbackCmd()
}

func (m *model) removePendingApprovalToolCall(toolCallID string) {
	if m.assembler != nil {
		m.assembler.RemoveToolCall(toolCallID)
	}
	m.markToolCallResolved(toolCallID)
}

func (m *model) handleSessionPickerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.resumeMenu {
			return tea.Quit
		}
		m.mode = modeChat
	case "up", "k":
		m.sessionIndex = prevSessionChoiceIndex(m.sessionChoices, m.sessionIndex)
	case "down", "j":
		m.sessionIndex = nextSessionChoiceIndex(m.sessionChoices, m.sessionIndex)
	case "enter":
		selected := sessionChoiceNumberAt(m.sessionChoices, m.sessionIndex)
		if selected > 0 {
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSelectSession, SessionInput: strconv.Itoa(selected)})
		}
	}
	return nil
}

func (m *model) handleUserInputKey(msg tea.KeyMsg) tea.Cmd {
	if len(m.userInput.questions) == 0 {
		m.dispatchIntent(protocol.Intent{Kind: protocol.IntentCancelUserInput, ToolCallID: m.userInput.toolCallID})
		m.mode = modeChat
		return nil
	}
	q := m.userInput.questions[m.userInput.index]
	switch msg.String() {
	case "esc":
		if m.busy {
			return m.interruptBusyTurn()
		}
		m.dispatchIntent(protocol.Intent{Kind: protocol.IntentCancelUserInput, ToolCallID: m.userInput.toolCallID})
		m.mode = modeChat
	case "up", "k":
		if m.userInput.selectedOption > 0 {
			m.userInput.selectedOption--
		}
	case "down", "j":
		if m.userInput.selectedOption < len(q.Options)-1 {
			m.userInput.selectedOption++
		}
	case "enter":
		opt := q.Options[m.userInput.selectedOption]
		m.userInput.answers = append(m.userInput.answers, protocol.UserInputAnswer{ID: q.ID, Label: opt.Label, Value: opt.Label})
		m.userInput.index++
		m.userInput.selectedOption = 0
		if m.userInput.index >= len(m.userInput.questions) {
			resp := protocol.UserInputResponse{Answers: m.userInput.answers}
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSubmitUserInput, ToolCallID: m.userInput.toolCallID, UserInput: &resp})
			m.mode = modeChat
		}
	}
	return nil
}

func (m *model) handleModelPickerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.modelPicker.stage > 0 {
			m.modelPicker.stage--
		} else {
			m.mode = modeChat
		}
	case "up", "k":
		if m.modelPicker.stage == 0 && m.modelPicker.modelIx > 0 {
			m.modelPicker.modelIx--
		}
		if m.modelPicker.stage == 1 && m.modelPicker.effIx > 0 {
			m.modelPicker.effIx--
		}
		if m.modelPicker.stage == 2 && m.modelPicker.thinkIx > 0 {
			m.modelPicker.thinkIx--
		}
	case "down", "j":
		if m.modelPicker.stage == 0 && m.modelPicker.modelIx < len(m.modelPicker.models)-1 {
			m.modelPicker.modelIx++
		}
		if m.modelPicker.stage == 1 && m.modelPicker.effIx < len(m.modelPicker.efforts)-1 {
			m.modelPicker.effIx++
		}
		if m.modelPicker.stage == 2 && m.modelPicker.thinkIx < len(m.modelPicker.thinkings)-1 {
			m.modelPicker.thinkIx++
		}
	case "enter":
		if m.modelPicker.stage == 0 {
			m.modelPicker.stage = 1
		} else if m.modelPicker.stage == 1 {
			m.modelPicker.stage = 2
		} else {
			modelName := safeChoice(m.modelPicker.models, m.modelPicker.modelIx)
			effort := safeChoice(m.modelPicker.efforts, m.modelPicker.effIx)
			thinking := safeChoice(m.modelPicker.thinkings, m.modelPicker.thinkIx)
			if modelName != "" && effort != "" && thinking != "" {
				m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSetModelAndEffort, Model: modelName, Effort: effort, Thinking: thinking})
				m.model = modelName
				m.effort = effort
				m.thinking = thinking
			}
			m.mode = modeChat
		}
	}
	return nil
}

func (m *model) handlePermissionsMenuKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.mode = modeChat
	case "up", "k", "left", "h":
		if m.permissionsMenu.selected > 0 {
			m.permissionsMenu.selected--
		}
	case "down", "j", "right", "l", "tab":
		if m.permissionsMenu.selected < 1 {
			m.permissionsMenu.selected++
		}
	case "enter":
		if m.permissionsMenu.selected == 0 {
			mode := "auto_accept"
			if m.permissionsMenu.autoAccept {
				mode = "ask"
			}
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentSetApprovalMode, ApprovalMode: mode})
		}
		m.mode = modeChat
	}
	return nil
}

func (m *model) handlePlanImplementationKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.declinePlanImplementation()
	case "up", "k", "left", "h":
		if m.planImplementation.index > 0 {
			m.planImplementation.index--
		}
	case "down", "j", "right", "l", "tab":
		if m.planImplementation.index < 1 {
			m.planImplementation.index++
		}
	case "enter":
		if m.localSubmitPending > 0 {
			m.status = "wait for command to finish"
			m.refreshViewportContent()
			return m.flushNativeScrollbackCmd()
		}
		if m.planImplementation.index == 0 {
			m.appendTranscript("you", tuirender.KindText, "Implement the plan.")
			m.beginTurnTranscript()
			m.startBusy()
			m.status = "running"
			m.chatMode = "agent"
			m.dispatchIntent(protocol.Intent{Kind: protocol.IntentImplementPlan})
			m.mode = modeChat
			m.refreshViewportContentFollow(true)
			return tea.Sequence(m.flushNativeScrollbackCmd(), busyTickCmd())
		}
		m.declinePlanImplementation()
	}
	return nil
}

func (m *model) declinePlanImplementation() {
	m.mode = modeChat
	m.status = "plan not approved"
	m.lastProposedPlan = ""
	m.sawPlanThisTurn = false
	m.deferredPlanPicker = false
	m.planImplementation.index = 0
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentDeclinePlan})
	m.refreshViewportContent()
}
