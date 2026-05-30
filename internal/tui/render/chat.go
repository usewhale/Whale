package render

import (
	"strings"
)

func ChatLines(messages []UIMessage, width int) []string {
	if len(messages) == 0 {
		return nil
	}
	if width < 20 {
		width = 20
	}
	out := make([]string, 0, len(messages)*6)
	pendingWorkSeparator := false
	for _, e := range messages {
		block := strings.TrimSpace(e.Text)
		if block == "" {
			continue
		}
		if pendingWorkSeparator && NeedsWorkSeparatorBefore(e) {
			out = append(out, WorkSeparator(width))
			out = append(out, "")
			pendingWorkSeparator = false
		}
		out = append(out, renderCard(e, block, width)...)
		out = append(out, "")
		switch {
		case IsWorkEvent(e):
			pendingWorkSeparator = true
		case e.Role == "you" || (e.Role == "assistant" && e.Kind == KindText):
			pendingWorkSeparator = false
		}
	}
	return out
}

func IsWorkEvent(m UIMessage) bool {
	return m.Kind == KindToolCall || m.Kind == KindToolResult || m.Kind == KindSubagent
}

func NeedsWorkSeparatorBefore(m UIMessage) bool {
	return m.Role == "assistant" && m.Kind == KindText
}

func renderCard(m UIMessage, block string, width int) []string {
	if m.Role == "header" {
		return strings.Split(strings.TrimRight(block, "\n"), "\n")
	}
	if m.Role == "you" {
		return renderUserPrompt(block, width)
	}
	if m.Role == "assistant" && m.Kind == KindText {
		return renderAssistantMarkdown(block, width)
	}
	if m.Kind == KindNotice || m.Role == "notice" {
		return renderNotice(m, block, width)
	}
	if m.Kind == KindStatus || m.Role == "status" {
		return renderStatusCard(m, block, width)
	}
	if m.Kind == KindLocalStatus || m.Kind == KindLocalMCP || m.Local != nil {
		return renderLocalResultCard(m, width)
	}
	if m.Kind == KindThinking || m.Role == "think" {
		return renderThinkingCard(m, block, width)
	}
	if m.Kind == KindPlan {
		return renderProposedPlanCard(m, block, width)
	}
	if m.Kind == KindPlanUpdate {
		return renderPlanUpdateCard(m, block, width)
	}
	if m.Kind == KindToolSummary && m.FocusSummary != nil {
		return renderFocusSummaryCard(m, width)
	}
	if IsWorkEvent(m) {
		return renderToolEvent(m, block, width)
	}
	borderColor := roleBorderColor(m)

	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}

	rendered := hardWrapRendered(renderEntryText(m.Role, block, contentWidth), contentWidth)

	card := spacedCardStyle(width, borderColor).
		Render(strings.TrimRight(rendered, "\n"))

	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}
