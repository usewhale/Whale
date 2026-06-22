package tui

import (
	"strings"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const noFinalAnswerStatusPrefix = "The model returned reasoning only"

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

func (m *model) removeNoFinalAnswerStatusMessages() {
	removed := false
	if len(m.transcript) > 0 {
		out := m.transcript[:0]
		for _, msg := range m.transcript {
			if isNoFinalAnswerStatus(msg) {
				removed = true
				continue
			}
			out = append(out, msg)
		}
		m.transcript = out
	}
	if m.assembler != nil && m.assembler.RemoveStatusMessagesWithPrefix(noFinalAnswerStatusPrefix) {
		removed = true
	}
	if removed {
		m.refreshViewportContentFollow(false)
	}
}

func isNoFinalAnswerStatus(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindStatus && strings.HasPrefix(strings.TrimSpace(msg.Text), noFinalAnswerStatusPrefix)
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
	// In Plan mode the whole reply streams as plan deltas and is finalized into a
	// Proposed Plan card (plan_completed → SetPlan), so visibleAssistantThisTurn is
	// empty and the committed message is KindPlan, not KindText. Without this guard
	// reconcile would not recognize the plan card as the final answer and would
	// append the same text again as an assistant bubble — rendering it twice. The
	// plan card already IS this turn's final answer, so there is nothing to do.
	if m.sawPlanCompletedThisTurn {
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
	if m.hasPendingLifecycleItems() {
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
	// A real plan finalized this turn (plan_completed) — nothing missing.
	if !wasBusy || m.chatMode != "plan" || m.sawPlanCompletedThisTurn {
		return false
	}
	// Only act when the turn actually streamed plan content that never finalized
	// (e.g. an investigation preamble before the turn ended via the cap/forced
	// summary). A turn that produced nothing, or ended on a clarifying question,
	// is handled elsewhere and must not be flagged as a missing plan here.
	if !m.sawPlanThisTurn {
		return false
	}
	// The streamed text is investigation, not an approvable plan: demote the
	// Proposed Plan card to ordinary assistant text so it is not mislabeled.
	if m.assembler != nil {
		m.assembler.DemoteUncompletedPlan()
	}
	m.appendStatus("No plan was produced. Stay in Plan mode and ask the model to write the final plan as its reply.")
	m.addLog(logEntry{
		Kind:    "missing_proposed_plan",
		Source:  "assistant",
		Summary: "plan-mode turn streamed content without finalizing a plan",
		Raw:     "The model produced Plan-mode content but did not finalize a plan.",
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
