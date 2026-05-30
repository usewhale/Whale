package tui

import (
	"fmt"
)

func (m model) cachedViewDuringWindowsPaste() (string, bool) {
	if !m.hasWindowsPasteBuffer() || m.viewCache == nil || !m.viewCache.valid {
		return "", false
	}
	if m.viewCache.page != m.page || m.viewCache.width != m.width || m.viewCache.height != m.height {
		return "", false
	}
	if m.viewCache.signature != m.viewCacheSignature() {
		return "", false
	}
	return m.viewCache.view, true
}

func (m model) rememberView(view string) {
	if m.viewCache == nil {
		return
	}
	m.viewCache.valid = true
	m.viewCache.page = m.page
	m.viewCache.width = m.width
	m.viewCache.height = m.height
	m.viewCache.signature = m.viewCacheSignature()
	m.viewCache.view = view
}

func (m model) viewCacheSignature() string {
	approvalToolCallID := ""
	approvalToolName := ""
	approvalReason := ""
	approvalSelected := 0
	if m.mode == modeApproval {
		approvalToolCallID = m.approval.toolCallID
		approvalToolName = m.approval.toolName
		approvalReason = m.approval.reason
		approvalSelected = m.approval.selected
	}
	userInputToolCallID := ""
	userInputQuestion := ""
	userInputIndex := 0
	userInputSelected := 0
	userInputOptionCount := 0
	if m.mode == modeUserInput {
		userInputToolCallID = m.userInput.toolCallID
		userInputIndex = m.userInput.index
		userInputSelected = m.userInput.selectedOption
		if m.userInput.index >= 0 && m.userInput.index < len(m.userInput.questions) {
			q := m.userInput.questions[m.userInput.index]
			userInputQuestion = q.Question
			userInputOptionCount = len(q.Options)
		}
	}
	return fmt.Sprintf(
		"mode=%d page=%d status=%s busy=%t stopping=%t local=%d chat=%s auto=%t view=%s model=%s effort=%s thinking=%s branch=%s cwd=%s slash=%d/%d/%s files=%t/%t/%d/%d/%s skills=%d/%d approval=%s/%s/%s/%d user=%s/%d/%d/%d/%s",
		m.mode, m.page, m.status, m.busy, m.stopping, m.localSubmitPending, m.chatMode, m.autoAccept, m.viewMode,
		m.model, m.effort, m.thinking, m.gitBranch, m.cwd,
		len(m.slash.matches), m.slash.selected, m.slash.argumentHint,
		m.files.active, m.files.searching, len(m.files.matches), m.files.selected, m.files.query,
		len(m.skills.matches), m.skills.selected,
		approvalToolCallID, approvalToolName, approvalReason, approvalSelected,
		userInputToolCallID, userInputIndex, userInputSelected, userInputOptionCount, userInputQuestion,
	)
}
