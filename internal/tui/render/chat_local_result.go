package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func renderLocalResultCard(m UIMessage, width int) []string {
	if m.Local == nil {
		return renderNotice(m, m.Text, width)
	}
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	titleText := strings.TrimSpace(m.Local.Title)
	if titleText == "" {
		titleText = "Local result"
	}
	title := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Info).
		Bold(true).
		Render(titleText)
	body := renderLocalResultBody(m.Local, contentWidth)
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, tuitheme.Default.Info).
		Render(strings.TrimRight(rendered, "\n"))
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderLocalResultBody(result *protocol.LocalResult, width int) string {
	if result == nil {
		return ""
	}
	blocks := make([]string, 0, 1+len(result.Sections))
	if fields := renderLocalResultFields(result.Fields, width); fields != "" {
		blocks = append(blocks, fields)
	}
	for _, section := range result.Sections {
		title := strings.TrimSpace(section.Title)
		fields := renderLocalResultFields(section.Fields, width)
		if title == "" && fields == "" {
			continue
		}
		var block string
		if title != "" {
			block = lipgloss.NewStyle().
				Foreground(tuitheme.Default.Info).
				Bold(true).
				Render(title)
		}
		if fields != "" {
			if block == "" {
				block = fields
			} else {
				block = joinTitleAndBody(block, fields)
			}
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
}

func renderLocalResultFields(fields []protocol.LocalResultField, width int) string {
	if len(fields) == 0 {
		return ""
	}
	labelWidth, valueWidth, separator := localResultFieldWidths(fields, width)
	lines := make([]string, 0, len(fields))
	labelStyle := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	for _, field := range fields {
		label := truncatePlain(field.Label, labelWidth)
		label = labelStyle.Width(labelWidth).Render(label)
		value := localResultValueStyle(field.Tone).Render(field.Value)
		wrapped := strings.Split(strings.TrimRight(hardWrapRendered(value, valueWidth), "\n"), "\n")
		if len(wrapped) == 0 {
			lines = append(lines, label+separator)
			continue
		}
		lines = append(lines, label+separator+wrapped[0])
		for _, line := range wrapped[1:] {
			lines = append(lines, strings.Repeat(" ", labelWidth)+separator+line)
		}
	}
	return strings.Join(lines, "\n")
}

func localResultFieldWidths(fields []protocol.LocalResultField, width int) (labelWidth int, valueWidth int, separator string) {
	if width < 1 {
		width = 1
	}
	separator = "   "
	separatorWidth := lipgloss.Width(separator)
	if width <= separatorWidth+2 {
		separator = " "
		separatorWidth = 1
	}
	desiredLabelWidth := 0
	for _, field := range fields {
		if w := lipgloss.Width(field.Label); w > desiredLabelWidth {
			desiredLabelWidth = w
		}
	}
	if desiredLabelWidth > 18 {
		desiredLabelWidth = 18
	}
	minValueWidth := 8
	if maxValueWidth := width - separatorWidth - 1; maxValueWidth < minValueWidth {
		minValueWidth = max(1, maxValueWidth)
	}
	maxLabelWidth := width - separatorWidth - minValueWidth
	if maxLabelWidth < 1 {
		maxLabelWidth = 1
	}
	labelWidth = min(desiredLabelWidth, maxLabelWidth)
	if labelWidth < 1 {
		labelWidth = 1
	}
	valueWidth = width - separatorWidth - labelWidth
	if valueWidth < 1 {
		valueWidth = 1
	}
	return labelWidth, valueWidth, separator
}

func localResultValueStyle(tone string) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Text)
	switch tone {
	case "info":
		return style.Foreground(tuitheme.Default.Info)
	case "warn":
		return style.Foreground(tuitheme.Default.Warn)
	case "error":
		return style.Foreground(tuitheme.Default.Error)
	case "muted":
		return style.Foreground(tuitheme.Default.Muted)
	case "result":
		return style.Foreground(tuitheme.Default.Result)
	default:
		return style
	}
}
