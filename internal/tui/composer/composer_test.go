package composer

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func TestComposerCtrlJInsertsNewline(t *testing.T) {
	c := New()
	c.SetValue("hello")
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlJ}) {
		t.Fatal("expected ctrl+j to be handled")
	}
	if got := c.Value(); got != "hello\n" {
		t.Fatalf("unexpected value: %q", got)
	}
}

func TestComposerPasteNormalizesTabsToSpaces(t *testing.T) {
	c := New()
	c.HandlePaste("foo\n\tbar")
	if got := c.Value(); got != "foo\n    bar" {
		t.Fatalf("expected pasted tab to normalize to spaces, got %q", got)
	}
}

func TestComposerMultilinePromptOnlyMarksFirstLine(t *testing.T) {
	c := New()
	c.SetValue("hello\nworld")
	view := c.View()
	if strings.Count(view, "›") != 1 {
		t.Fatalf("expected only first line to use prompt glyph, got %q", view)
	}
	if !strings.Contains(view, "\n  world") {
		t.Fatalf("expected continuation line indentation, got %q", view)
	}
}

func TestComposerCtrlUKillsToLineStart(t *testing.T) {
	// readline semantics: Ctrl+U kills from the cursor to the start of the
	// current line. With cursor at the end of "two" on line 2, only "two"
	// is removed — "one\n" must survive.
	c := New()
	c.SetValue("one\ntwo")
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if got := c.Value(); got != "one\n" {
		t.Fatalf("expected ctrl+u to kill only current line, got %q", got)
	}
}

func TestComposerCtrlDDeletesCharacterForward(t *testing.T) {
	c := New()
	c.SetValue("hello")
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlA}) // cursor → line start
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if got := c.Value(); got != "ello" {
		t.Fatalf("expected ctrl+d to delete the character at cursor, got %q", got)
	}
}

func TestComposerCtrlWDeletesPreviousWord(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if got := c.Value(); got != "hello " {
		t.Fatalf("unexpected value: %q", got)
	}
}

func TestComposerCtrlAAndCtrlEMoveWithinCurrentLine(t *testing.T) {
	c := New()
	c.SetValue("abc\ndef")
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if got := c.Value(); got != "abc\nXdef" {
		t.Fatalf("expected insert at second line start, got %q", got)
	}
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	if got := c.Value(); got != "abc\nXdefY" {
		t.Fatalf("expected insert at second line end, got %q", got)
	}
}

func TestComposerHomeAndEndMoveWithinCurrentLine(t *testing.T) {
	// Home/End are the keyboard-key counterparts of Ctrl+A/Ctrl+E and must
	// behave identically — jump to the start/end of the *current visual
	// line*, not the buffer extremes. Whale used to route Home/End through
	// the transcript viewport; the readline alignment moves them onto the
	// composer (see internal/tui/model_keys.go handleChatModeKey).
	c := New()
	c.SetValue("abc\ndef")
	c.Update(tea.KeyMsg{Type: tea.KeyHome})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if got := c.Value(); got != "abc\nXdef" {
		t.Fatalf("expected Home to insert at second line start, got %q", got)
	}
	c.Update(tea.KeyMsg{Type: tea.KeyEnd})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	if got := c.Value(); got != "abc\nXdefY" {
		t.Fatalf("expected End to insert at second line end, got %q", got)
	}
}

func TestComposerCtrlKKillsToEndOfLine(t *testing.T) {
	c := New()
	c.SetValue("hello world\nsecond")
	// Cursor starts at end of "second"; move to the start of that line so
	// Ctrl+K has something to kill but must stop at the next newline.
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if got := c.Value(); got != "hello world\n" {
		t.Fatalf("expected ctrl+k to kill to end of current line, got %q", got)
	}
}

func TestComposerCtrlBCtrlFMoveByCharacter(t *testing.T) {
	c := New()
	c.SetValue("ab")
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if got := c.Value(); got != "aXb" {
		t.Fatalf("expected ctrl+b to move one char back, got %q", got)
	}
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	if got := c.Value(); got != "aXbY" {
		t.Fatalf("expected ctrl+f to move one char forward, got %q", got)
	}
}

func TestComposerAltBAltFMoveByWord(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if got := c.Value(); got != "hello Xworld" {
		t.Fatalf("expected alt+b to jump to start of current word, got %q", got)
	}
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	if got := c.Value(); got != "hello XworldY" {
		t.Fatalf("expected alt+f to jump past current word, got %q", got)
	}
}

func TestComposerAltDDeletesWordForward(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	c.Update(tea.KeyMsg{Type: tea.KeyCtrlA}) // line start
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d"), Alt: true})
	if got := c.Value(); got != " world" {
		t.Fatalf("expected alt+d to delete word forward, got %q", got)
	}
}

func TestComposerAltBackspaceDeletesWordBackward(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	c.Update(tea.KeyMsg{Type: tea.KeyBackspace, Alt: true})
	if got := c.Value(); got != "hello " {
		t.Fatalf("expected alt+backspace to delete previous word, got %q", got)
	}
}

func TestComposerPgUpPgDownJumpBuffer(t *testing.T) {
	c := New()
	c.SetValue("first\nsecond\nthird")
	c.HandleKey(tea.KeyMsg{Type: tea.KeyPgUp})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	if got := c.Value(); got != "Afirst\nsecond\nthird" {
		t.Fatalf("expected insert at buffer start, got %q", got)
	}
	c.HandleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if got := c.Value(); got != "Afirst\nsecond\nthirdZ" {
		t.Fatalf("expected insert at buffer end, got %q", got)
	}
}

func TestComposerFoldedViewKeepsFullContentHint(t *testing.T) {
	c := New()
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = "line"
	}
	c.SetValue(strings.Join(lines, "\n"))
	view := c.View()
	if !strings.Contains(view, "lines hidden - full content kept") {
		t.Fatalf("expected folded full-content hint, got %q", view)
	}
	if !strings.Contains(view, "25 lines") {
		t.Fatalf("expected line-count hint, got %q", view)
	}
	if got := c.Value(); strings.Count(got, "\n")+1 != 25 {
		t.Fatalf("folded view should not alter buffer, got %d lines", strings.Count(got, "\n")+1)
	}
}

func TestComposerFoldedUpDownJumpVisibleLines(t *testing.T) {
	c := New()
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = "line"
	}
	c.SetValue(strings.Join(lines, "\n"))
	if got := c.textarea.Line(); got != 24 {
		t.Fatalf("expected cursor to start at final line, got %d", got)
	}

	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Fatal("expected folded up to be handled")
	}
	if got := c.textarea.Line(); got != 23 {
		t.Fatalf("expected first folded up to move to visible tail line 23, got %d", got)
	}
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Fatal("expected folded up from tail to be handled")
	}
	if got := c.textarea.Line(); got != 2 {
		t.Fatalf("expected second folded up to jump to visible head line 2, got %d", got)
	}
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyDown}) {
		t.Fatal("expected folded down from head to be handled")
	}
	if got := c.textarea.Line(); got != 23 {
		t.Fatalf("expected folded down to jump to visible tail line 23, got %d", got)
	}
}

func TestComposerPlainUpDownRemainTextareaKeys(t *testing.T) {
	c := New()
	c.SetValue("first\nsecond\nthird")
	if c.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Fatal("did not expect plain up to be handled by folded navigation")
	}
	c.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := c.textarea.Line(); got != 1 {
		t.Fatalf("expected textarea up to move one physical line, got %d", got)
	}
}

func TestComposerTwentyLinesRenderWithoutFoldHint(t *testing.T) {
	c := New()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	c.SetValue(strings.Join(lines, "\n"))
	view := c.View()
	if strings.Contains(view, "lines hidden") {
		t.Fatalf("did not expect folded hint for 20 lines, got %q", view)
	}
	if c.Height() != 20 {
		t.Fatalf("expected textarea height to grow to 20, got %d", c.Height())
	}
}

func TestComposerSoftWrapIncreasesHeight(t *testing.T) {
	c := New()
	c.SetWidth(20)
	c.SetValue("1234567890abcdefghijk")
	if c.Height() <= 1 {
		t.Fatalf("expected soft-wrapped input to grow beyond one row, got %d", c.Height())
	}
}

func TestComposerSoftWrapKeepsFirstVisibleLine(t *testing.T) {
	c := New()
	c.SetWidth(20)
	for _, r := range "1234567890abcdefghijk" {
		c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view := c.View()
	if !strings.Contains(view, "› 1234567890abcdef") {
		t.Fatalf("expected first wrapped row to remain visible, got %q", view)
	}
}

func TestComposerCurrentPrefixedTokenWorksAfterSoftWrap(t *testing.T) {
	c := New()
	c.SetWidth(20)
	prefix := strings.Repeat("a", 30)
	c.SetValue(prefix + " @read")
	got, ok := c.CurrentPrefixedToken('@')
	if !ok || got != "read" {
		t.Fatalf("expected @read token after soft wrap, got %q ok=%v", got, ok)
	}
	if !c.ReplaceCurrentPrefixedToken('@', "README.md ") {
		t.Fatal("expected soft-wrapped @ token replacement to succeed")
	}
	if got := c.Value(); got != prefix+" README.md " {
		t.Fatalf("unexpected replacement result: %q", got)
	}
}

func TestComposerEmptyPlaceholderCollapsesToSingleLine(t *testing.T) {
	c := New()
	c.SetWidth(20)
	c.SetValue("1234567890abcdefghijk")
	if c.Height() <= 1 {
		t.Fatalf("expected wrapped content to grow composer before reset, got %d", c.Height())
	}
	c.Reset()

	view := c.View()

	if !strings.Contains(view, "Type message or") {
		t.Fatalf("expected placeholder text in empty composer view:\n%s", view)
	}
	if strings.Count(view, "\n") > 1 {
		t.Fatalf("expected empty composer to render as a single visible row, got:\n%s", view)
	}
}

func TestComposerEmptyViewOrdersPromptCursorPlaceholder(t *testing.T) {
	c := New()
	c.SetWidth(40)

	plain := xansi.Strip(c.View())
	want := "› █Type message or command"
	if !strings.Contains(plain, want) {
		t.Fatalf("expected empty composer order %q, got %q", want, plain)
	}

	promptIndex := strings.Index(plain, "›")
	cursorIndex := strings.Index(plain, "█")
	placeholderIndex := strings.Index(plain, "Type message or command")
	if promptIndex != 0 {
		t.Fatalf("expected prompt at left edge, got index %d in %q", promptIndex, plain)
	}
	if !(promptIndex < cursorIndex && cursorIndex < placeholderIndex) {
		t.Fatalf("expected prompt, cursor, placeholder order, got %q", plain)
	}
	if plain[promptIndex:cursorIndex] != "› " {
		t.Fatalf("expected tight prompt/cursor spacing, got %q", plain[promptIndex:cursorIndex])
	}
}

func TestComposerEmptyViewUsesPromptAndPlaceholderThemeStyles(t *testing.T) {
	prompt := composerPromptStyle()
	if got := prompt.GetForeground(); got != tuitheme.Default.Accent {
		t.Fatalf("prompt foreground: want %s, got %s", tuitheme.Default.Accent, got)
	}
	if !prompt.GetBold() {
		t.Fatal("expected prompt style to be bold")
	}

	placeholder := composerPlaceholderStyle()
	if got := placeholder.GetForeground(); got != tuitheme.Default.Muted {
		t.Fatalf("placeholder foreground: want %s, got %s", tuitheme.Default.Muted, got)
	}
}

func TestComposerNonEmptyViewOmitsDefaultPlaceholder(t *testing.T) {
	c := New()
	c.SetValue("hello")

	plain := xansi.Strip(c.View())
	if strings.Contains(plain, "Type message or command") {
		t.Fatalf("did not expect placeholder after input, got %q", plain)
	}
	if !strings.Contains(plain, "› hello") {
		t.Fatalf("expected non-empty input to use existing prompt path, got %q", plain)
	}
}

func TestComposerViewHasNoTrailingNewline(t *testing.T) {
	c := New()
	c.SetWidth(24)
	view := c.View()
	if strings.HasSuffix(view, "\n") {
		t.Fatalf("expected composer view without trailing newline, got %q", view)
	}
}

func TestZeroValueComposerViewUsesDefaultState(t *testing.T) {
	var c Composer
	view := c.View()
	if !strings.Contains(view, "Type message or command") {
		t.Fatalf("expected zero-value composer view to render default placeholder, got %q", view)
	}
	if c.width != 0 {
		t.Fatalf("expected value-receiver View not to mutate zero-value composer, width=%d", c.width)
	}
}

func TestComposerViewPadsVisibleWidth(t *testing.T) {
	c := New()
	c.SetWidth(24)
	view := c.View()
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != 24 {
			t.Fatalf("expected padded line width 24, got %d in %q", got, line)
		}
	}
}

func TestComposerLargePasteUsesSingleLinePlaceholder(t *testing.T) {
	c := New()
	paste := strings.Repeat("x\n", largePasteCharThreshold/2+2)

	c.HandlePaste(paste)

	if got := c.Value(); got != paste {
		t.Fatalf("expected expanded paste value, got %q", got)
	}
	view := c.View()
	if !strings.Contains(view, "[Pasted Content") {
		t.Fatalf("expected pasted-content placeholder, got %q", view)
	}
	if strings.Contains(view, "\nx") {
		t.Fatalf("expected large paste to render as one placeholder line, got %q", view)
	}
	if c.Height() != 1 {
		t.Fatalf("expected collapsed large paste height 1, got %d", c.Height())
	}
}

func TestComposerSetValueCollapsesLargeHistoryEntry(t *testing.T) {
	c := New()
	value := strings.Repeat("line\n", largePasteCharThreshold/4+1)

	c.SetValue(value)

	if got := c.Value(); got != value {
		t.Fatalf("expected expanded value after SetValue, got %q", got)
	}
	if got := c.Height(); got != 1 {
		t.Fatalf("expected recalled large value to render as one line, got height %d", got)
	}
}

// visualLineCountUncached recomputes the wrap height without using the
// composer's wrap cache or rawValue cache. Test-only oracle used to
// verify the cached path produces the same answer.
func visualLineCountUncached(c Composer) int {
	width := c.textarea.Width()
	lines := strings.Split(c.textarea.Value(), "\n")
	if c.textarea.Value() == "" {
		lines = []string{""}
	}
	if width <= 0 {
		return len(lines)
	}
	total := 0
	for _, line := range lines {
		total += wrappedLineCount([]rune(line), width)
	}
	return total
}

func TestVisualLineCountCacheMatchesUncached(t *testing.T) {
	c := New()
	c.SetWidth(120)

	steps := []func(){
		func() { c.HandlePaste("hello world") },
		func() { c.HandlePaste(" goodbye") },
		func() { c.InsertNewline() },
		func() { c.HandlePaste(strings.Repeat("lorem ipsum dolor sit amet ", 10)) },
		func() { c.SetWidth(40) },
		func() { c.InsertNewline() },
		func() { c.HandlePaste("中文 line with multibyte runes") },
		func() { c.SetWidth(80) },
		func() { c.Reset() },
		func() { c.HandlePaste("fresh start\nline two\nline three") },
	}
	for i, step := range steps {
		step()
		got := c.visualLineCount()
		want := visualLineCountUncached(c)
		if got != want {
			t.Fatalf("step %d: cached visualLineCount=%d, uncached=%d, value=%q",
				i, got, want, c.Value())
		}
	}
}

func TestVisualLineCountCacheBoundsMemory(t *testing.T) {
	c := New()
	c.SetWidth(80)
	// Pump enough unique lines through the composer to force the wrapCache
	// cap to kick in. Each Reset followed by a fresh paste introduces a
	// new line content, ensuring the cache grows.
	for i := 0; i < wrapCacheMaxEntries*2; i++ {
		c.Reset()
		c.HandlePaste(strings.Repeat("x", i%600))
		c.InsertNewline()
		_ = c.visualLineCount()
	}
	if len(c.wrapCache) > wrapCacheMaxEntries {
		t.Fatalf("wrapCache exceeded cap: got %d entries, want <= %d",
			len(c.wrapCache), wrapCacheMaxEntries)
	}
}

func TestRawCacheConsistentAfterMutations(t *testing.T) {
	c := New()
	c.SetWidth(80)

	mutations := []func(){
		func() { c.HandlePaste("alpha") },
		func() { c.InsertNewline() },
		func() { c.HandlePaste("beta gamma") },
		func() { c.SetValue("entire replacement\nnew content") },
		func() { c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}) },
		func() { c.Reset() },
	}
	for i, m := range mutations {
		m()
		// rawValue (possibly cached) must match the textarea's live state.
		if got, want := c.rawValue(), c.textarea.Value(); got != want {
			t.Fatalf("mutation %d: rawValue=%q, textarea.Value()=%q", i, got, want)
		}
	}
}

// Regression: HandlePaste of a >largePasteCharThreshold-rune paste must
// keep the original text accessible via Value(). The pending-paste
// placeholder is inserted into the textarea, but a stale rawCache used to
// hide it from prunePendingPastes(), which then dropped the entry and
// caused Value() to return the placeholder literal instead of the
// expanded original.
func TestHandlePasteLargeContentPreservedAcrossRawCache(t *testing.T) {
	c := New()
	c.SetWidth(80)
	original := strings.Repeat("lorem ipsum dolor sit amet ", 50) // ~1350 chars
	c.HandlePaste(original)
	if got := c.Value(); got != original {
		t.Fatalf("Value() after large paste = %q (len=%d), want original (len=%d)",
			truncateForLog(got), len(got), len(original))
	}
}

func truncateForLog(s string) string {
	if len(s) > 80 {
		return s[:40] + "..." + s[len(s)-40:]
	}
	return s
}

// --- Selection tests ---

func TestSelectionShiftLeftRightSelectsCharacters(t *testing.T) {
	c := New()
	c.SetValue("hello")
	// Place cursor at end (after "hello")
	// Shift+Right at end should move cursor right to end — no-op
	// Move cursor to position between 'l' and 'o' (index 3)
	// We need a helper or we start from the end and shift+left
	// Simpler: start at end, shift+left 3 times to select "llo"

	// Start: cursor at end of "hello" (offset 5)
	// Shift+left once: anchor=5, cursor=4 → selected "o"
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft}) {
		t.Fatal("expected shift+left to be handled")
	}
	if !c.hasSelection() {
		t.Fatal("expected selection after shift+left")
	}
	start, end := c.selectionRange()
	if start != 4 || end != 5 {
		t.Fatalf("expected selection [4,5), got [%d,%d)", start, end)
	}
	if got := c.selectedText(); got != "o" {
		t.Fatalf("expected selected text 'o', got %q", got)
	}

	// Shift+left again: anchor=5, cursor=3 → selected "lo"
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft}) {
		t.Fatal("expected shift+left to be handled")
	}
	start, end = c.selectionRange()
	if start != 3 || end != 5 {
		t.Fatalf("expected selection [3,5), got [%d,%d)", start, end)
	}
	if got := c.selectedText(); got != "lo" {
		t.Fatalf("expected selected text 'lo', got %q", got)
	}
}

func TestSelectionShiftRightSelectsCharacters(t *testing.T) {
	c := New()
	c.SetValue("ab")
	// Cursor is at end after SetValue. Navigate to start via textarea
	// by sending left key through Update (since HandleKey returns false for it).
	c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if c.hasSelection() {
		t.Fatal("expected no selection after non-shift left")
	}

	// shift+right: anchor=0, cursor=1 → selected "a"
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftRight}) {
		t.Fatal("expected shift+right to be handled")
	}
	if got := c.selectedText(); got != "a" {
		t.Fatalf("expected selected text 'a', got %q", got)
	}

	// shift+right: anchor=0, cursor=2 → selected "ab"
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftRight}) {
		t.Fatal("expected shift+right to be handled")
	}
	if got := c.selectedText(); got != "ab" {
		t.Fatalf("expected selected text 'ab', got %q", got)
	}
}

func TestSelectionBackspaceDeletesSelection(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	// Move cursor to end, then shift+left 5 times to select "world"
	for i := 0; i < 5; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	if got := c.selectedText(); got != "world" {
		t.Fatalf("expected 'world' selected, got %q", got)
	}

	// Backspace should delete selection
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace}) {
		t.Fatal("expected backspace to be handled")
	}
	if got := c.Value(); got != "hello " {
		t.Fatalf("expected 'hello ' after backspace, got %q", got)
	}
	if c.hasSelection() {
		t.Fatal("expected selection to be cleared after backspace")
	}
}

func TestSelectionDeleteDeletesSelection(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	// Navigate cursor to start using textarea Update
	for range "hello world" {
		c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	// shift+right 5 times to select "hello"
	for i := 0; i < 5; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftRight})
	}
	if got := c.selectedText(); got != "hello" {
		t.Fatalf("expected 'hello' selected, got %q", got)
	}
	// Delete via backspace
	c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := c.Value(); got != " world" {
		t.Fatalf("expected ' world' after delete, got %q", got)
	}
}

func TestSelectionDeleteWithDeleteKey(t *testing.T) {
	c := New()
	c.SetValue("abcdef")
	// Cursor at end. Shift+left 3 times to select "def"
	for i := 0; i < 3; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	if got := c.selectedText(); got != "def" {
		t.Fatalf("expected 'def' selected, got %q", got)
	}

	// Delete key should remove selection
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyDelete}) {
		t.Fatal("expected delete to be handled")
	}
	if got := c.Value(); got != "abc" {
		t.Fatalf("expected 'abc' after delete, got %q", got)
	}
}

func TestSelectionNonShiftMovementClearsSelection(t *testing.T) {
	c := New()
	c.SetValue("hello")
	// Select "llo" (shift+left 3 times)
	for i := 0; i < 3; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	if !c.hasSelection() {
		t.Fatal("expected selection to exist")
	}

	// Left (non-shift) should clear selection
	c.HandleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if c.hasSelection() {
		t.Fatal("expected selection cleared after non-shift left")
	}
}

func TestSelectionDeleteThenClear(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	// Select "world" (shift+left 5 times from end)
	for i := 0; i < 5; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}

	// Delete selection via backspace
	c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := c.Value(); got != "hello " {
		t.Fatalf("expected 'hello ' after delete, got %q", got)
	}

	// Should be able to type again
	if c.hasSelection() {
		t.Fatal("expected selection cleared after delete")
	}
}

func TestSelectionRangeNormalized(t *testing.T) {
	c := New()
	c.SetValue("abcdef")
	// Cursor at end. Shift+left 2 times → anchor=6, cursor=4 → selected "ef"
	for i := 0; i < 2; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	start, end := c.selectionRange()
	if start != 4 || end != 6 {
		t.Fatalf("expected [4,6), got [%d,%d)", start, end)
	}

	// Test normalization: anchor > cursor should still give [4,6)
	c2 := New()
	c2.SetValue("abcdef")
	// Navigate to position 3 via textarea Update
	c2.Update(tea.KeyMsg{Type: tea.KeyLeft})
	c2.Update(tea.KeyMsg{Type: tea.KeyLeft})
	c2.Update(tea.KeyMsg{Type: tea.KeyLeft})

	// shift+right: anchor=3, cursor=4 → selected "d"
	c2.HandleKey(tea.KeyMsg{Type: tea.KeyShiftRight})
	start, end = c2.selectionRange()
	if start != 3 || end != 4 {
		t.Fatalf("expected [3,4), got [%d,%d)", start, end)
	}
}

func TestSelectionAtStartOfText(t *testing.T) {
	c := New()
	c.SetValue("hello")
	// Go to start via textarea Update for left key
	for c.cursorRuneOffset() > 0 {
		c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	if c.cursorRuneOffset() != 0 {
		t.Fatalf("expected cursor at offset 0, got %d", c.cursorRuneOffset())
	}

	// shift+left at start should be a no-op (no selection created)
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft}) {
		t.Fatal("expected shift+left at start to be handled")
	}
	if c.hasSelection() {
		t.Fatal("expected no selection from shift+left at start")
	}
}

func TestSelectionCollapseDoesNotBlockBackspace(t *testing.T) {
	c := New()
	c.SetValue("hello")

	c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	if !c.hasSelection() {
		t.Fatal("expected selection after shift+left")
	}
	c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftRight})
	if c.hasSelection() {
		t.Fatal("expected collapsed selection to clear")
	}

	if c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace}) {
		t.Fatal("expected backspace to fall through after collapsed selection")
	}
	c.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := c.Value(); got != "hell" {
		t.Fatalf("expected normal backspace after collapsed selection, got %q", got)
	}
}

func TestSelectionShiftHomeAtLineStartDoesNotBlockDelete(t *testing.T) {
	c := New()
	c.SetValue("hello")
	for c.cursorRuneOffset() > 0 {
		c.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}

	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftHome}) {
		t.Fatal("expected shift+home at start to be handled")
	}
	if c.hasSelection() {
		t.Fatal("expected no selection from shift+home at line start")
	}

	if c.HandleKey(tea.KeyMsg{Type: tea.KeyDelete}) {
		t.Fatal("expected delete to fall through after collapsed selection")
	}
	c.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := c.Value(); got != "ello" {
		t.Fatalf("expected normal delete after collapsed selection, got %q", got)
	}
}

func TestSelectionDeleteClampsStaleAnchor(t *testing.T) {
	c := New()
	c.SetValue("ab")
	c.selectionRuneOffset = 3

	if c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace}) {
		t.Fatal("expected stale collapsed selection to fall through")
	}
	c.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := c.Value(); got != "a" {
		t.Fatalf("expected normal backspace with stale anchor, got %q", got)
	}
}

func TestPrintableCharDeletesSelection(t *testing.T) {
	c := New()
	c.SetValue("hello")
	// Select "hello" (shift+left 5 times from end)
	for i := 0; i < 5; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	if got := c.selectedText(); got != "hello" {
		t.Fatalf("expected 'hello' selected, got %q", got)
	}

	// In the real app, printable chars flow through HandleKey first
	// (to delete selection), then Update (to insert the typed char).
	// Simulate both steps.
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")}
	if c.HandleKey(msg) {
		t.Fatal("expected printable char not to claim handling")
	}
	c.Update(msg)
	if got := c.Value(); got != "X" {
		t.Fatalf("expected 'X' after typing, got %q", got)
	}
	if c.hasSelection() {
		t.Fatal("expected selection cleared after typing")
	}
}

func TestSelectionDeletePreservesUntouchedLargePaste(t *testing.T) {
	c := New()
	large := strings.Repeat("x", largePasteCharThreshold+1)
	c.HandlePaste(large)
	c.HandlePaste(" suffix")

	for i := 0; i < len(" suffix"); i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	if got := c.selectedText(); got != " suffix" {
		t.Fatalf("expected suffix selected, got %q", got)
	}
	c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})

	if got := c.Value(); got != large {
		t.Fatalf("expected large paste expansion preserved, got len=%d want=%d", len(got), len(large))
	}
	if strings.Contains(c.Value(), "[Pasted Content") {
		t.Fatalf("expected submitted value to expand placeholder, got %q", truncateForLog(c.Value()))
	}
}

func TestSelectionShiftHomeEnd(t *testing.T) {
	c := New()
	c.SetValue("hello world")
	// Cursor at end. Shift+home should select all
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftHome}) {
		t.Fatal("expected shift+home to be handled")
	}
	if got := c.selectedText(); got != "hello world" {
		t.Fatalf("expected 'hello world' selected, got %q", got)
	}

	// Backspace deletes all
	c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := c.Value(); got != "" {
		t.Fatalf("expected empty after select-all+backspace, got %q", got)
	}
}

func TestSelectionMultiline(t *testing.T) {
	c := New()
	c.SetValue("hello\nworld")
	// Cursor at end of "world"
	// shift+home from end of line 2 = cursor to start of line 2, selects "world"
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftHome}) {
		t.Fatal("expected shift+home to be handled")
	}
	start, end := c.selectionRange()
	if start > end {
		start, end = end, start
	}
	if wantStart := len("hello\n"); start != wantStart {
		t.Fatalf("expected selection start %d, got %d", wantStart, start)
	}
	if wantEnd := len("hello\nworld"); end != wantEnd {
		t.Fatalf("expected selection end %d, got %d", wantEnd, end)
	}

	// shift+up: anchor stays at end of text (offset 11),
	// cursor moves to start of line 0 → selection = [0, 11)
	if !c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftUp}) {
		t.Fatal("expected shift+up to be handled")
	}
	if !c.hasSelection() {
		t.Fatal("expected selection after shift+up")
	}
	selStart, selEnd := c.selectionRange()
	if selStart != 0 || selEnd != 11 {
		t.Fatalf("expected [0,11), got [%d,%d)", selStart, selEnd)
	}
}

func TestSelectionCtrlShiftLeftRightSelectsWords(t *testing.T) {
	t.Run("select word left from middle selects to word start", func(t *testing.T) {
		c := New()
		c.SetValue("hello world foo")
		// Navigate cursor to middle of "world" (offset 8, the 'r')
		for c.cursorRuneOffset() > 8 {
			c.Update(tea.KeyMsg{Type: tea.KeyLeft})
		}
		// ctrl+shift+left from middle: selects from cursor back to word start
		if !c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}) {
			t.Fatal("expected ctrl+shift+left to be handled")
		}
		if got := c.selectedText(); got != "wo" {
			t.Fatalf("expected 'wo' selected (cursor at 'r', back to word start), got %q", got)
		}
	})

	t.Run("select whole word left from word end", func(t *testing.T) {
		c := New()
		c.SetValue("hello world foo")
		// Navigate cursor to end of "world" (offset 11, after the 'd')
		for c.cursorRuneOffset() > 11 {
			c.Update(tea.KeyMsg{Type: tea.KeyLeft})
		}
		if !c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}) {
			t.Fatal("expected ctrl+shift+left to be handled")
		}
		if got := c.selectedText(); got != "world" {
			t.Fatalf("expected 'world' selected, got %q", got)
		}
	})

	t.Run("select word right from start of word", func(t *testing.T) {
		c := New()
		c.SetValue("hello world foo")
		// Navigate cursor to start of "world" (offset 6)
		for c.cursorRuneOffset() > 6 {
			c.Update(tea.KeyMsg{Type: tea.KeyLeft})
		}
		if !c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlShiftRight}) {
			t.Fatal("expected ctrl+shift+right to be handled")
		}
		if got := c.selectedText(); got != "world" {
			t.Fatalf("expected 'world' selected, got %q", got)
		}
	})

	t.Run("ctrl+shift+left at start does not select", func(t *testing.T) {
		c := New()
		c.SetValue("hello")
		for c.cursorRuneOffset() > 0 {
			c.Update(tea.KeyMsg{Type: tea.KeyLeft})
		}
		c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlShiftLeft})
		if c.hasSelection() {
			t.Fatal("expected no selection at start")
		}
	})

	t.Run("ctrl+shift+right at end does not select", func(t *testing.T) {
		c := New()
		c.SetValue("hello")
		// Already at end
		c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlShiftRight})
		if c.hasSelection() {
			t.Fatal("expected no selection at end")
		}
	})

	t.Run("delete word selection with backspace", func(t *testing.T) {
		c := New()
		c.SetValue("hello world foo")
		// Navigate to end
		// ctrl+shift+left from end should select "foo"
		if !c.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}) {
			t.Fatal("expected ctrl+shift+left to be handled")
		}
		if got := c.selectedText(); got != "foo" {
			t.Fatalf("expected 'foo' selected, got %q", got)
		}
		c.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
		if got := c.Value(); got != "hello world " {
			t.Fatalf("expected 'hello world ' after delete, got %q", got)
		}
	})
}

func TestSelectionViewContainsSelectionMarker(t *testing.T) {
	c := New()
	c.SetValue("hello")
	// Select "ell" (shift+left 3 times from end)
	for i := 0; i < 3; i++ {
		c.HandleKey(tea.KeyMsg{Type: tea.KeyShiftLeft})
	}
	view := c.View()
	// View should contain the input text (cursor char █ splits it)
	if !strings.Contains(view, "he") || !strings.Contains(view, "llo") || view == "" {
		t.Fatalf("view should contain text, got %q", view)
	}
	// Should contain the cursor marker
	if !strings.Contains(view, "█") {
		t.Fatalf("view should contain cursor, got %q", view)
	}
}
