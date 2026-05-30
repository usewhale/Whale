package render

import (
	"github.com/charmbracelet/lipgloss"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"strings"
	"unicode/utf8"
)

func RenderCommandLike(text string) string {
	if text == "" {
		return ""
	}
	if rendered, ok := highlightShellCommand(text); ok {
		return rendered
	}
	return renderCommandLikeFallback(text)
}

func renderCommandLikeFallback(text string) string {
	var out strings.Builder
	tokenIndex := 0
	commandPosition := true
	for _, part := range splitCommandPreservingSpace(text) {
		if part == "" {
			continue
		}
		if isCommandSpace(part) {
			out.WriteString(part)
			if strings.ContainsAny(part, "\n\r") {
				commandPosition = true
			}
			continue
		}
		out.WriteString(styleCommandToken(part, tokenIndex, commandPosition))
		commandPosition = isShellCommandBoundary(part)
		tokenIndex++
	}
	return out.String()
}

func splitCommandPreservingSpace(text string) []string {
	parts := make([]string, 0, 8)
	start := 0
	inQuote := rune(0)
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			}
			i += size
			continue
		}
		switch r {
		case '\'', '"':
			inQuote = r
			i += size
		case ' ', '\t', '\n', '\r':
			if start < i {
				parts = append(parts, text[start:i])
			}
			spaceStart := i
			spaceEnd := i + size
			for spaceEnd < len(text) {
				next, nextSize := utf8.DecodeRuneInString(text[spaceEnd:])
				if next != ' ' && next != '\t' && next != '\n' && next != '\r' {
					break
				}
				spaceEnd += nextSize
			}
			parts = append(parts, text[spaceStart:spaceEnd])
			start = spaceEnd
			i = spaceEnd
		default:
			i += size
		}
	}
	if start < len(text) {
		parts = append(parts, text[start:])
	}
	return parts
}

func isCommandSpace(text string) bool {
	for _, r := range text {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return text != ""
}

func styleCommandToken(token string, index int, commandPosition bool) string {
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Text)
	switch {
	case isShellOperator(token):
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Palette)
	case strings.HasPrefix(token, "-"):
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Warn)
	case strings.HasPrefix(token, "\"") || strings.HasPrefix(token, "'"):
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Result)
	case index == 0 || commandPosition:
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Info)
	}
	return style.Render(token)
}

func isShellCommandBoundary(token string) bool {
	switch token {
	case "&&", "||", "|", ";":
		return true
	default:
		return false
	}
}

func isShellOperator(token string) bool {
	switch token {
	case "&&", "||", "|", ";", ">", ">>", "<", "2>", "2>>":
		return true
	default:
		return false
	}
}
