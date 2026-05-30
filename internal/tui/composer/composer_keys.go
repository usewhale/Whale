package composer

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (c *Composer) Update(msg tea.Msg) tea.Cmd {
	c.ensureInitialized()
	c.clearCollapsedSelection()
	wasAtEnd := c.AtEnd()
	prevHeight := c.textarea.Height()
	var cmd tea.Cmd
	c.textarea, cmd = c.textarea.Update(msg)
	c.markRawCacheStale()
	c.prunePendingPastes()
	c.reflow()
	if wasAtEnd && c.textarea.Height() > prevHeight {
		c.realignViewportAtEnd()
	}
	return cmd
}

func (c *Composer) HandleKey(msg tea.KeyMsg) bool {
	c.ensureInitialized()

	// When selection is active, handle selection-aware operations first.
	// Non-selection keys either clear the selection (movement) or delete
	// the selected range (backspace/delete, printable characters).
	if c.hasSelection() && !isSelectionKey(msg) {
		switch msg.String() {
		case "backspace", "ctrl+h", "delete", "ctrl+d":
			c.deleteSelection()
			return true
		default:
			if len(msg.Runes) > 0 {
				// Printable character: delete selection first, then let
				// textarea insert the typed character via Update().
				c.deleteSelection()
			} else {
				// Non-printable non-selection key: just clear selection.
				c.clearSelection()
			}
		}
	}

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
	// Selection keys
	case "shift+left":
		if c.cursorRuneOffset() <= 0 {
			return true
		}
		c.startOrExtendSelection()
		c.moveCursorLeftByRune()
		return true
	case "shift+right":
		if c.cursorRuneOffset() >= len([]rune(c.rawValue())) {
			return true
		}
		c.startOrExtendSelection()
		c.moveCursorRightByRune()
		return true
	case "shift+up":
		c.startOrExtendSelection()
		c.textarea.CursorUp()
		c.clearCollapsedSelection()
		return true
	case "shift+down":
		c.startOrExtendSelection()
		c.textarea.CursorDown()
		c.clearCollapsedSelection()
		return true
	case "shift+home":
		c.startOrExtendSelection()
		c.textarea.CursorStart()
		c.clearCollapsedSelection()
		return true
	case "shift+end":
		c.startOrExtendSelection()
		c.textarea.CursorEnd()
		c.clearCollapsedSelection()
		return true
	// Word-level selection keys
	case "ctrl+shift+left":
		if c.cursorRuneOffset() <= 0 {
			return true
		}
		c.startOrExtendSelection()
		c.moveCursorToPrevWord()
		return true
	case "ctrl+shift+right":
		if c.cursorRuneOffset() >= len([]rune(c.rawValue())) {
			return true
		}
		c.startOrExtendSelection()
		c.moveCursorToNextWord()
		return true
	}
	return false
}

// isSelectionKey returns true when msg is a key that extends or maintains
// the current selection rather than clearing it.
func isSelectionKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "shift+left", "shift+right", "shift+up", "shift+down",
		"shift+home", "shift+end",
		"ctrl+shift+left", "ctrl+shift+right":
		return true
	}
	return false
}

// moveCursorLeftByRune moves the cursor one rune to the left, stopping at
// the beginning of the text.
func (c *Composer) moveCursorLeftByRune() {
	offset := c.cursorRuneOffset()
	if offset <= 0 {
		return
	}
	c.moveCursorToRuneOffset(offset - 1)
	c.clearCollapsedSelection()
}

// moveCursorRightByRune moves the cursor one rune to the right, stopping at
// the end of the text.
func (c *Composer) moveCursorRightByRune() {
	offset := c.cursorRuneOffset()
	textLen := len([]rune(c.rawValue()))
	if offset >= textLen {
		return
	}
	c.moveCursorToRuneOffset(offset + 1)
	c.clearCollapsedSelection()
}

// moveCursorToPrevWord moves the cursor to the beginning of the current
// or previous word. Words are sequences of non-whitespace runes.
func (c *Composer) moveCursorToPrevWord() {
	offset := c.cursorRuneOffset()
	if offset <= 0 {
		return
	}
	runes := []rune(c.rawValue())
	// Skip whitespace immediately to the left of the cursor.
	i := offset - 1
	for i >= 0 && runes[i] == ' ' {
		i--
	}
	// Skip to the start of the word.
	for i >= 0 && runes[i] != ' ' {
		i--
	}
	c.moveCursorToRuneOffset(i + 1)
}

// moveCursorToNextWord moves the cursor to the end of the current or next
// word. Words are sequences of non-whitespace runes.
func (c *Composer) moveCursorToNextWord() {
	offset := c.cursorRuneOffset()
	runes := []rune(c.rawValue())
	textLen := len(runes)
	if offset >= textLen {
		return
	}
	// Skip whitespace immediately to the right of the cursor.
	i := offset
	for i < textLen && runes[i] == ' ' {
		i++
	}
	// Skip to the end of the word.
	for i < textLen && runes[i] != ' ' {
		i++
	}
	c.moveCursorToRuneOffset(i)
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

func (c *Composer) realignViewportAtEnd() {
	value := c.textarea.Value()
	c.textarea.SetValue(value)
}
