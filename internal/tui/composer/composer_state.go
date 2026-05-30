package composer

import (
	"strings"
	"unicode"
)

func (c Composer) Value() string {
	return c.expandPendingPastes(c.rawValue())
}

func (c *Composer) SetValue(value string) {
	c.ensureInitialized()
	c.pendingPastes = nil
	c.selectionRuneOffset = -1
	c.textarea.SetValue(c.collapseLargeValue(value))
	c.markRawCacheStale()
	c.moveToEnd()
	c.reflow()
}

func (c *Composer) Reset() {
	c.ensureInitialized()
	c.pendingPastes = nil
	c.wrapCache = nil
	c.selectionRuneOffset = -1
	c.textarea.Reset()
	c.markRawCacheStale()
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
	c.selectionRuneOffset = -1
	c.textarea.SetValue(next)
	c.markRawCacheStale()
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
	if c.hasSelection() {
		c.deleteSelection()
	}
	c.textarea.InsertRune('\n')
	c.markRawCacheStale()
	c.reflow()
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
	if c.rawCacheValid {
		return c.rawCache
	}
	return c.textarea.Value()
}

// primeRawCache must be called by pointer-receiver methods after any
// textarea mutation. Subsequent rawValue() calls within the same tick then
// return the cached string instead of re-materializing it.

func (c *Composer) primeRawCache() {
	c.rawCache = c.textarea.Value()
	c.rawCacheValid = true
}

// markRawCacheStale invalidates the cached rawValue. Pointer-receiver
// methods must call this *immediately* after every textarea mutation,
// before any helper (prunePendingPastes, AtEnd, moveCursorToRuneOffset, …)
// reads rawValue() — otherwise those helpers would see the pre-mutation
// buffer. The most user-visible failure mode is prunePendingPastes
// dropping the just-inserted large-paste placeholder because the stale
// snapshot doesn't contain it yet.

func (c *Composer) markRawCacheStale() {
	c.rawCacheValid = false
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

// hasSelection returns true if there is an active, non-empty selection.
// A zero-width range (anchor == cursor) is treated as no selection so that
// Backspace/Delete fall through to the textarea's normal single-character
// deletion instead of being silently swallowed by deleteSelection().
func (c Composer) hasSelection() bool {
	start, end := c.selectionRange()
	return end > start
}

func (c *Composer) clearCollapsedSelection() {
	if c.selectionRuneOffset >= 0 && !c.hasSelection() {
		c.selectionRuneOffset = -1
	}
}

// selectionRange returns the [start, end) rune offsets of the current
// selection, normalized so start <= end. If there is no active selection
// both values equal the cursor offset.
func (c Composer) selectionRange() (start, end int) {
	valueLen := len([]rune(c.rawValue()))
	cur := clampRuneOffset(c.cursorRuneOffset(), valueLen)
	anchor := clampRuneOffset(c.selectionRuneOffset, valueLen)
	if anchor < 0 {
		return cur, cur
	}
	if cur < anchor {
		return cur, anchor
	}
	return anchor, cur
}

func clampRuneOffset(offset, valueLen int) int {
	if offset < 0 {
		return offset
	}
	if offset > valueLen {
		return valueLen
	}
	return offset
}

// selectedText returns the portion of the value that is currently selected.
func (c Composer) selectedText() string {
	start, end := c.selectionRange()
	runes := []rune(c.rawValue())
	return string(runes[start:end])
}

// deleteSelection removes the selected text and moves the cursor to the
// start of the former selection range. It is a no-op when there is no
// selection.
func (c *Composer) deleteSelection() {
	if !c.hasSelection() {
		c.clearCollapsedSelection()
		return
	}
	start, end := c.selectionRange()
	value := c.rawValue()
	start, end = c.expandSelectionRangeForPendingPastes(start, end, value)
	runes := []rune(value)
	start = max(0, min(start, len(runes)))
	end = max(0, min(end, len(runes)))
	if end <= start {
		c.clearSelection()
		return
	}
	newText := string(runes[:start]) + string(runes[end:])
	c.textarea.SetValue(newText)
	c.markRawCacheStale()
	c.selectionRuneOffset = -1
	c.prunePendingPastes()
	c.moveCursorToRuneOffset(start)
	c.reflow()
}

func (c Composer) expandSelectionRangeForPendingPastes(start, end int, value string) (int, int) {
	for _, paste := range c.pendingPastes {
		if paste.placeholder == "" {
			continue
		}
		searchFrom := 0
		for {
			idx := strings.Index(value[searchFrom:], paste.placeholder)
			if idx < 0 {
				break
			}
			byteStart := searchFrom + idx
			byteEnd := byteStart + len(paste.placeholder)
			placeholderStart := len([]rune(value[:byteStart]))
			placeholderEnd := placeholderStart + len([]rune(value[byteStart:byteEnd]))
			if start < placeholderEnd && end > placeholderStart {
				start = min(start, placeholderStart)
				end = max(end, placeholderEnd)
			}
			searchFrom = byteEnd
		}
	}
	return start, end
}

// clearSelection resets the selection state.
func (c *Composer) clearSelection() {
	c.selectionRuneOffset = -1
}

// startOrExtendSelection records the current cursor position as the
// selection anchor if no selection is active. The caller is responsible
// for moving the cursor to the desired end position.
func (c *Composer) startOrExtendSelection() {
	c.ensureInitialized()
	if !c.hasSelection() {
		c.selectionRuneOffset = c.cursorRuneOffset()
	}
}
