package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

var (
	pickerTitleStyle = lipgloss.NewStyle().
				Foreground(tuitheme.Default.Info).
				Bold(true)
	pickerSectionStyle = lipgloss.NewStyle().
				Foreground(tuitheme.Default.Muted)
	pickerHintStyle = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Muted)
	pickerTextStyle = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Text)
	pickerSelectedStyle = lipgloss.NewStyle().
				Foreground(tuitheme.Default.Info).
				Bold(true)
	pickerWarnStyle = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Warn)
)

func pickerTitle(text string) string {
	return pickerTitleStyle.Render(text)
}

func pickerSection(text string) string {
	return pickerSectionStyle.Render(text)
}

func pickerHint(text string) string {
	return pickerHintStyle.Render(text)
}

func pickerTone(text, tone string) string {
	switch tone {
	case "info":
		return pickerSelectedStyle.Render(text)
	case "warn":
		return pickerWarnStyle.Render(text)
	case "muted":
		return pickerSectionStyle.Render(text)
	default:
		return pickerTextStyle.Render(text)
	}
}

func pickerStateLine(label, value, tone string) string {
	return pickerSection(label+": ") + pickerTone(value, tone)
}

func pickerRow(label string, selected, muted bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	style := pickerTextStyle
	if muted {
		style = pickerSectionStyle
	}
	if selected {
		style = pickerSelectedStyle
	}
	return pickerSectionStyle.Render(prefix) + style.Render(label)
}

func pickerSuggestionRow(label, description string, selected bool, labelWidth int) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	style := pickerTextStyle
	if selected {
		style = pickerSelectedStyle
	}
	row := pickerSectionStyle.Render(prefix) + style.Render(padVisibleRight(label, labelWidth))
	if desc := strings.TrimSpace(description); desc != "" {
		row += " " + pickerSection(desc)
	}
	return row
}

func pickerInlineDescriptionRow(label, description string, selected bool, labelWidth int) string {
	if desc := strings.TrimSpace(description); desc != "" {
		return pickerSuggestionRow(label, "- "+desc, selected, labelWidth)
	}
	return pickerSuggestionRow(label, "", selected, labelWidth)
}

func pickerCheckboxRow(label string, enabled bool, markerTone string, description string, selected bool, labelWidth int) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	marker := " "
	if enabled {
		marker = "x"
	}
	labelStyle := pickerTextStyle
	if selected {
		labelStyle = pickerSelectedStyle
	}
	head := pickerSectionStyle.Render(prefix) +
		pickerSectionStyle.Render("[") +
		pickerTone(marker, markerTone) +
		pickerSectionStyle.Render("]") +
		" " +
		labelStyle.Render(padVisibleRight(label, labelWidth))
	if desc := strings.TrimSpace(description); desc != "" {
		head += " " + pickerSection(desc)
	}
	return head
}

func pickerSessionChoiceRow(choice sessionChoiceDisplay, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	numberStyle := pickerTextStyle
	conversationStyle := pickerTextStyle
	if selected {
		numberStyle = pickerSelectedStyle
		conversationStyle = pickerSelectedStyle
	}
	branch := choice.Branch
	if branch == "" {
		branch = "-"
	}
	return pickerSectionStyle.Render(prefix) +
		numberStyle.Render(padVisibleRight(choice.Number, 4)) + " " +
		pickerSectionStyle.Render(padVisibleRight(choice.Updated, 9)) + " " +
		pickerTextStyle.Render(padVisibleRight(branch, 24)) + " " +
		conversationStyle.Render(choice.Conversation)
}

func padVisibleRight(text string, width int) string {
	if width <= 0 {
		return text
	}
	if visible := lipgloss.Width(text); visible < width {
		return text + strings.Repeat(" ", width-visible)
	}
	return text
}

func pickerVisibleLabelWidth(items []slashSuggestion, start, end int, maxWidth int) int {
	width := 0
	for i := start; i < end; i++ {
		width = max(width, lipgloss.Width(items[i].Display))
	}
	if maxWidth > 0 {
		width = min(width, maxWidth)
	}
	return width
}
