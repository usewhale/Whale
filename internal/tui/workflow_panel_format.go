package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func workflowPanelTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Palette).Bold(true)
}

func workflowPanelSectionLabel(text string) string {
	return lipgloss.NewStyle().Bold(true).Render(text)
}

func workflowPanelTitleName(text string) string {
	return lipgloss.NewStyle().Bold(true).Render(text)
}

func workflowPanelMeta(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return tuitheme.MutedStyle().Render(text)
}

func workflowPanelAction(text string) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true).Render(text)
}

func workflowPanelMutedSep() string {
	return tuitheme.MutedStyle().Render(" · ")
}

func workflowPanelJoinMeta(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, workflowPanelMutedSep())
}

func workflowPanelMaybeSelected(text string, selected bool) string {
	if selected {
		return lipgloss.NewStyle().Bold(true).Render(text)
	}
	return text
}

func workflowPanelDetailHeader(header string) string {
	parts := strings.Split(header, " · ")
	if len(parts) == 0 {
		return workflowPanelSectionLabel(header)
	}
	rendered := []string{workflowPanelSectionLabel(parts[0])}
	for _, part := range parts[1:] {
		if strings.Contains(part, "expand") || strings.Contains(part, "collapse") || strings.Contains(part, "scroll") {
			rendered = append(rendered, workflowPanelAction(part))
		} else {
			rendered = append(rendered, workflowPanelMeta(part))
		}
	}
	return workflowPanelJoinMeta(rendered...)
}

func workflowPanelStatusStyle(status string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done", "success":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Success)
	case "running", "in_progress":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft)
	case "failed", "error":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Error)
	case "cancelled", "canceled", "stopped":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Warn)
	case "queued":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Subtle)
	default:
		return tuitheme.MutedStyle()
	}
}

func workflowPanelStatusIconStyled(status string) string {
	return workflowPanelStatusStyle(status).Bold(true).Render(workflowPanelStatusIcon(status))
}

func workflowPanelActivityPreview(status, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Error).Render(text)
	default:
		return workflowPanelMeta(text)
	}
}

func workflowPanelStatusIcon(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done", "success":
		return "✔"
	case "running", "in_progress":
		return "⏺"
	case "failed", "error":
		return "✘"
	case "cancelled", "canceled", "stopped", "queued":
		return "◌"
	default:
		return "◌"
	}
}

func workflowPanelTokenCount(tokens int64) string {
	if tokens >= 1000 {
		v := float64(tokens) / 1000
		if tokens%1000 == 0 {
			return fmt.Sprintf("%.0fk", v)
		}
		return fmt.Sprintf("%.1fk", v)
	}
	return fmt.Sprintf("%d", tokens)
}

func workflowPanelTaskOutputTokens(task protocol.WorkflowPanelTask) string {
	if task.CompletionTokens <= 0 {
		return ""
	}
	return workflowPanelTokenCount(task.CompletionTokens) + " out"
}

func workflowPanelDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	d := time.Duration(ms) * time.Millisecond
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
	if d >= time.Second {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return fmt.Sprintf("%dms", ms)
}

func workflowPanelPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func workflowPanelTruncate(s string, width int) string {
	s = strings.TrimSpace(workflowPanelCellLine(s))
	if width <= 0 || xansi.StringWidth(s) <= width {
		return s
	}
	return xansi.Truncate(s, width, "…")
}

func workflowPanelOneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func workflowPanelCellLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func workflowPanelPad(s string, width int) string {
	s = workflowPanelTruncate(workflowPanelCellLine(s), width)
	padding := width - xansi.StringWidth(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}
