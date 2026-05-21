package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

type slashSuggestion struct {
	Display     string
	Description string
	InsertText  string
	AutoRun     bool
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
	m.slash.argumentHint = ""
	if m.mode != modeChat {
		return
	}
	raw := m.input.Value()
	if m.inHistoryNav && raw == m.lastHistoryText {
		m.slash.matches = nil
		m.slash.selected = 0
		return
	}
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
	// Trigger full slash list on "/" or "/ ", prefix match on "/xxx",
	// and command-specific option hints on "/cmd ...".
	prefix := ""
	switch {
	case raw == "/":
		prefix = ""
	case strings.HasPrefix(raw, "/ "):
		prefix = ""
	case strings.Contains(raw, " "):
		m.updateSlashArgumentMatches(raw)
		return
	default:
		prefix = strings.ToLower(strings.TrimPrefix(raw, "/"))
	}
	matches := make([]slashSuggestion, 0, len(m.slash.all))
	for _, cmd := range m.slash.all {
		name := strings.TrimPrefix(strings.ToLower(cmd.Name), "/")
		if strings.HasPrefix(name, prefix) {
			insert := cmd.Name
			autoRun := cmd.AutoRun
			if !cmd.AutoRun {
				insert = cmd.Name + " "
				autoRun = false
			}
			matches = append(matches, slashSuggestion{
				Display:     cmd.Name,
				Description: cmd.Description,
				InsertText:  insert,
				AutoRun:     autoRun,
			})
		}
	}
	m.slash.matches = matches
	if m.slash.selected >= len(m.slash.matches) {
		m.slash.selected = max(0, len(m.slash.matches)-1)
	}
}

func (m *model) updateSlashArgumentMatches(raw string) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		m.slash.matches = nil
		m.slash.selected = 0
		return
	}
	spec, ok := m.slashSpec(fields[0])
	if !ok {
		m.slash.matches = nil
		m.slash.selected = 0
		return
	}
	m.slash.argumentHint = strings.TrimSpace(spec.ArgumentHint)
	query, ok := slashOptionQuery(raw, spec)
	if !ok {
		m.slash.matches = nil
		m.slash.selected = 0
		m.slash.argumentHint = ""
		return
	}
	matches := make([]slashSuggestion, 0, len(spec.Options))
	for _, opt := range spec.Options {
		if !strings.HasPrefix(strings.ToLower(opt.Token), query) {
			continue
		}
		insert := opt.InsertText
		if strings.TrimSpace(insert) == "" {
			insert = spec.Name + " " + opt.Token
		}
		matches = append(matches, slashSuggestion{
			Display:     opt.Token,
			Description: opt.Description,
			InsertText:  insert,
			AutoRun:     opt.AutoRun,
		})
	}
	m.slash.matches = matches
	if m.slash.selected >= len(m.slash.matches) {
		m.slash.selected = max(0, len(m.slash.matches)-1)
	}
}

func (m model) slashSpec(name string) (appcommands.SlashCommandSpec, bool) {
	for _, spec := range m.slash.all {
		if spec.Name == name {
			return spec, true
		}
	}
	return appcommands.SlashCommandSpec{}, false
}

func slashOptionQuery(raw string, spec appcommands.SlashCommandSpec) (string, bool) {
	command := spec.Name
	trimmed := strings.TrimSpace(raw)
	if trimmed == command {
		return "", true
	}
	if !strings.HasPrefix(trimmed, command+" ") {
		return "", false
	}
	rest := strings.TrimLeft(strings.TrimPrefix(trimmed, command), " \t")
	if strings.ContainsAny(rest, " \t") {
		return "", false
	}
	if slashOptionExact(rest, spec) {
		return "", false
	}
	return strings.ToLower(rest), true
}

func slashOptionExact(rest string, spec appcommands.SlashCommandSpec) bool {
	for _, opt := range spec.Options {
		if opt.Token == rest {
			return true
		}
	}
	return false
}

func (m model) hasSlashSuggestions() bool {
	return len(m.slash.matches) > 0
}

func (m model) hasSlashPanel() bool {
	return m.hasSlashSuggestions() || strings.TrimSpace(m.slash.argumentHint) != ""
}

func (m model) renderSlashSuggestions() string {
	rows := []string{}
	if hint := strings.TrimSpace(m.slash.argumentHint); hint != "" {
		rows = append(rows, "Arguments "+hint)
	}
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
		item := m.slash.matches[i]
		prefix := "  "
		if i == m.slash.selected {
			prefix = "> "
		}
		if desc := strings.TrimSpace(item.Description); desc != "" {
			rows = append(rows, fmt.Sprintf("%s%-16s %s", prefix, item.Display, desc))
		} else {
			rows = append(rows, prefix+item.Display)
		}
	}
	if len(m.slash.matches) > 0 {
		rows = append(rows, "  ↑/↓ navigate · Tab/Enter pick · Esc cancel")
	}
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

func (m model) selectedSlashSuggestion() (slashSuggestion, bool) {
	if m.slash.selected < 0 || m.slash.selected >= len(m.slash.matches) {
		return slashSuggestion{}, false
	}
	return m.slash.matches[m.slash.selected], true
}

func (m model) slashSelectionAlreadyTyped() bool {
	suggestion, ok := m.selectedSlashSuggestion()
	if !ok {
		return false
	}
	return strings.TrimSpace(m.input.Value()) == strings.TrimSpace(suggestion.InsertText)
}
