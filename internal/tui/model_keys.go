package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/core"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const (
	windowsPasteEnterDelay = 300 * time.Millisecond
	// Time-until-flush after the last paste chunk arrives. Has to be
	// long enough to bridge intra-paste gaps in slow conhost delivery
	// but short enough that the user does not perceive the buffered
	// portion as a separate, late-arriving second insert. Sits one tier
	// above the 60ms cadence window so a single paste cannot be split,
	// and close to the ~100ms human "instant" perception threshold.
	// Codex / DeepSeek-TUI ship 60ms on Windows; this is the safer
	// neighbor that still feels snappy.
	windowsPasteQuietDelay         = 80 * time.Millisecond
	windowsPasteContinuationWindow = 30 * time.Millisecond
)

type windowsDeferredEnterMsg struct {
	id          int
	wasBusy     bool
	wasStopping bool
}

type windowsPasteBurstFlushMsg struct {
	id int
}

type windowsPendingEnterTailMsg struct {
	id int
}

type windowsPasteFallbackState struct {
	enabled             bool
	pendingEnterID      int
	pendingEnter        bool
	pendingEnterBusy    bool
	pendingEnterStop    bool
	pendingEnterTailID  int
	pendingEnterTail    string
	burstID             int
	burstFlushScheduled bool
	buffer              string
	activeUntil         time.Time
	busyInput           bool
	busyInputStop       bool
	bracketedThisInput  bool
	suppressNextCtrlJ   bool
	classifier          windowsPasteBurstClassifier
	// nowFunc lets tests inject a deterministic clock so cadence-based
	// classification can be exercised without real time delays.
	nowFunc func() time.Time
}

func (s *windowsPasteFallbackState) now() time.Time {
	if s.nowFunc != nil {
		return s.nowFunc()
	}
	return time.Now()
}

func (m *model) handleKeyMsg(msg tea.KeyMsg) (tea.Cmd, bool, bool) {
	if !m.quitArmedUntil.IsZero() && time.Now().After(m.quitArmedUntil) {
		m.quitArmedUntil = time.Time{}
		if m.status == "Press Ctrl+C again to quit" {
			m.status = "ready"
		}
	}
	m.updateSlashMatches()
	if m.mode == modeChat && msg.Paste {
		m.cancelWindowsDeferredEnter()
		m.input.HandlePaste(string(msg.Runes))
		m.markWindowsPastedInput()
		m.resetHistoryNavigation()
		m.updateSlashMatches()
		return nil, false, true
	}
	if m.btwPanel.visible {
		if handled := m.handleBtwPanelKey(msg); handled {
			return nil, false, true
		}
	}
	if msg.String() == "ctrl+c" && m.busy {
		return m.interruptBusyTurn(), false, true
	}
	if m.mode == modeChat {
		if cmd, handled := m.handleWindowsPasteFallbackKey(msg); handled {
			return cmd, false, true
		}
	}
	if m.mode == modeChat {
		if cmd, handled := m.handleChatModeKey(msg); handled {
			return cmd, false, true
		}
	}
	switch m.mode {
	case modeApproval:
		return m.handleApprovalKey(msg), false, true
	case modeSessionPicker:
		return m.handleSessionPickerKey(msg), false, true
	case modeUserInput:
		return m.handleUserInputKey(msg), false, true
	case modeModelPicker:
		return m.handleModelPickerKey(msg), false, true
	case modePermissionsPicker:
		return m.handlePermissionsPickerKey(msg), false, true
	case modePermissionsProjectTrustConfirm:
		return m.handlePermissionsProjectTrustConfirmKey(msg), false, true
	case modePermissionsProjectClearConfirm:
		return m.handlePermissionsProjectClearConfirmKey(msg), false, true
	case modePlanImplementation:
		return m.handlePlanImplementationKey(msg), false, true
	case modeSkillsMenu:
		return m.handleSkillsMenuKey(msg), false, true
	case modeSkillsManager:
		return m.handleSkillsManagerKey(msg), false, true
	case modePluginsManager:
		return m.handlePluginsManagerKey(msg), false, true
	case modeReviewMenu:
		return m.handleReviewMenuKey(msg), false, true
	case modeReviewBranchPicker, modeReviewCommitPicker, modeReviewPRPicker:
		return m.handleReviewTargetPickerKey(msg), false, true
	case modeHelp:
		return m.handleHelpKey(msg), false, true
	}
	cmd, quit, handled := m.handleGlobalKey(msg)
	if handled {
		return cmd, quit, true
	}
	cmd, handled = m.handleComposerKey(msg)
	return cmd, false, handled
}

func (m *model) interruptBusyTurn() tea.Cmd {
	m.quitArmedUntil = time.Time{}
	alreadyStopping := m.stopping
	m.cancelBlockingModalForInterrupt(!alreadyStopping)
	if !alreadyStopping {
		if m.svc != nil {
			m.dispatchIntent(service.Intent{Kind: service.IntentShutdown})
		}
		m.status = "stopping"
		m.stopping = true
		m.markWindowsBusyInputStopped()
		m.cancelWindowsDeferredEnter()
		m.appendNotice(m.turnInterruptedNoticeText())
	}
	m.commitLiveTranscript(false)
	return m.flushNativeScrollbackCmd()
}

func (m *model) cancelBlockingModalForInterrupt(dispatch bool) {
	switch m.mode {
	case modeApproval:
		if dispatch && m.approval.toolCallID != "" {
			m.dispatchIntent(service.Intent{Kind: service.IntentCancelToolApproval, ToolCallID: m.approval.toolCallID})
		}
		m.mode = modeChat
	case modeUserInput:
		if dispatch && m.userInput.toolCallID != "" {
			m.dispatchIntent(service.Intent{Kind: service.IntentCancelUserInput, ToolCallID: m.userInput.toolCallID})
		}
		m.mode = modeChat
	}
}

func (m *model) handleChatModeKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "shift+tab", "backtab":
		if m.localSubmitPending > 0 {
			m.status = "wait for command to finish"
			m.refreshViewportContent()
			return m.flushNativeScrollbackCmd(), true
		}
		if !m.busy && !m.hasSlashSuggestions() && !m.hasSkillSuggestions() {
			m.dispatchIntent(service.Intent{Kind: service.IntentToggleMode})
			return nil, true
		}
	case "up":
		if m.shouldHandleHistoryNavigation() && m.historyPrev() {
			return nil, true
		}
		if m.hasSlashSuggestions() {
			if m.slash.selected > 0 {
				m.slash.selected--
			}
			return nil, true
		}
		if m.hasSkillSuggestions() {
			if m.skills.selected > 0 {
				m.skills.selected--
			}
			return nil, true
		}
	case "down":
		if m.shouldHandleHistoryNavigation() && m.historyNext() {
			return nil, true
		}
		if m.hasSlashSuggestions() {
			if m.slash.selected < len(m.slash.matches)-1 {
				m.slash.selected++
			}
			return nil, true
		}
		if m.hasSkillSuggestions() {
			if m.skills.selected < len(m.skills.matches)-1 {
				m.skills.selected++
			}
			return nil, true
		}
	case "tab":
		if m.hasSlashSuggestions() {
			if suggestion, ok := m.selectedSlashSuggestion(); ok {
				m.input.SetValue(suggestion.InsertText)
				m.skillBinding = nil
				m.updateSlashMatches()
			}
			return nil, true
		}
		if m.insertSelectedSkill() {
			return nil, true
		}
	case "esc":
		if m.busy {
			return m.interruptBusyTurn(), true
		}
		if m.hasSlashPanel() {
			m.slash.matches = nil
			m.slash.selected = 0
			m.slash.argumentHint = ""
			return nil, true
		}
		if m.hasSkillSuggestions() {
			m.skills.matches = nil
			m.skills.selected = 0
			return nil, true
		}
	case "pgup", "pgdown", "ctrl+d", "home", "end":
		return m.handleViewportScrollKey(msg.String()), true
	}
	return nil, false
}

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
			return m.submitApprovalDecision(service.IntentAllowTool, "approval_allow", "allow", "approved", "allow")
		case 1:
			return m.submitApprovalDecision(service.IntentAllowToolForSession, "approval_allow_session", "allow for session", "approved for session", "allow_session")
		default:
			return m.submitApprovalDecision(service.IntentDenyTool, "approval_deny", "deny", "rejected", "deny")
		}
	case "a":
		return m.submitApprovalDecision(service.IntentAllowTool, "approval_allow", "allow", "approved", "allow")
	case "s":
		return m.submitApprovalDecision(service.IntentAllowToolForSession, "approval_allow_session", "allow for session", "approved for session", "allow_session")
	case "d":
		return m.submitApprovalDecision(service.IntentDenyTool, "approval_deny", "deny", "rejected", "deny")
	case "esc", "ctrl+c":
		return m.submitApprovalDecision(service.IntentCancelToolApproval, "approval_cancel", "cancel", "canceled", "cancel")
	}
	return nil
}

func (m *model) submitApprovalDecision(kind service.IntentKind, logKind, summary, status, notice string) tea.Cmd {
	toolCallID := m.approval.toolCallID
	if kind == service.IntentCancelToolApproval {
		m.removePendingApprovalToolCall(toolCallID)
		m.sawTerminalToolOutcomeThisTurn = true
	}
	m.dispatchIntent(service.Intent{Kind: kind, ToolCallID: m.approval.toolCallID})
	m.addLog(logEntry{Kind: logKind, Source: m.approval.toolName, Summary: summary, Raw: notice})
	m.mode = modeChat
	m.status = status
	m.appendNotice(m.approvalNoticeText(notice))
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
		m.mode = modeChat
	case "up", "k":
		m.sessionIndex = prevSessionChoiceIndex(m.sessionChoices, m.sessionIndex)
	case "down", "j":
		m.sessionIndex = nextSessionChoiceIndex(m.sessionChoices, m.sessionIndex)
	case "enter":
		selected := sessionChoiceNumberAt(m.sessionChoices, m.sessionIndex)
		if selected > 0 {
			m.dispatchIntent(service.Intent{Kind: service.IntentSelectSession, SessionInput: strconv.Itoa(selected)})
		}
		m.mode = modeChat
	}
	return nil
}

func (m *model) handleUserInputKey(msg tea.KeyMsg) tea.Cmd {
	if len(m.userInput.questions) == 0 {
		m.dispatchIntent(service.Intent{Kind: service.IntentCancelUserInput, ToolCallID: m.userInput.toolCallID})
		m.mode = modeChat
		return nil
	}
	q := m.userInput.questions[m.userInput.index]
	switch msg.String() {
	case "esc":
		m.dispatchIntent(service.Intent{Kind: service.IntentCancelUserInput, ToolCallID: m.userInput.toolCallID})
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
		m.userInput.answers = append(m.userInput.answers, core.UserInputAnswer{ID: q.ID, Label: opt.Label, Value: opt.Label})
		m.userInput.index++
		m.userInput.selectedOption = 0
		if m.userInput.index >= len(m.userInput.questions) {
			resp := core.UserInputResponse{Answers: m.userInput.answers}
			m.dispatchIntent(service.Intent{Kind: service.IntentSubmitUserInput, ToolCallID: m.userInput.toolCallID, UserInput: &resp})
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
				m.dispatchIntent(service.Intent{Kind: service.IntentSetModelAndEffort, Model: modelName, Effort: effort, Thinking: thinking})
				m.model = modelName
				m.effort = effort
				m.thinking = thinking
			}
			m.mode = modeChat
		}
	}
	return nil
}

func (m *model) handlePermissionsPickerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.mode = modeChat
	case "up", "k":
		if m.permissionsPicker.index > 0 {
			m.permissionsPicker.index--
		}
	case "down", "j":
		if m.permissionsPicker.index < len(m.permissionsPicker.choices)-1 {
			m.permissionsPicker.index++
		}
	case "enter":
		choice := safeChoice(m.permissionsPicker.choices, m.permissionsPicker.index)
		switch choice {
		case service.ApprovalChoiceTrustProject:
			m.permissionsProjectConfirm.index = 0
			m.mode = modePermissionsProjectTrustConfirm
			return nil
		case service.ApprovalChoiceClearProject:
			m.permissionsProjectConfirm.index = 0
			m.mode = modePermissionsProjectClearConfirm
			return nil
		}
		mode := approvalChoiceMode(choice)
		if mode != "" {
			m.dispatchIntent(service.Intent{Kind: service.IntentSetApprovalMode, ApprovalMode: mode})
		}
		m.mode = modeChat
	}
	return nil
}

func (m *model) handlePermissionsProjectTrustConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.mode = modePermissionsPicker
	case "up", "k", "down", "j":
		m.permissionsProjectConfirm.index = 1 - m.permissionsProjectConfirm.index
	case "enter":
		if m.permissionsProjectConfirm.index == 0 {
			m.dispatchIntent(service.Intent{Kind: service.IntentSetProjectApproval, ApprovalMode: "never-ask"})
			m.mode = modeChat
		} else {
			m.mode = modePermissionsPicker
		}
	}
	return nil
}

func (m *model) handlePermissionsProjectClearConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.mode = modePermissionsPicker
	case "up", "k", "down", "j":
		m.permissionsProjectConfirm.index = 1 - m.permissionsProjectConfirm.index
	case "enter":
		if m.permissionsProjectConfirm.index == 0 {
			m.dispatchIntent(service.Intent{Kind: service.IntentClearProjectApproval})
			m.mode = modeChat
		} else {
			m.mode = modePermissionsPicker
		}
	}
	return nil
}

func (m *model) handlePlanImplementationKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.mode = modeChat
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
			m.dispatchIntent(service.Intent{Kind: service.IntentImplementPlan, Input: m.lastProposedPlan})
			m.mode = modeChat
			m.refreshViewportContentFollow(true)
			return tea.Sequence(m.flushNativeScrollbackCmd(), busyTickCmd())
		}
		m.mode = modeChat
	}
	return nil
}

func (m *model) handleGlobalKey(msg tea.KeyMsg) (tea.Cmd, bool, bool) {
	switch msg.String() {
	case "ctrl+c":
		if strings.TrimSpace(m.input.Value()) != "" {
			m.input.Reset()
			m.skillBinding = nil
			m.resetWindowsPasteFallbackInputState()
			m.resetHistoryNavigation()
			m.updateSlashMatches()
			m.skills.matches = nil
			m.skills.selected = 0
			m.status = "input cleared"
			return nil, false, true
		}
		now := time.Now()
		if !m.quitArmedUntil.IsZero() && now.Before(m.quitArmedUntil) {
			m.dispatchIntent(service.Intent{Kind: service.IntentShutdown})
			return nil, true, true
		}
		m.quitArmedUntil = now.Add(2 * time.Second)
		m.status = "Press Ctrl+C again to quit"
		return armQuitCmd(2 * time.Second), false, true
	case "enter":
		if m.busy {
			m.submitPromptWhileBusy(m.input.Value())
			return m.flushNativeScrollbackCmd(), false, true
		}
		if m.localSubmitPending > 0 {
			m.status = "wait for command to finish"
			m.refreshViewportContent()
			return m.flushNativeScrollbackCmd(), false, true
		}
		if m.hasSlashSuggestions() && !m.slashSelectionAlreadyTyped() {
			if suggestion, ok := m.selectedSlashSuggestion(); ok {
				m.input.SetValue(suggestion.InsertText)
				m.skillBinding = nil
				m.updateSlashMatches()
				if suggestion.AutoRun {
					return tea.Sequence(m.flushNativeScrollbackCmd(), m.submitPrompt(suggestion.InsertText)), false, true
				}
			}
			return nil, false, true
		}
		if m.insertSelectedSkill() {
			return nil, false, true
		}
		if m.page == pageLogs && m.logFilterInput.Focused() {
			m.logFilter = strings.TrimSpace(m.logFilterInput.Value())
			m.logFilterInput.Blur()
			return nil, false, true
		}
		if raw := m.input.Value(); strings.HasSuffix(raw, "\\") {
			m.input.SetValue(strings.TrimSuffix(raw, "\\") + "\n")
			m.skillBinding = nil
			m.resetHistoryNavigation()
			m.updateSlashMatches()
			return nil, false, true
		}
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			return nil, false, true
		}
		return tea.Sequence(m.flushNativeScrollbackCmd(), m.submitPrompt(value)), false, true
	}
	return nil, false, false
}

func (m *model) handleWindowsDeferredEnter(msg windowsDeferredEnterMsg) tea.Cmd {
	if !m.windowsPasteFallbackEnabled() || !m.windowsPaste.pendingEnter || msg.id != m.windowsPaste.pendingEnterID {
		return nil
	}
	tail := m.consumePendingEnterTail()
	submitCmd := m.resolvePendingEnterAsSubmit()
	if tail != "" {
		m.input.HandlePaste(tail)
		m.resetHistoryNavigation()
		m.updateSlashMatches()
		m.refreshViewportContent()
	}
	return submitCmd
}

func (m *model) resolvePendingEnterAsSubmit() tea.Cmd {
	if !m.windowsPaste.pendingEnter {
		return nil
	}
	wasBusy := m.windowsPaste.pendingEnterBusy
	wasStopping := m.windowsPaste.pendingEnterStop
	m.clearWindowsDeferredEnter()
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		return nil
	}
	if m.busy {
		return tea.Sequence(m.flushNativeScrollbackCmd(), m.submitPromptFromDeferredBusyEnter(value, wasStopping))
	}
	if wasBusy && wasStopping {
		return nil
	}
	if m.localSubmitPending > 0 {
		m.status = "wait for command to finish"
		m.refreshViewportContent()
		return m.flushNativeScrollbackCmd()
	}
	return tea.Sequence(m.flushNativeScrollbackCmd(), m.submitPrompt(value))
}

func (m *model) handleWindowsPasteFallbackKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !m.windowsPasteFallbackEnabled() {
		return nil, false
	}
	now := m.windowsPaste.now()
	// Editing keys (Enter, Tab, arrows, backspace, …) arrive with no
	// rune payload. They must segment paste-cadence detection so a slow
	// typist who hits Enter mid-edit doesn't have their next keystroke
	// folded into a phantom burst.
	if len(msg.Runes) == 0 {
		m.windowsPaste.classifier.reset()
	}
	if m.hasWindowsPasteBuffer() && !m.windowsPaste.activeUntil.IsZero() && now.After(m.windowsPaste.activeUntil) {
		m.flushWindowsPasteBurstToComposer()
	}
	if msg.String() == "enter" {
		switch {
		case m.hasWindowsPasteBuffer():
			if !m.windowsPasteBufferHasLineBreak() {
				return m.deferWindowsSingleLinePasteSubmit(), true
			}
			return m.appendWindowsPasteBurst(now, "\n", true), true
		case m.windowsPaste.pendingEnter:
			suffix := "\n" + m.consumePendingEnterTail() + "\n"
			return m.startWindowsPasteBurstFromComposer(now, suffix, true), true
		case m.shouldDeferWindowsEnterSubmit():
			return m.deferWindowsEnterSubmit(), true
		}
	}
	if msg.String() == "ctrl+j" && (m.windowsPaste.pendingEnter || m.hasWindowsPasteBuffer()) {
		if m.windowsPaste.suppressNextCtrlJ {
			m.windowsPaste.suppressNextCtrlJ = false
			if m.hasWindowsPasteBuffer() {
				return m.scheduleWindowsPasteBurstFlush(now), true
			}
			return nil, true
		}
		if m.hasWindowsPasteBuffer() {
			return m.appendWindowsPasteBurst(now, "\n", false), true
		}
		suffix := "\n"
		if tail := m.consumePendingEnterTail(); tail != "" {
			suffix = "\n" + tail + "\n"
		}
		return m.startWindowsPasteBurstFromComposer(now, suffix, false), true
	}
	if msg.String() == "tab" && !m.hasSlashSuggestions() && !m.hasSkillSuggestions() {
		if m.windowsPaste.pendingEnter || m.hasWindowsPasteBuffer() {
			return m.appendWindowsPasteFallbackText(now, "    "), true
		}
		m.insertWindowsPasteFallbackInactiveText("    ")
		m.refreshViewportContent()
		return nil, true
	}
	if len(msg.Runes) > 0 {
		text := string(msg.Runes)
		// classify() is stateful: it records arrival time so the next chunk
		// can detect terminal-streamed paste cadence even when both chunks
		// look like ordinary typing in isolation.
		decision := m.windowsPaste.classifier.classify(now, text)
		if m.hasWindowsPasteBuffer() {
			return m.appendWindowsPasteBurst(now, text, false), true
		}
		if m.windowsPaste.pendingEnter {
			tailHeld := m.windowsPaste.pendingEnterTail != ""
			if tailHeld || decision == windowsPasteChunkBurst || isASCIIMultiRune(text) {
				suffix := "\n" + m.consumePendingEnterTail() + text
				return m.startWindowsPasteBurstFromComposer(now, suffix, false), true
			}
			return m.deferWindowsPendingEnterTail(text), true
		}
		if decision == windowsPasteChunkBurst {
			// When time-escalation triggers, the first chunks of the paste
			// have already been inserted into the textarea at the cursor.
			// We deliberately do NOT pull them back out of the composer:
			// the subsequent flush ends with HandlePaste, which inserts at
			// the current cursor — immediately after the already-typed
			// chunks — preserving original character order even when the
			// cursor sits mid-line (e.g. pasting XYZ into "a|c" yields
			// "aXYZc", not "aXYcZ").
			return m.startWindowsPasteBurst(now, text, false), true
		}
		m.markWindowsBusyInput(m.busy, m.stopping)
		return nil, false
	}
	if m.windowsPaste.pendingEnter && m.shouldCancelWindowsDeferredEnterForKey(msg) {
		m.cancelWindowsDeferredEnter()
	}
	if m.hasWindowsPasteBuffer() {
		m.flushWindowsPasteBurstToComposer()
	}
	return nil, false
}

func (m *model) shouldDeferWindowsEnterSubmit() bool {
	if !m.windowsPasteFallbackEnabled() || m.mode != modeChat || m.windowsPaste.bracketedThisInput {
		return false
	}
	if m.hasSlashSuggestions() || m.hasSkillSuggestions() {
		return false
	}
	if m.localSubmitPending > 0 {
		return false
	}
	if m.page == pageLogs && m.logFilterInput.Focused() {
		return false
	}
	raw := m.input.Value()
	if strings.HasSuffix(raw, "\\") {
		return false
	}
	return strings.TrimSpace(raw) != ""
}

func (m *model) shouldCancelWindowsDeferredEnterForKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		return !m.busy
	case "enter", "ctrl+j", "tab":
		return false
	}
	return true
}

func (m *model) deferWindowsEnterSubmit() tea.Cmd {
	return m.deferWindowsEnterSubmitAfter(windowsPasteEnterDelay)
}

func (m *model) deferWindowsEnterSubmitAfter(delay time.Duration) tea.Cmd {
	m.windowsPaste.pendingEnterID++
	id := m.windowsPaste.pendingEnterID
	m.windowsPaste.pendingEnter = true
	m.windowsPaste.pendingEnterBusy = m.busy
	m.windowsPaste.pendingEnterStop = m.stopping
	m.windowsPaste.pendingEnterTail = ""
	m.markWindowsBusyInput(m.busy, m.stopping)
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return windowsDeferredEnterMsg{
			id:          id,
			wasBusy:     m.windowsPaste.pendingEnterBusy,
			wasStopping: m.windowsPaste.pendingEnterStop,
		}
	})
}

func (m *model) appendWindowsPasteFallbackText(now time.Time, text string) tea.Cmd {
	if m.windowsPaste.pendingEnter {
		suffix := "\n" + m.consumePendingEnterTail() + text
		return m.startWindowsPasteBurstFromComposer(now, suffix, false)
	}
	return m.appendWindowsPasteBurst(now, text, false)
}

func (m *model) insertWindowsPasteFallbackInactiveText(text string) {
	m.input.HandlePaste(text)
	m.resetHistoryNavigation()
	m.updateSlashMatches()
}

func (m *model) cancelWindowsDeferredEnter() {
	m.clearWindowsDeferredEnter()
}

func (m *model) clearWindowsDeferredEnter() {
	m.windowsPaste.pendingEnter = false
	m.windowsPaste.pendingEnterBusy = false
	m.windowsPaste.pendingEnterStop = false
	m.windowsPaste.pendingEnterTail = ""
	m.windowsPaste.suppressNextCtrlJ = false
}

func (m *model) deferWindowsPendingEnterTail(text string) tea.Cmd {
	m.windowsPaste.pendingEnterTailID++
	id := m.windowsPaste.pendingEnterTailID
	m.windowsPaste.pendingEnterTail = text
	return tea.Tick(windowsPasteContinuationWindow, func(time.Time) tea.Msg {
		return windowsPendingEnterTailMsg{id: id}
	})
}

// consumePendingEnterTail returns and clears any rune parked in the 30 ms
// tail window, invalidating its in-flight tick so it becomes a no-op.
func (m *model) consumePendingEnterTail() string {
	tail := m.windowsPaste.pendingEnterTail
	if tail == "" {
		return ""
	}
	m.windowsPaste.pendingEnterTail = ""
	m.windowsPaste.pendingEnterTailID++
	return tail
}

func (m *model) handleWindowsPendingEnterTail(msg windowsPendingEnterTailMsg) tea.Cmd {
	if !m.windowsPasteFallbackEnabled() || !m.windowsPaste.pendingEnter || msg.id != m.windowsPaste.pendingEnterTailID || m.windowsPaste.pendingEnterTail == "" {
		return nil
	}
	tail := m.consumePendingEnterTail()
	submitCmd := m.resolvePendingEnterAsSubmit()
	m.input.HandlePaste(tail)
	m.resetHistoryNavigation()
	m.updateSlashMatches()
	m.refreshViewportContent()
	return submitCmd
}

func (m model) hasWindowsPasteBuffer() bool {
	return m.windowsPaste.buffer != ""
}

func (m model) windowsPasteBufferHasLineBreak() bool {
	return strings.Contains(m.windowsPaste.buffer, "\n")
}

func (m *model) deferWindowsSingleLinePasteSubmit() tea.Cmd {
	m.flushWindowsPasteBurstToComposer()
	if m.localSubmitPending > 0 {
		m.status = "wait for command to finish"
		m.refreshViewportContent()
		return m.flushNativeScrollbackCmd()
	}
	return m.deferWindowsEnterSubmit()
}

func (m *model) startWindowsPasteBurstFromComposer(now time.Time, suffix string, suppressNextCtrlJ bool) tea.Cmd {
	prefix := m.input.Value()
	m.input.SetValue("")
	return m.startWindowsPasteBurst(now, prefix+suffix, suppressNextCtrlJ)
}

func (m *model) startWindowsPasteBurst(now time.Time, text string, suppressNextCtrlJ bool) tea.Cmd {
	m.windowsPaste.buffer = text
	return m.afterWindowsPasteBurstChanged(now, suppressNextCtrlJ)
}

func (m *model) appendWindowsPasteBurst(now time.Time, text string, suppressNextCtrlJ bool) tea.Cmd {
	m.windowsPaste.buffer += text
	return m.afterWindowsPasteBurstChanged(now, suppressNextCtrlJ)
}

func (m *model) afterWindowsPasteBurstChanged(now time.Time, suppressNextCtrlJ bool) tea.Cmd {
	wasBusy := m.windowsPaste.pendingEnterBusy || m.windowsPaste.busyInput || m.busy
	wasStopping := m.windowsPaste.pendingEnterStop || m.windowsPaste.busyInputStop || m.stopping
	m.clearWindowsDeferredEnter()
	m.windowsPaste.bracketedThisInput = false
	m.windowsPaste.suppressNextCtrlJ = suppressNextCtrlJ
	m.markWindowsBusyInput(wasBusy, wasStopping)
	m.resetHistoryNavigation()
	m.updateSlashMatches()
	return m.scheduleWindowsPasteBurstFlush(now)
}

func (m *model) scheduleWindowsPasteBurstFlush(now time.Time) tea.Cmd {
	return m.scheduleWindowsPasteBurstFlushAfter(now, windowsPasteQuietDelay)
}

func (m *model) scheduleWindowsPasteBurstFlushAfter(now time.Time, delay time.Duration) tea.Cmd {
	m.windowsPaste.activeUntil = now.Add(delay)
	if m.windowsPaste.burstFlushScheduled {
		return nil
	}
	m.windowsPaste.burstID++
	id := m.windowsPaste.burstID
	m.windowsPaste.burstFlushScheduled = true
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return windowsPasteBurstFlushMsg{id: id}
	})
}

func (m *model) handleWindowsPasteBurstFlush(msg windowsPasteBurstFlushMsg) tea.Cmd {
	if !m.windowsPasteFallbackEnabled() || msg.id != m.windowsPaste.burstID {
		return nil
	}
	if !m.hasWindowsPasteBuffer() {
		m.windowsPaste.burstFlushScheduled = false
		return nil
	}
	if !m.windowsPaste.activeUntil.IsZero() {
		if remaining := time.Until(m.windowsPaste.activeUntil); remaining > 0 {
			return tea.Tick(remaining, func(time.Time) tea.Msg {
				return windowsPasteBurstFlushMsg{id: msg.id}
			})
		}
	}
	m.flushWindowsPasteBurstToComposer()
	return nil
}

func (m *model) flushWindowsPasteBurstToComposer() {
	text := m.windowsPaste.buffer
	if text == "" {
		return
	}
	m.windowsPaste.buffer = ""
	m.windowsPaste.activeUntil = time.Time{}
	m.windowsPaste.burstFlushScheduled = false
	m.windowsPaste.suppressNextCtrlJ = false
	m.input.HandlePaste(text)
	m.resetHistoryNavigation()
	m.updateSlashMatches()
	m.refreshViewportContent()
}

func (m model) hasPendingWindowsBusyInput() bool {
	if !m.windowsPasteFallbackEnabled() {
		return false
	}
	if strings.TrimSpace(m.input.Value()) == "" && strings.TrimSpace(m.windowsPaste.buffer) == "" {
		return false
	}
	return (m.windowsPaste.pendingEnter && m.windowsPaste.pendingEnterBusy) || m.windowsPaste.busyInput || m.hasWindowsPasteBuffer()
}

func (m *model) markWindowsBusyInput(wasBusy, wasStopping bool) {
	if !m.windowsPasteFallbackEnabled() {
		return
	}
	if wasBusy {
		m.windowsPaste.busyInput = true
	}
	if wasStopping {
		m.windowsPaste.busyInputStop = true
	}
}

func (m *model) markWindowsBusyInputStopped() {
	if !m.windowsPasteFallbackEnabled() {
		return
	}
	if m.windowsPaste.pendingEnter {
		m.windowsPaste.pendingEnterBusy = m.windowsPaste.pendingEnterBusy || m.busy
		m.windowsPaste.pendingEnterStop = true
	}
	if m.windowsPaste.busyInput || !m.windowsPaste.activeUntil.IsZero() {
		m.windowsPaste.busyInput = true
		m.windowsPaste.busyInputStop = true
	}
}

func (m *model) markWindowsPastedInput() {
	m.windowsPaste.buffer = ""
	m.windowsPaste.bracketedThisInput = true
	m.windowsPaste.activeUntil = time.Time{}
	m.windowsPaste.burstFlushScheduled = false
	m.windowsPaste.suppressNextCtrlJ = false
	m.markWindowsBusyInput(m.busy, m.stopping)
}

func (m *model) resetWindowsPasteFallbackInputState() {
	m.clearWindowsDeferredEnter()
	m.windowsPaste.buffer = ""
	m.windowsPaste.bracketedThisInput = false
	m.windowsPaste.activeUntil = time.Time{}
	m.windowsPaste.burstFlushScheduled = false
	m.windowsPaste.busyInput = false
	m.windowsPaste.busyInputStop = false
	m.windowsPaste.suppressNextCtrlJ = false
}

func (m *model) resetWindowsPasteFallbackIfInputEmpty() {
	if m.hasWindowsPasteBuffer() {
		return
	}
	if strings.TrimSpace(m.input.Value()) == "" {
		m.resetWindowsPasteFallbackInputState()
	}
}

func (m model) windowsPasteFallbackEnabled() bool {
	return m.windowsPaste.enabled
}

func (m *model) handleComposerKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "shift+enter", "ctrl+j":
		m.input.InsertNewline()
		m.resetHistoryNavigation()
		m.updateSlashMatches()
		return nil, true
	case "ctrl+p":
		m.historyPrev()
		return nil, true
	case "ctrl+n":
		m.historyNext()
		return nil, true
	}
	if m.input.HandleKey(msg) {
		if msg.String() == "ctrl+u" {
			m.skillBinding = nil
			m.resetWindowsPasteFallbackInputState()
		}
		m.resetWindowsPasteFallbackIfInputEmpty()
		m.resetHistoryNavigation()
		m.updateSlashMatches()
		return nil, true
	}
	return nil, false
}

func (m *model) applyPalette() {
	if m.palette.selected < 0 || m.palette.selected >= len(m.palette.actions) {
		return
	}
	m.palette.actions[m.palette.selected].Run(m)
}
