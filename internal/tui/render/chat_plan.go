package render

import (
	"github.com/charmbracelet/lipgloss"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"strings"
)

func renderProposedPlanCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(roleBorderColor(m)).
		Bold(true).
		Render("Proposed Plan")
	body := strings.TrimRight(hardWrapRendered(renderEntryText("plan", block, contentWidth), contentWidth), "\n")
	if body != "" {
		body = lipgloss.NewStyle().
			Background(tuitheme.Default.PlanBackground).
			Render(body)
	}
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
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
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
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
