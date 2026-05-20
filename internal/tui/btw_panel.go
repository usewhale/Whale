package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tuirender "github.com/usewhale/whale/internal/tui/render"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

type btwPanelState struct {
	visible  bool
	id       int
	question string
	response string
	err      string
	loading  bool
	scroll   int
}

func (m *model) startBtwPanel(id int, question string) {
	m.popLocalSubmitCommand()
	m.btwPanel = btwPanelState{
		visible:  true,
		id:       id,
		question: strings.TrimSpace(question),
		loading:  true,
	}
	m.status = "answering side question"
}

func (m *model) appendBtwDelta(id int, delta string) {
	if !m.btwPanel.visible || m.btwPanel.id != id {
		return
	}
	m.btwPanel.response += delta
}

func (m *model) finishBtwPanel(id int, text string) {
	if !m.btwPanel.visible || m.btwPanel.id != id {
		return
	}
	m.btwPanel.loading = false
	m.btwPanel.response = strings.TrimSpace(text)
	m.btwPanel.err = ""
	m.btwPanel.scroll = clampBtwScrollToPage(m.btwPanel.scroll, m.btwContentLines(m.chatRenderWidth()), m.btwMaxBodyLines())
	if !m.busy {
		m.status = "ready"
	}
}

func (m *model) failBtwPanel(id int, text string) {
	if !m.btwPanel.visible || m.btwPanel.id != id {
		return
	}
	m.btwPanel.loading = false
	m.btwPanel.response = ""
	m.btwPanel.err = strings.TrimSpace(text)
	m.btwPanel.scroll = 0
	m.status = "error"
}

func (m *model) dismissBtwPanel() {
	m.btwPanel = btwPanelState{}
	if !m.busy && m.status != "error" {
		m.status = "ready"
	}
}

func (m *model) scrollBtwPanel(delta int) {
	if !m.btwPanel.visible {
		return
	}
	m.btwPanel.scroll = clampBtwScrollToPage(m.btwPanel.scroll+delta, m.btwContentLines(m.chatRenderWidth()), m.btwMaxBodyLines())
}

func (m *model) handleBtwPanelKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+d":
		m.dismissBtwPanel()
		return true
	case "ctrl+p":
		m.scrollBtwPanel(-1)
		return true
	case "ctrl+n":
		m.scrollBtwPanel(1)
		return true
	default:
		return false
	}
}

func (m model) renderBtwPanel(width int) string {
	if !m.btwPanel.visible {
		return ""
	}
	panelWidth := max(20, width)
	contentWidth := max(20, panelWidth-4)
	heading := lipgloss.NewStyle().Foreground(tuitheme.Default.Plan).Bold(true).Render("/btw")
	question := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(m.btwPanel.question)
	lines := []string{heading + " " + question}
	body := m.btwContentLines(contentWidth)
	maxBodyLines := m.btwMaxBodyLines()
	if maxBodyLines > 0 && len(body) > maxBodyLines {
		maxScroll := len(body) - maxBodyLines
		scroll := clampInt(m.btwPanel.scroll, 0, maxScroll)
		body = body[scroll : scroll+maxBodyLines]
	}
	lines = append(lines, body...)
	if !m.btwPanel.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render("Ctrl+P/Ctrl+N to scroll · Esc/Ctrl+C/Ctrl+D to dismiss"))
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderTop(false).
		BorderRight(false).
		BorderBottom(false).
		BorderForeground(tuitheme.Default.Plan).
		PaddingLeft(1).
		Width(panelWidth - 1).
		Render(strings.Join(lines, "\n"))
}

func (m model) btwContentLines(width int) []string {
	width = max(20, width)
	if m.btwPanel.loading && strings.TrimSpace(m.btwPanel.response) == "" {
		return []string{lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render("Answering...")}
	}
	text := strings.TrimSpace(m.btwPanel.response)
	quiet := false
	if strings.TrimSpace(m.btwPanel.err) != "" {
		text = m.btwPanel.err
		quiet = true
	}
	if text == "" {
		text = "No response received"
	}
	rendered := tuirender.Markdown(text, width, quiet)
	if strings.TrimSpace(rendered) == "" {
		return []string{text}
	}
	return strings.Split(strings.TrimRight(rendered, "\n"), "\n")
}

func (m model) btwMaxBodyLines() int {
	if m.height <= 0 {
		return 8
	}
	return max(3, minInt(12, m.height/3))
}

func clampBtwScrollToPage(scroll int, lines []string, maxBodyLines int) int {
	if scroll < 0 || len(lines) == 0 {
		return 0
	}
	if maxBodyLines <= 0 || len(lines) <= maxBodyLines {
		return 0
	}
	maxScroll := len(lines) - maxBodyLines
	if scroll > maxScroll {
		return maxScroll
	}
	return scroll
}
