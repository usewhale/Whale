package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	tuirender "github.com/usewhale/whale/internal/tui/render"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m model) approvalNoticeText(decision string) string {
	return m.approvalNotice(decision).Text()
}

func (m model) approvalNotice(decision string) *tuirender.SystemNotice {
	detail, command := approvalNoticeActionParts(m.approval.reason)
	switch decision {
	case "allow":
		return &tuirender.SystemNotice{Kind: "approval_allowed", Tone: "success", Action: "Approved", Detail: detail, Command: command, Scope: "this time"}
	case "allow_session":
		return &tuirender.SystemNotice{Kind: "approval_allowed_session", Tone: "success", Action: "Approved", Detail: detail, Command: command, Scope: "for this session"}
	case "cancel":
		return &tuirender.SystemNotice{Kind: "approval_canceled", Tone: "muted", Action: "Canceled", Subject: "request", Detail: detail, Command: command}
	default:
		return &tuirender.SystemNotice{Kind: "approval_denied", Tone: "error", Action: "Denied", Subject: "request", Detail: detail, Command: command}
	}
}

func approvalNoticeActionParts(summary string) (string, string) {
	summary = truncateLine(strings.TrimSpace(summary), 140)
	if summary == "" {
		return "to use", "the requested tool"
	}
	if cmd, ok := strings.CutPrefix(summary, "shell_run:"); ok {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			return "to run", cmd
		}
	}
	return "to use", summary
}

func permissionNoticeFromInfo(text string) *tuirender.SystemNotice {
	switch strings.TrimSpace(text) {
	case "Session auto-accept enabled":
		return &tuirender.SystemNotice{
			Kind:    "permission_auto_accept_enabled",
			Tone:    "info",
			Action:  "Session auto-accept",
			Subject: "enabled",
		}
	case "Session auto-accept disabled":
		return &tuirender.SystemNotice{
			Kind:    "permission_auto_accept_disabled",
			Tone:    "muted",
			Action:  "Session auto-accept",
			Subject: "disabled",
		}
	default:
		return nil
	}
}

func (m model) turnInterruptedNoticeText() string {
	return lipgloss.NewStyle().
		Foreground(tuitheme.Default.Error).
		Bold(true).
		Render("■ Conversation interrupted - tell the model what to do differently.")
}
