package tui

import (
	"strings"

	"github.com/usewhale/whale/internal/runtime/protocol"
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

func (m *model) appendLiveAssistantMessage(text string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddAssistantMessage(text)
	m.refreshLiveViewportContent()
}

func (m *model) appendSystemNotice(notice *tuirender.SystemNotice) {
	if notice == nil {
		return
	}
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddSystemNotice(notice)
	m.refreshLiveViewportContent()
}

func (m *model) appendStatus(text string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddStatus(text)
	m.refreshLiveViewportContent()
}

func (m *model) appendLiveLocalResult(result *protocol.LocalResult) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.assembler.AddLocalResult(result)
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
	m.resetBusyTokenEstimate()
}

func (m *model) recordAssistantDelta(text string) {
	m.visibleAssistantThisTurn += text
	m.recordModelOutputDelta(text)
}

func (m *model) recordModelOutputDelta(text string) {
	m.addBusyTokenEstimate(text)
}

// estimateTokens approximates token count from text using DeepSeek's
// documented ratios: English char ≈ 0.3 token, Chinese char ≈ 0.6 token.
// +1 safety margin is applied once to the total, not per-chunk.
func estimateTokens(s string) int {
	ascii, nonASCII := tokenEstimateCharCounts(s)
	return estimateTokensFromCounts(ascii, nonASCII)
}

func (m *model) resetBusyTokenEstimate() {
	m.busyTokenCount = 0
	m.busyTokenASCIIChars = 0
	m.busyTokenNonASCIIChars = 0
}

func (m *model) addBusyTokenEstimate(s string) {
	ascii, nonASCII := tokenEstimateCharCounts(s)
	m.busyTokenASCIIChars += ascii
	m.busyTokenNonASCIIChars += nonASCII
	m.busyTokenCount = estimateTokensFromCounts(m.busyTokenASCIIChars, m.busyTokenNonASCIIChars)
}

func tokenEstimateCharCounts(s string) (ascii int, nonASCII int) {
	for _, r := range s {
		if r > 127 {
			nonASCII++
		} else {
			ascii++
		}
	}
	return ascii, nonASCII
}

func estimateTokensFromCounts(ascii, nonASCII int) int {
	if ascii == 0 && nonASCII == 0 {
		return 0
	}
	return ascii*3/10 + nonASCII*3/5 + 1
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
		m.appendStatus("The model returned reasoning only and did not produce a visible plan. Ask it to propose the plan again.")
	} else {
		m.appendStatus("The model returned reasoning only and did not produce a visible answer. Ask it to answer directly or retry the last step.")
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
	case "result_denied", "result_canceled", "result_timeout", "result_blocked", "result_mode_hint", "result_http_error", "result_usage_hint":
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
