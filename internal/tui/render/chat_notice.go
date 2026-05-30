package render

import (
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"strings"
)

func renderNotice(m UIMessage, block string, width int) []string {
	contentWidth := width - 2
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(renderSystemNotice(m.Notice, block, contentWidth), "\n")
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, "  "+line)
	}
	return out
}

func renderSystemNotice(notice *SystemNotice, fallback string, width int) string {
	if notice == nil {
		return renderEntryText("notice", fallback, width)
	}
	text := notice.Text()
	if strings.TrimSpace(text) == "" {
		text = fallback
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	line := styledNoticeLine(notice)
	if strings.TrimSpace(xansi.Strip(line)) == "" {
		line = text
	}
	return hardWrapRendered(line, width)
}

func styledNoticeLine(notice *SystemNotice) string {
	if notice == nil {
		return ""
	}
	glyph := noticeGlyph(notice)
	parts := make([]string, 0, 6)
	if glyph != "" {
		parts = append(parts, noticeToneStyle(notice.Tone).Render(glyph))
	}
	if action := strings.TrimSpace(notice.Action); action != "" {
		parts = append(parts, noticeToneStyle(notice.Tone).Bold(true).Render(action))
	}
	if subject := strings.TrimSpace(notice.Subject); subject != "" {
		parts = append(parts, subject)
	}
	if detail := strings.TrimSpace(notice.Detail); detail != "" {
		parts = append(parts, detail)
	}
	if command := strings.TrimSpace(notice.Command); command != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(tuitheme.Default.Tool).Render(command))
	}
	line := strings.Join(parts, " ")
	if scope := strings.TrimSpace(notice.Scope); scope != "" {
		if line != "" {
			line += " "
		}
		line += tuitheme.MutedStyle().Render("· " + scope)
	}
	return line
}

func noticeGlyph(notice *SystemNotice) string {
	if notice == nil {
		return ""
	}
	switch notice.Tone {
	case "success":
		return "✓"
	case "warn", "warning":
		return "!"
	case "error":
		return "✗"
	default:
		if strings.HasPrefix(notice.Kind, "permission_") || strings.HasPrefix(notice.Kind, "session_") {
			return "•"
		}
		return "•"
	}
}

func noticeToneStyle(tone string) lipgloss.Style {
	switch strings.TrimSpace(tone) {
	case "success":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Success)
	case "info":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Info)
	case "warn", "warning":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Warn)
	case "error":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Error)
	default:
		return tuitheme.MutedStyle()
	}
}

func renderStatusCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(roleBorderColor(m)).
		Bold(true).
		Render("Reasoning only")
	body := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Render(hardWrapRendered(renderEntryText(m.Role, block, contentWidth), contentWidth))
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
		Render(strings.TrimRight(rendered, "\n"))
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}
