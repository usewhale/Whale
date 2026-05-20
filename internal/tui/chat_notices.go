package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m model) approvalNoticeText(decision string) string {
	action := approvalNoticeAction(m.approval.reason)
	product := strings.ToLower(strings.TrimSpace(m.product))
	if product == "" {
		product = "whale"
	}
	switch decision {
	case "allow":
		return lipgloss.NewStyle().
			Foreground(tuitheme.Default.Success).
			Render(fmt.Sprintf("✔ You approved %s to %s this time", product, action))
	case "allow_session":
		return lipgloss.NewStyle().
			Foreground(tuitheme.Default.Success).
			Render(fmt.Sprintf("✔ You approved %s to %s for this session", product, action))
	case "cancel":
		return lipgloss.NewStyle().
			Foreground(tuitheme.Default.Muted).
			Render(fmt.Sprintf("• You canceled the request to %s", action))
	default:
		return lipgloss.NewStyle().
			Foreground(tuitheme.Default.Error).
			Render(fmt.Sprintf("✗ You canceled the request to %s", action))
	}
}

func approvalNoticeAction(summary string) string {
	summary = truncateLine(strings.TrimSpace(summary), 140)
	if summary == "" {
		return "use the requested tool"
	}
	if cmd, ok := strings.CutPrefix(summary, "shell_run:"); ok {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			return "run " + cmd
		}
	}
	return "use " + summary
}

func (m model) turnInterruptedNoticeText() string {
	return lipgloss.NewStyle().
		Foreground(tuitheme.Default.Error).
		Bold(true).
		Render("■ Conversation interrupted - tell the model what to do differently.")
}
