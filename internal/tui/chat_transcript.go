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
		m.transcript = append(m.transcript, msg)
	}
}

func (m model) liveTranscriptMessages() []tuirender.UIMessage {
	assemblerMessages := []tuirender.UIMessage(nil)
	if m.assembler != nil {
		assemblerMessages = m.assembler.Snapshot()
	}
	before, after := splitAssemblerAroundTimeline(assemblerMessages)
	timelineMessages := m.timelineSnapshotMessages()
	out := make([]tuirender.UIMessage, 0, len(before)+len(timelineMessages)+len(after))
	out = append(out, m.visibleLiveMessages(before)...)
	out = append(out, m.visibleLiveMessages(timelineMessages)...)
	out = append(out, m.visibleLiveMessages(after)...)
	return out
}

func splitAssemblerAroundTimeline(messages []tuirender.UIMessage) ([]tuirender.UIMessage, []tuirender.UIMessage) {
	split := 0
	for split < len(messages) && isModelOutputMessage(messages[split]) {
		split++
	}
	return messages[:split], messages[split:]
}

func isModelOutputMessage(msg tuirender.UIMessage) bool {
	switch msg.Role {
	case "assistant", "think", "plan":
		return msg.Kind == tuirender.KindText || msg.Kind == tuirender.KindThinking || msg.Kind == tuirender.KindPlan
	default:
		return false
	}
}

func (m *model) commitLiveTranscript(forceBottom bool) {
	if m.assembler == nil && m.timeline == nil {
		return
	}
	if m.assembler != nil {
		before, after := splitAssemblerAroundTimeline(m.assembler.Snapshot())
		m.appendTranscriptMessages(before)
		m.appendTranscriptMessages(m.timelineSnapshotMessages())
		m.appendTranscriptMessages(after)
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
