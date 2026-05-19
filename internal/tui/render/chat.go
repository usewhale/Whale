package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func ChatLines(messages []UIMessage, width int) []string {
	if len(messages) == 0 {
		return nil
	}
	if width < 20 {
		width = 20
	}
	out := make([]string, 0, len(messages)*6)
	for _, e := range messages {
		block := strings.TrimSpace(e.Text)
		if block == "" {
			continue
		}
		out = append(out, renderCard(e, block, width)...)
		out = append(out, "")
	}
	return out
}

func renderEntryText(role, text string, width int) string {
	quiet := role == "you"
	switch role {
	case "assistant", "think", "plan", "result", "result_ok", "result_denied", "result_failed", "result_timeout", "result_canceled", "result_error", "result_running", "error", "info", "tool", "tool_summary":
		return Markdown(text, width, quiet)
	case "shell_result_ok", "shell_result_denied", "shell_result_failed", "shell_result_timeout", "shell_result_canceled", "shell_result_error", "shell_result_running":
		return text
	default:
		return text
	}
}

func renderCard(m UIMessage, block string, width int) []string {
	if m.Role == "you" {
		return renderUserPrompt(block, width)
	}
	if m.Role == "assistant" && m.Kind == KindText {
		return renderAssistantMarkdown(block, width)
	}
	if m.Kind == KindNotice || m.Role == "notice" {
		return renderNotice(block, width)
	}
	if m.Kind == KindThinking || m.Role == "think" {
		return renderThinkingCard(m, block, width)
	}
	if m.Kind == KindPlanUpdate {
		return renderPlanUpdateCard(m, block, width)
	}
	borderColor := roleBorderColor(m)

	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}

	rendered := hardWrapRendered(renderEntryText(m.Role, block, contentWidth), contentWidth)

	card := lipgloss.NewStyle().
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderTop(false).
		BorderRight(false).
		BorderBottom(false).
		BorderForeground(borderColor).
		PaddingLeft(1).
		Width(width - 1).
		Render(strings.TrimRight(rendered, "\n"))

	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderPlanUpdateCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(roleBorderColor(m)).
		Bold(true).
		Render("Updated Plan")
	body := renderPlanUpdateBody(block, contentWidth)
	rendered := strings.TrimRight(title+"\n"+body, "\n")
	card := lipgloss.NewStyle().
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderTop(false).
		BorderRight(false).
		BorderBottom(false).
		BorderForeground(roleBorderColor(m)).
		PaddingLeft(1).
		Width(width - 1).
		Render(rendered)
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderPlanUpdateBody(block string, width int) string {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(body) > 0 && body[len(body)-1] != "" {
				body = append(body, "")
			}
			continue
		}
		status, text, ok := parsePlanUpdateLine(line)
		if !ok {
			style := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Italic(true)
			body = append(body, wrapWithPrefixes(style.Render(line), "", "", width-4)...)
			continue
		}
		body = append(body, renderPlanUpdateStep(status, text, width-4)...)
	}
	if len(body) == 0 {
		return ""
	}
	return strings.TrimRight(strings.Join(prefixPlanUpdateBody(body), "\n"), "\n")
}

func parsePlanUpdateLine(line string) (string, string, bool) {
	for _, prefix := range []string{"[x] ", "[X] ", "[~] ", "[ ] "} {
		if strings.HasPrefix(line, prefix) {
			return prefix, strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", "", false
}

func renderPlanUpdateStep(status, text string, width int) []string {
	var marker string
	var style lipgloss.Style
	switch status {
	case "[x] ", "[X] ":
		marker = "✔ "
		style = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Muted).
			Strikethrough(true)
	case "[~] ":
		marker = "□ "
		style = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Plan).
			Bold(true)
	default:
		marker = "□ "
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	}
	return wrapWithPrefixes(style.Render(marker+text), "", "  ", width)
}

func prefixPlanUpdateBody(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(out) == 0 {
			out = append(out, "  └ "+line)
			continue
		}
		out = append(out, "    "+line)
	}
	return out
}

func wrapWithPrefixes(text, firstPrefix, nextPrefix string, width int) []string {
	wrapWidth := width - lipgloss.Width(firstPrefix)
	if wrapWidth < 8 {
		wrapWidth = 8
	}
	wrapped := hardWrapRendered(text, wrapWidth)
	lines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, firstPrefix+line)
			continue
		}
		out = append(out, nextPrefix+line)
	}
	return out
}

func renderAssistantMarkdown(block string, width int) []string {
	contentWidth := width - 2
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(hardWrapRendered(renderEntryText("assistant", block, contentWidth), contentWidth), "\n")
	if rendered == "" {
		return nil
	}
	return strings.Split(rendered, "\n")
}

func hardWrapRendered(text string, width int) string {
	if width < 1 || text == "" {
		return text
	}
	return xansi.Hardwrap(text, width, true)
}

func renderNotice(block string, width int) []string {
	contentWidth := width - 2
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(renderEntryText("notice", block, contentWidth), "\n")
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, "  "+line)
	}
	return out
}

func renderUserPrompt(block string, width int) []string {
	contentWidth := width - 4
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(hardWrapRendered(renderEntryText("you", block, contentWidth), contentWidth), "\n")
	lines := strings.Split(rendered, "\n")
	glyph := lipgloss.NewStyle().
		Foreground(roleBorderColor(UIMessage{Role: "you"})).
		Bold(true).
		Render("›")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, glyph+" "+line)
			continue
		}
		out = append(out, "  "+line)
	}
	return out
}

func renderThinkingCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Bold(true).
		Render("Thinking")
	body := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Italic(true).
		Render(hardWrapRendered(renderEntryText("think", block, contentWidth), contentWidth))
	rendered := strings.TrimRight(title+"\n"+body, "\n")
	card := lipgloss.NewStyle().
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderTop(false).
		BorderRight(false).
		BorderBottom(false).
		BorderForeground(roleBorderColor(m)).
		PaddingLeft(1).
		Width(width - 1).
		Render(rendered)
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func roleBorderColor(m UIMessage) lipgloss.Color {
	return tuitheme.RoleBorder(m.Role)
}

func toolNamePrefix(text string) string {
	idx := strings.Index(text, ":")
	if idx <= 0 {
		return ""
	}
	name := strings.TrimSpace(text[:idx])
	name = strings.TrimPrefix(name, "[")
	name = strings.TrimSuffix(name, "]")
	return name
}
