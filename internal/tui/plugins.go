package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/plugins"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

type pluginManagerItem struct {
	ID          string
	Name        string
	Description string
	Usage       string
	Enabled     bool
}

func (m *model) setPluginsManagerItems(statuses []plugins.PluginStatus) {
	current := ""
	if m.pluginsManager.selected >= 0 && m.pluginsManager.selected < len(m.pluginsManager.matches) {
		idx := m.pluginsManager.matches[m.pluginsManager.selected]
		if idx >= 0 && idx < len(m.pluginsManager.all) {
			current = m.pluginsManager.all[idx].ID
		}
	}
	items := make([]pluginManagerItem, 0, len(statuses))
	for _, st := range statuses {
		name := strings.TrimSpace(st.Manifest.Name)
		if name == "" {
			name = st.Manifest.ID
		}
		items = append(items, pluginManagerItem{
			ID:          strings.TrimSpace(st.Manifest.ID),
			Name:        name,
			Description: strings.TrimSpace(st.Manifest.Description),
			Usage:       pluginUsageSummary(st),
			Enabled:     st.Enabled,
		})
	}
	m.pluginsManager.all = items
	m.resetPluginsManagerMatches()
	if current != "" {
		for visible, idx := range m.pluginsManager.matches {
			if idx >= 0 && idx < len(m.pluginsManager.all) && m.pluginsManager.all[idx].ID == current {
				m.pluginsManager.selected = visible
				return
			}
		}
	}
}

func pluginUsageSummary(st plugins.PluginStatus) string {
	parts := []string{}
	if len(st.Commands) > 0 {
		usages := make([]string, 0, len(st.Commands))
		for _, cmd := range st.Commands {
			if usage := strings.TrimSpace(cmd.Usage); usage != "" {
				usages = append(usages, usage)
				continue
			}
			if name := strings.TrimSpace(cmd.Name); name != "" {
				usages = append(usages, name)
			}
		}
		if len(usages) > 0 {
			parts = append(parts, "Run "+strings.Join(usages, ", "))
		}
	}
	if len(st.Tools) > 0 {
		parts = append(parts, "Agent tools: "+strings.Join(st.Tools, ", "))
	}
	if len(st.Skills) > 0 {
		parts = append(parts, "Adds skill: $"+strings.Join(st.Skills, ", $"))
	}
	if len(st.Hooks) > 0 {
		parts = append(parts, fmt.Sprintf("Hooks: %d active", len(st.Hooks)))
	}
	if len(parts) == 0 && len(st.Manifest.Capabilities) > 0 {
		caps := make([]string, 0, len(st.Manifest.Capabilities))
		for _, cap := range st.Manifest.Capabilities {
			caps = append(caps, string(cap))
		}
		parts = append(parts, strings.Join(caps, ", "))
	}
	return strings.Join(parts, "\n")
}

func (m *model) resetPluginsManagerMatches() {
	matches := make([]int, 0, len(m.pluginsManager.all))
	for i := range m.pluginsManager.all {
		matches = append(matches, i)
	}
	m.pluginsManager.matches = matches
	if len(matches) == 0 {
		m.pluginsManager.selected = 0
		return
	}
	if m.pluginsManager.selected < 0 {
		m.pluginsManager.selected = 0
	}
	if m.pluginsManager.selected >= len(matches) {
		m.pluginsManager.selected = len(matches) - 1
	}
}

func (m *model) handlePluginsManagerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeChat
		m.pluginsManager.matches = nil
		m.pluginsManager.selected = 0
		m.status = "ready"
	case "up", "k":
		if m.pluginsManager.selected > 0 {
			m.pluginsManager.selected--
		}
	case "down", "j":
		if m.pluginsManager.selected < len(m.pluginsManager.matches)-1 {
			m.pluginsManager.selected++
		}
	case " ":
		m.toggleSelectedManagedPlugin()
	}
	return nil
}

func (m *model) toggleSelectedManagedPlugin() {
	if m.pluginsManager.selected < 0 || m.pluginsManager.selected >= len(m.pluginsManager.matches) {
		return
	}
	idx := m.pluginsManager.matches[m.pluginsManager.selected]
	if idx < 0 || idx >= len(m.pluginsManager.all) {
		return
	}
	item := &m.pluginsManager.all[idx]
	item.Enabled = !item.Enabled
	m.dispatchIntent(service.Intent{Kind: service.IntentSetPluginEnabled, PluginID: item.ID, PluginEnabled: item.Enabled})
}

func (m model) renderPluginsManager() string {
	rows := []string{
		pickerTitle("Plugins"),
		pickerHint("Installed plugins"),
		"",
	}
	const maxRows = 8
	if len(m.pluginsManager.all) == 0 {
		rows = append(rows, pickerHint("  no plugins found"))
	} else {
		start := 0
		if len(m.pluginsManager.matches) > maxRows {
			start = m.pluginsManager.selected - maxRows/2
			if start < 0 {
				start = 0
			}
			if start > len(m.pluginsManager.matches)-maxRows {
				start = len(m.pluginsManager.matches) - maxRows
			}
		}
		end := len(m.pluginsManager.matches)
		if end > start+maxRows {
			end = start + maxRows
		}
		for visible := start; visible < end; visible++ {
			item := m.pluginsManager.all[m.pluginsManager.matches[visible]]
			rows = append(rows, renderPluginsManagerRow(item, visible == m.pluginsManager.selected, m.width)...)
		}
	}
	rows = append(rows, "", pickerHint("  ↑/↓ select · Space enable/disable · Esc close"))
	return strings.Join(rows, "\n")
}

func renderPluginsManagerRow(item pluginManagerItem, selected bool, width int) []string {
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	selector := muted.Render("  ")
	if selected {
		selector = lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true).Render("> ")
	}

	marker := " "
	markerStyle := muted
	if item.Enabled {
		marker = "x"
		markerStyle = lipgloss.NewStyle().Foreground(tuitheme.Default.Success).Bold(true)
	}

	nameStyle := lipgloss.NewStyle()
	if selected {
		nameStyle = nameStyle.Foreground(tuitheme.Default.InfoSoft).Bold(true)
	}

	head := selector +
		muted.Render("[") +
		markerStyle.Render(marker) +
		muted.Render("]") +
		" " +
		nameStyle.Render(item.ID)

	details := []string{}
	if desc := strings.TrimSpace(item.Description); desc != "" {
		details = append(details, desc)
	}
	if usage := strings.TrimSpace(item.Usage); usage != "" {
		details = append(details, usage)
	}
	out := []string{head}
	if len(details) == 0 {
		return out
	}
	detailWidth := width - 8
	if detailWidth < 24 {
		detailWidth = 72
	}
	for _, detail := range details {
		for _, line := range wrapPluginDetailLine(detail, detailWidth) {
			out = append(out, muted.Render("      "+line))
		}
	}
	return out
}

func wrapPluginDetailLine(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if width < 8 {
		width = 8
	}
	wrapped := strings.TrimRight(xansi.Wordwrap(text, width, " "), "\n")
	if wrapped == "" {
		return nil
	}
	return strings.Split(wrapped, "\n")
}
