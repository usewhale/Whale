package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/usewhale/whale/internal/build"
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
		rows = append(rows, whaleLargeLogo...)
		rows = append(rows, "")
		rows = append(rows, fmt.Sprintf("version:   %s", h.version))
		rows = append(rows, fmt.Sprintf("model:     %s   /model to change", h.model))
		rows = append(rows, fmt.Sprintf("effort:    %s", h.effort))
		rows = append(rows, fmt.Sprintf("thinking:  %s   /model to change", h.thinking))
		rows = append(rows, fmt.Sprintf("directory: %s", h.formatDirectory()))
	case "compact":
		rows = append(rows, fmt.Sprintf("WHALE %s", h.version))
		rows = append(rows, "")
		rows = append(rows, fmt.Sprintf("version:   %s", h.version))
		rows = append(rows, fmt.Sprintf("model:     %s   /model to change", h.model))
		rows = append(rows, fmt.Sprintf("effort:    %s", h.effort))
		rows = append(rows, fmt.Sprintf("thinking:  %s   /model to change", h.thinking))
		rows = append(rows, fmt.Sprintf("directory: %s", h.formatDirectory()))
	case "tiny":
		rows = append(rows, fmt.Sprintf("WHALE %s", h.version))
		rows = append(rows, fmt.Sprintf("model: %s", h.model))
		rows = append(rows, fmt.Sprintf("dir:   %s", h.formatDirectoryForPrefix("dir:   ")))
	default:
		rows = append(rows, fmt.Sprintf("WHALE %s", h.version))
	}
	return withHeaderBorder(rows, innerWidth)
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
	out = append(out, "╭"+strings.Repeat("─", contentWidth+2)+"╮")
	for _, line := range lines {
		line = truncateRight(line, contentWidth)
		padding := contentWidth - lipgloss.Width(line)
		if padding < 0 {
			padding = 0
		}
		out = append(out, "│ "+line+strings.Repeat(" ", padding)+" │")
	}
	out = append(out, "╰"+strings.Repeat("─", contentWidth+2)+"╯")
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
