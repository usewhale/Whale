package tui

import (
	"strings"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) append(role, text string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AppendDelta(role, text)
	m.refreshLiveViewportContent()
}

func (m *model) appendNotice(text string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddNotice(text)
	m.refreshLiveViewportContent()
}

func (m *model) setEphemeralInfo(text string) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		m.ephemeralMessages = nil
		return
	}
	m.ephemeralMessages = []tuirender.UIMessage{{
		Role: "info",
		Kind: tuirender.KindText,
		Text: t,
	}}
	m.refreshViewportContentFollow(true)
}

func (m *model) clearEphemeralMessages() {
	if len(m.ephemeralMessages) == 0 {
		return
	}
	m.ephemeralMessages = nil
	m.refreshViewportContent()
}

func (m *model) appendLiveToolResult(text, role string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddToolResultWithRole("", text, role)
	m.refreshLiveViewportContent()
}

func (m *model) beginTurnTranscript() {
	m.turnTranscriptStart = len(m.transcript)
	m.visibleAssistantThisTurn = ""
}

func (m *model) recordAssistantDelta(text string) {
	m.visibleAssistantThisTurn += text
}

func (m *model) reconcileFinalAssistant(lastResponse string) bool {
	final := strings.TrimRight(lastResponse, "\n")
	if strings.TrimSpace(final) == "" {
		return false
	}
	visible := strings.TrimRight(m.visibleAssistantThisTurn, "\n")
	if visible == final {
		return false
	}
	if visible != "" && strings.HasPrefix(final, visible) {
		m.append("assistant", strings.TrimPrefix(final, visible))
		m.sawAssistantThisTurn = true
		return true
	}
	if m.replaceLiveTurnAssistant(final) {
		m.sawAssistantThisTurn = true
		return true
	}
	if m.replaceCommittedTurnAssistant(final) {
		m.sawAssistantThisTurn = true
		return true
	}
	m.append("assistant", final)
	m.sawAssistantThisTurn = true
	return true
}

func (m *model) replaceLiveTurnAssistant(text string) bool {
	if m.assembler == nil || !m.assembler.ReplaceTrailingAssistantMessages(text) {
		return false
	}
	m.refreshLiveViewportContent()
	return true
}

func (m *model) replaceCommittedTurnAssistant(text string) bool {
	if m.assembler != nil && m.assembler.Len() > 0 {
		return false
	}
	start := m.turnTranscriptStart
	if start < 0 || start > len(m.transcript) {
		start = len(m.transcript)
	}
	firstAssistantRel := -1
	for i, msg := range m.transcript[start:] {
		if msg.Role == "assistant" && msg.Kind == tuirender.KindText {
			if firstAssistantRel == -1 {
				firstAssistantRel = i
			}
			continue
		}
		if firstAssistantRel != -1 {
			return false
		}
	}
	if firstAssistantRel == -1 {
		return false
	}
	out := make([]tuirender.UIMessage, 0, len(m.transcript))
	out = append(out, m.transcript[:start]...)
	replaced := false
	for _, msg := range m.transcript[start:] {
		if msg.Role == "assistant" && msg.Kind == tuirender.KindText {
			if !replaced {
				msg.Text = text
				out = append(out, msg)
				replaced = true
			}
			continue
		}
		out = append(out, msg)
	}
	m.transcript = out
	if m.nativeScrollbackPrinted > start {
		m.nativeScrollbackPrinted = start
	}
	m.refreshViewportContentFollow(false)
	return true
}

func (m *model) markNoFinalAnswerIfNeeded() bool {
	if !m.sawReasoningThisTurn || m.sawAssistantThisTurn || m.sawPlanThisTurn || m.sawTerminalToolOutcomeThisTurn {
		return false
	}
	if m.chatMode == "plan" {
		m.appendNotice("No plan was produced. Ask the model to propose the plan again.")
	} else {
		m.appendNotice("No final answer was produced. Ask the model to answer directly or retry the last step.")
	}
	m.addLog(logEntry{
		Kind:    "no_final_answer",
		Source:  "assistant",
		Summary: "reasoning-only turn completed without final answer",
		Raw:     "The model produced reasoning content but no assistant content.",
	})
	return true
}

func (m *model) markMissingProposedPlanIfNeeded(wasBusy bool) bool {
	if !wasBusy || m.chatMode != "plan" || m.sawPlanThisTurn || !m.sawAssistantThisTurn {
		return false
	}
	m.appendNotice("No proposed plan was produced. Continue planning, or ask the model to output the final plan inside <proposed_plan>...</proposed_plan>.")
	m.addLog(logEntry{
		Kind:    "missing_proposed_plan",
		Source:  "assistant",
		Summary: "plan-mode turn completed without a proposed_plan block",
		Raw:     "The model produced assistant content in Plan mode but did not emit a <proposed_plan> block.",
	})
	return true
}

func suppressesNoFinalAnswer(role string) bool {
	switch strings.TrimSpace(role) {
	case "result_denied", "result_canceled", "result_timeout":
		return true
	default:
		return false
	}
}

func (m *model) appendPlanDelta(text string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddPlanDelta(text)
	m.refreshLiveViewportContent()
}
