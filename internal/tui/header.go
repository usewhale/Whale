package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/usewhale/whale/internal/build"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

const headerLargeLogoMinWidth = 64
const headerMaxOuterWidth = 82
const (
	headerMiniMinHeight    = 3
	headerTinyMinHeight    = 6
	headerLargeMinHeight   = 14
	headerCompactMinHeight = 9
)

var whaleLargeLogo = []string{
	"██╗    ██╗██╗  ██╗ █████╗ ██╗     ███████╗",
	"██║    ██║██║  ██║██╔══██╗██║     ██╔════╝",
	"██║ █╗ ██║███████║███████║██║     █████╗",
	"██║███╗██║██╔══██║██╔══██║██║     ██╔══╝",
	"╚███╔███╔╝██║  ██║██║  ██║███████╗███████╗",
}

type headerInfo struct {
	model    string
	effort   string
	thinking string
	cwd      string
	version  string
	width    int
	height   int
}

func buildHeaderBanner(modelName, effort, thinking, cwd, version string, width, height int) string {
	info := headerInfo{
		model:    valueOrUnknown(modelName),
		effort:   valueOrUnknown(effort),
		thinking: valueOrUnknown(thinking),
		cwd:      valueOrUnknown(cwd),
		version:  valueOrUnknown(version),
		width:    width,
		height:   height,
	}
	return info.render()
}

func (h headerInfo) render() string {
	if h.height > 0 && h.height < headerMiniMinHeight {
		return ""
	}
	rows := make([]string, 0, len(whaleLargeLogo)+6)
	innerWidth := h.innerWidth()
	switch h.mode(innerWidth) {
	case "large":
		rows = append(rows, h.logoLines()...)
		rows = append(rows, "")
		rows = append(rows, h.fieldLine("version:", h.version, ""))
		rows = append(rows, h.fieldLine("model:", h.model, "/model to change"))
		rows = append(rows, h.fieldLine("effort:", h.effort, ""))
		rows = append(rows, h.fieldLine("thinking:", h.thinking, "/model to change"))
		rows = append(rows, h.fieldLine("directory:", h.formatDirectory(), ""))
	case "compact":
		rows = append(rows, h.wordmarkLine())
		rows = append(rows, "")
		rows = append(rows, h.fieldLine("version:", h.version, ""))
		rows = append(rows, h.fieldLine("model:", h.model, "/model to change"))
		rows = append(rows, h.fieldLine("effort:", h.effort, ""))
		rows = append(rows, h.fieldLine("thinking:", h.thinking, "/model to change"))
		rows = append(rows, h.fieldLine("directory:", h.formatDirectory(), ""))
	case "tiny":
		rows = append(rows, h.wordmarkLine())
		rows = append(rows, h.compactFieldLine("model:", h.model, 1))
		rows = append(rows, h.compactFieldLine("dir:", h.formatDirectoryForPrefix("dir:   "), 3))
	default:
		rows = append(rows, h.wordmarkLine())
	}
	return withHeaderBorder(rows, innerWidth)
}

func (h headerInfo) logoLines() []string {
	out := make([]string, 0, len(whaleLargeLogo))
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Accent).Bold(true)
	for _, line := range whaleLargeLogo {
		out = append(out, style.Render(line))
	}
	return out
}

func (h headerInfo) wordmarkLine() string {
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Accent).Bold(true).Render("WHALE") +
		" " +
		lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(h.version)
}

func (h headerInfo) fieldLine(label, value, hint string) string {
	const labelWidth = len("directory:")
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	line := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(label)
	padding := labelWidth - lipgloss.Width(label) + 1
	if padding < 1 {
		padding = 1
	}
	line += strings.Repeat(" ", padding)
	line += lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Render(value)
	if strings.TrimSpace(hint) != "" {
		line += "   " + lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(hint)
	}
	return line
}

func (h headerInfo) compactFieldLine(label, value string, spaces int) string {
	if spaces < 1 {
		spaces = 1
	}
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(strings.TrimSpace(label)) +
		strings.Repeat(" ", spaces) +
		lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Render(strings.TrimSpace(value))
}

func (h headerInfo) mode(innerWidth int) string {
	if innerWidth >= whaleLogoWidth() && h.width >= headerLargeLogoMinWidth && h.height >= headerLargeMinHeight {
		return "large"
	}
	if h.height >= headerCompactMinHeight {
		return "compact"
	}
	if h.height >= headerTinyMinHeight {
		return "tiny"
	}
	if h.height >= headerMiniMinHeight {
		return "mini"
	}
	return "mini"
}

func (h headerInfo) formatDirectory() string {
	return h.formatDirectoryForPrefix("directory: ")
}

func (h headerInfo) formatDirectoryForPrefix(prefix string) string {
	if h.width <= 0 || lipgloss.Width(h.cwd) <= h.maxDirectoryWidth() {
		return h.cwd
	}
	maxWidth := h.innerWidth() - len(prefix)
	if maxWidth < 1 {
		maxWidth = 1
	}
	return truncatePathMiddle(h.cwd, maxWidth)
}

func (h headerInfo) maxDirectoryWidth() int {
	if h.width <= 0 {
		return lipgloss.Width(h.cwd)
	}
	maxWidth := h.innerWidth() - len("directory: ")
	if maxWidth < 1 {
		return 1
	}
	return maxWidth
}

func (h headerInfo) innerWidth() int {
	if h.width <= 0 {
		return whaleLogoWidth()
	}
	outerWidth := h.width
	if outerWidth > headerMaxOuterWidth {
		outerWidth = headerMaxOuterWidth
	}
	innerWidth := outerWidth - 4
	if innerWidth < 16 {
		return 16
	}
	return innerWidth
}

func whaleLogoWidth() int {
	maxWidth := 0
	for _, line := range whaleLargeLogo {
		if width := lipgloss.Width(line); width > maxWidth {
			maxWidth = width
		}
	}
	return maxWidth
}

func withHeaderBorder(lines []string, innerWidth int) string {
	contentWidth := innerWidth
	out := make([]string, 0, len(lines)+2)
	border := lipgloss.NewStyle().Foreground(tuitheme.Default.Border)
	out = append(out, border.Render("╭"+strings.Repeat("─", contentWidth+2)+"╮"))
	for _, line := range lines {
		line = truncateStyledRight(line, contentWidth)
		padding := contentWidth - lipgloss.Width(line)
		if padding < 0 {
			padding = 0
		}
		out = append(out, border.Render("│ ")+line+strings.Repeat(" ", padding)+border.Render(" │"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", contentWidth+2)+"╯"))
	return strings.Join(out, "\n")
}

func valueOrUnknown(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return v
}

func truncatePathMiddle(path string, maxWidth int) string {
	if maxWidth <= 0 || lipgloss.Width(path) <= maxWidth {
		return path
	}
	const ellipsis = "..."
	parts := strings.Split(path, string(os.PathSeparator))
	if len(parts) <= 2 {
		return truncateRight(path, maxWidth)
	}
	first := parts[0]
	last := parts[len(parts)-1]
	if first == "" {
		first = string(os.PathSeparator)
	}
	candidate := filepath.Join(first, ellipsis, last)
	if lipgloss.Width(candidate) <= maxWidth {
		return candidate
	}
	return truncateRight(candidate, maxWidth)
}

func truncateRight(s string, maxWidth int) string {
	if maxWidth <= 3 {
		return strings.Repeat(".", max(0, maxWidth))
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	out := ""
	for _, r := range s {
		next := out + string(r)
		if lipgloss.Width(next)+3 > maxWidth {
			break
		}
		out = next
	}
	return out + "..."
}

func truncateStyledRight(s string, maxWidth int) string {
	if maxWidth <= 3 {
		return strings.Repeat(".", max(0, maxWidth))
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	return xansi.Truncate(s, maxWidth, "...")
}

func resolveVersion() string {
	return build.CurrentVersion()
}

func resolveWorkingDirectory() string {
	wd := resolveWorkingDirectoryPath()
	home, _ := os.UserHomeDir()
	return displayWorkingDirectory(wd, home, runtime.GOOS)
}

func resolveWorkingDirectoryPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func displayWorkingDirectory(wd, home, goos string) string {
	if goos == "windows" {
		return wd
	}
	if home != "" {
		if rel, rErr := filepath.Rel(home, wd); rErr == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
		if filepath.Clean(wd) == filepath.Clean(home) {
			return "~"
		}
	}
	return wd
}
