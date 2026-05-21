package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m model) filteredLogs() []string {
	if len(m.logs) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(m.logs))
	q := strings.ToLower(m.logFilter)
	for _, entry := range m.logs {
		line := fmt.Sprintf("[%s][%s] %s", entry.Kind, entry.Source, entry.Summary)
		if q == "" || strings.Contains(strings.ToLower(line), q) || strings.Contains(strings.ToLower(entry.Raw), q) {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return []string{"[logs] no matches"}
	}
	return out
}

func (m *model) addLog(entry logEntry) {
	if strings.TrimSpace(entry.Summary) == "" {
		return
	}
	m.logs = append(m.logs, entry)
}

func (m *model) captureDiff(source, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if strings.Contains(s, "@@") || strings.HasPrefix(s, "diff --git") || strings.HasPrefix(s, "--- ") || strings.HasPrefix(s, "+++ ") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
			m.diffs = append(m.diffs, diffEntry{Source: source, Line: line})
		}
	}
}

func (m *model) captureDiffMetadata(source string, metadata map[string]any) {
	diff := renderFileDiffMetadataPlain(metadata, 0)
	if strings.TrimSpace(diff) == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m.diffs = append(m.diffs, diffEntry{Source: source, Line: line})
	}
}

func (m model) renderDiffs() []string {
	if len(m.diffs) == 0 {
		return []string{"[diff] no diff-like output yet"}
	}
	rows := make([]string, 0, len(m.diffs))
	for _, d := range m.diffs {
		if strings.TrimSpace(d.Source) == "" {
			rows = append(rows, d.Line)
			continue
		}
		rows = append(rows, fmt.Sprintf("[%s] %s", d.Source, d.Line))
	}
	return rows
}

func (m *model) setDiffText(text string) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	m.diffs = make([]diffEntry, 0, len(lines))
	for _, line := range lines {
		m.diffs = append(m.diffs, diffEntry{Line: line})
	}
	if len(m.diffs) == 0 {
		m.diffs = []diffEntry{{Line: "No changes detected."}}
	}
}

func (m model) renderDiffPagerHints(mainWidth int) string {
	lines := []string{
		"↑/↓/j/k scroll  PgUp/PgDown page  Home/End jump",
		"q/Esc close",
	}
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Width(mainWidth).MaxWidth(mainWidth)
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func tail(items []string, n int) []string {
	if n <= 0 || len(items) <= n {
		return items
	}
	return items[len(items)-n:]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
