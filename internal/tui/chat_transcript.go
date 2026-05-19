package tui

import (
	"strings"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) resetTranscript() {
	m.transcript = nil
	m.nativeScrollbackPrinted = 0
	m.turnTranscriptStart = len(m.transcript)
	m.visibleAssistantThisTurn = ""
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

func (m *model) appendTranscriptMessages(messages []tuirender.UIMessage) {
	for _, msg := range messages {
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		m.transcript = append(m.transcript, msg)
	}
}

func (m *model) commitLiveTranscript(forceBottom bool) {
	if m.assembler == nil {
		return
	}
	m.appendTranscriptMessages(m.assembler.Snapshot())
	m.assembler.Reset()
	m.clearPendingToolCalls()
	m.refreshViewportContentFollow(forceBottom)
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
