package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

const (
	maxShellHighlightBytes = 64 * 1024
	maxShellHighlightLines = 1000
)

func highlightShellCommand(text string) (string, bool) {
	if len(text) > maxShellHighlightBytes || shellCommandLineCount(text) > maxShellHighlightLines {
		return "", false
	}
	lexer := lexers.Get("bash")
	if lexer == nil {
		return "", false
	}

	var out strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		body := strings.TrimSuffix(line, "\n")
		if body != "" {
			rendered, ok := highlightShellCommandLine(chroma.Coalesce(lexer), body)
			if !ok {
				return "", false
			}
			out.WriteString(rendered)
		}
		if strings.HasSuffix(line, "\n") {
			out.WriteString("\n")
		}
	}
	if out.Len() == 0 && text != "" {
		return "", false
	}
	return out.String(), true
}

func highlightShellCommandLine(lexer chroma.Lexer, line string) (string, bool) {
	iterator, err := lexer.Tokenise(nil, line)
	if err != nil {
		return "", false
	}

	var out strings.Builder
	var raw strings.Builder
	for token := iterator(); token != chroma.EOF; token = iterator() {
		raw.WriteString(token.Value)
		out.WriteString(renderShellToken(token))
	}
	if raw.String() != line {
		return "", false
	}
	return out.String(), true
}

func shellCommandLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func renderShellToken(token chroma.Token) string {
	if token.Value == "" {
		return ""
	}
	if strings.TrimSpace(token.Value) == "" {
		return token.Value
	}
	if token.Type == chroma.Text {
		return renderCommandLikeFallback(token.Value)
	}
	return shellTokenStyle(token).Render(token.Value)
}

func shellTokenStyle(token chroma.Token) lipgloss.Style {
	value := strings.TrimSpace(token.Value)
	switch {
	case strings.HasPrefix(value, "-") && value != "-":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Warn)
	case isShellOperator(value):
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Palette)
	case token.Type >= chroma.NameVariable && token.Type <= chroma.NameVariableMagic:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Palette)
	case token.Type >= chroma.Keyword && token.Type < chroma.Name:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Palette)
	case token.Type >= chroma.Name && token.Type < chroma.Literal:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Info)
	case token.Type >= chroma.LiteralString && token.Type < chroma.LiteralNumber:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Result)
	case token.Type >= chroma.LiteralNumber && token.Type < chroma.Operator:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Warn)
	case token.Type >= chroma.Operator && token.Type < chroma.Comment:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Palette)
	case token.Type >= chroma.Comment && token.Type < chroma.Generic:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	default:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Text)
	}
}
