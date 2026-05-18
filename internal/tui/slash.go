package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/app/service"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func approvalChoiceMode(choice string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case strings.ToLower(service.ApprovalChoiceAskFirst), "ask first":
		return "on-request"
	case strings.ToLower(service.ApprovalChoiceAutoApproveSession), "auto approve":
		return "never-ask"
	default:
		return ""
	}
}

func indexOf(xs []string, target string) int {
	for i, x := range xs {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(target)) {
			return i
		}
	}
	return 0
}

func safeChoice(xs []string, idx int) string {
	if idx < 0 || idx >= len(xs) {
		return ""
	}
	return xs[idx]
}

func (m *model) updateSlashMatches() {
	defer m.updateSkillMatches()
	if m.mode != modeChat {
		return
	}
	raw := m.input.Value()
	if strings.Contains(raw, "\n") {
		m.slash.matches = nil
		m.slash.selected = 0
		return
	}
	if !appcommands.LooksLikeSlashCommand(raw) {
		m.slash.matches = nil
		m.slash.selected = 0
		return
	}
	// Trigger full slash list on "/" or "/ " and do prefix match on
	// "/xxx". Once arguments start ("/cmd ..."), hide suggestions.
	prefix := ""
	switch {
	case raw == "/":
		prefix = ""
	case strings.HasPrefix(raw, "/ "):
		prefix = ""
	case strings.Contains(raw, " "):
		m.slash.matches = nil
		m.slash.selected = 0
		return
	default:
		prefix = strings.ToLower(strings.TrimPrefix(raw, "/"))
	}
	matches := make([]string, 0, len(m.slash.all))
	for _, cmd := range m.slash.all {
		name := strings.TrimPrefix(strings.ToLower(cmd), "/")
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, cmd)
		}
	}
	m.slash.matches = matches
	if m.slash.selected >= len(m.slash.matches) {
		m.slash.selected = max(0, len(m.slash.matches)-1)
	}
}

func (m model) hasSlashSuggestions() bool {
	return len(m.slash.matches) > 0
}

func (m model) renderSlashSuggestions() string {
	rows := []string{}
	const maxRows = 8
	start := 0
	if len(m.slash.matches) > maxRows {
		start = m.slash.selected - maxRows/2
		if start < 0 {
			start = 0
		}
		if start > len(m.slash.matches)-maxRows {
			start = len(m.slash.matches) - maxRows
		}
	}
	end := len(m.slash.matches)
	if end > start+maxRows {
		end = start + maxRows
	}
	for i := start; i < end; i++ {
		cmd := m.slash.matches[i]
		prefix := "  "
		if i == m.slash.selected {
			prefix = "> "
		}
		rows = append(rows, prefix+cmd)
	}
	rows = append(rows, "  ↑/↓ navigate · Tab/Enter pick · Esc cancel")
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
}

func parseSlashCommands(help string) []string {
	parts := strings.Split(help, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		field := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(field, "/") {
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

func buildSlashAutoRunMap(help string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(help, ",") {
		seg := strings.TrimSpace(part)
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		cmd := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(cmd, "/") {
			continue
		}
		// Commands with required args ("<...>") should not auto-run after
		// suggestion enter; fill input first and let user decide.
		out[cmd] = !strings.Contains(seg, "<")
	}
	return out
}

func (m model) shouldAutoRunSlash(cmd string) bool {
	if m.slash.autoRun == nil {
		return false
	}
	return m.slash.autoRun[strings.TrimSpace(cmd)]
}
