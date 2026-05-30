package render

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"strings"
)

const thinkingStreamingPreviewLines = 3

const thinkingSettledHeadLines = 2

const thinkingSettledTailLines = 2

const thinkingLargeLineThreshold = 80

const thinkingLargeCharThreshold = 12000

const thinkingPreviewLineRuneLimit = 1200

func renderThinkingCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Bold(true).
		Render("Thinking")
	bodyText := displayThinkingText(block, m.Streaming, m.FullReasoning)
	body := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Italic(true).
		Render(hardWrapRendered(renderEntryText("think", bodyText, contentWidth), contentWidth))
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
		Render(rendered)
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func displayThinkingText(block string, streaming, full bool) string {
	text := strings.TrimRight(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return text
	}
	if full {
		return text
	}
	lines := strings.Split(text, "\n")
	if streaming {
		if len(lines) <= thinkingStreamingPreviewLines {
			return capThinkingPreviewLines(lines)
		}
		return strings.Join(append([]string{"..."}, capThinkingPreviewLineSlice(lines[len(lines)-thinkingStreamingPreviewLines:])...), "\n")
	}
	if len(lines) > thinkingLargeLineThreshold || len(text) > thinkingLargeCharThreshold {
		visible := capThinkingPreviewLineSlice(tailLines(lines, thinkingSettledTailLines))
		return strings.Join(append([]string{"... reasoning scrolled past"}, visible...), "\n")
	}
	totalShown := thinkingSettledHeadLines + thinkingSettledTailLines
	if len(lines) <= totalShown {
		return capThinkingPreviewLines(lines)
	}
	head := capThinkingPreviewLineSlice(lines[:thinkingSettledHeadLines])
	tail := capThinkingPreviewLineSlice(tailLines(lines, thinkingSettledTailLines))
	omitted := len(lines) - len(head) - len(tail)
	out := make([]string, 0, len(head)+1+len(tail))
	out = append(out, head...)
	out = append(out, fmt.Sprintf("... %d lines omitted", omitted))
	out = append(out, tail...)
	return strings.Join(out, "\n")
}

func capThinkingPreviewLines(lines []string) string {
	return strings.Join(capThinkingPreviewLineSlice(lines), "\n")
}

func capThinkingPreviewLineSlice(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = capThinkingPreviewLine(line)
	}
	return out
}

func capThinkingPreviewLine(line string) string {
	runes := []rune(line)
	if len(runes) <= thinkingPreviewLineRuneLimit {
		return line
	}
	return string(runes[:thinkingPreviewLineRuneLimit]) + "..."
}

func tailLines(lines []string, count int) []string {
	if count <= 0 || len(lines) == 0 {
		return nil
	}
	if len(lines) <= count {
		return lines
	}
	return lines[len(lines)-count:]
}
