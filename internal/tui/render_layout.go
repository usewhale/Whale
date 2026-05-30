package tui

import (
	"github.com/charmbracelet/lipgloss"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
)

func (m model) chatViewNeedsBottomGap(body, bottom string) bool {
	if m.page != pageChat {
		return false
	}
	if m.height > 0 && countVisibleLines(body)+countVisibleLines(bottom)+1 > m.height {
		return false
	}
	liveLen := 0
	if m.assembler != nil {
		liveLen = len(m.assembler.Snapshot())
	}
	return m.startupHeaderMessage() != nil &&
		len(m.transcript) == 0 &&
		liveLen == 0 &&
		len(m.ephemeralMessages) == 0
}

func (m model) chatBodyHeightForView(mainWidth, maxBodyHeight int) int {
	if maxBodyHeight <= 0 {
		return 0
	}
	lines := tuirender.ChatLines(m.chatViewportMessages(), max(20, mainWidth-2))
	if len(lines) == 0 {
		return 0
	}
	return min(len(lines), maxBodyHeight)
}

func (m model) viewportBodyHeight(mainWidth int) int {
	if m.height <= 0 {
		return 0
	}
	return max(0, m.height-countVisibleLines(m.renderBottom(mainWidth)))
}

func (m model) chatViewportBodyHeight(mainWidth, bodyHeight int) int {
	return max(0, bodyHeight)
}

func countVisibleLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func padVisibleLines(s string, targetLines, width int) string {
	if targetLines <= 0 {
		return ""
	}
	s = strings.TrimRight(s, "\n")
	lines := []string{}
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	if len(lines) > targetLines {
		lines = lines[len(lines)-targetLines:]
	}
	for len(lines) < targetLines {
		lines = append(lines, "")
	}
	style := lipgloss.NewStyle().Width(width).MaxWidth(width)
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func (m model) layoutDims() (mainWidth, bodyHeight int) {
	bodyHeight = max(3, m.height-6)
	mainWidth = m.width
	if m.sidebar && m.width > 80 {
		mainWidth = int(float64(m.width) * 0.72)
	}
	return mainWidth, bodyHeight
}

func (m model) chatRenderWidth() int {
	mainWidth, _ := m.layoutDims()
	return max(20, max(10, mainWidth-2))
}
