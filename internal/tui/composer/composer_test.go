package composer

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
