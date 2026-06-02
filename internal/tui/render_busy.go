package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	appcommands "github.com/usewhale/whale/internal/runtime/commands"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"math"
	"strings"
	"time"
)

const busyTokenMinDisplayAge = 2 * time.Second

func (m model) renderBusyStatusLine(width int) string {
	if !m.busy {
		return ""
	}
	if m.mode == modeApproval {
		return ""
	}
	label := "Working"
	if m.stopping {
		if status := busySlashDraftStatus(m.input.Value(), m.status); status != "" {
			label = status
		} else {
			label = "Stopping"
		}
	} else if status := strings.TrimSpace(m.providerRetryStatus); status != "" && time.Now().Before(m.providerRetryUntil) {
		label = status
	} else if status := busySlashDraftStatus(m.input.Value(), m.status); status != "" {
		label = status
	}
	busyElapsed := m.busyElapsed()
	line := fmt.Sprintf("%s (%s)", label, formatElapsedCompact(busyElapsed))
	if m.busyTokenCount > 0 && busyElapsed >= busyTokenMinDisplayAge {
		line += fmt.Sprintf(" · ↓ %s tokens", formatTokenCount(m.busyTokenCount))
	}
	if !m.stopping {
		if m.mode == modeChat {
			input := m.input.Value()
			if busySlashDraftStatus(input, m.status) != "" {
				line += " · Edit command or press Esc to interrupt · Ctrl+C clears draft"
			} else if busySlashDraftImmediate(input) {
				line += " · Enter to run · Esc to interrupt · Ctrl+C clears draft"
			} else if appcommands.LooksLikeSlashCommand(input) {
				line += " · Slash commands are disabled while working · Esc to interrupt · Ctrl+C clears draft"
			} else if input == "" {
				line += " · Type follow-up, Enter to queue · Esc/Ctrl+C to interrupt"
			} else if strings.TrimSpace(input) == "" {
				line += " · Type follow-up · Esc to interrupt · Ctrl+C clears draft"
			} else {
				line += " · Enter to queue · Esc interrupts and sends · Ctrl+C clears draft"
			}
		} else {
			line += " · Ctrl+C to interrupt"
		}
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(tuitheme.Default.Warn).
		Render(line)
}

func busyStatusLabel(status string) string {
	status = strings.TrimSpace(status)
	if strings.Contains(status, " disabled while ") {
		return status
	}
	return ""
}

func busySlashDraftStatus(input, status string) string {
	status = busyStatusLabel(status)
	if status == "" {
		return ""
	}
	fields := strings.Fields(status)
	if len(fields) == 0 {
		return ""
	}
	input = strings.TrimSpace(input)
	if busySlashDraftMatchesCommand(input, fields[0]) {
		return status
	}
	return ""
}

func busySlashDraftMatchesCommand(input, command string) bool {
	if input == command || strings.HasPrefix(input, command+" ") {
		return true
	}
	fields := strings.Fields(input)
	if len(fields) != 1 {
		return false
	}
	return appcommands.ExpandUniqueSlashPrefix(fields[0], appcommands.CommandsHelp(), "/mcp") == command
}

func busySlashDraftImmediate(input string) bool {
	input = strings.TrimSpace(input)
	if !appcommands.LooksLikeSlashCommand(input) {
		return false
	}
	return appcommands.ClassifySubmit(input, appcommands.CommandsHelp(), "/mcp").BusyImmediate()
}

func (m model) renderQueuedPrompts(width int) string {
	if (len(m.pendingSteers) == 0 && len(m.queuedPrompts) == 0) || width <= 0 {
		return ""
	}
	rows := []string{}
	if len(m.pendingSteers) > 0 {
		limit := 3
		if len(m.pendingSteers) < limit {
			limit = len(m.pendingSteers)
		}
		rows = append(rows, lipgloss.NewStyle().
			Foreground(tuitheme.Default.Warn).
			Render(fmt.Sprintf("pending steer (%d)", len(m.pendingSteers))))
		for i := 0; i < limit; i++ {
			preview := queuedPromptPreview(m.pendingSteers[i].Text, max(1, width-4))
			rows = append(rows, lipgloss.NewStyle().
				Foreground(tuitheme.Default.Muted).
				Render("  "+preview))
		}
		if hidden := len(m.pendingSteers) - limit; hidden > 0 {
			rows = append(rows, lipgloss.NewStyle().
				Foreground(tuitheme.Default.Muted).
				Render(fmt.Sprintf("  ... %d more", hidden)))
		}
	}
	if len(m.queuedPrompts) > 0 {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		limit := 3
		if len(m.queuedPrompts) < limit {
			limit = len(m.queuedPrompts)
		}
		rows = append(rows, lipgloss.NewStyle().
			Foreground(tuitheme.Default.Warn).
			Render(fmt.Sprintf("queued follow-up (%d)", len(m.queuedPrompts))))
		for i := 0; i < limit; i++ {
			preview := queuedPromptPreview(m.queuedPrompts[i].Text, max(1, width-4))
			rows = append(rows, lipgloss.NewStyle().
				Foreground(tuitheme.Default.Muted).
				Render("  "+preview))
		}
		if hidden := len(m.queuedPrompts) - limit; hidden > 0 {
			rows = append(rows, lipgloss.NewStyle().
				Foreground(tuitheme.Default.Muted).
				Render(fmt.Sprintf("  ... %d more", hidden)))
		}
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strings.Join(rows, "\n"))
}

func queuedPromptPreview(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if width <= 0 || text == "" {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(text)
	out := ""
	for i := range runes {
		next := string(runes[:i+1])
		if lipgloss.Width(next+"...") > width {
			break
		}
		out = next
	}
	if out == "" {
		return "..."
	}
	return out + "..."
}

func (m model) busyElapsed() time.Duration {
	if m.busySince.IsZero() {
		return 0
	}
	return time.Since(m.busySince)
}

func formatElapsedCompact(elapsed time.Duration) string {
	seconds := int(elapsed / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		minutes := seconds / 60
		remSeconds := seconds % 60
		return fmt.Sprintf("%dm %02ds", minutes, remSeconds)
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remSeconds := seconds % 60
	return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, remSeconds)
}

// formatTokenCount formats a token count for display, compacting large values.
// e.g., 3281 → "3.3k", 500 → "500".

func formatTokenCount(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	thousands := float64(count) / 1000
	// Round to 1 decimal, then strip trailing zero
	rounded := math.Round(thousands*10) / 10
	return fmt.Sprintf("%.1fk", rounded)
}
