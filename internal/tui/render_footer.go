package tui

import (
	"github.com/charmbracelet/lipgloss"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"strings"
)

func appendFooterDir(base, cwd string, width, reserve int) string {
	segment := "  "
	available := width - lipgloss.Width(base) - lipgloss.Width(segment) - reserve
	if available <= 0 {
		return base
	}
	return base + segment + footerPath(fitTail(cwd, available))
}

func appendFooterBranch(base, branch string, width, reserve int) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return base
	}
	segment := "  " + branch
	if lipgloss.Width(base)+lipgloss.Width(segment)+reserve > width {
		return base
	}
	return base + segment
}

func appendFooterViewIndicator(base, indicator string, width int) string {
	indicator = strings.TrimSpace(indicator)
	if indicator == "" {
		return base
	}
	segment := "  " + footerFocus(indicator)
	if lipgloss.Width(base)+lipgloss.Width(segment) > width {
		return base
	}
	return base + segment
}

func footerBranchCanRenderWithDir(base, cwd, branch string, width, reserve int) bool {
	if strings.TrimSpace(branch) == "" {
		return false
	}
	required := lipgloss.Width(base) + footerBranchReserve(branch) + reserve
	if cwd != "" {
		required += footerDirReserve(cwd)
	}
	return required <= width
}

func footerBranchReserveForWidth(base, branch string, width, reserve int) int {
	branchReserve := footerBranchReserve(branch)
	if branchReserve == 0 {
		return 0
	}
	if lipgloss.Width(base)+branchReserve+reserve > width {
		return 0
	}
	return branchReserve
}

func footerField(label, value string, valueColor lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(label) +
		" " +
		lipgloss.NewStyle().Foreground(valueColor).Render(value)
}

func footerModelEffort(model, effort string) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Render(model) +
		" " +
		lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(".") +
		" " +
		lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Render(effort)
}

func footerHint(text string) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(text)
}

func footerPath(text string) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Subtle).Render(text)
}

func footerFocus(text string) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Accent).Bold(true).Render(text)
}

func footerAutoAccept(text string) string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Warn).Bold(true).Render(text)
}

func thinkingFooterColor(thinking string) lipgloss.Color {
	switch strings.ToLower(strings.TrimSpace(thinking)) {
	case "on", "enabled", "true":
		return tuitheme.Default.Success
	case "off", "disabled", "false":
		return tuitheme.Default.Muted
	default:
		return tuitheme.Default.InfoSoft
	}
}

func footerDirReserve(cwd string) int {
	trimmed := strings.TrimRight(cwd, `/\`)
	if trimmed == "" {
		trimmed = cwd
	}
	tail := trimmed
	if idx := strings.LastIndexAny(trimmed, `/\`); idx >= 0 && idx < len(trimmed)-1 {
		tail = trimmed[idx+1:]
	}
	if tail == "" {
		return 0
	}
	return lipgloss.Width("  ") + lipgloss.Width(tail)
}

func footerBranchReserve(branch string) int {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return 0
	}
	return lipgloss.Width("  ") + lipgloss.Width(branch)
}

func footerViewIndicatorReserve(indicator string) int {
	indicator = strings.TrimSpace(indicator)
	if indicator == "" {
		return 0
	}
	return lipgloss.Width("  ") + lipgloss.Width(indicator)
}

func fitTail(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(s)
	tail := ""
	for i := len(runes) - 1; i >= 0; i-- {
		next := string(runes[i:])
		if lipgloss.Width("..."+next) > width {
			break
		}
		tail = next
	}
	return "..." + tail
}
