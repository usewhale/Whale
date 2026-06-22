package tui

import (
	"strings"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) resetTranscript() {
	m.transcript = nil
	m.resetTimeline()
	m.startupHeaderPrinted = false
	if m.startupHeaderOnce == nil {
		m.startupHeaderOnce = new(bool)
	}
	*m.startupHeaderOnce = false
	m.nativeScrollbackPrinted = 0
	m.turnTranscriptStart = len(m.transcript)
	m.visibleAssistantThisTurn = ""
	m.resetBusyTokenEstimate()
	m.viewportLayoutReady = false
	m.viewportFrozen = false
	m.frozenChatMessages = nil
}

func (m *model) appendTranscript(role string, kind tuirender.MessageKind, text string) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		return
	}
	if kind == "" {
		kind = tuirender.KindText
	}
	m.transcript = append(m.transcript, tuirender.UIMessage{
		Role: role,
		Kind: kind,
		Text: t,
	})
	m.refreshViewportContentFollow(true)
}

func (m *model) appendLocalResult(result *protocol.LocalResult) {
	if result == nil {
		return
	}
	msg := tuirender.UIMessage{
		Role:  "local_" + strings.TrimSpace(result.Kind),
		Kind:  tuirender.KindText,
		Text:  strings.TrimSpace(strings.TrimRight(result.PlainText, "\n")),
		Local: result,
	}
	switch result.Kind {
	case "status":
		msg.Kind = tuirender.KindLocalStatus
	case "mcp":
		msg.Kind = tuirender.KindLocalMCP
	}
	if msg.Text == "" {
		msg.Text = strings.TrimSpace(strings.TrimRight(result.Title, "\n"))
	}
	if msg.Text == "" {
		return
	}
	m.transcript = append(m.transcript, msg)
	m.refreshViewportContentFollow(true)
}

func (m *model) appendTranscriptMessages(messages []tuirender.UIMessage) {
	for _, msg := range messages {
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		msg.Streaming = false
		msg.FullReasoning = false
		if m.replaceCommittedWorkMessage(msg) {
			continue
		}
		m.transcript = append(m.transcript, msg)
	}
}

func (m *model) replaceCommittedWorkMessage(msg tuirender.UIMessage) bool {
	if strings.TrimSpace(msg.ID) == "" || !tuirender.IsWorkEvent(msg) {
		return false
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		existing := m.transcript[i]
		if existing.ID != msg.ID || !tuirender.IsWorkEvent(existing) {
			continue
		}
		m.transcript[i] = msg
		return true
	}
	return false
}

func (m model) liveTranscriptMessages() []tuirender.UIMessage {
	assemblerMessages := []tuirender.UIMessage(nil)
	if m.assembler != nil {
		assemblerMessages = m.assembler.Snapshot()
	}
	// While a Plan-mode turn is still streaming and no plan has been finalized
	// (plan_completed), render its plan deltas as ordinary assistant text rather
	// than a Proposed Plan card. The model streams every reply — including any
	// pre-tool preamble ("let me inspect the file first") — as plan deltas, so
	// showing the card chrome before finalization makes that interim text flash as
	// a Proposed Plan and then revert. Snapshot returns a copy, so this is a
	// display-only reclassification; the assembler keeps KindPlan for SetPlan to
	// finalize once plan_completed arrives.
	if m.busy && m.chatMode == "plan" && !m.sawPlanCompletedThisTurn {
		for i := range assemblerMessages {
			if assemblerMessages[i].Kind == tuirender.KindPlan {
				assemblerMessages[i].Kind = tuirender.KindText
				assemblerMessages[i].Role = "assistant"
			}
		}
	}
	merged := mergeBySeq(assemblerMessages, m.timelineSnapshotMessages())
	return m.visibleLiveMessages(merged)
}

// mergeBySeq interleaves the assembler's text rows with the timeline's tool rows
// by their render sequence, restoring the true text<->tool ordering that the old
// before/timeline/after split collapsed. Both inputs are already ascending by
// Seq (assembler stamps in append order; timeline items are anchored to an
// ascending SeqFloor), so this is a standard merge of two sorted lists.
// Unsequenced rows (Seq == 0) sort first, which only happens when neither side
// carries sequence info — harmless for the empty/degenerate cases.
func mergeBySeq(assembler, timeline []tuirender.UIMessage) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(assembler)+len(timeline))
	i, j := 0, 0
	for i < len(assembler) && j < len(timeline) {
		if timeline[j].Seq < assembler[i].Seq {
			out = append(out, timeline[j])
			j++
		} else {
			out = append(out, assembler[i])
			i++
		}
	}
	out = append(out, assembler[i:]...)
	out = append(out, timeline[j:]...)
	return out
}

func (m *model) commitLiveTranscript(forceBottom bool) {
	if m.assembler == nil && m.timeline == nil {
		return
	}
	// A Plan-mode turn streams its content as plan deltas, including any preamble
	// the model writes before a tool call ("let me inspect the file first"). When
	// that turn is committed mid-flight — e.g. a shell_run approval decision
	// commits the live transcript before the real plan is finalized — the preamble
	// would freeze into the transcript as a Proposed Plan card that the later
	// plan_completed can no longer replace. Demote any not-yet-completed plan card
	// to ordinary assistant text first. Gated on busy so hydrated plan cards from
	// past completed turns are never demoted.
	if m.busy && m.chatMode == "plan" && !m.sawPlanCompletedThisTurn && m.assembler != nil {
		m.assembler.DemoteUncompletedPlan()
	}
	if m.assembler != nil {
		m.appendTranscriptMessages(mergeBySeq(m.assembler.Snapshot(), m.timelineSnapshotMessages()))
		m.assembler.Reset()
	} else {
		m.appendTranscriptMessages(m.timelineSnapshotMessages())
	}
	m.resetTimeline()
	m.refreshViewportContentFollow(forceBottom)
}

func (m *model) discardCurrentTurnModelOutput() {
	start := m.turnTranscriptStart
	if start < 0 || start > len(m.transcript) {
		start = len(m.transcript)
	}
	out := make([]tuirender.UIMessage, 0, len(m.transcript))
	out = append(out, m.transcript[:start]...)
	changed := false
	for _, msg := range m.transcript[start:] {
		if isResettableModelOutput(msg) {
			changed = true
			continue
		}
		out = append(out, msg)
	}
	if !changed {
		return
	}
	m.transcript = out
	if m.nativeScrollbackPrinted > start {
		m.nativeScrollbackPrinted = start
	}
}

func isResettableModelOutput(msg tuirender.UIMessage) bool {
	if msg.Local != nil {
		return false
	}
	return (msg.Role == "assistant" && msg.Kind == tuirender.KindText) ||
		msg.Role == "think" ||
		msg.Kind == tuirender.KindThinking ||
		msg.Kind == tuirender.KindPlan
}

const maxHydratedTranscriptLines = 300

func (m *model) trimHydratedTranscriptForDisplay(maxLines int) {
	if maxLines <= 0 || len(m.transcript) == 0 {
		return
	}
	messages := m.transcript
	width := m.chatRenderWidth()
	selected := make([]tuirender.UIMessage, 0, len(messages))
	lineCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msgLines := len(tuirender.ChatLines([]tuirender.UIMessage{messages[i]}, width))
		if len(selected) > 0 && lineCount+msgLines > maxLines {
			break
		}
		lineCount += msgLines
		selected = append(selected, messages[i])
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	m.transcript = selected
	m.refreshViewportContentFollow(true)
}
