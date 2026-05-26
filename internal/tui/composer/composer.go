package composer

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

const (
	composerCollapseThreshold = 20
	composerHeadLines         = 3
	composerTailLines         = 2
	largePasteCharThreshold   = 1000
)

type Composer struct {
	textarea         textarea.Model
	width            int
	pendingPastes    []pendingPaste
	largePasteCounts map[int]int
}

type pendingPaste struct {
	placeholder string
	text        string
}

func New() Composer {
	ta := textarea.New()
	ta.Placeholder = "Type message or command"
	ta.Prompt = "› "
	ta.SetPromptFunc(2, func(lineIdx int) string {
		if lineIdx == 0 {
			return "› "
		}
		return "  "
	})
	ta.ShowLineNumbers = false
	ta.CharLimit = 20000
	ta.MaxHeight = composerCollapseThreshold
	ta.SetWidth(80)
	ta.SetHeight(1)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.Focus()
	return Composer{textarea: ta, width: 80}
}

func (c Composer) Value() string {
	return c.expandPendingPastes(c.rawValue())
}

func (c *Composer) SetValue(value string) {
	c.ensureInitialized()
	c.pendingPastes = nil
	c.textarea.SetValue(c.collapseLargeValue(value))
	c.moveToEnd()
	c.reflow()
}

func (c *Composer) Reset() {
	c.ensureInitialized()
	c.pendingPastes = nil
	c.textarea.Reset()
	c.reflow()
}

func (c *Composer) SetCursorEnd() {
	c.ensureInitialized()
	c.moveToEnd()
}

func (c *Composer) CurrentPrefixedToken(prefix rune) (string, bool) {
	c.ensureInitialized()
	return c.currentPrefixedToken(prefix)
}

func (c *Composer) ReplaceCurrentPrefixedToken(prefix rune, replacement string) bool {
	c.ensureInitialized()
	start, end, ok := c.currentPrefixedTokenRange(prefix)
	if !ok {
		return false
	}
	value := c.rawValue()
	runes := []rune(value)
	replacementRunes := []rune(replacement)
	next := string(runes[:start]) + replacement + string(runes[end:])
	c.pendingPastes = nil
	c.textarea.SetValue(next)
	c.moveCursorToRuneOffset(start + len(replacementRunes))
	c.reflow()
	return true
}

func (c *Composer) SetWidth(width int) {
	c.ensureInitialized()
	c.width = max(20, width)
	c.textarea.SetWidth(max(16, c.width-2))
	c.reflow()
}

func (c *Composer) InsertNewline() {
	c.ensureInitialized()
	c.textarea.InsertRune('\n')
	c.reflow()
}

func (c *Composer) HandlePaste(value string) {
	c.ensureInitialized()
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if len([]rune(value)) > largePasteCharThreshold {
		c.textarea.InsertString(c.addPendingPaste(value))
	} else {
		c.textarea.InsertString(value)
	}
	c.prunePendingPastes()
	c.reflow()
}

func (c *Composer) Update(msg tea.Msg) tea.Cmd {
	c.ensureInitialized()
	wasAtEnd := c.AtEnd()
	prevHeight := c.textarea.Height()
	var cmd tea.Cmd
	c.textarea, cmd = c.textarea.Update(msg)
	c.prunePendingPastes()
	c.reflow()
	if wasAtEnd && c.textarea.Height() > prevHeight {
		c.realignViewportAtEnd()
	}
	return cmd
}

func (c *Composer) HandleKey(msg tea.KeyMsg) bool {
	c.ensureInitialized()
	switch msg.String() {
	case "ctrl+p", "ctrl+n":
		return false
	case "ctrl+j", "shift+enter":
		c.InsertNewline()
		return true
	case "up":
		return c.moveFoldedVisibleLine(-1)
	case "down":
		return c.moveFoldedVisibleLine(1)
	case "pgup":
		for c.textarea.Line() > 0 {
			c.textarea.CursorUp()
		}
		c.textarea.CursorStart()
		return true
	case "pgdown":
		c.moveToEnd()
		return true
	}
	return false
}

func (c Composer) View() string {
	c = c.initialized()
	var view string
	if c.rawValue() == "" {
		copy := c.textarea
		copy.SetHeight(1)
		view = copy.View()
	} else {
		value := c.rawValue()
		lines := splitComposerLines(value)
		if len(lines) <= composerCollapseThreshold {
			view = c.plainView(lines)
		} else {
			view = c.foldedView(lines)
		}
	}
	return c.normalizeView(view)
}

func (c Composer) LineCount() int {
	return c.textarea.LineCount()
}

func (c Composer) Height() int {
	return c.textarea.Height()
}

func (c Composer) AtStart() bool {
	return c.textarea.Line() == 0 && c.textarea.LineInfo().ColumnOffset == 0
}

func (c Composer) AtEnd() bool {
	if c.textarea.Line() != c.textarea.LineCount()-1 {
		return false
	}
	info := c.textarea.LineInfo()
	lines := splitComposerLines(c.rawValue())
	if c.textarea.Line() < 0 || c.textarea.Line() >= len(lines) {
		return false
	}
	line := lines[c.textarea.Line()]
	return info.StartColumn+info.ColumnOffset >= len([]rune(line))
}

func (c *Composer) moveToEnd() {
	for c.textarea.Line() < c.textarea.LineCount()-1 {
		c.textarea.CursorDown()
	}
	c.textarea.CursorEnd()
}

func (c *Composer) moveCursorToRuneOffset(offset int) {
	if offset < 0 {
		offset = 0
	}
	lines := splitComposerLines(c.rawValue())
	line := 0
	col := offset
	for line < len(lines) {
		lineLen := len([]rune(lines[line]))
		if col <= lineLen {
			break
		}
		col -= lineLen + 1
		line++
	}
	if line >= len(lines) {
		c.moveToEnd()
		return
	}
	c.moveToLine(line)
	c.textarea.SetCursor(col)
}

func (c *Composer) moveToLine(line int) {
	line = max(0, min(line, c.textarea.LineCount()-1))
	for c.textarea.Line() > line {
		c.textarea.CursorUp()
	}
	for c.textarea.Line() < line {
		c.textarea.CursorDown()
	}
}

func (c *Composer) moveFoldedVisibleLine(direction int) bool {
	lines := splitComposerLines(c.rawValue())
	if len(lines) <= composerCollapseThreshold {
		return false
	}
	visible := foldedVisibleLineIndexes(len(lines), c.textarea.Line())
	current := c.textarea.Line()
	switch {
	case direction < 0:
		for i := len(visible) - 1; i >= 0; i-- {
			if visible[i] < current {
				c.moveToLine(visible[i])
				c.reflow()
				return true
			}
		}
	case direction > 0:
		for _, line := range visible {
			if line > current {
				c.moveToLine(line)
				c.reflow()
				return true
			}
		}
	}
	return true
}

func (c *Composer) reflow() {
	height := c.visualLineCount()
	if height < 1 {
		height = 1
	}
	if height > composerCollapseThreshold {
		height = composerCollapseThreshold
	}
	if height != c.textarea.Height() {
		c.textarea.SetHeight(height)
	}
}

func (c Composer) foldedView(lines []string) string {
	cursorLine := c.textarea.Line()
	keep := map[int]bool{}
	for _, line := range foldedVisibleLineIndexes(len(lines), cursorLine) {
		keep[line] = true
	}

	out := make([]string, 0, composerHeadLines+composerTailLines+4)
	prev := -1
	for i := 0; i < len(lines); i++ {
		if !keep[i] {
			continue
		}
		if i-prev > 1 {
			out = append(out, c.hiddenLine(i-prev-1))
		}
		out = append(out, c.promptLine(lines[i], i == 0, i == cursorLine))
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

func (c Composer) plainView(lines []string) string {
	cursorLine := c.textarea.Line()
	cursorCol := -1
	if cursorLine >= 0 && cursorLine < len(lines) {
		info := c.textarea.LineInfo()
		cursorCol = info.StartColumn + info.ColumnOffset
	}

	out := make([]string, 0, c.visualLineCount())
	displayLine := 0
	wrapWidth := c.textarea.Width()
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
			out = append(out, c.promptLineAt(segment.text, displayLine == 0, hasCursor, relativeCursor))
			displayLine++
		}
	}
	return strings.Join(out, "\n")
}

type composerLineSegment struct {
	text       string
	start, end int
}

func wrapComposerLine(line string, width int) []composerLineSegment {
	runes := []rune(line)
	if len(runes) == 0 {
		return []composerLineSegment{{text: "", start: 0, end: 0}}
	}
	if width <= 0 {
		return []composerLineSegment{{text: line, start: 0, end: len(runes)}}
	}
	segments := []composerLineSegment{}
	start := 0
	cells := 0
	for i, r := range runes {
		w := rw.RuneWidth(r)
		if w < 0 {
			w = 0
		}
		if cells > 0 && cells+w > width {
			segments = append(segments, composerLineSegment{
				text:  string(runes[start:i]),
				start: start,
				end:   i,
			})
			start = i
			cells = 0
		}
		cells += w
	}
	segments = append(segments, composerLineSegment{
		text:  string(runes[start:]),
		start: start,
		end:   len(runes),
	})
	return segments
}

func (c Composer) promptLine(line string, first bool, cursor bool) string {
	info := c.textarea.LineInfo()
	return c.promptLineAt(line, first, cursor, info.StartColumn+info.ColumnOffset)
}

func (c Composer) promptLineAt(line string, first bool, cursor bool, col int) string {
	prefix := "  "
	if first {
		prefix = lipgloss.NewStyle().Foreground(tuitheme.Default.Accent).Bold(true).Render("›") + " "
	}
	return prefix + renderComposerLineText(line, cursor, col)
}

func renderComposerLineText(line string, cursor bool, col int) string {
	runes := []rune(line)
	if col < 0 {
		col = 0
	}
	if col > len(runes) {
		col = len(runes)
	}
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

func (c Composer) visualLineCount() int {
	width := c.textarea.Width()
	if width <= 0 {
		return len(splitComposerLines(c.rawValue()))
	}
	total := 0
	for _, line := range splitComposerLines(c.rawValue()) {
		total += wrappedLineCount([]rune(line), width)
	}
	return total
}

func wrappedLineCount(runes []rune, width int) int {
	if width <= 0 {
		return 1
	}
	var (
		lines         = 1
		rowWidth      int
		wordWidth     int
		lastCharWidth int
		spaces        int
	)

	flushWord := func() {
		if wordWidth == 0 && spaces == 0 {
			return
		}
		if spaces > 0 {
			if rowWidth+wordWidth+spaces > width {
				lines++
				rowWidth = wordWidth + spaces
			} else {
				rowWidth += wordWidth + spaces
			}
			wordWidth = 0
			lastCharWidth = 0
			spaces = 0
			return
		}
		// Handle very long words that exceed the wrap width.
		if wordWidth+lastCharWidth > width {
			if rowWidth > 0 {
				lines++
				rowWidth = 0
			}
			rowWidth = wordWidth
			wordWidth = 0
			lastCharWidth = 0
		}
	}

	for _, r := range runes {
		w := rw.RuneWidth(r)
		if w < 0 {
			w = 0
		}
		if unicode.IsSpace(r) {
			spaces++
		} else {
			wordWidth += w
			lastCharWidth = w
		}
		flushWord()
	}

	if rowWidth+wordWidth+spaces >= width {
		lines++
	}

	return lines
}

func repeatSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}

func (c *Composer) realignViewportAtEnd() {
	value := c.textarea.Value()
	c.textarea.SetValue(value)
}

func (c *Composer) ensureInitialized() {
	if c.width != 0 {
		return
	}
	*c = New()
}

func (c Composer) initialized() Composer {
	if c.width != 0 {
		return c
	}
	return New()
}

func (c Composer) rawValue() string {
	return c.textarea.Value()
}

func (c Composer) currentPrefixedToken(prefix rune) (string, bool) {
	start, end, ok := c.currentPrefixedTokenRange(prefix)
	if !ok {
		return "", false
	}
	runes := []rune(c.rawValue())
	return string(runes[start+1 : end]), true
}

func (c Composer) currentPrefixedTokenRange(prefix rune) (int, int, bool) {
	value := c.rawValue()
	if value == "" || strings.Contains(value, "\n") {
		return 0, 0, false
	}
	runes := []rune(value)
	cursor := c.cursorRuneOffset()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	start := cursor
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	end := cursor
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	if start >= end || runes[start] != prefix {
		return 0, 0, false
	}
	if start > 0 && !unicode.IsSpace(runes[start-1]) {
		return 0, 0, false
	}
	if end > start+1 && strings.ContainsRune(string(runes[start+1:end]), '\t') {
		return 0, 0, false
	}
	return start, end, true
}

func (c Composer) cursorRuneOffset() int {
	line := c.textarea.Line()
	info := c.textarea.LineInfo()
	lines := splitComposerLines(c.rawValue())
	if line < 0 {
		return 0
	}
	if line >= len(lines) {
		total := 0
		for i, text := range lines {
			total += len([]rune(text))
			if i < len(lines)-1 {
				total++
			}
		}
		return total
	}
	offset := 0
	for i := 0; i < line; i++ {
		offset += len([]rune(lines[i])) + 1
	}
	col := info.StartColumn + info.ColumnOffset
	lineLen := len([]rune(lines[line]))
	if col > lineLen {
		col = lineLen
	}
	return offset + col
}

func (c *Composer) collapseLargeValue(value string) string {
	if len([]rune(value)) <= largePasteCharThreshold {
		return value
	}
	return c.addPendingPaste(value)
}

func (c *Composer) addPendingPaste(value string) string {
	placeholder := c.nextLargePastePlaceholder(len([]rune(value)))
	c.pendingPastes = append(c.pendingPastes, pendingPaste{
		placeholder: placeholder,
		text:        value,
	})
	return placeholder
}

func (c *Composer) nextLargePastePlaceholder(charCount int) string {
	if c.largePasteCounts == nil {
		c.largePasteCounts = map[int]int{}
	}
	c.largePasteCounts[charCount]++
	base := fmt.Sprintf("[Pasted Content %d chars]", charCount)
	if c.largePasteCounts[charCount] == 1 {
		return base
	}
	return fmt.Sprintf("%s #%d", base, c.largePasteCounts[charCount])
}

func (c Composer) expandPendingPastes(value string) string {
	for _, paste := range c.pendingPastes {
		value = strings.ReplaceAll(value, paste.placeholder, paste.text)
	}
	return value
}

func (c *Composer) prunePendingPastes() {
	if len(c.pendingPastes) == 0 {
		return
	}
	value := c.rawValue()
	kept := c.pendingPastes[:0]
	for _, paste := range c.pendingPastes {
		if strings.Contains(value, paste.placeholder) {
			kept = append(kept, paste)
		}
	}
	c.pendingPastes = kept
}
