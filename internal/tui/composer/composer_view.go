package composer

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (c Composer) View() string {
	c = c.initialized()
	selStart, selEnd := -1, -1
	if c.hasSelection() {
		selStart, selEnd = c.selectionRange()
	}
	var view string
	if c.rawValue() == "" {
		copy := c.textarea
		copy.SetHeight(1)
		view = copy.View()
	} else {
		value := c.rawValue()
		lines := splitComposerLines(value)
		if len(lines) <= composerCollapseThreshold {
			view = c.plainView(lines, selStart, selEnd)
		} else {
			view = c.foldedView(lines, selStart, selEnd)
		}
	}
	return c.normalizeView(view)
}

func (c Composer) foldedView(lines []string, selStart, selEnd int) string {
	cursorLine := c.textarea.Line()
	keep := map[int]bool{}
	for _, line := range foldedVisibleLineIndexes(len(lines), cursorLine) {
		keep[line] = true
	}

	out := make([]string, 0, composerHeadLines+composerTailLines+4)
	globalRuneOffset := 0
	prev := -1
	for i := 0; i < len(lines); i++ {
		if !keep[i] {
			globalRuneOffset += len([]rune(lines[i])) + 1
			continue
		}
		if i-prev > 1 {
			out = append(out, c.hiddenLine(i-prev-1))
		}
		out = append(out, c.promptLine(lines[i], i == 0, i == cursorLine, selStart, selEnd, globalRuneOffset))
		globalRuneOffset += len([]rune(lines[i])) + 1
		prev = i
	}
	out = append(out, c.hintLine(len(lines)))
	return strings.Join(out, "\n")
}

func foldedVisibleLineIndexes(lineCount int, cursorLine int) []int {
	if lineCount <= 0 {
		return nil
	}
	keep := map[int]bool{}
	for i := 0; i < composerHeadLines && i < lineCount; i++ {
		keep[i] = true
	}
	for i := max(0, lineCount-composerTailLines); i < lineCount; i++ {
		keep[i] = true
	}
	if cursorLine >= 0 && cursorLine < lineCount {
		keep[cursorLine] = true
	}

	out := make([]int, 0, len(keep))
	for i := 0; i < lineCount; i++ {
		if keep[i] {
			out = append(out, i)
		}
	}
	return out
}

func (c Composer) plainView(lines []string, selStart, selEnd int) string {
	cursorLine := c.textarea.Line()
	cursorCol := -1
	if cursorLine >= 0 && cursorLine < len(lines) {
		info := c.textarea.LineInfo()
		cursorCol = info.StartColumn + info.ColumnOffset
	}

	out := make([]string, 0, c.visualLineCount())
	displayLine := 0
	wrapWidth := c.textarea.Width()
	globalRuneOffset := 0
	for i, line := range lines {
		lineRunes := []rune(line)
		for _, segment := range wrapComposerLine(line, wrapWidth) {
			hasCursor := false
			relativeCursor := 0
			if i == cursorLine {
				switch {
				case cursorCol >= segment.start && cursorCol < segment.end:
					hasCursor = true
					relativeCursor = cursorCol - segment.start
				case cursorCol == len(lineRunes) && segment.end == len(lineRunes):
					hasCursor = true
					relativeCursor = len([]rune(segment.text))
				case len(lineRunes) == 0 && segment.start == 0 && segment.end == 0:
					hasCursor = true
				}
			}
			segmentRuneStart := globalRuneOffset + segment.start
			out = append(out, c.promptLineAt(segment.text, displayLine == 0, hasCursor, relativeCursor, selStart, selEnd, segmentRuneStart))
			displayLine++
		}
		globalRuneOffset += len(lineRunes) + 1 // +1 for newline
	}
	return strings.Join(out, "\n")
}

func (c Composer) promptLine(line string, first bool, cursor bool, selStart, selEnd int, globalRuneOffset int) string {
	info := c.textarea.LineInfo()
	return c.promptLineAt(line, first, cursor, info.StartColumn+info.ColumnOffset, selStart, selEnd, globalRuneOffset)
}

func (c Composer) promptLineAt(line string, first bool, cursor bool, col int, selStart, selEnd int, lineRuneStart int) string {
	prefix := "  "
	if first {
		prefix = lipgloss.NewStyle().Foreground(tuitheme.Default.Accent).Bold(true).Render("›") + " "
	}
	return prefix + renderComposerLineText(line, cursor, col, selStart, selEnd, lineRuneStart)
}

func renderComposerLineText(line string, cursor bool, col int, selStart, selEnd int, lineRuneStart int) string {
	runes := []rune(line)
	if col < 0 {
		col = 0
	}
	if col > len(runes) {
		col = len(runes)
	}

	lineRuneEnd := lineRuneStart + len(runes)
	hasSelection := selStart >= 0 && lineRuneEnd > selStart && lineRuneStart < selEnd

	if !hasSelection {
		// Fast path: no selection on this segment — original rendering.
		var out strings.Builder
		if cursor && col == 0 {
			out.WriteString("█")
		}
		for i, r := range runes {
			out.WriteRune(r)
			if cursor && col == i+1 {
				out.WriteString("█")
			}
		}
		if len(runes) == 0 && cursor && col == 0 {
			return "█"
		}
		return out.String()
	}

	// Selection is active and overlaps this segment.
	localSelStart := max(0, selStart-lineRuneStart)
	localSelEnd := min(len(runes), selEnd-lineRuneStart)
	selStyle := lipgloss.NewStyle().
		Background(tuitheme.Default.Selection).
		Foreground(lipgloss.Color("255"))

	var out strings.Builder
	i := 0
	for i < len(runes) {
		inSel := i >= localSelStart && i < localSelEnd
		atCursor := cursor && col == i

		// Render cursor before this character if it belongs here.
		if atCursor {
			if inSel {
				// Cursor inside selection — render cursor over selected
				// background by inverting the cursor char.
				out.WriteString(selStyle.Render("█"))
			} else {
				out.WriteString("█")
			}
		}

		// Render this character.
		if inSel {
			out.WriteString(selStyle.Render(string(runes[i])))
		} else {
			out.WriteRune(runes[i])
		}
		i++
	}

	// Cursor after the last character.
	if cursor && col == len(runes) {
		if localSelStart <= len(runes) && localSelEnd >= len(runes) {
			out.WriteString(selStyle.Render("█"))
		} else {
			out.WriteString("█")
		}
	}

	if len(runes) == 0 && cursor && col == 0 {
		return "█"
	}
	return out.String()
}

func (c Composer) hiddenLine(n int) string {
	return lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Render(fmt.Sprintf("  [… %d lines hidden - full content kept …]", n))
}

func (c Composer) hintLine(n int) string {
	return lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Render(fmt.Sprintf("  [%d lines · Ctrl+A/E/K/U line · Ctrl+W/Alt+B,F word · Ctrl+C clear · PgUp/PgDn]", n))
}

func splitComposerLines(value string) []string {
	if value == "" {
		return []string{""}
	}
	return strings.Split(value, "\n")
}

func (c Composer) normalizeView(view string) string {
	view = strings.TrimRight(view, "\n")
	if view == "" {
		return ""
	}
	lines := strings.Split(view, "\n")
	padded := make([]string, 0, len(lines))
	style := lipgloss.NewStyle().Width(c.width).MaxWidth(c.width)
	for _, line := range lines {
		padded = append(padded, style.Render(line))
	}
	return strings.Join(padded, "\n")
}
