package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/usewhale/whale/internal/app"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/skills"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1700000000, 0)} }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func writeFileSuggestionFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func runFileSuggestionSearchForTest(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected file suggestion search command")
	}
	msg := cmd()
	updated, _ := updateTestModel(t, m, msg)
	return updated
}

func newModelWithDispatchSpy() (model, *[]service.Intent) {
	m := newModel(nil, "", "", "")
	intents := []service.Intent{}
	m.dispatch = func(in service.Intent) {
		intents = append(intents, in)
	}
	return m, &intents
}

func updateTestModel(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	updated, ok := next.(model)
	if !ok {
		t.Fatalf("expected model update, got %T", next)
	}
	return updated, cmd
}

func flushWindowsPasteBurstForTest(t *testing.T, m model) model {
	t.Helper()
	if !m.hasWindowsPasteBuffer() {
		t.Fatal("expected active Windows paste burst")
	}
	m.windowsPaste.activeUntil = time.Now().Add(-time.Millisecond)
	next, _ := updateTestModel(t, m, windowsPasteBurstFlushMsg{id: m.windowsPaste.burstID})
	return next
}

func TestHistoryNavigationContinuesAcrossSlashCommandEntry(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.promptHistory = []string{"a", "b", "c", "/status"}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "/status" {
		t.Fatalf("expected first Up to recall slash command, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected recalled slash command not to show suggestions during history navigation")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "c" {
		t.Fatalf("expected second Up to continue history navigation, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected non-slash history entry to clear slash suggestions")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.input.Value(); got != "/status" {
		t.Fatalf("expected Down to return to slash history entry, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected slash suggestions to stay hidden after returning to slash history entry")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Down from newest history entry to restore draft, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatal("expected empty draft to clear slash suggestions")
	}
}

func TestHistoryNavigationSuppressesSkillSuggestions(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{{Name: "code-review"}}
	m.promptHistory = []string{"a", "$code-review"}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "$code-review" {
		t.Fatalf("expected first Up to recall skill trigger, got %q", got)
	}
	if m.hasSkillSuggestions() {
		t.Fatal("expected recalled skill trigger not to show suggestions during history navigation")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "a" {
		t.Fatalf("expected second Up to continue history navigation, got %q", got)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.input.Value(); got != "$code-review" {
		t.Fatalf("expected Down to return to skill history entry, got %q", got)
	}
	if m.hasSkillSuggestions() {
		t.Fatal("expected skill suggestions to stay hidden after returning to skill history entry")
	}
}

func typeRunesForTest(t *testing.T, m model, value string) model {
	t.Helper()
	for _, r := range value {
		m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if m.hasWindowsPasteBuffer() {
			m = flushWindowsPasteBurstForTest(t, m)
		}
	}
	return m
}

func TestWindowsUnbracketedPasteFallbackKeepsLinesInOnePrompt(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first enter to be deferred")
	}
	if len(*intents) != 0 {
		t.Fatalf("first pasted newline submitted early: %+v", *intents)
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.input.Value(); got != "" {
		t.Fatalf("paste burst should stay buffered before flush, input=%q", got)
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("buffer after second pasted line = %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after paste detection: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.windowsPasteBuffer(); got != "line one\nline two\n" {
		t.Fatalf("trailing pasted newline buffer = %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("trailing pasted newline submitted early: %+v", *intents)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != "line one\nline two\n" {
		t.Fatalf("input after paste flush = %q", got)
	}
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final enter after fallback paste to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	if len(*intents) != 0 {
		t.Fatalf("final enter submitted before defer elapsed: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit after paste quiet period, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "line one\nline two" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsUnbracketedLargeMultilinePasteBurstUsesPlaceholder(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	firstLine := strings.Repeat("x", 600)
	secondLine := strings.Repeat("y", 500)
	large := firstLine + "\n" + secondLine

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(firstLine)})
	if cmd == nil {
		t.Fatal("expected multi-rune pasted text to schedule a flush")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("first pasted line should stay buffered before flush, got input %q", got)
	}
	if got := m.windowsPasteBuffer(); got != firstLine {
		t.Fatalf("first pasted line should enter paste buffer, got %q", got)
	}
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected pasted newline to defer submit")
	}
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secondLine)})
	if cmd == nil {
		t.Fatal("expected large paste burst to schedule a flush")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("large paste should stay buffered before flush, input=%q", got)
	}
	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != large {
		t.Fatalf("expanded large paste value length = %d, want %d", len([]rune(got)), len([]rune(large)))
	}
	if view := m.input.View(); !strings.Contains(view, "[Pasted Content 1101 chars]") {
		t.Fatalf("expected large paste placeholder in composer view, got %q", view)
	}
}

func TestWindowsPasteBufferSurvivesUnhandledMessagesBeforeFlush(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.setWindowsPasteBuffer("line one\nline two")
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)
	m.windowsPaste.burstID = 7

	m, _ = updateTestModel(t, m, struct{}{})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("unhandled message should not clear active paste buffer, got %q", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("unhandled message should not flush paste buffer early, got input %q", got)
	}

	m, _ = updateTestModel(t, m, windowsPasteBurstFlushMsg{id: 7})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("early flush timer should keep paste buffer until quiet window, got %q", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("early flush timer should not flush paste buffer, got input %q", got)
	}

	m.windowsPaste.activeUntil = time.Now().Add(-time.Millisecond)
	m, _ = updateTestModel(t, m, windowsPasteBurstFlushMsg{id: 7})
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("paste buffer should flush after quiet window, got %q", got)
	}
}

func TestWindowsPasteBurstKeepsSingleDebounceTimer(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one\n")})
	if cmd == nil {
		t.Fatal("expected first paste chunk to schedule debounce timer")
	}
	firstID := m.windowsPaste.burstID
	firstDeadline := m.windowsPaste.activeUntil
	if !m.windowsPaste.burstFlushScheduled {
		t.Fatal("expected paste flush timer to be marked scheduled")
	}

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if cmd != nil {
		t.Fatal("expected pasted rune to reuse existing debounce timer")
	}
	if m.windowsPaste.burstID != firstID {
		t.Fatalf("expected debounce id to stay %d, got %d", firstID, m.windowsPaste.burstID)
	}
	if m.windowsPaste.activeUntil.Before(firstDeadline) {
		t.Fatalf("expected paste deadline not to move backward, old=%v new=%v", firstDeadline, m.windowsPaste.activeUntil)
	}

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ine two")})
	if cmd != nil {
		t.Fatal("expected pasted continuation to reuse existing debounce timer")
	}
	if m.windowsPaste.burstID != firstID {
		t.Fatalf("expected debounce id to remain %d after continuation, got %d", firstID, m.windowsPaste.burstID)
	}

	m.windowsPaste.activeUntil = time.Now().Add(-time.Millisecond)
	m, _ = updateTestModel(t, m, windowsPasteBurstFlushMsg{id: firstID})
	if m.windowsPaste.burstFlushScheduled {
		t.Fatal("expected debounce timer mark to clear after flush")
	}
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("expected paste to flush after quiet window, got %q", got)
	}
}

func TestWindowsPasteFallbackFastEnterSubmitsTypedCharacter(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("hell")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("expected typed character to render immediately, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("ordinary typed character should not enter paste buffer, got %q", got)
	}

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected fast enter after typed character to defer submit")
	}
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("fast enter should keep typed prompt intact, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("fast enter should not create paste buffer, got %q", got)
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "hello" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsPasteFallbackEnterThenHumanTypingSubmitsAndKeepsRune(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("hello")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter to defer submit")
	}
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected pendingEnter to be armed")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !m.windowsPaste.pendingEnter {
		t.Fatal("first typed rune should wait briefly before resolving the deferred enter")
	}
	if got := m.windowsPaste.pendingEnterTail; got != "x" {
		t.Fatalf("expected typed rune to wait in pending tail, got %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("pending tail should not submit until the short continuation window closes, got %+v", *intents)
	}

	tailID := m.windowsPaste.pendingEnterTailID
	m, _ = updateTestModel(t, m, windowsPendingEnterTailMsg{id: tailID})

	if m.windowsPaste.pendingEnter {
		t.Fatal("typing without paste continuation should resolve the deferred enter")
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("typed rune should not enter paste buffer, got %q", got)
	}
	if got := m.input.Value(); got != "x" {
		t.Fatalf("expected typed rune to start a fresh composer, got %q", got)
	}
	if len(*intents) != 1 {
		t.Fatalf("expected the deferred prompt to submit once, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "hello" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsPasteFallbackEnterThenRapidRunesBecomeContinuation(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("hello")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter to defer submit")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if got := m.windowsPasteBuffer(); got != "hello\nwo" {
		t.Fatalf("rapid runes should upgrade to paste continuation, got buffer %q", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("paste continuation should move composer text to buffer, got input %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter should not submit after paste continuation, got %+v", *intents)
	}
}

func TestWindowsPasteFallbackLFPastePreservesTailBetweenNewlines(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	if got := m.windowsPaste.pendingEnterTail; got != "" {
		t.Fatalf("expected tail to be folded into burst before second newline, got %q", got)
	}
	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != "a\nb\nc" {
		t.Fatalf("expected LF-only paste to preserve single-char line, got %q", got)
	}
}

func TestWindowsPasteFallbackDeferredSubmitPreservesTailRune(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("hello")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter to defer submit")
	}
	submitID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got := m.windowsPaste.pendingEnterTail; got != "x" {
		t.Fatalf("expected tail to hold rune, got %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})

	if m.windowsPaste.pendingEnter {
		t.Fatal("expected deferred submit tick to resolve pending enter")
	}
	if got := m.input.Value(); got != "x" {
		t.Fatalf("expected held tail rune to land in fresh composer when submit tick wins the race, got %q", got)
	}
	if len(*intents) != 1 || (*intents)[0].Input != "hello" {
		t.Fatalf("expected single submit of original prompt, got %+v", *intents)
	}
}

// Issue #61(1) review: non-rune editing keys (Enter, Tab, arrows, …)
// must segment paste-cadence detection. Otherwise "ab" + Enter + "c"
// arriving within 60ms of each other would treat "c" as the third paste
// chunk and buffer "ab\nc" instead of submitting "ab" and starting a
// fresh prompt with "c".
func TestWindowsPasteFallbackEnterSegmentsPasteCadence(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	clock := newFakeClock()
	m.windowsPaste.nowFunc = clock.now

	clock.advance(50 * time.Millisecond)
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	clock.advance(50 * time.Millisecond)
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	// Enter — defers submit on windows fallback. classifier must reset.
	clock.advance(30 * time.Millisecond)
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected enter to enter deferred-submit state")
	}

	// Quick keystroke after Enter MUST stay typed, not escalate.
	clock.advance(30 * time.Millisecond)
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.hasWindowsPasteBuffer() {
		t.Fatalf("post-Enter keystroke escalated into burst buffer: %q", m.windowsPasteBuffer())
	}

	// Resolve the deferred Enter as a real submit; "ab" should ship as
	// the prompt, "c" stays in the composer for the next turn.
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 || (*intents)[0].Input != "ab" {
		t.Fatalf("expected single submit of %q, got %+v", "ab", *intents)
	}
	if got := m.input.Value(); got != "c" {
		t.Fatalf("post-submit composer should hold %q, got %q", "c", got)
	}
}

// Issue #61(1): a paste delivered as many "typed-looking" single-rune
// chunks (no whitespace, no newlines, each < 16 runes) must be caught by
// the cadence escalator and routed through the paste burst path instead
// of being inserted into the textarea one rune at a time.
func TestWindowsPasteFallbackEscalatesFastSingleRuneStream(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	clock := newFakeClock()
	m.windowsPaste.nowFunc = clock.now

	const streamed = "abcdefghijklmnop"
	for _, r := range streamed {
		clock.advance(2 * time.Millisecond) // sub-typing cadence
		m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !m.hasWindowsPasteBuffer() {
		t.Fatalf("expected fast single-rune stream to populate paste buffer; composer=%q buffer=%q",
			m.input.Value(), m.windowsPasteBuffer())
	}
	combined := m.input.Value() + m.windowsPasteBuffer()
	if combined != streamed {
		t.Fatalf("expected combined composer+buffer to equal stream, got %q", combined)
	}
	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != streamed {
		t.Fatalf("after flush composer should hold full stream, got %q", got)
	}
}

// Issue #61(1) review: when cadence-escalation triggers mid-composer
// (cursor not at end), the early typed chunks have already landed at the
// cursor; the rest must flow through HandlePaste at that same cursor
// position so character order is preserved. Pasting "XYZ" into "a|c"
// must yield "aXYZc", not "aXYcZ".
func TestWindowsPasteFallbackEscalatedPastePreservesCursorPosition(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	clock := newFakeClock()
	m.windowsPaste.nowFunc = clock.now

	// Set up "a|c" — composer "ac" with cursor between the two runes.
	m.input.SetValue("ac")
	m.input.SetCursorEnd()
	// Move cursor one step left so it sits between 'a' and 'c'.
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyLeft})

	// Now simulate a fast paste of "XYZ" arriving as 3 single-rune chunks.
	for _, r := range "XYZ" {
		clock.advance(2 * time.Millisecond)
		m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.hasWindowsPasteBuffer() {
		m = flushWindowsPasteBurstForTest(t, m)
	}
	if got := m.input.Value(); got != "aXYZc" {
		t.Fatalf("mid-cursor escalated paste corrupted order: got %q, want %q", got, "aXYZc")
	}
}

func TestWindowsPasteFallbackFastEnterSubmitsShortBurst(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	clock := newFakeClock()
	m.windowsPaste.nowFunc = clock.now

	clock.advance(100 * time.Millisecond)
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	clock.advance(100 * time.Millisecond)
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if got := m.input.Value(); got != "hi" {
		t.Fatalf("expected short typed burst to render immediately, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("ordinary typed burst should not enter paste buffer, got %q", got)
	}

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected fast enter after short burst to defer submit")
	}
	if got := m.input.Value(); got != "hi" {
		t.Fatalf("expected fast enter to keep prompt intact, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("fast enter should not create paste buffer, got %q", got)
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "hi" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsPasteFallbackFastEnterSubmitsLongTypedPrompt(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	clock := newFakeClock()
	m.windowsPaste.nowFunc = clock.now
	line := "Reply later"

	for _, r := range line {
		clock.advance(100 * time.Millisecond)
		m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.input.Value(); got != line {
		t.Fatalf("expected typed prompt to render immediately, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("typed prompt should not enter paste buffer, got %q", got)
	}

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter after typed prompt to defer submit")
	}
	if got := m.input.Value(); got != line {
		t.Fatalf("enter should preserve typed prompt before deferred submit, got %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("deferred enter submitted early: %+v", *intents)
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != line {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsPasteFallbackSingleLinePasteThenEnterSubmits(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix bug")})
	if cmd == nil {
		t.Fatal("expected single-line pasted burst to schedule flush")
	}
	if got := m.windowsPasteBuffer(); got != "fix bug" {
		t.Fatalf("expected single-line paste to stay buffered, got %q", got)
	}

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter after single-line paste to defer submit")
	}
	if got := m.input.Value(); got != "fix bug" {
		t.Fatalf("expected single-line paste to flush before deferred submit, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("expected paste buffer to flush before deferred submit, got %q", got)
	}
	submitID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected single-line paste to submit after deferred enter, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "fix bug" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsPasteFallbackMultiRunePastedLineUpgradesDeferredEnterOnContinuation(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	if cmd == nil {
		t.Fatal("expected first pasted line to schedule burst flush")
	}
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected pasted line enter to defer submit")
	}
	if got := m.input.Value(); got != "line one" {
		t.Fatalf("expected first pasted line to flush while enter is deferred, got input %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("expected buffer to flush while enter is deferred, got %q", got)
	}
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected pasted line enter to arm deferred submit")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected continued paste buffer, got %q", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected continued paste to move flushed prefix back to buffer, got input %q", got)
	}
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after paste continuation: %+v", *intents)
	}
	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("expected combined paste after flush, got %q", got)
	}
}

func TestWindowsUnbracketedPasteFallbackNormalizesCRLF(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("foo")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected CR half of pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if got := m.windowsPasteBuffer(); got != "foo\n" {
		t.Fatalf("buffer after CRLF pasted newline = %q", got)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bar")})
	if got := m.windowsPasteBuffer(); got != "foo\nbar" {
		t.Fatalf("buffer after CRLF pasted text = %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after CRLF paste detection: %+v", *intents)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != "foo\nbar" {
		t.Fatalf("input after CRLF paste flush = %q", got)
	}
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final submit enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit after CRLF paste, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "foo\nbar" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsUnbracketedPasteFallbackNormalizesCRLFBlankLine(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("a")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first CR half of pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if got := m.windowsPasteBuffer(); got != "a\n\nb" {
		t.Fatalf("buffer after CRLF blank line paste = %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after CRLF blank-line paste detection: %+v", *intents)
	}
	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != "a\n\nb" {
		t.Fatalf("input after CRLF blank-line paste flush = %q", got)
	}
}

func TestWindowsUnbracketedPasteFallbackAllowsSubsequentPasteBlocks(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("first")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first paste newline to be deferred")
	}
	firstDeferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("block")})
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: firstDeferredID})
	if len(*intents) != 0 {
		t.Fatalf("first paste block submitted early: %+v", *intents)
	}
	m = flushWindowsPasteBurstForTest(t, m)

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected second paste newline to be deferred")
	}
	secondDeferredID := m.windowsPaste.pendingEnterID
	if len(*intents) != 0 {
		t.Fatalf("second paste newline submitted early: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	if got := m.windowsPasteBuffer(); got != "first\nblock\nsecond" {
		t.Fatalf("buffer after subsequent pasted block = %q", got)
	}
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: secondDeferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale second deferred enter submitted after paste detection: %+v", *intents)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final submit enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit after both paste blocks, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "first\nblock\nsecond" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsUnbracketedPasteFallbackPreservesTabIndentAsSpaces(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("foo")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.windowsPasteBuffer(); got != "foo\n    " {
		t.Fatalf("buffer after pasted tab indentation = %q", got)
	}
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after pasted tab: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bar")})
	if got := m.windowsPasteBuffer(); got != "foo\n    bar" {
		t.Fatalf("buffer after tab-indented pasted text = %q", got)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final submit enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit after tab-indented paste, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "foo\n    bar" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsUnbracketedPasteFallbackPreservesTabBeforeFirstNewline(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != "    " {
		t.Fatalf("input after leading pasted tab = %q", got)
	}
	if !m.windowsPaste.activeUntil.IsZero() {
		t.Fatalf("leading pasted tab should not start quiet window, activeUntil=%v", m.windowsPaste.activeUntil)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("foo")})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.input.Value(); got != "    foo" {
		t.Fatalf("input after tab-indented first pasted line = %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("buffer after tab-indented first pasted line = %q", got)
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bar")})
	if got := m.windowsPasteBuffer(); got != "    foo\nbar" {
		t.Fatalf("buffer after tab-indented first pasted text = %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after pasted leading tab: %+v", *intents)
	}
	m = flushWindowsPasteBurstForTest(t, m)
	if got := m.input.Value(); got != "    foo\nbar" {
		t.Fatalf("input after tab-indented first pasted line flush = %q", got)
	}
}

func TestWindowsUnbracketedPasteFallbackPreservesSingleLineTabBeforeSubmit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("a")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if got := m.input.Value(); got != "a    b" {
		t.Fatalf("input after single-line pasted tab = %q", got)
	}
	if !m.windowsPaste.activeUntil.IsZero() {
		t.Fatalf("single-line pasted tab should not start quiet window, activeUntil=%v", m.windowsPaste.activeUntil)
	}

	m, cmd := updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter after single-line pasted tab to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	if got := m.input.Value(); got != "a    b" {
		t.Fatalf("enter after single-line pasted tab should not insert newline, got %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit after single-line pasted tab, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "a    b" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsUnbracketedPasteFallbackPreservesBlankLines(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("a")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first enter to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.windowsPasteBuffer(); got != "a\n\n" {
		t.Fatalf("buffer after blank pasted line = %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("blank pasted line submitted early: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after blank-line paste detection: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if got := m.windowsPasteBuffer(); got != "a\n\nb" {
		t.Fatalf("buffer after pasted text following blank line = %q", got)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final enter after fallback paste to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	if len(*intents) != 0 {
		t.Fatalf("final enter submitted before defer elapsed: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit after paste quiet period, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "a\n\nb" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsUnbracketedPasteFallbackQueuesWhileBusy(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first busy enter to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("first pasted newline queued early: %+v", m.queuedPrompts)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("buffer after second pasted line while busy = %q", got)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("pasted multiline prompt queued before final enter: %+v", m.queuedPrompts)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("stale deferred enter queued after paste detection: %+v", m.queuedPrompts)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final enter after fallback paste to be deferred")
	}
	queueID := m.windowsPaste.pendingEnterID
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("final enter queued before defer elapsed: %+v", m.queuedPrompts)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: queueID})
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "line one\nline two" {
		t.Fatalf("expected one queued multiline prompt, got %+v", m.queuedPrompts)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no submitted intent while busy, got %+v", *intents)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after queueing, got %q", got)
	}
}

func TestWindowsPasteFallbackCtrlCResetsQuietWindow(t *testing.T) {
	// Ctrl+C is the canonical clear-with-state-reset path after PR 2 moved
	// Ctrl+U to readline kill-to-line-start. handleGlobalKey clears the
	// composer and resets the Windows paste fallback state in one go.
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("old")
	m.setWindowsPasteBuffer("pending paste")
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+C to clear composer, got %q", got)
	}
	if m.windowsPaste.pendingEnter || m.windowsPasteBuffer() != "" || !m.windowsPaste.activeUntil.IsZero() {
		t.Fatalf("expected Ctrl+C to reset paste fallback state, got %+v", m.windowsPaste)
	}

	m = typeRunesForTest(t, m, "replacement")
	m, cmd := updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected replacement prompt enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	if got := m.input.Value(); got != "replacement" {
		t.Fatalf("replacement enter should submit, not insert a pasted newline; input=%q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one replacement submit, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "replacement" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsPasteFallbackBackspaceClearsEmptyComposerState(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("x")
	m.windowsPaste.pendingEnterID = 7
	m.windowsPaste.pendingEnter = true
	m.windowsPaste.pendingEnterBusy = true
	m.windowsPaste.pendingEnterStop = true
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)
	m.windowsPaste.busyInput = true
	m.windowsPaste.busyInputStop = true
	m.windowsPaste.bracketedThisInput = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Backspace to clear composer, got %q", got)
	}
	if m.windowsPaste.pendingEnter || m.windowsPaste.pendingEnterBusy || m.windowsPaste.pendingEnterStop ||
		!m.windowsPaste.activeUntil.IsZero() || m.windowsPaste.busyInput || m.windowsPaste.busyInputStop ||
		m.windowsPaste.bracketedThisInput {
		t.Fatalf("expected empty composer to reset paste fallback state, got %+v", m.windowsPaste)
	}
}

func TestWindowsDeferredBusyEnterQueuesSingleLine(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.input.SetValue("follow up while working")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	deferredID := m.windowsPaste.pendingEnterID
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("enter queued before defer elapsed: %+v", m.queuedPrompts)
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "follow up while working" {
		t.Fatalf("expected one queued prompt, got %+v", m.queuedPrompts)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no submitted intent while busy, got %+v", *intents)
	}
}

func TestWindowsDeferredBusyEnterPreservesBusySlashClassification(t *testing.T) {
	t.Run("read only local command executes", func(t *testing.T) {
		m, intents := newModelWithDispatchSpy()
		m.windowsPaste.enabled = true
		m.busy = true
		m.status = "running"
		m.input.SetValue("/status ")

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		deferredID := m.windowsPaste.pendingEnterID
		if len(*intents) != 0 {
			t.Fatalf("busy slash should wait for deferred enter tick, got %+v", *intents)
		}

		next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
		m = next.(model)
		if len(*intents) != 1 {
			t.Fatalf("expected immediate local dispatch after deferred tick, got %+v", *intents)
		}
		if got := (*intents)[0]; got.Kind != service.IntentSubmitLocal || got.Input != "/status" {
			t.Fatalf("unexpected dispatched intent: %+v", got)
		}
		if len(m.queuedPrompts) != 0 {
			t.Fatalf("expected no queued prompt for busy local command, got %+v", m.queuedPrompts)
		}
		if got := m.input.Value(); got != "" {
			t.Fatalf("expected input cleared after busy local command, got %q", got)
		}
	})

	t.Run("pending local submit does not block busy read only local command", func(t *testing.T) {
		m, intents := newModelWithDispatchSpy()
		m.windowsPaste.enabled = true
		m.busy = true
		m.localSubmitPending = 1
		m.status = "running"
		m.input.SetValue("/status")

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		if len(*intents) != 1 {
			t.Fatalf("expected immediate busy local dispatch, got %+v", *intents)
		}
		if got := (*intents)[0]; got.Kind != service.IntentSubmitLocal || got.Input != "/status" {
			t.Fatalf("unexpected dispatched intent: %+v", got)
		}
		if m.windowsPaste.pendingEnter {
			t.Fatal("busy local command should not arm Windows deferred enter when local submit is pending")
		}
		if m.localSubmitPending != 2 {
			t.Fatalf("expected busy local command to keep existing pending submit and add one, got %d", m.localSubmitPending)
		}
	})

	t.Run("turn starting slash command is blocked", func(t *testing.T) {
		m, intents := newModelWithDispatchSpy()
		m.windowsPaste.enabled = true
		m.busy = true
		m.status = "running"
		m.input.SetValue("/ask inspect this")

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		deferredID := m.windowsPaste.pendingEnterID

		next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
		m = next.(model)
		if len(*intents) != 0 {
			t.Fatalf("turn-starting slash should be blocked while busy, got %+v", *intents)
		}
		if len(m.queuedPrompts) != 0 {
			t.Fatalf("turn-starting slash should not queue while busy, got %+v", m.queuedPrompts)
		}
		if got := m.input.Value(); got != "/ask inspect this" {
			t.Fatalf("expected blocked slash command to remain editable, got %q", got)
		}
		if m.status != "/ask disabled while working" {
			t.Fatalf("expected disabled status, got %q", m.status)
		}
	})

	t.Run("turn starting slash submits if turn finishes before tick", func(t *testing.T) {
		m, intents := newModelWithDispatchSpy()
		m.windowsPaste.enabled = true
		m.busy = true
		m.status = "running"
		m.input.SetValue("/ask inspect this")

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		deferredID := m.windowsPaste.pendingEnterID

		next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
		m = next.(model)
		if m.busy {
			t.Fatal("expected turn done to clear busy before deferred tick")
		}

		next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
		m = next.(model)
		if len(*intents) != 1 {
			t.Fatalf("expected slash command to submit after turn finished, got %+v", *intents)
		}
		if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "/ask inspect this" {
			t.Fatalf("unexpected dispatched intent: %+v", got)
		}
		if got := m.input.Value(); got != "" {
			t.Fatalf("expected input cleared after submit, got %q", got)
		}
		if !m.busy {
			t.Fatal("expected submitted slash to start a new turn")
		}
	})

	t.Run("local slash executes if turn finishes before tick", func(t *testing.T) {
		m, intents := newModelWithDispatchSpy()
		m.windowsPaste.enabled = true
		m.busy = true
		m.status = "running"
		m.input.SetValue("/new scratch")

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		deferredID := m.windowsPaste.pendingEnterID

		next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
		m = next.(model)
		if m.busy {
			t.Fatal("expected turn done to clear busy before deferred tick")
		}

		next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
		m = next.(model)
		if len(*intents) != 1 {
			t.Fatalf("expected local command to execute after turn finished, got %+v", *intents)
		}
		if got := (*intents)[0]; got.Kind != service.IntentSubmitLocal || got.Input != "/new scratch" {
			t.Fatalf("unexpected dispatched intent: %+v", got)
		}
		if got := m.input.Value(); got != "" {
			t.Fatalf("expected input cleared after local command, got %q", got)
		}
		if m.localSubmitPending != 1 {
			t.Fatalf("expected local submit pending count, got %d", m.localSubmitPending)
		}
	})

	t.Run("local submit barrier still applies after turn finishes before tick", func(t *testing.T) {
		m, intents := newModelWithDispatchSpy()
		m.windowsPaste.enabled = true
		m.busy = true
		m.status = "running"
		m.input.SetValue("/ask inspect this")

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
		deferredID := m.windowsPaste.pendingEnterID

		m.localSubmitPending = 1
		next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
		m = next.(model)
		if m.busy {
			t.Fatal("expected turn done to clear busy before deferred tick")
		}

		next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
		m = next.(model)
		if len(*intents) != 0 {
			t.Fatalf("local-submit barrier should block deferred slash after turn finished, got %+v", *intents)
		}
		if got := m.input.Value(); got != "/ask inspect this" {
			t.Fatalf("expected blocked slash command to remain editable, got %q", got)
		}
		if m.status != "wait for command to finish" {
			t.Fatalf("expected wait status while local submit is pending, got %q", m.status)
		}
	})
}

func TestWindowsDeferredBusyEnterSuppressesPlanPickerWhenTurnDoneArrivesFirst(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.input.SetValue("follow up while working")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected busy enter to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("pending deferred busy enter should suppress plan implementation picker")
	}
	if len(*intents) != 0 {
		t.Fatalf("deferred enter submitted before delay elapsed: %+v", *intents)
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "follow up while working" {
		t.Fatalf("expected deferred busy follow-up to submit after turn done, got %+v", *intents)
	}
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after deferred follow-up submit, got %v", m.mode)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected no stale queued prompts, got %+v", m.queuedPrompts)
	}
}

func TestWindowsDeferredBusyEnterSurvivesQueuedDrainBeforeTick(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.queuedPrompts = []queuedPrompt{{Text: "older queued"}}
	m.input.SetValue("new follow up")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected busy enter to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "older queued" {
		t.Fatalf("expected older queued prompt to start, got %+v", *intents)
	}
	if got := m.input.Value(); got != "new follow up" {
		t.Fatalf("expected pending Windows input to survive queued drain, got %q", got)
	}
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected deferred enter state restored after queued drain")
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("deferred follow-up should queue behind running turn, got intents %+v", *intents)
	}
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "new follow up" {
		t.Fatalf("expected new follow-up queued after deferred tick, got %+v", m.queuedPrompts)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected composer cleared after deferred queue, got %q", got)
	}
}

func TestWindowsActiveBusyPasteSuppressesPlanPickerWhenTurnDoneArrivesFirst(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("buffer after pasted continuation = %q", got)
	}

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if m.mode == modePlanImplementation {
		t.Fatal("active busy paste should suppress plan implementation picker")
	}
	if len(*intents) != 0 {
		t.Fatalf("active paste submitted before final enter: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter should not submit active paste, got %+v", *intents)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final submit enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "line one\nline two" {
		t.Fatalf("expected active busy paste to submit after final enter, got %+v", *intents)
	}
}

func TestWindowsActiveBusyPasteSuppressesPlanPickerAfterQuietWindow(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	m.windowsPaste.activeUntil = time.Now().Add(-time.Second)

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if m.mode == modePlanImplementation {
		t.Fatal("expired quiet window should not let plan implementation picker cover busy pasted input")
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected pasted input to remain buffered, got %q", got)
	}
	if !m.hasPendingWindowsBusyInput() {
		t.Fatal("expected pasted busy input to stay protected after quiet window")
	}
	if len(*intents) != 0 {
		t.Fatalf("pasted input should not submit before final enter, got %+v", *intents)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter should not submit expired active paste, got %+v", *intents)
	}
}

func TestWindowsActiveBusyPasteSurvivesQueuedDrainBeforeFinalEnter(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.queuedPrompts = []queuedPrompt{{Text: "older queued"}}
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("buffer after pasted continuation = %q", got)
	}

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "older queued" {
		t.Fatalf("expected older queued prompt to start, got %+v", *intents)
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected active Windows paste to survive queued drain, got %q", got)
	}
	if m.windowsPaste.activeUntil.IsZero() {
		t.Fatal("expected active paste window restored after queued drain")
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 1 {
		t.Fatalf("stale deferred enter should not submit active paste, got %+v", *intents)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final submit enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("final paste should queue behind running turn, got intents %+v", *intents)
	}
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "line one\nline two" {
		t.Fatalf("expected pasted follow-up queued after final enter, got %+v", m.queuedPrompts)
	}
}

func TestWindowsExpiredActiveBusyPasteSurvivesQueuedDrainBeforeFinalEnter(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.queuedPrompts = []queuedPrompt{{Text: "older queued"}}
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	m.windowsPaste.activeUntil = time.Now().Add(-time.Second)

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "older queued" {
		t.Fatalf("expected older queued prompt to start, got %+v", *intents)
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected expired active Windows paste to survive queued drain, got %q", got)
	}
	if !m.hasPendingWindowsBusyInput() {
		t.Fatal("expected expired active paste to remain protected after queued drain")
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 1 {
		t.Fatalf("stale deferred enter should not submit expired active paste, got %+v", *intents)
	}

	m = flushWindowsPasteBurstForTest(t, m)
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected final submit enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("final paste should queue behind running turn, got intents %+v", *intents)
	}
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "line one\nline two" {
		t.Fatalf("expected pasted follow-up queued after final enter, got %+v", m.queuedPrompts)
	}
}

func TestWindowsActiveBusyPasteSurvivesLocalSubmitDeferredQueuedDrain(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("buffer after pasted continuation = %q", got)
	}

	m.localSubmitPending = 1
	m.queuedPrompts = []queuedPrompt{{Text: "older queued"}}
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if len(*intents) != 0 {
		t.Fatalf("queued prompt should wait for local submit done, got %+v", *intents)
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected active Windows paste to remain while local submit is pending, got %q", got)
	}

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "older queued" {
		t.Fatalf("expected older queued prompt to start after local submit done, got %+v", *intents)
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected active Windows paste to survive deferred queued drain, got %q", got)
	}
	if !m.hasPendingWindowsBusyInput() {
		t.Fatal("expected pasted busy input to stay protected after deferred queued drain")
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 1 {
		t.Fatalf("stale deferred enter should not submit restored active paste, got %+v", *intents)
	}
}

func TestWindowsActiveBusyPasteSuppressesDeferredPlanPickerAfterLocalSubmitDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.input.SetValue("line one")

	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected first pasted newline to be deferred")
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	m.localSubmitPending = 1
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if !m.deferredPlanPicker {
		t.Fatal("expected plan implementation picker to defer while local submit is pending")
	}

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	if m.mode == modePlanImplementation {
		t.Fatal("pending Windows paste should suppress deferred implementation picker")
	}
	if m.deferredPlanPicker {
		t.Fatal("expected pending Windows paste to clear deferred picker flag")
	}
	if got := m.windowsPasteBuffer(); got != "line one\nline two" {
		t.Fatalf("expected active Windows paste to remain after local submit done, got %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("pending paste should not submit before final enter, got %+v", *intents)
	}
}

func TestWindowsDeferredStoppingBusyEnterDoesNotSubmitAfterTurnDoneArrivesFirst(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.stopping = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.input.SetValue("follow up while stopping")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected stopping enter to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("pending deferred stopping enter should suppress plan implementation picker")
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("stopping deferred enter should not submit after turn done, got %+v", *intents)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected no queued prompt after stopped turn, got %+v", m.queuedPrompts)
	}
	if got := m.input.Value(); got != "follow up while stopping" {
		t.Fatalf("expected stopping follow-up to remain in composer, got %q", got)
	}
	if m.windowsPaste.pendingEnter {
		t.Fatal("expected deferred enter state cleared")
	}
}

func TestWindowsDeferredBusyEnterInterruptedBeforeTickStaysInComposer(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.input.SetValue("follow up before interrupt")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected busy enter to be deferred")
	}
	deferredID := m.windowsPaste.pendingEnterID

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if !m.stopping {
		t.Fatal("expected Esc to start stopping")
	}
	if m.windowsPaste.pendingEnter {
		t.Fatal("expected interrupt to clear pending Windows enter")
	}
	if !m.windowsPaste.busyInput {
		t.Fatal("expected interrupt to preserve busy-input marker so plan picker stays suppressed")
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("pending interrupted enter should suppress plan implementation picker")
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("interrupted deferred enter should not submit after turn done, got %+v", *intents)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected no queued prompt after interrupted turn, got %+v", m.queuedPrompts)
	}
	if got := m.input.Value(); got != "follow up before interrupt" {
		t.Fatalf("expected interrupted follow-up to remain in composer, got %q", got)
	}
	if m.windowsPaste.pendingEnter {
		t.Fatal("expected deferred enter state cleared")
	}
}

func TestWindowsDeferredEnterStillSubmitsSingleLine(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("run the tests")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("windows enter should be deferred before submitting, got %+v", *intents)
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: m.windowsPaste.pendingEnterID})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected deferred submit, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "run the tests" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsDeferredEnterHonorsLocalSubmitBarrier(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.localSubmitPending = 1
	m.status = "command pending"
	m.input.SetValue("start a turn")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 0 {
		t.Fatalf("pending local submit should block Windows deferred enter, got %+v", *intents)
	}
	if m.windowsPaste.pendingEnter {
		t.Fatal("local submit barrier should not arm Windows deferred enter")
	}
	if got := m.input.Value(); got != "start a turn" {
		t.Fatalf("expected prompt to remain editable, got %q", got)
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while local submit is pending, got %q", m.status)
	}
}

func TestWindowsDeferredEnterTickHonorsLocalSubmitBarrier(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("start a turn")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	deferredID := m.windowsPaste.pendingEnterID
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected Windows enter to be deferred")
	}

	m.localSubmitPending = 1
	m.status = "command pending"
	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)

	if len(*intents) != 0 {
		t.Fatalf("deferred enter should not overtake pending local submit, got %+v", *intents)
	}
	if m.windowsPaste.pendingEnter {
		t.Fatal("expected deferred enter state cleared after blocked tick")
	}
	if got := m.input.Value(); got != "start a turn" {
		t.Fatalf("expected prompt to remain editable, got %q", got)
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while local submit is pending, got %q", m.status)
	}
}

func TestWindowsDeferredEnterCancelsOnEditingKey(t *testing.T) {
	tests := []struct {
		name      string
		key       tea.KeyMsg
		wantInput string
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}, wantInput: "ru"},
		{name: "left", key: tea.KeyMsg{Type: tea.KeyLeft}, wantInput: "run"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.windowsPaste.enabled = true
			m.input.SetValue("run")

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)
			deferredID := m.windowsPaste.pendingEnterID
			if !m.windowsPaste.pendingEnter {
				t.Fatal("expected Windows enter to be deferred")
			}

			next, _ = m.Update(tt.key)
			m = next.(model)
			if m.windowsPaste.pendingEnter {
				t.Fatal("editing key should cancel Windows deferred enter")
			}
			if got := m.input.Value(); got != tt.wantInput {
				t.Fatalf("unexpected input after editing key: got %q want %q", got, tt.wantInput)
			}

			next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
			m = next.(model)
			if len(*intents) != 0 {
				t.Fatalf("canceled deferred enter should not submit, got %+v", *intents)
			}
		})
	}
}

func TestWindowsDeferredEnterShiftEnterCancelsDeferredSubmit(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	if !m.shouldCancelWindowsDeferredEnterForKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")}) {
		t.Fatal("Shift+Enter must cancel pending Windows deferred enter before composer inserts newline")
	}
}

func TestWindowsBracketedPasteCanSubmitImmediately(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one\nline two"), Paste: true})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected bracketed paste to submit immediately, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "line one\nline two" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestWindowsBracketedPasteContainingMouseCSIFragmentIsNotSwallowed(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.startBusy()

	pasted := "\x1b[<64;10;10M\npayload"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	m = next.(model)

	if got := m.input.Value(); got == "" || !strings.Contains(got, "[<64;10;10M\npayload") {
		t.Fatalf("expected bracketed paste content not swallowed, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("expected bracketed paste not to enter fallback buffer, got %q", got)
	}
}

func TestWindowsPasteFallbackIMECommitSubmitsNormally(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("你好")})
	m = next.(model)
	if got := m.input.Value(); got != "你好" {
		t.Fatalf("expected IME commit to render normally, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("expected IME commit not to enter paste buffer, got %q", got)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected Windows enter to be deferred")
	}
	submitID := m.windowsPaste.pendingEnterID
	next, _ = m.Update(windowsDeferredEnterMsg{id: submitID})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "你好" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestDefaultEnterSubmitRemainsImmediate(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("run the tests")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected immediate submit, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "run the tests" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
}

func TestClearScreenCmdClearsWindowsScrollbackAndRenderer(t *testing.T) {
	var out bytes.Buffer
	cmd := clearScreenCmdForOS("windows", &out)
	msg := cmd()
	if got, want := out.String(), "\033[H\033[2J\033[3J"; got != want {
		t.Fatalf("windows clear sequence = %q, want %q", got, want)
	}
	if msg == nil {
		t.Fatal("windows clear should return a Bubble Tea clear-screen message")
	}
}

func TestClearScreenCmdPreservesUnixScrollbackClear(t *testing.T) {
	var out bytes.Buffer
	cmd := clearScreenCmdForOS("linux", &out)
	msg := cmd()
	if msg == nil {
		t.Fatal("unix clear should also return a Bubble Tea clear-screen message")
	}
	if got, want := out.String(), "\033[H\033[2J\033[3J"; got != want {
		t.Fatalf("unix clear sequence = %q, want %q", got, want)
	}
}

func agentTurnMetadata() map[string]any {
	return map[string]any{service.EventMetadataAgentTurn: true}
}

func selectSlashCommand(t *testing.T, m *model, want string) {
	t.Helper()
	for i, cmd := range m.slash.matches {
		if cmd.InsertText == want || cmd.Display == want {
			m.slash.selected = i
			return
		}
	}
	t.Fatalf("slash command %q not found in matches %+v", want, m.slash.matches)
}

func newLongHistoryComposerModel(historyCount int, input string) model {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 12
	m.transcript = make([]tuirender.UIMessage, 0, historyCount)
	m.input.SetValue(input)
	for i := 0; i < historyCount; i++ {
		m.transcript = append(m.transcript, tuirender.UIMessage{
			Role: "info",
			Kind: tuirender.KindText,
			Text: fmt.Sprintf("entry-%04d", i),
		})
	}
	m.refreshViewportContentFollow(true)
	return m
}

func TestIsEnvironmentInventoryBlock_PositiveChinese(t *testing.T) {
	text := "- 系统： macOS\n- 版本： 26.0\n- 构建号： 25A354"
	if !isEnvironmentInventoryBlock(text) {
		t.Fatalf("expected environment inventory block to be detected")
	}
}

func TestIsEnvironmentInventoryBlock_NegativeNormalAssistantText(t *testing.T) {
	text := "I checked the version mismatch in package constraints and suggest bumping one dependency."
	if isEnvironmentInventoryBlock(text) {
		t.Fatalf("did not expect normal assistant text to be detected as environment inventory block")
	}
}

func TestHydrateSessionMessages_SuppressesEnvironmentInventoryAssistantBlock(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "- 系统： macOS\n- 版本： 26.0\n- 构建号： 25A354",
		},
	}
	m.hydrateSessionMessages(msgs)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries for environment inventory block, got %d", got)
	}
}

func TestHydrateSessionMessages_KeptForNormalAssistantText(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "Implemented the layout update and kept footer semantics unchanged.",
		},
	}
	m.hydrateSessionMessages(msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one assistant entry, got %d", len(snap))
	}
	if snap[0].Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", snap[0].Role)
	}
}

func TestHydrateSessionMessages_RendersReasoningAsThinkingOnly(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role:      core.RoleAssistant,
			Reasoning: "I should answer the age question.",
		},
	}
	m.hydrateSessionMessages(msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected only thinking entry, got %+v", snap)
	}
	if snap[0].Role != "think" || snap[0].Kind != tuirender.KindThinking {
		t.Fatalf("expected first entry to be thinking, got %+v", snap[0])
	}
}

func TestHydrateSessionMessages_RendersReasoningAndAssistantSeparately(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role:      core.RoleAssistant,
			Reasoning: "I should answer succinctly.",
			Text:      "I do not have an age.",
		},
	}
	m.hydrateSessionMessages(msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected thinking plus assistant entries, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindThinking || snap[0].Role != "think" {
		t.Fatalf("expected thinking entry first, got %+v", snap[0])
	}
	if snap[1].Role != "assistant" || snap[1].Kind != tuirender.KindText {
		t.Fatalf("expected assistant text second, got %+v", snap[1])
	}
}

func TestHydrateSessionMessages_SuppressesHiddenUserText(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role:   core.RoleUser,
			Text:   "Generate a file named AGENTS.md",
			Hidden: true,
		},
	}
	m.hydrateSessionMessages(msgs)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries for hidden user text, got %d", got)
	}
}

func TestHydrateSessionMessages_RestoresUpdatePlanAsPlanUpdate(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{
				ID:    "plan-1",
				Name:  "update_plan",
				Input: `{"plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"},{"step":"Test","status":"pending"}]}`,
			}},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{{
				ToolCallID: "plan-1",
				Name:       "update_plan",
				Content:    `{"success":true,"data":{"explanation":"resume checklist","plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"},{"step":"Test","status":"pending"}]}}`,
			}},
		},
	}
	m.hydrateSessionMessages(msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 || snap[0].Kind != tuirender.KindPlanUpdate {
		t.Fatalf("expected hydrated plan update only, got %+v", snap)
	}
	if strings.Contains(snap[0].Text, "Updated plan") || strings.Contains(snap[0].Text, "update_plan") {
		t.Fatalf("expected checklist content, not generic tool row: %+v", snap[0])
	}
	rendered := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	for _, want := range []string{"Updated Plan", "resume checklist", "✔ Inspect", "□ Patch", "□ Test"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in hydrated plan update:\n%s", want, rendered)
		}
	}
}

func TestHydrateSessionMessages_LimitsVisibleResumeHistory(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := make([]core.Message, 0, 12)
	for i := 0; i < 12; i++ {
		msgs = append(msgs, core.Message{
			Role: core.RoleUser,
			Text: fmt.Sprintf("user-%02d", i),
		})
	}
	m.hydrateSessionMessages(msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != maxHydratedVisibleMessages {
		t.Fatalf("expected %d visible messages, got %d", maxHydratedVisibleMessages, len(snap))
	}
	joined := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	if strings.Contains(joined, "user-03") || !strings.Contains(joined, "user-04") || !strings.Contains(joined, "user-11") {
		t.Fatalf("expected only recent resume messages in UI hydrate:\n%s", joined)
	}
}

func TestSessionHydrationTrimsRenderedResumeHistoryLines(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	msgs := make([]core.Message, 0, 8)
	for i := 0; i < 8; i++ {
		msgs = append(msgs, core.Message{
			Role: core.RoleUser,
			Text: fmt.Sprintf("msg-%02d\n%s", i, strings.Repeat("line\n", 70)),
		})
	}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:     service.EventSessionHydrated,
		Messages: msgs,
	}))
	m = next.(model)
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(rendered, "msg-00") || !strings.Contains(rendered, "msg-07") {
		t.Fatalf("expected rendered resume transcript to keep recent tail only:\n%s", rendered)
	}
	if got := len(tuirender.ChatLines(m.transcript[1:], m.chatRenderWidth())); got > maxHydratedTranscriptLines {
		t.Fatalf("expected hydrated transcript to be bounded, got %d lines", got)
	}
}

func TestSlashCommandsShowSupportedCommandsAndOmitRemovedCommands(t *testing.T) {
	cmds := parseSlashCommands(app.CommandsHelp)
	if !containsString(cmds, "/permissions") {
		t.Fatalf("expected /permissions in slash commands: %+v", cmds)
	}
	if !containsString(cmds, "/agent") {
		t.Fatalf("expected /agent in slash commands: %+v", cmds)
	}
	if !containsString(cmds, "/plan") {
		t.Fatalf("expected /plan in slash commands: %+v", cmds)
	}
	if !containsString(cmds, "/ask") {
		t.Fatalf("expected /ask in slash commands: %+v", cmds)
	}
	if !containsString(cmds, "/diff") {
		t.Fatalf("expected /diff in slash commands: %+v", cmds)
	}
	if containsString(cmds, "/approval") {
		t.Fatalf("removed command /approval should not appear in slash commands: %+v", cmds)
	}
	if containsString(cmds, "/thinking") {
		t.Fatalf("removed command /thinking should not appear in slash commands: %+v", cmds)
	}
	if containsString(cmds, "/budget") {
		t.Fatalf("removed command /budget should not appear in slash commands: %+v", cmds)
	}
	if containsString(cmds, "/step") {
		t.Fatalf("removed command /step should not appear in slash commands: %+v", cmds)
	}
}

func TestPickerEventsClearBusyState(t *testing.T) {
	tests := []struct {
		name string
		ev   service.Event
		mode mode
	}{
		{
			name: "model picker",
			ev: service.Event{
				Kind:            service.EventModelPicker,
				ModelChoices:    []string{"deepseek-v4-pro"},
				EffortChoices:   []string{"normal"},
				ThinkingChoices: []string{"on", "off"},
				CurrentModel:    "deepseek-v4-pro",
				CurrentEffort:   "normal",
				CurrentThinking: "on",
			},
			mode: modeModelPicker,
		},
		{
			name: "permissions menu",
			ev: service.Event{
				Kind:       service.EventPermissionsMenu,
				AutoAccept: true,
			},
			mode: modePermissionsMenu,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{assembler: tuirender.NewAssembler(), mode: modeChat, busy: true, stopping: true}
			m.busySince = time.Now().Add(-5 * time.Minute)
			next, _ := m.Update(svcMsg(tt.ev))
			m = next.(model)
			if m.busy || m.stopping || !m.busySince.IsZero() {
				t.Fatalf("expected picker event to clear busy state, busy=%v stopping=%v busySince=%v", m.busy, m.stopping, m.busySince)
			}
			if m.mode != tt.mode {
				t.Fatalf("expected mode %v, got %v", tt.mode, m.mode)
			}
		})
	}
}

func TestPermissionsMenuRendersStateAndDispatchesExplicitMode(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventPermissionsMenu, AutoAccept: false}))
	m = next.(model)
	if m.mode != modePermissionsMenu {
		t.Fatalf("expected permissions menu mode, got %v", m.mode)
	}
	rendered := m.renderPermissionsMenu()
	plain := xansi.Strip(rendered)
	if !strings.Contains(plain, "Session auto-accept: off") || !strings.Contains(plain, "Enable session auto-accept") {
		t.Fatalf("unexpected permissions menu:\n%s", rendered)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after selection, got %v", m.mode)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSetApprovalMode || (*intents)[0].ApprovalMode != "auto_accept" {
		t.Fatalf("unexpected dispatched intent: %+v", *intents)
	}

	m, intents = newModelWithDispatchSpy()
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventPermissionsMenu, AutoAccept: true}))
	m = next.(model)
	rendered = m.renderPermissionsMenu()
	plain = xansi.Strip(rendered)
	if !strings.Contains(plain, "Session auto-accept: on") || !strings.Contains(plain, "Disable session auto-accept") {
		t.Fatalf("unexpected enabled permissions menu:\n%s", rendered)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSetApprovalMode || (*intents)[0].ApprovalMode != "ask" {
		t.Fatalf("unexpected dispatched intent: %+v", *intents)
	}

	m, intents = newModelWithDispatchSpy()
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventPermissionsMenu, AutoAccept: false}))
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 || m.mode != modeChat {
		t.Fatalf("cancel should not dispatch and should return to chat, intents=%+v mode=%v", *intents, m.mode)
	}
}

func TestPermissionsMenuUsesSegmentedPickerStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := newModel(nil, "", "", "")
	m.mode = modePermissionsMenu
	rendered := m.renderPermissionsMenu()
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected styled permissions menu, got:\n%s", rendered)
	}
	plain := xansi.Strip(rendered)
	for _, want := range []string{"Permissions", "Session auto-accept: off", "> Enable session auto-accept", "  Cancel"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected plain menu to contain %q, got:\n%s", want, plain)
		}
	}
}

func assertStyledPickerContains(t *testing.T, rendered string, wants ...string) {
	t.Helper()
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected styled picker, got:\n%s", rendered)
	}
	plain := xansi.Strip(rendered)
	for _, want := range wants {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected plain picker to contain %q, got:\n%s", want, plain)
		}
	}
}

func TestModelPickerUsesSegmentedPickerStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := newModel(nil, "deepseek-chat", "medium", "auto")
	m.mode = modeModelPicker
	m.modelPicker.models = []string{"deepseek-chat", "deepseek-reasoner"}
	m.modelPicker.efforts = []string{"low", "medium", "high"}
	assertStyledPickerContains(t, m.renderModelPicker(), "Select Model and Effort", "Model:", "> deepseek-chat", "  deepseek-reasoner", "(up/down choose, enter next/confirm, esc back)")
}

func TestSessionPickerEnterDispatchesSelectedSession(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventSessionsListed,
		Choices: []string{
			"recent sessions:",
			"   #   Updated   Branch                    Conversation",
			"   1) 1m ago    main                     first",
			"   2) 2m ago    feature                  second",
		},
	}))
	m = next.(model)
	if m.mode != modeSessionPicker {
		t.Fatalf("expected session picker mode, got %v", m.mode)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	got := (*intents)[0]
	if got.Kind != service.IntentSelectSession || got.SessionInput != "2" {
		t.Fatalf("unexpected intent: %+v", got)
	}
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after selection, got %v", m.mode)
	}
}

func TestSessionPickerUsesSegmentedPickerStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := newModel(nil, "", "", "")
	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventSessionsListed,
		Choices: []string{
			"recent sessions:",
			"   #   Updated   Branch                    Conversation",
			"*  1) 4s ago    -                        current",
			"   2) 1m ago    feature                  second session",
		},
	}))
	m = next.(model)
	assertStyledPickerContains(t, m.renderSessionPicker(), "sessions", "#   Updated   Branch", "> 1)   4s ago", "  2)   1m ago", "feature", "second session")
}

func TestSecondaryPickersUseSegmentedPickerStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := newModel(nil, "", "", "")
	m.files.matches = []fileSuggestion{{Path: "internal/tui/render.go"}, {Path: "internal/tui", IsDir: true}}
	m.files.selected = 1
	assertStyledPickerContains(t, m.renderFileSuggestions(), "Files", "> internal/tui/", "dir", "Tab/Enter insert")

	m = newModel(nil, "", "", "")
	m.palette.actions = []paletteAction{{Label: "Open logs"}, {Label: "Show help"}}
	m.palette.selected = 1
	assertStyledPickerContains(t, m.renderPalette(), "Command Palette", "(enter to run, esc to close)", "> Show help")

	m = newModel(nil, "", "", "")
	m.skills.matches = []skillSuggestion{{Name: "code-review", Description: "Review local changes"}, {Name: "plan", Description: "Make a plan"}}
	m.skills.selected = 0
	assertStyledPickerContains(t, m.renderSkillSuggestions(), "Skills", "> $code-review", "Review local changes", "Tab/Enter insert")

	m = newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{Kind: service.EventSkillsMenu})
	assertStyledPickerContains(t, m.renderSkillsMenu(), "Skills", "Choose an action", "> List skills", "Enable/Disable Skills")

	m = newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{
		Kind: service.EventSkillsManager,
		Skills: []skills.SkillView{
			{Name: "code-review", Description: "Review local changes", Status: skills.AvailabilityReady},
		},
	})
	assertStyledPickerContains(t, m.renderSkillsManager(), "Enable/Disable Skills", "[x] code-review", "Review local changes")

	m = newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{
		Kind: service.EventPluginsManager,
		Plugins: []plugins.PluginStatus{{
			Manifest: plugins.Manifest{ID: "memory", Name: "Memory", Description: "Durable memory"},
			Enabled:  true,
		}},
	})
	assertStyledPickerContains(t, m.renderPluginsManager(), "Plugins", "Installed plugins", "[x] memory", "Durable memory")

	m = newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	assertStyledPickerContains(t, m.renderReviewMenu(), "Review", "Choose what to review", "> Local changes", "Branch")

	m.reviewTargetPicker.branches = []reviewBranchItem{{Name: "main"}, {Name: "feature", Current: true}}
	m.reviewTargetPicker.defaultBranch = "main"
	m.mode = modeReviewBranchPicker
	assertStyledPickerContains(t, m.renderReviewTargetPicker(), "Choose base branch", "Type to search branches", "> feature -> main")

	m = newModel(nil, "", "", "")
	m.planImplementation.index = 1
	assertStyledPickerContains(t, m.renderPlanImplementationPicker(), "Implement this plan?", "> No, stay in Plan mode")

	m = newModel(nil, "", "", "")
	m.userInput.questions = []core.UserInputQuestion{{
		Question: "Pick deployment target",
		Options:  []core.UserInputOption{{Label: "Staging", Description: "Use staging"}, {Label: "Production", Description: "Use production"}},
	}}
	m.userInput.selectedOption = 1
	assertStyledPickerContains(t, m.renderUserInputPicker(), "Pick deployment target", "> Production", "- Use production")

	m = newModel(nil, "", "", "")
	m.worktreeExit.summary = app.WorktreeExitSummary{
		Session: app.WorktreeSession{Name: "feat", Branch: "feature/work", Path: "/tmp/work"},
	}
	assertStyledPickerContains(t, m.renderWorktreeExit(), "Exiting worktree session", "worktree: feat", "> Keep worktree", "No worktree changes were detected.")
}

func TestCrossWorkspaceResumeInfoRendersInTUI(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	msg := strings.Join([]string{
		"This conversation is from a different directory.",
		"",
		"To resume, run:",
		"  cd '/tmp/other workspace' && '/usr/local/bin/whale' resume sess-1",
	}, "\n")

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventInfo, Text: msg}))
	m = next.(model)
	view := m.View()
	if !strings.Contains(view, "This conversation is from a different directory.") ||
		!strings.Contains(view, "To resume, run:") ||
		!strings.Contains(view, "resume sess-1") {
		t.Fatalf("expected cross-workspace resume message in TUI:\n%s", view)
	}
}

func TestTurnDoneReasoningOnlyCommitsFallback(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventReasoningDelta, Text: "I should answer."}))
	m = next.(model)
	next, cmd := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "Reasoning only") || !strings.Contains(got, "did not produce a visible answer") {
		t.Fatalf("expected reasoning-only status in transcript:\n%s", got)
	}
	if m.sawReasoningThisTurn || m.sawAssistantThisTurn {
		t.Fatal("expected turn tracking flags to reset")
	}
}

func TestPlanTurnDoneWithAssistantButNoProposedPlanDoesNotShowNotice(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		width:     80,
		height:    24,
		busy:      true,
	}
	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventAssistantDelta,
		Text: "Here is the test execution plan:\n\n- Run TUI tests\n- Run full tests",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("did not expect implementation picker without a proposed_plan block")
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "Here is the test execution plan") {
		t.Fatalf("expected assistant text to remain visible:\n%s", got)
	}
	if strings.Contains(got, "No proposed plan was produced") || strings.Contains(got, "<proposed_plan>") {
		t.Fatalf("did not expect missing proposed plan notice in transcript:\n%s", got)
	}
	if m.sawAssistantThisTurn || m.sawPlanThisTurn {
		t.Fatal("expected turn tracking flags to reset")
	}
}

func TestTurnDoneReconcilesDroppedAssistantDeltaFromLastResponse(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 8, busy: true}
	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventAssistantDelta,
		Text: "visible answer head\n",
	}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "visible answer head\nmissing latest tail",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "missing latest tail") {
		t.Fatalf("expected turn completion to reconcile dropped assistant delta from LastResponse:\n%s", got)
	}
}

func TestTurnDoneRecoversAssistantWhenAllDeltasDropped(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.appendTranscript("you", tuirender.KindText, "prompt")
	m.beginTurnTranscript()
	m.busy = true

	next, _ := m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "final answer only present in LastResponse",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "final answer only present in LastResponse") {
		t.Fatalf("expected turn completion to recover assistant text from LastResponse:\n%s", got)
	}
	if strings.Contains(got, "did not produce a visible answer") {
		t.Fatalf("did not expect reasoning-only fallback after LastResponse recovery:\n%s", got)
	}
}

func TestTurnDoneAddsDurationNoticeForLongTurn(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	m.busySince = time.Now().Add(-(3*time.Minute + 5*time.Second))

	next, _ := m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "done") {
		t.Fatalf("expected final assistant response in transcript:\n%s", got)
	}
	if !strings.Contains(got, "✻ Worked for 3m ") {
		t.Fatalf("expected turn duration notice in transcript:\n%s", got)
	}
}

func TestTurnDoneSkipsDurationNoticeForShortTurn(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	m.busySince = time.Now().Add(-29 * time.Second)

	next, _ := m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(got, "Worked for") {
		t.Fatalf("did not expect turn duration notice for short turn:\n%s", got)
	}
}

func TestTurnDoneSkipsDurationNoticeWhileStopping(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true, stopping: true}
	m.busySince = time.Now().Add(-2 * time.Minute)

	next, _ := m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(got, "Worked for") {
		t.Fatalf("did not expect turn duration notice for stopped turn:\n%s", got)
	}
}

func TestAppendTurnDurationNoticeThresholdAndBusyState(t *testing.T) {
	m := model{width: 80, height: 24, viewportFrozen: true}
	m.appendTurnDurationNotice(true, false, 30*time.Second)
	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "✻ Worked for 30s") {
		t.Fatalf("expected duration notice at threshold:\n%s", got)
	}

	m = model{width: 80, height: 24, viewportFrozen: true}
	m.appendTurnDurationNotice(true, false, 29*time.Second)
	if len(m.transcript) != 0 {
		t.Fatalf("did not expect duration notice below threshold, got %+v", m.transcript)
	}

	m = model{width: 80, height: 24, viewportFrozen: true}
	m.appendTurnDurationNotice(false, false, 2*time.Minute)
	if len(m.transcript) != 0 {
		t.Fatalf("did not expect duration notice when turn was not busy, got %+v", m.transcript)
	}
}

func TestFormatTurnDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{name: "negative", duration: -time.Second, want: "0s"},
		{name: "seconds", duration: 45 * time.Second, want: "45s"},
		{name: "minutes", duration: 2*time.Minute + 9*time.Second, want: "2m 9s"},
		{name: "hours", duration: time.Hour + 2*time.Minute + 3*time.Second, want: "1h 2m 3s"},
		{name: "days", duration: 24*24*time.Hour + time.Minute + 2*time.Second, want: "24d 0h 1m 2s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTurnDuration(tt.duration); got != tt.want {
				t.Fatalf("formatTurnDuration(%s) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestReplacingCurrentTurnAssistantRewindsNativeScrollback(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.appendTranscript("you", tuirender.KindText, "prompt")
	m.beginTurnTranscript()
	m.appendTranscript("assistant", tuirender.KindText, "partial visible answer")
	m.appendTranscript("result_ok", tuirender.KindToolResult, "✓")
	m.visibleAssistantThisTurn = "partial visible answer"
	m.sawAssistantThisTurn = true
	m.nativeScrollbackPrinted = len(m.transcript)

	reconciled := m.reconcileFinalAssistant("corrected final answer")
	m.commitLiveTranscript(reconciled)

	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected rewritten transcript to produce corrected native scrollback output")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "corrected final answer") {
		t.Fatalf("expected corrected final answer in native scrollback output, got %s", got)
	}
}

func TestMarkNoFinalAnswerIfNeeded(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		sawReasoningThisTurn: true,
	}
	if !m.markNoFinalAnswerIfNeeded() {
		t.Fatal("expected no-final-answer status to be marked")
	}
	if m.status != "" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one notice entry, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindStatus || snap[0].Role != "status" {
		t.Fatalf("expected status entry, got %+v", snap[0])
	}
	if !strings.Contains(snap[0].Text, "reasoning only") || !strings.Contains(snap[0].Text, "visible answer") {
		t.Fatalf("expected generic reasoning-only status, got %q", snap[0].Text)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "no_final_answer" {
		t.Fatalf("expected diagnostic log entry, got %+v", m.logs)
	}
}

func TestMarkNoFinalAnswerIfNeededAddsPlanNotice(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		chatMode:             "plan",
		sawReasoningThisTurn: true,
	}
	if !m.markNoFinalAnswerIfNeeded() {
		t.Fatal("expected no-final-answer status to be marked")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one notice entry, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindStatus || snap[0].Role != "status" {
		t.Fatalf("expected status entry, got %+v", snap[0])
	}
	if !strings.Contains(snap[0].Text, "reasoning only") || !strings.Contains(snap[0].Text, "visible plan") {
		t.Fatalf("expected reasoning-only plan status, got %q", snap[0].Text)
	}
}

func TestMarkNoFinalAnswerIfNeededSkippedWithTerminalToolOutcome(t *testing.T) {
	m := model{
		assembler:                      tuirender.NewAssembler(),
		sawReasoningThisTurn:           true,
		sawTerminalToolOutcomeThisTurn: true,
	}
	if m.markNoFinalAnswerIfNeeded() {
		t.Fatal("did not expect no-final-answer status after terminal tool outcome")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries, got %d", got)
	}
}

func TestMarkNoFinalAnswerIfNeededSkippedWithAssistant(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		sawReasoningThisTurn: true,
		sawAssistantThisTurn: true,
	}
	if m.markNoFinalAnswerIfNeeded() {
		t.Fatal("did not expect no-final-answer status with assistant answer")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries, got %d", got)
	}
}

func TestMarkMissingProposedPlanIfNeededLogsOnly(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		chatMode:             "plan",
		sawAssistantThisTurn: true,
	}
	if !m.markMissingProposedPlanIfNeeded(true) {
		t.Fatal("expected missing proposed plan to be marked")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected no user-visible notice entry, got %+v", snap)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "missing_proposed_plan" {
		t.Fatalf("expected diagnostic log entry, got %+v", m.logs)
	}
}

func TestMarkMissingProposedPlanIfNeededSkippedWithPlan(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		chatMode:             "plan",
		sawAssistantThisTurn: true,
		sawPlanThisTurn:      true,
	}
	if m.markMissingProposedPlanIfNeeded(true) {
		t.Fatal("did not expect missing proposed plan notice after plan completion")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries, got %d", got)
	}
}

func TestVisibleSubmittedTextForPlanPrompt(t *testing.T) {
	if got := visibleSubmittedText("/ask inspect the parser"); got != "inspect the parser" {
		t.Fatalf("unexpected visible text for ask prompt: %q", got)
	}
	if got := visibleSubmittedText("/plan inspect the parser"); got != "inspect the parser" {
		t.Fatalf("unexpected visible text: %q", got)
	}
	if got := visibleSubmittedText("/plan"); got != "/plan" {
		t.Fatalf("unexpected visible text for bare plan: %q", got)
	}
	if got := visibleSubmittedText("/plan off"); got != "/plan off" {
		t.Fatalf("unexpected visible text for unsupported plan off: %q", got)
	}
}

func TestSlashSuggestionsHiddenForMultilineInput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/sta\nmore")
	m.updateSlashMatches()
	if len(m.slash.matches) != 0 {
		t.Fatalf("expected slash suggestions hidden for multiline input, got %+v", m.slash.matches)
	}
}

func TestSlashSuggestionsShownForSingleLineSlash(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/")
	m.updateSlashMatches()
	if len(m.slash.matches) == 0 {
		t.Fatal("expected slash suggestions for bare slash")
	}
}

func TestSlashSuggestionsRenderDescriptions(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/")
	m.updateSlashMatches()
	rendered := m.renderSlashSuggestions()
	for _, want := range []string{"/model", "Choose model, effort, and thinking settings"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected slash suggestions to contain %q:\n%s", want, rendered)
		}
	}
}

func TestSlashArgumentHintShownForCommandSpace(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/model ")
	m.updateSlashMatches()
	if !m.hasSlashPanel() {
		t.Fatal("expected slash argument hint panel")
	}
	if len(m.slash.matches) != 0 {
		t.Fatalf("did not expect /model option matches, got %+v", m.slash.matches)
	}
	if rendered := m.renderSlashSuggestions(); !strings.Contains(xansi.Strip(rendered), "Arguments [model]") {
		t.Fatalf("expected /model argument hint, got:\n%s", rendered)
	}
}

func TestSlashArgumentHintEscClearsPanelWithoutMutatingInput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/model ")
	m.updateSlashMatches()
	if !m.hasSlashPanel() {
		t.Fatal("expected slash argument hint panel")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if got := m.input.Value(); got != "/model " {
		t.Fatalf("expected esc to preserve input, got %q", got)
	}
	if m.hasSlashPanel() || m.slash.argumentHint != "" || len(m.slash.matches) != 0 {
		t.Fatalf("expected esc to clear hint-only slash panel, hint=%q matches=%+v", m.slash.argumentHint, m.slash.matches)
	}
}

func TestSlashOptionSuggestionsInsertSubcommand(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/stats ")
	m.updateSlashMatches()
	if len(m.slash.matches) == 0 {
		t.Fatal("expected /stats option suggestions")
	}
	selectSlashCommand(t, &m, "/stats usage")
	if rendered := m.renderSlashSuggestions(); !strings.Contains(xansi.Strip(rendered), "/stats usage") || !strings.Contains(xansi.Strip(rendered), "Show token and cost usage") {
		t.Fatalf("expected /stats option description, got:\n%s", rendered)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected /stats usage option to dispatch, got intents %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/stats usage" {
		t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after /stats usage option, got %q", got)
	}
}

func TestSlashStatsOptionSuggestionsUseFullCommandLabels(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := newModel(nil, "", "", "")
	m.input.SetValue("/stats ")
	m.updateSlashMatches()
	rendered := m.renderSlashSuggestions()
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected styled stats suggestions, got:\n%s", rendered)
	}
	plain := xansi.Strip(rendered)
	for _, want := range []string{"/stats usage", "/stats tools", "Show token and cost usage", "Show tool-call counts"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected stats suggestions to contain %q, got:\n%s", want, plain)
		}
	}
}

func TestSlashCommandWithOptionsDrillsDownWhenSelected(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/mem")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/memory ")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("selecting /memory should only show options, got intents %+v", *intents)
	}
	if got := m.input.Value(); got != "/memory " {
		t.Fatalf("expected /memory selection to add trailing space, got %q", got)
	}
	if len(m.slash.matches) == 0 {
		t.Fatal("expected /memory option suggestions after selection")
	}
	if rendered := m.renderSlashSuggestions(); !strings.Contains(rendered, "list") || !strings.Contains(rendered, "List remembered entries") {
		t.Fatalf("expected /memory option list after selection, got:\n%s", rendered)
	}
}

func TestSlashCommandWithOptionsAndAutoRunStillExecutesBareCommand(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/rev")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/review")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected selected /review to dispatch, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/review" {
		t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after /review autorun, got %q", got)
	}
}

func TestSlashOptionSuggestionsFilterByTypedPrefix(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/review p")
	m.updateSlashMatches()
	if len(m.slash.matches) != 1 {
		t.Fatalf("expected one /review option match, got %+v", m.slash.matches)
	}
	if got := m.slash.matches[0].InsertText; got != "/review pr " {
		t.Fatalf("expected /review pr option, got %q", got)
	}
}

func TestSlashOptionNeedingArgumentOnlyFillsInput(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/review p")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/review pr ")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("/review pr option should wait for argument, got intents %+v", *intents)
	}
	if got := m.input.Value(); got != "/review pr " {
		t.Fatalf("expected /review pr option to keep trailing space, got %q", got)
	}
}

func TestSlashExactOptionDoesNotShowNestedSuggestions(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/review branch")
	m.updateSlashMatches()
	if m.hasSlashPanel() {
		t.Fatalf("expected no nested slash panel for exact option, got hint=%q matches=%+v", m.slash.argumentHint, m.slash.matches)
	}
}

func TestSlashSuggestionsHiddenForAbsolutePathInput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/Users/goranka/Engineer/ai/dsk 里有好几个go项目的，你看看它们怎么做的")
	m.updateSlashMatches()
	if len(m.slash.matches) != 0 {
		t.Fatalf("expected slash suggestions hidden for absolute path prompt, got %+v", m.slash.matches)
	}
}

func TestFileSuggestionsShownForAtInput(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "internal", "tui", "model.go"), "package tui\n")
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("@mod")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions for @mod")
	}
	if got := m.files.matches[0].Path; got != "internal/tui/model.go" {
		t.Fatalf("expected model.go first, got %+v", m.files.matches)
	}
	if m.hasSkillSuggestions() {
		t.Fatalf("expected skill suggestions hidden while file suggestions are visible, got %+v", m.skills.matches)
	}
}

func TestBareAtShowsFileHintWithoutScanning(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m, intents := newModelWithDispatchSpy()
	m.cwdPath = dir
	m.input.SetValue("@")
	if cmd := m.updateSlashMatches(); cmd != nil {
		t.Fatal("bare @ should not start a file suggestion search")
	}
	if m.hasFileSuggestions() {
		t.Fatalf("bare @ should not expand file suggestions, got %+v", m.files.matches)
	}
	if !m.hasFilePanel() {
		t.Fatal("bare @ should show the file hint panel")
	}
	if rendered := m.renderFileSuggestions(); !strings.Contains(rendered, "Type to search workspace files") {
		t.Fatalf("expected idle file-search hint, got:\n%s", rendered)
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != "@" {
		t.Fatalf("tab on bare @ should preserve input, got %q", got)
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.input.Value(); got != "" {
		t.Fatalf("enter on bare @ should submit and clear input, got %q", got)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "@" {
		t.Fatalf("expected bare @ to submit as normal text, got %+v", *intents)
	}
}

func TestFindFileSuggestionsEmptyQueryReturnsNoMatches(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	if got := findFileSuggestions(dir, ""); len(got) != 0 {
		t.Fatalf("empty query should not scan and return matches, got %+v", got)
	}
}

func TestFindFileSuggestionsRanksLaterWorkspaceMatches(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 600; i++ {
		writeFileSuggestionFixture(t, filepath.Join(dir, "aaa", fmt.Sprintf("target.go-%03d.md", i)), "noise\n")
	}
	writeFileSuggestionFixture(t, filepath.Join(dir, "zzz", "src", "target.go"), "package src\n")

	got := findFileSuggestions(dir, "target.go")
	if len(got) == 0 {
		t.Fatal("expected file suggestions")
	}
	if got[0].Path != "zzz/src/target.go" {
		t.Fatalf("expected later exact workspace match first, got %+v", got)
	}
}

func TestFileSuggestionEnterInsertsSelectedPath(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "internal", "tui", "model.go"), "package tui\n")
	m, intents := newModelWithDispatchSpy()
	m.cwdPath = dir
	m.input.SetValue("review @mod")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.input.Value(); got != "review internal/tui/model.go " {
		t.Fatalf("expected selected path inserted, got %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no dispatch when inserting file suggestion, got %+v", *intents)
	}
	if m.hasFileSuggestions() {
		t.Fatalf("expected file suggestions cleared, got %+v", m.files.matches)
	}
	if m.hasFilePanel() {
		t.Fatal("expected file suggestion panel cleared after insertion")
	}
}

func TestFileSuggestionTabQuotesPathsWithSpaces(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "docs", "my file.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("@my")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != `"docs/my file.md" ` {
		t.Fatalf("expected quoted selected path, got %q", got)
	}
}

func TestFileSuggestionTabEscapesQuotedPathWithSpaces(t *testing.T) {
	if got := quoteFileSuggestionPath(`docs/my "file".md`); got != `"docs/my \"file\".md"` {
		t.Fatalf("expected escaped quoted path, got %q", got)
	}
}

func TestFileSuggestionEscClearsSuggestionsWithoutMutatingInput(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("@read")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := m.input.Value(); got != "@read" {
		t.Fatalf("expected esc to preserve input, got %q", got)
	}
	if m.hasFileSuggestions() || m.files.selected != 0 {
		t.Fatalf("expected file suggestions cleared, got matches=%v selected=%d", m.files.matches, m.files.selected)
	}
}

func TestFileSuggestionsHiddenForMultilineBusyAndHistory(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("@read\nmore")
	m.updateSlashMatches()
	if m.hasFileSuggestions() {
		t.Fatalf("expected file suggestions hidden for multiline input, got %+v", m.files.matches)
	}
	m.input.SetValue("@read")
	m.busy = true
	m.updateSlashMatches()
	if m.hasFileSuggestions() {
		t.Fatalf("expected file suggestions hidden while busy, got %+v", m.files.matches)
	}
	m.busy = false
	m.inHistoryNav = true
	m.lastHistoryText = "@read"
	m.updateSlashMatches()
	if m.hasFileSuggestions() {
		t.Fatalf("expected file suggestions hidden during history navigation, got %+v", m.files.matches)
	}
}

func TestFileSuggestionsTakePriorityInsideSlashArguments(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "docs", "review.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("/review @rev")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if m.hasSlashPanel() {
		t.Fatalf("expected file suggestions to suppress slash panel, hint=%q matches=%+v", m.slash.argumentHint, m.slash.matches)
	}
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions inside slash arguments")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.input.Value(); got != "/review docs/review.md " {
		t.Fatalf("expected selected path inserted into slash command argument, got %q", got)
	}
}

func TestFileSuggestionsWorkInsideOpenSlashArguments(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("/open @read")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if m.hasSlashPanel() {
		t.Fatalf("expected file suggestions to suppress /open slash panel, hint=%q matches=%+v", m.slash.argumentHint, m.slash.matches)
	}
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions inside /open arguments")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != "/open README.md " {
		t.Fatalf("expected selected path inserted into /open argument, got %q", got)
	}
}

func TestFileSuggestionsQuoteOpenSlashArgumentsWithSpaces(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "docs", "my file.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("/open @my")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions inside /open arguments")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != `/open "docs/my file.md" ` {
		t.Fatalf("expected quoted selected path inserted into /open argument, got %q", got)
	}
}

func TestFileSuggestionsEscapeWorkspaceRelativeTildeForOpen(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "~", "notes.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("/open @notes")
	m = runFileSuggestionSearchForTest(t, m, m.updateSlashMatches())
	if !m.hasFileSuggestions() {
		t.Fatal("expected file suggestions inside /open arguments")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != "/open ./~/notes.md " {
		t.Fatalf("expected workspace-relative tilde path escaped for /open, got %q", got)
	}
}

func TestFilePanelNavigationSuppressesHistoryWhenNoMatches(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("@missing")
	m.promptHistory = []string{"previous prompt"}
	m.inHistoryNav = true
	m.lastHistoryText = "@missing"
	m.files.active = true
	m.files.query = "missing"
	m.files.searching = false

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "@missing" {
		t.Fatalf("expected visible file panel to keep history navigation suppressed, got %q", got)
	}
}

func TestFileSuggestionsIgnoreStaleAsyncResults(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "internal", "tui", "model.go"), "package tui\n")
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("@mod")
	staleCmd := m.updateSlashMatches()
	m.input.SetValue("@read")
	freshCmd := m.updateSlashMatches()
	m = runFileSuggestionSearchForTest(t, m, staleCmd)
	if m.hasFileSuggestions() {
		t.Fatalf("expected stale results ignored, got %+v", m.files.matches)
	}
	m = runFileSuggestionSearchForTest(t, m, freshCmd)
	if !m.hasFileSuggestions() || m.files.matches[0].Path != "README.md" {
		t.Fatalf("expected fresh README match, got %+v", m.files.matches)
	}
}

func TestFileSuggestionsCancelPreviousAsyncSearch(t *testing.T) {
	dir := t.TempDir()
	writeFileSuggestionFixture(t, filepath.Join(dir, "internal", "tui", "model.go"), "package tui\n")
	writeFileSuggestionFixture(t, filepath.Join(dir, "README.md"), "# test\n")
	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	m.input.SetValue("@mod")
	staleCmd := m.updateSlashMatches()
	m.input.SetValue("@read")
	freshCmd := m.updateSlashMatches()

	msg, ok := staleCmd().(fileSuggestionsLoadedMsg)
	if !ok {
		t.Fatalf("expected fileSuggestionsLoadedMsg, got %T", msg)
	}
	if len(msg.matches) != 0 {
		t.Fatalf("expected canceled search to return no matches, got %+v", msg.matches)
	}
	m = runFileSuggestionSearchForTest(t, m, freshCmd)
	if !m.hasFileSuggestions() || m.files.matches[0].Path != "README.md" {
		t.Fatalf("expected fresh search to remain usable, got %+v", m.files.matches)
	}
}

func TestSlashSuggestionEnterAutoRunsSingleCommandAndClearsSuggestions(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/co")
	m.updateSlashMatches()
	if len(m.slash.matches) == 0 {
		t.Fatal("expected slash matches")
	}
	selectSlashCommand(t, &m, "/compact")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one dispatched intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "/compact" {
		t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after autorun slash enter, got %q", got)
	}
	if len(m.slash.matches) != 0 || m.slash.selected != 0 {
		t.Fatalf("expected slash state cleared, got matches=%v selected=%d", m.slash.matches, m.slash.selected)
	}
	if !m.busy || m.status != "running" {
		t.Fatalf("expected running state after autorun slash enter, busy=%v status=%q", m.busy, m.status)
	}
}

func TestHelpCommandOpensInteractiveHelp(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 30
	m.input.SetValue("/help")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 0 {
		t.Fatalf("/help should open local help without dispatching, got %+v", *intents)
	}
	if m.mode != modeHelp {
		t.Fatalf("expected help mode, got %v", m.mode)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared, got %q", got)
	}
	view := m.View()
	for _, want := range []string{"Whale help", "Browse default commands:", "/diff", "For more help:", helpDocsURL, "Esc to cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected help view to contain %q:\n%s", want, view)
		}
	}
	for _, msg := range m.transcript {
		if msg.Role == "you" && msg.Text == "/help" {
			t.Fatalf("/help should not be written as a user transcript row")
		}
	}
}

func TestHelpCommandKeyboardNavigationIgnoresMouse(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 100
	m.height = 18
	m.openHelp()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.help.selected != 1 {
		t.Fatalf("expected down to move help selection, got %d", m.help.selected)
	}

	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(model)
	if m.help.selected != 1 {
		t.Fatalf("expected mouse wheel to be ignored in help, got selection %d", m.help.selected)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.mode != modeChat {
		t.Fatalf("expected esc to close help, got mode %v", m.mode)
	}
}

func TestShiftTabModeToggleDoesNotStartWorkingState(t *testing.T) {
	m, intents := newModelWithDispatchSpy()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one mode toggle intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentToggleMode {
		t.Fatalf("unexpected intent: %+v", (*intents)[0])
	}
	if m.busy || !m.busySince.IsZero() {
		t.Fatalf("mode toggle should not start working state, busy=%v busySince=%v", m.busy, m.busySince)
	}
	if m.status != "ready" {
		t.Fatalf("mode toggle should wait for service info instead of local switching status, got %q", m.status)
	}
	if strings.Contains(m.View(), "Working") {
		t.Fatalf("mode toggle should not render working status:\n%s", m.View())
	}
}

func TestShiftTabModeToggleWaitsForPendingLocalSubmit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.localSubmitPending = 1
	m.status = "command pending"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)

	if len(*intents) != 0 {
		t.Fatalf("pending local submit should block mode toggle intent, got %+v", *intents)
	}
	if m.localSubmitPending != 1 {
		t.Fatalf("expected pending local submit to remain, got %d", m.localSubmitPending)
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while local submit is pending, got %q", m.status)
	}
	if m.busy {
		t.Fatal("mode shortcut barrier should not start working state")
	}
}

func TestLocalImmediateSlashCommandsDoNotStartWorkingState(t *testing.T) {
	for _, cmd := range []string{
		"/agent",
		"/ask",
		"/plan",
		"/model",
		"/permissions",
		"/skills",
		"/status",
		"/stats",
		"/stats usage",
		"/stats tools",
		"/stats repair",
		"/stats recent",
		"/stats profile",
		"/stats all",
		"/mcp",
		"/resume",
		"/clear",
		"/new",
		"/new scratch",
		"/fork",
		"/fork scratch",
		"/model xxx",
		"/skills xxx",
		"/resume xxx",
		"/new a b",
		"/fork a b",
		"/stats bad",
		"/compact bad",
		"/plan show",
	} {
		t.Run(cmd, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.input.SetValue(cmd)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)
			if len(*intents) != 1 {
				t.Fatalf("expected one dispatched intent, got %+v", *intents)
			}
			if (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != cmd {
				t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
			}
			if got := m.input.Value(); got != "" {
				t.Fatalf("expected input cleared after %s, got %q", cmd, got)
			}
			if m.busy || !m.busySince.IsZero() {
				t.Fatalf("%s should not start working state, busy=%v busySince=%v", cmd, m.busy, m.busySince)
			}
			for _, msg := range m.transcript {
				if msg.Role == "you" && msg.Text == cmd {
					t.Fatalf("%s should not be written as a user transcript row", cmd)
				}
			}
		})
	}
}

func TestOpenCommandUsesTerminalExecInsteadOfServiceDispatch(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/open .")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if cmd == nil {
		t.Fatal("expected /open to return a terminal exec command")
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no service dispatch for /open, got %+v", *intents)
	}
	if m.localSubmitPending != 1 {
		t.Fatalf("expected pending local submit, got %d", m.localSubmitPending)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared, got %q", got)
	}
	if m.busy || !m.busySince.IsZero() {
		t.Fatalf("/open should not start working state, busy=%v busySince=%v", m.busy, m.busySince)
	}
}

func TestSlashSuggestionTabFillsInputWithoutDispatch(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("/co")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/compact")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("expected no dispatch on tab, got %+v", *intents)
	}
	if got := m.input.Value(); got != "/compact" {
		t.Fatalf("expected tab to fill exact command, got %q", got)
	}
	if len(m.slash.matches) == 0 {
		t.Fatal("expected slash matches to remain after tab completion")
	}
}

func TestSlashSuggestionEscClearsSuggestionsWithoutMutatingInput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/co")
	m.updateSlashMatches()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if got := m.input.Value(); got != "/co" {
		t.Fatalf("expected esc to preserve input, got %q", got)
	}
	if len(m.slash.matches) != 0 || m.slash.selected != 0 {
		t.Fatalf("expected esc to clear slash suggestions, got matches=%v selected=%d", m.slash.matches, m.slash.selected)
	}
}

func TestSkillSuggestionsShownForDollarInput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.skills.all = []skillSuggestion{
		{Name: "code-review", Description: "Review local changes", When: "Use when reviewing code"},
		{Name: "release", Description: "Prepare a release"},
	}
	m.input.SetValue("$rev")
	m.updateSlashMatches()
	if len(m.skills.matches) != 1 || m.skills.matches[0].Name != "code-review" {
		t.Fatalf("expected code-review skill match, got %+v", m.skills.matches)
	}
	if m.hasSlashSuggestions() {
		t.Fatalf("expected slash suggestions to stay hidden for skill input: %+v", m.slash.matches)
	}
}

func TestSkillSuggestionEnterInsertsMention(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes", SkillFilePath: "/tmp/code-review/SKILL.md"}}
	m.input.SetValue("$co")
	m.updateSlashMatches()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if got := m.input.Value(); got != "$code-review " {
		t.Fatalf("expected selected skill inserted, got %q", got)
	}
	if m.skillBinding == nil || m.skillBinding.Name != "code-review" || m.skillBinding.SkillFilePath != "/tmp/code-review/SKILL.md" {
		t.Fatalf("expected skill binding for selected mention, got %+v", m.skillBinding)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no dispatch when inserting skill mention, got %+v", *intents)
	}
	if len(m.skills.matches) != 0 || m.skills.selected != 0 {
		t.Fatalf("expected skill suggestions cleared, got matches=%v selected=%d", m.skills.matches, m.skills.selected)
	}
}

func TestSkillSuggestionDownNavigationPreservesSelection(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{
		{Name: "code-review", Description: "Review local changes"},
		{Name: "git-worktree", Description: "Create an isolated worktree"},
		{Name: "grill-me", Description: "Interview the user relentlessly"},
		{Name: "skill-creator", Description: "Create or update skills"},
	}
	m.input.SetValue("$")
	m.updateSlashMatches()
	if len(m.skills.matches) != 4 {
		t.Fatalf("expected four skill matches, got %+v", m.skills.matches)
	}

	for i := 0; i < 3; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	if got := m.skills.selected; got != 3 {
		t.Fatalf("expected selected index 3 after three down presses, got %d", got)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if got := m.input.Value(); got != "$skill-creator " {
		t.Fatalf("expected selected skill inserted, got %q", got)
	}
}

func TestSkillSuggestionSubmitIncludesBinding(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes", SkillFilePath: "/tmp/code-review/SKILL.md"}}
	m.input.SetValue("$co")
	m.updateSlashMatches()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.input.SetValue("$code-review review this diff")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	got := (*intents)[0]
	if got.Kind != service.IntentSubmit || got.Input != "$code-review review this diff" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
	if got.SkillBinding == nil || got.SkillBinding.Name != "code-review" || got.SkillBinding.SkillFilePath != "/tmp/code-review/SKILL.md" {
		t.Fatalf("expected submit skill binding, got %+v", got.SkillBinding)
	}
}

func TestWindowsPasteFallbackTypingPreservesSkillBinding(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes", SkillFilePath: "/tmp/code-review/SKILL.md"}}
	m.input.SetValue("$co")
	m.updateSlashMatches()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.skillBinding == nil {
		t.Fatal("expected selected skill to set binding")
	}

	m = typeRunesForTest(t, m, "review this diff")
	if got := m.input.Value(); got != "$code-review review this diff" {
		t.Fatalf("unexpected composer value after Windows fallback typing: %q", got)
	}
	if m.skillBinding == nil || m.skillBinding.Name != "code-review" {
		t.Fatalf("expected Windows fallback typing to preserve skill binding, got %+v", m.skillBinding)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	submitID := m.windowsPaste.pendingEnterID
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: submitID})
	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	got := (*intents)[0]
	if got.Kind != service.IntentSubmit || got.Input != "$code-review review this diff" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
	if got.SkillBinding == nil || got.SkillBinding.Name != "code-review" || got.SkillBinding.SkillFilePath != "/tmp/code-review/SKILL.md" {
		t.Fatalf("expected submit skill binding after Windows fallback typing, got %+v", got.SkillBinding)
	}
}

func TestSkillSuggestionSubmitDropsStaleBindingAfterNameEdit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes", SkillFilePath: "/tmp/code-review/SKILL.md"}}
	m.input.SetValue("$co")
	m.updateSlashMatches()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.input.SetValue("$find-skills")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one submit intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.SkillBinding != nil {
		t.Fatalf("expected stale binding to be dropped, got %+v", got.SkillBinding)
	}
}

func TestSkillSuggestionsHiddenForSlashAndBusy(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes"}}
	m.input.SetValue("/")
	m.updateSlashMatches()
	if len(m.skills.matches) != 0 {
		t.Fatalf("expected skill suggestions hidden for slash input, got %+v", m.skills.matches)
	}
	m.input.SetValue("$co")
	m.busy = true
	m.updateSlashMatches()
	if len(m.skills.matches) != 0 {
		t.Fatalf("expected skill suggestions hidden while busy, got %+v", m.skills.matches)
	}
}

func TestSkillSuggestionsHiddenAfterInsertedMentionWithSpace(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes"}}
	m.input.SetValue("$code-review ")
	m.updateSlashMatches()
	if len(m.skills.matches) != 0 {
		t.Fatalf("expected skill suggestions hidden after mention insert, got %+v", m.skills.matches)
	}
}

func TestProviderRetryEventUpdatesStatusWithoutTranscript(t *testing.T) {
	m := newModel(nil, "", "", "")
	beforeTranscript := len(m.transcript)
	m.handleServiceEvent(service.Event{Kind: service.EventProviderRetry, Text: "API rate limited, retrying in 2s (1/3)", Metadata: map[string]any{"delay_ms": int64(2000)}})

	if m.status != "ready" {
		t.Fatalf("status should not be overwritten by retry, got %s", m.status)
	}
	if m.providerRetryStatus != "API rate limited, retrying in 2s (1/3)" {
		t.Fatalf("providerRetryStatus: %s", m.providerRetryStatus)
	}
	if len(m.transcript) != beforeTranscript {
		t.Fatalf("retry event should not append transcript, before=%d after=%d", beforeTranscript, len(m.transcript))
	}
	if len(m.logs) == 0 || m.logs[len(m.logs)-1].Kind != "api_retry" {
		t.Fatalf("missing api_retry log: %+v", m.logs)
	}
}

func TestProviderRetryStatusClearsOnAssistantDelta(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{Kind: service.EventProviderRetry, Text: "API rate limited, retrying in 2s (1/3)", Metadata: map[string]any{"delay_ms": int64(2000)}})
	m.handleServiceEvent(service.Event{Kind: service.EventAssistantDelta, Text: "ok"})

	if m.providerRetryStatus != "" || !m.providerRetryUntil.IsZero() {
		t.Fatalf("provider retry status not cleared: %q until=%v", m.providerRetryStatus, m.providerRetryUntil)
	}
}

func TestProviderRetryStreamResetClearsLiveAttempt(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{Kind: service.EventAssistantDelta, Text: "old answer"})
	m.handleServiceEvent(service.Event{Kind: service.EventReasoningDelta, Text: "old thought"})
	m.appendToolCall("tc-old", "shell_run", `{"command":"date"}`)

	if len(m.assembler.Snapshot()) == 0 {
		t.Fatal("expected live attempt content before retry reset")
	}
	m.handleServiceEvent(service.Event{
		Kind:     service.EventProviderRetry,
		Text:     "API stream disconnected, retrying in 1s (1/1)",
		Metadata: map[string]any{"delay_ms": int64(1000), "stage": "stream", "stream_reset": true},
	})

	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected live attempt cleared, got %+v", m.assembler.Snapshot())
	}
	if m.visibleAssistantThisTurn != "" || m.sawAssistantThisTurn || m.sawReasoningThisTurn {
		t.Fatalf("turn visibility not reset: visible=%q assistant=%v reasoning=%v", m.visibleAssistantThisTurn, m.sawAssistantThisTurn, m.sawReasoningThisTurn)
	}
	if m.providerRetryStatus == "" {
		t.Fatal("expected provider retry status after reset")
	}
	if len(m.transcript) != 0 {
		t.Fatalf("retry reset should not append transcript: %+v", m.transcript)
	}
}

func TestSkillsManagerRendersSearchesAndToggles(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{
		Kind: service.EventSkillsManager,
		Skills: []skills.SkillView{
			{Name: "code-review", Description: "Review local changes", Status: skills.AvailabilityReady},
			{Name: "legacy-review", Reason: "Disabled in config", Status: skills.AvailabilityDisabled},
		},
	})
	if m.mode != modeSkillsManager {
		t.Fatalf("expected skills manager mode, got %v", m.mode)
	}
	rendered := m.renderSkillsManager()
	for _, want := range []string{"Enable/Disable Skills", "[x] code-review", "[ ] legacy-review", "Space/Enter toggle"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected skills manager render to contain %q, got:\n%s", want, rendered)
		}
	}

	for _, r := range "legacy" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	if len(m.skillsManager.matches) != 1 {
		t.Fatalf("expected one filtered skill, got matches=%v", m.skillsManager.matches)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one toggle intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSetSkillEnabled || got.SkillName != "legacy-review" || !got.SkillEnabled {
		t.Fatalf("unexpected toggle intent: %+v", got)
	}
	idx := m.skillsManager.matches[m.skillsManager.selected]
	if !m.skillsManager.all[idx].Enabled {
		t.Fatalf("expected selected skill to be optimistically enabled: %+v", m.skillsManager.all[idx])
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if m.mode != modeChat {
		t.Fatalf("expected ctrl+c to close skills manager, got mode %v", m.mode)
	}
}

func TestSkillsMenuListsAndOpensManager(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventSkillsMenu})
	if m.mode != modeSkillsMenu {
		t.Fatalf("expected skills menu mode, got %v", m.mode)
	}
	rendered := m.renderSkillsMenu()
	for _, want := range []string{"Skills", "List skills", "Enable/Disable Skills", "press $"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected skills menu render to contain %q, got:\n%s", want, rendered)
		}
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one manager request intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentRequestSkillsManage {
		t.Fatalf("unexpected intent: %+v", got)
	}
}

func TestSkillsMenuListActionOpensDollarPicker(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.skills.all = []skillSuggestion{{Name: "code-review", Description: "Review local changes"}}
	m.handleServiceEvent(service.Event{Kind: service.EventSkillsMenu})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after list action, got %v", m.mode)
	}
	if got := m.input.Value(); got != "$" {
		t.Fatalf("expected input to contain dollar picker trigger, got %q", got)
	}
	if len(m.skills.matches) != 1 || m.skills.matches[0].Name != "code-review" {
		t.Fatalf("expected skill picker matches, got %+v", m.skills.matches)
	}
}

func TestPluginsManagerRendersAndToggles(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{
		Kind: service.EventPluginsManager,
		Plugins: []plugins.PluginStatus{
			{
				Manifest: plugins.Manifest{ID: "memory", Name: "Memory", Description: "Durable memory"},
				Enabled:  true,
				Tools:    []string{"forget", "recall_memory", "remember"},
				Commands: []plugins.SlashCommand{{Name: "/memory"}},
			},
		},
	})
	if m.mode != modePluginsManager {
		t.Fatalf("expected plugins manager mode, got %v", m.mode)
	}
	rendered := m.renderPluginsManager()
	for _, want := range []string{"Plugins", "Installed plugins", "[x] memory", "Run /memory", "Agent tools: forget", "Space enable/disable"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected plugins manager render to contain %q, got:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"Type to search plugins", "skills-improver"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("plugins manager should not render search UI %q:\n%s", unwanted, rendered)
		}
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one toggle intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSetPluginEnabled || got.PluginID != "memory" || got.PluginEnabled {
		t.Fatalf("unexpected toggle intent: %+v", got)
	}
	idx := m.pluginsManager.matches[m.pluginsManager.selected]
	if m.pluginsManager.all[idx].Enabled {
		t.Fatalf("expected selected plugin to be optimistically disabled: %+v", m.pluginsManager.all[idx])
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.mode != modeChat {
		t.Fatalf("expected esc to close plugins manager, got mode %v", m.mode)
	}
}

func TestReviewMenuDispatchesAndPrefillsTargets(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	if m.mode != modeReviewMenu {
		t.Fatalf("expected review menu mode, got %v", m.mode)
	}
	rendered := m.renderReviewMenu()
	for _, want := range []string{"Review", "Local changes", "Branch", "vs default branch", "Pull request", "Custom instructions"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected review menu render to contain %q, got:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Current branch") {
		t.Fatalf("review menu should not contain duplicate Current branch entry:\n%s", rendered)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "/review local" {
		t.Fatalf("expected /review local submit intent, got %+v", *intents)
	}
	if m.mode != modeChat {
		t.Fatalf("expected review menu to close, got mode %v", m.mode)
	}
	if !m.busy || m.status != "running" {
		t.Fatalf("expected review action to enter running state, busy=%v status=%q", m.busy, m.status)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected review action to clear input, got %q", got)
	}

	m, _ = newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeReviewBranchPicker || !m.reviewTargetPicker.loading {
		t.Fatalf("expected branch picker loading mode, mode=%v picker=%+v", m.mode, m.reviewTargetPicker)
	}
}

func TestReviewTargetPickersSubmitSelectedTargets(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 2
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeReviewPRPicker {
		t.Fatalf("expected PR picker, got %v", m.mode)
	}
	m, _ = updateTestModel(t, m, reviewPRsLoadedMsg{items: []reviewPRItem{
		{Number: 102, Title: "Improve review command", Head: "feat/review", Author: "alice"},
	}})
	rendered := m.renderReviewTargetPicker()
	if !strings.Contains(rendered, "#102 Improve review command") || !strings.Contains(rendered, "Type number or URL manually") {
		t.Fatalf("unexpected PR picker render:\n%s", rendered)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "/review pr 102" {
		t.Fatalf("expected selected PR submit intent, got %+v", *intents)
	}

	m, intents = newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 3
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeReviewCommitPicker {
		t.Fatalf("expected commit picker, got %v", m.mode)
	}
	m, _ = updateTestModel(t, m, reviewCommitsLoadedMsg{items: []reviewCommitItem{
		{SHA: "abc1234", Subject: "fix review picker", Author: "g", When: "2 minutes ago"},
	}})
	rendered = m.renderReviewTargetPicker()
	if !strings.Contains(rendered, "abc1234 fix review picker") || !strings.Contains(rendered, "Type SHA manually") {
		t.Fatalf("unexpected commit picker render:\n%s", rendered)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "/review commit abc1234" {
		t.Fatalf("expected selected commit submit intent, got %+v", *intents)
	}
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after commit submit, got %v", m.mode)
	}

	m, intents = newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 1
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeReviewBranchPicker {
		t.Fatalf("expected branch picker, got %v", m.mode)
	}
	m, _ = updateTestModel(t, m, reviewBranchesLoadedMsg{items: []reviewBranchItem{
		{Name: "feature/review", Current: true},
		{Name: "diagnose/input-scroll"},
	}, defaultBranch: "origin/main"})
	rendered = m.renderReviewTargetPicker()
	if !strings.Contains(rendered, "Type to search branches") ||
		!strings.Contains(rendered, "feature/review -> origin/main") ||
		!strings.Contains(rendered, "feature/review -> diagnose/input-scroll") ||
		strings.Contains(rendered, "feature/review -> feature/review") ||
		!strings.Contains(rendered, "Type branch manually") {
		t.Fatalf("unexpected branch picker render:\n%s", rendered)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "/review branch origin/main" {
		t.Fatalf("expected selected branch submit intent, got %+v", *intents)
	}
}

func TestReviewBranchPickerFiltersBranches(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m, _ = updateTestModel(t, m, reviewBranchesLoadedMsg{items: []reviewBranchItem{
		{Name: "main"},
		{Name: "feat/btw-command", Current: true},
		{Name: "diagnose/input-scroll-garbled"},
		{Name: "design/plugin-memory"},
	}})
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("diag")})
	m = next.(model)
	rendered := m.renderReviewTargetPicker()
	if !strings.Contains(rendered, "Type to search branches: diag") ||
		!strings.Contains(rendered, "feat/btw-command -> diagnose/input-scroll-garbled") ||
		strings.Contains(rendered, "feat/btw-command -> design/plugin-memory") ||
		strings.Contains(rendered, "feat/btw-command -> feat/btw-command") {
		t.Fatalf("unexpected filtered branch picker render:\n%s", rendered)
	}
}

func TestReviewBranchPickerDefaultFirstAndLimitsVisibleRows(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m, _ = updateTestModel(t, m, reviewBranchesLoadedMsg{items: []reviewBranchItem{
		{Name: "topic", Current: true},
		{Name: "z-last"},
		{Name: "main"},
		{Name: "branch-1"},
		{Name: "branch-2"},
		{Name: "branch-3"},
		{Name: "branch-4"},
		{Name: "branch-5"},
		{Name: "branch-6"},
	}, defaultBranch: "origin/main"})

	branches := m.filteredReviewBranches()
	if len(branches) == 0 || branches[0].Name != "origin/main" {
		t.Fatalf("expected default branch first, got %+v", branches)
	}
	rendered := m.renderReviewTargetPicker()
	if !strings.Contains(rendered, "topic -> origin/main") || strings.Contains(rendered, "topic -> branch-6") || strings.Contains(rendered, "Type branch manually") {
		t.Fatalf("expected first page of 6 branch rows, got:\n%s", rendered)
	}

	for i := 0; i < 7; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	rendered = m.renderReviewTargetPicker()
	if !strings.Contains(rendered, "topic -> branch-5") {
		t.Fatalf("expected down arrow to scroll branch rows, got:\n%s", rendered)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "/review branch branch-5" {
		t.Fatalf("expected selected branch submit intent, got %+v", *intents)
	}
}

func TestParseReviewBranchesParsesTabSeparator(t *testing.T) {
	items := parseReviewBranches("main\t\nfeature\t*\n")
	if len(items) != 2 || items[0].Name != "main" || items[0].Current || items[1].Name != "feature" || !items[1].Current {
		t.Fatalf("unexpected parsed branches: %+v", items)
	}
}

func TestReviewTargetPickerManualInputAndEsc(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 3
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m, _ = updateTestModel(t, m, reviewCommitsLoadedMsg{})
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(model)
	if m.mode != modeChat || m.input.Value() != "/review commit a" {
		t.Fatalf("expected manual commit prefill, mode=%v input=%q", m.mode, m.input.Value())
	}

	m, _ = newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	m.reviewMenu.selected = 1
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m, _ = updateTestModel(t, m, reviewPRsLoadedMsg{})
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.mode != modeReviewMenu || m.status != "review" {
		t.Fatalf("expected esc to return to review menu, mode=%v status=%q", m.mode, m.status)
	}
}

func TestReviewMenuEscCloses(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.handleServiceEvent(service.Event{Kind: service.EventReviewMenu})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.mode != modeChat || m.status != "ready" {
		t.Fatalf("expected review menu to close, mode=%v status=%q", m.mode, m.status)
	}
}

func TestPickerAndModalViewsHideComposer(t *testing.T) {
	const draft = "composer draft should stay hidden"
	base := func() model {
		m := newModel(nil, "deepseek-chat", "medium", "on")
		m.width = 100
		m.height = 30
		m.input.SetValue(draft)
		return m
	}

	chat := base()
	chat.mode = modeChat
	if view := chat.renderBottom(100); !strings.Contains(view, draft) {
		t.Fatalf("chat mode should render composer draft:\n%s", view)
	}

	cases := []struct {
		name  string
		setup func(*model)
		want  string
	}{
		{
			name: "review menu",
			setup: func(m *model) {
				m.mode = modeReviewMenu
			},
			want: "Choose what to review",
		},
		{
			name: "review target picker",
			setup: func(m *model) {
				m.mode = modeReviewBranchPicker
				m.reviewTargetPicker.branches = []reviewBranchItem{{Name: "main"}}
			},
			want: "Type to search branches",
		},
		{
			name: "review commit picker",
			setup: func(m *model) {
				m.mode = modeReviewCommitPicker
				m.reviewTargetPicker.commits = []reviewCommitItem{{SHA: "abc1234", Subject: "fix picker"}}
			},
			want: "Choose commit",
		},
		{
			name: "review pr picker",
			setup: func(m *model) {
				m.mode = modeReviewPRPicker
				m.reviewTargetPicker.prs = []reviewPRItem{{Number: 12, Title: "Fix picker"}}
			},
			want: "Choose pull request",
		},
		{
			name: "approval",
			setup: func(m *model) {
				m.mode = modeApproval
				m.approval.toolCallID = "tool-1"
				m.approval.toolName = "shell_run"
				m.approval.reason = "ls"
			},
			want: "Approval required",
		},
		{
			name: "user input",
			setup: func(m *model) {
				m.mode = modeUserInput
				m.userInput.questions = []core.UserInputQuestion{{
					ID:       "continue",
					Question: "Continue?",
					Options:  []core.UserInputOption{{Label: "Yes", Description: "Continue now."}},
				}}
			},
			want: "Continue?",
		},
		{
			name: "model picker",
			setup: func(m *model) {
				m.mode = modeModelPicker
				m.modelPicker.models = []string{"deepseek-chat"}
				m.modelPicker.efforts = []string{"medium"}
				m.modelPicker.thinkings = []string{"on"}
			},
			want: "Select Model and Effort",
		},
		{
			name: "session picker",
			setup: func(m *model) {
				m.mode = modeSessionPicker
				m.sessionChoices = []string{"session-1"}
			},
			want: "sessions",
		},
		{
			name: "permissions menu",
			setup: func(m *model) {
				m.mode = modePermissionsMenu
			},
			want: "Session auto-accept:",
		},
		{
			name: "plan implementation picker",
			setup: func(m *model) {
				m.mode = modePlanImplementation
			},
			want: "Implement this plan?",
		},
		{
			name: "worktree exit",
			setup: func(m *model) {
				m.mode = modeWorktreeExit
				m.worktreeExit.summary = app.WorktreeExitSummary{
					Session:      app.WorktreeSession{Name: "feature", Path: "/tmp/repo/.whale/worktrees/feature", Branch: "worktree-feature"},
					ChangedFiles: 2,
					Commits:      1,
				}
			},
			want: "Exiting worktree session",
		},
		{
			name: "skills menu",
			setup: func(m *model) {
				m.mode = modeSkillsMenu
			},
			want: "Skills",
		},
		{
			name: "skills manager",
			setup: func(m *model) {
				m.mode = modeSkillsManager
				m.skillsManager.all = []skillManagerItem{{Name: "code-review", Enabled: true, Toggleable: true}}
				m.skillsManager.matches = []int{0}
			},
			want: "Enable/Disable Skills",
		},
		{
			name: "plugins manager",
			setup: func(m *model) {
				m.mode = modePluginsManager
				m.pluginsManager.all = []pluginManagerItem{{ID: "memory", Name: "Memory", Enabled: true}}
				m.pluginsManager.matches = []int{0}
			},
			want: "Plugins",
		},
		{
			name: "help",
			setup: func(m *model) {
				m.mode = modeHelp
			},
			want: "Whale help",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.setup(&m)
			view := m.renderBottom(100)
			if strings.Contains(view, draft) || strings.Contains(view, "Type message or command") {
				t.Fatalf("composer should be hidden while %s is active:\n%s", tc.name, view)
			}
			if !strings.Contains(view, tc.want) {
				t.Fatalf("expected %s view to contain %q:\n%s", tc.name, tc.want, view)
			}
		})
	}
}

func TestWindowsPasteViewCacheInvalidatesForApprovalModal(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	chatView := m.View()
	if strings.Contains(chatView, "Approval required") {
		t.Fatalf("chat view unexpectedly contained approval prompt:\n%s", chatView)
	}

	m.setWindowsPasteBuffer("pasted text still streaming")
	m.mode = modeApproval
	m.status = "approval required"
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"

	view := m.View()
	if !strings.Contains(view, "Approval required") {
		t.Fatalf("paste view cache hid approval prompt:\n%s", view)
	}
	if view == chatView {
		t.Fatal("expected modal transition to invalidate cached paste frame")
	}
}

func TestSkillLoadedEventUpdatesStatusAndLogOnly(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{Kind: service.EventSkillLoaded, Text: "loaded skill: code-review"})

	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("skill loaded event should not add chat entries, got %d", got)
	}
	if m.status != "loaded skill: code-review" {
		t.Fatalf("expected skill loaded status, got %q", m.status)
	}
	if len(m.logs) != 1 {
		t.Fatalf("expected one log entry, got %+v", m.logs)
	}
	if got := m.logs[0]; got.Kind != "skill_loaded" || got.Source != "skills" || got.Summary != "loaded skill: code-review" {
		t.Fatalf("unexpected skill loaded log: %+v", got)
	}
}

func TestSlashSuggestionPlanAutoRunsWhenSelected(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/pl")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/plan")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one dispatch for selected /plan, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/plan" {
		t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after /plan autorun, got %q", got)
	}
	if m.busy || !m.busySince.IsZero() {
		t.Fatalf("expected /plan autorun not to start working state, busy=%v busySince=%v", m.busy, m.busySince)
	}
}

func TestSlashSuggestionAskAutoRunsWhenSelected(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/as")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/ask")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one dispatch for selected /ask, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/ask" {
		t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after /ask autorun, got %q", got)
	}
	if m.busy || !m.busySince.IsZero() {
		t.Fatalf("expected /ask autorun not to start working state, busy=%v busySince=%v", m.busy, m.busySince)
	}
}

func TestSlashTurnStartingCommandsStillStartWorkingState(t *testing.T) {
	for _, prompt := range []string{"/ask inspect the parser", "/plan propose a fix", "/compact", "/init"} {
		t.Run(prompt, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.input.SetValue(prompt)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)
			if len(*intents) != 1 {
				t.Fatalf("expected one dispatched intent, got %+v", *intents)
			}
			if (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != prompt {
				t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
			}
			if !m.busy || m.status != "running" {
				t.Fatalf("expected prompt command to start working state, busy=%v status=%q", m.busy, m.status)
			}
			if got := m.input.Value(); got != "" {
				t.Fatalf("expected input cleared after prompt submit, got %q", got)
			}
		})
	}
}

func TestCtrlCClearsNonEmptyInputWithoutArmingQuit(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("draft")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if cmd != nil {
		t.Fatalf("expected no command when clearing input, got %T", cmd)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared, got %q", got)
	}
	if !m.quitArmedUntil.IsZero() {
		t.Fatal("expected ctrl+c on non-empty input not to arm quit")
	}
	if m.status != "input cleared" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestSecondIdleCtrlCRequestsExitFlow(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.quitArmedUntil = time.Now().Add(time.Second)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentRequestExit {
		t.Fatalf("expected exit request intent, got %+v", (*intents)[0])
	}
	if !m.quitArmedUntil.IsZero() || m.status != "exiting" {
		t.Fatalf("unexpected quit state/status: %v %q", m.quitArmedUntil, m.status)
	}
}

func TestEnterWhileBusyQueuesInputWithoutSubmitting(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.input.SetValue("follow up while working")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after queueing, got %q", got)
	}
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "follow up while working" {
		t.Fatalf("expected queued prompt, got %+v", m.queuedPrompts)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no submitted intent while busy, got %+v", *intents)
	}
	if !m.busy {
		t.Fatal("expected turn to remain busy")
	}
	if m.status != "queued (1)" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	if got := strings.Join(tuirender.ChatLines(m.assembler.Snapshot(), 80), "\n"); strings.Contains(got, "follow up while working") {
		t.Fatalf("queued prompt should not be written to live transcript:\n%s", got)
	}
}

func TestEnterWhileBusyExecutesReadOnlySlashAndExitImmediately(t *testing.T) {
	for _, cmd := range []string{"/status", "/stats usage", "/stats repair", "/mcp", "/diff", "/exit"} {
		t.Run(cmd, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.busy = true
			m.status = "running"
			m.input.SetValue(cmd)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)

			if len(*intents) != 1 {
				t.Fatalf("expected immediate local dispatch, got %+v", *intents)
			}
			if (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != cmd {
				t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
			}
			if got := m.input.Value(); got != "" {
				t.Fatalf("expected input cleared after %s, got %q", cmd, got)
			}
			if len(m.queuedPrompts) != 0 {
				t.Fatalf("expected no queued prompts, got %+v", m.queuedPrompts)
			}
			if !m.busy {
				t.Fatal("expected active turn to remain busy")
			}
			if m.localSubmitPending != 1 {
				t.Fatalf("expected pending local submit count to be 1, got %d", m.localSubmitPending)
			}
		})
	}
}

func TestDiffResultOpensDiffPageAndEscReturnsToChat(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 20
	m.input.SetValue("draft")
	m.localSubmitCommands = []string{"/diff"}

	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventDiffResult,
		Text: "diff --git a/README.md b/README.md\n@@ -1 +1 @@\n-old\n+new\n",
	}))
	m = next.(model)

	if m.page != pageDiff {
		t.Fatalf("expected diff page, got %v", m.page)
	}
	if m.shouldRenderComposer() {
		t.Fatal("diff page should hide the composer")
	}
	if view := m.View(); strings.Contains(view, "draft") || !strings.Contains(view, "q/Esc close") || strings.Contains(view, "Ctrl+C") || strings.Contains(view, "Space") || strings.Contains(view, "-old\n\n+new") {
		t.Fatalf("diff page should render pager hints without composer:\n%s", view)
	}
	if got := strings.Join(m.renderDiffs(), "\n"); !strings.Contains(got, "+new") || strings.Contains(got, "[") {
		t.Fatalf("unexpected diff page content:\n%s", got)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "/diff") || strings.Contains(rendered, "+new") {
		t.Fatalf("expected transcript to contain only command echo, got:\n%s", rendered)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(model)
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("diff page should ignore text input, got %q", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.page != pageChat {
		t.Fatalf("expected esc to return to chat, got %v", m.page)
	}
}

func TestDiffPageUsesPagerKeys(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 12
	lines := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}
	m.setDiffText(strings.Join(lines, "\n"))
	m.page = pageDiff
	m.refreshViewportContent()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("expected j to scroll diff down")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected k to scroll diff up, offset=%d", m.viewport.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("expected pgdown to page diff down")
	}
	pagedOffset := m.viewport.YOffset

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	if m.viewport.YOffset != pagedOffset {
		t.Fatalf("space should not page diff, offset=%d want %d", m.viewport.YOffset, pagedOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(model)
	if m.viewport.YOffset != pagedOffset {
		t.Fatalf("ctrl+d should not half-page diff, offset=%d want %d", m.viewport.YOffset, pagedOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected home to jump to top, offset=%d", m.viewport.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if m.page != pageChat {
		t.Fatalf("expected q to close diff page, got %v", m.page)
	}

	m.page = pageDiff
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if m.page != pageDiff {
		t.Fatalf("ctrl+c should not close diff page, got %v", m.page)
	}
	if m.status != "Press Ctrl+C again to quit" {
		t.Fatalf("ctrl+c on idle diff page should arm global quit, got status %q", m.status)
	}
}

func TestDiffResultResetsPagerToTop(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 12

	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}
	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventDiffResult,
		Text: strings.Join(lines, "\n"),
	}))
	m = next.(model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if m.viewport.YOffset == 0 {
		t.Fatal("expected End to scroll the diff away from the top")
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind: service.EventDiffResult,
		Text: strings.Join(lines, "\n"),
	}))
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected a fresh diff to reset the pager to the top, offset=%d", m.viewport.YOffset)
	}
}

func TestDiffPageCtrlCInterruptsBusyTurn(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.busy = true
	m.page = pageDiff

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if m.page != pageDiff {
		t.Fatalf("ctrl+c interrupt should not close diff page, got %v", m.page)
	}
	if !m.busy || !m.stopping || m.status != "stopping" {
		t.Fatalf("expected busy diff page ctrl+c to interrupt, busy=%v stopping=%v status=%q", m.busy, m.stopping, m.status)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentShutdown {
		t.Fatalf("expected shutdown intent from diff page ctrl+c, got %+v", *intents)
	}
}

func TestWorktreeExitPromptEnterDispatchesSelectedAction(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modeWorktreeExit
	m.worktreeExit.summary = app.WorktreeExitSummary{
		Session:      app.WorktreeSession{Name: "feature", Path: "/tmp/repo/.whale/worktrees/feature", Branch: "worktree-feature"},
		ChangedFiles: 1,
	}
	m.worktreeExit.selected = 1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentWorktreeExitChoice || (*intents)[0].WorktreeAction != "remove" {
		t.Fatalf("unexpected intent: %+v", (*intents)[0])
	}
	if m.mode != modeChat || m.status != "removing worktree" {
		t.Fatalf("unexpected mode/status: %v %q", m.mode, m.status)
	}
}

func TestWorktreeExitSummaryIncludesIgnoredFiles(t *testing.T) {
	got := worktreeExitSummaryText(0, 1, 0)
	if !strings.Contains(got, "1 ignored file") {
		t.Fatalf("expected ignored file warning, got %q", got)
	}
}

func TestWorktreeExitPromptEscCancelsExit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modeWorktreeExit
	m.worktreeExit.summary = app.WorktreeExitSummary{Session: app.WorktreeSession{Name: "feature"}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	if (*intents)[0].Kind != service.IntentWorktreeExitChoice || (*intents)[0].WorktreeAction != "cancel" {
		t.Fatalf("unexpected intent: %+v", (*intents)[0])
	}
	if m.mode != modeChat || m.status != "exit canceled" {
		t.Fatalf("unexpected mode/status: %v %q", m.mode, m.status)
	}
}

func TestBusyQueuedPromptWaitsForPendingLocalSubmit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.status = "running"
	m.input.SetValue("/stats all")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/stats all" {
		t.Fatalf("expected busy local submit dispatch, got %+v", *intents)
	}

	m.input.SetValue("queued after stats")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "queued after stats" {
		t.Fatalf("expected prompt to queue while busy local submit is pending, got %+v", m.queuedPrompts)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "done",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("queued prompt should not start before local submit done, got %+v", *intents)
	}
	if !strings.Contains(m.status, "command") {
		t.Fatalf("expected wait status while local submit is pending, got %q", m.status)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	m = next.(model)
	if len(*intents) != 2 || (*intents)[1].Kind != service.IntentSubmit || (*intents)[1].Input != "queued after stats" {
		t.Fatalf("expected queued prompt to start after local submit done, got %+v", *intents)
	}
	if !m.busy {
		t.Fatal("expected queued prompt to start a turn")
	}
}

func TestTurnDoneShowsWaitStatusWhileLocalSubmitPendingWithoutQueue(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 14, busy: true, status: "running", localSubmitPending: 1}

	next, _ := m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "done",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	if m.busy {
		t.Fatal("expected turn completion to clear busy state")
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while local submit remains pending, got %q", m.status)
	}
}

func TestQueuedPromptWaitsForAllPendingLocalSubmits(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.localSubmitPending = 2
	m.status = "wait for command to finish"
	m.queuedPrompts = []queuedPrompt{{Text: "after two locals"}}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	m = next.(model)
	if m.localSubmitPending != 1 {
		t.Fatalf("expected one pending local submit left, got %d", m.localSubmitPending)
	}
	if len(*intents) != 0 {
		t.Fatalf("queued prompt should not start before all local submits finish, got %+v", *intents)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	m = next.(model)
	if m.localSubmitPending != 0 {
		t.Fatalf("expected all local submits cleared, got %d", m.localSubmitPending)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "after two locals" {
		t.Fatalf("expected queued prompt after final local submit done, got %+v", *intents)
	}
	if !m.busy {
		t.Fatal("expected queued prompt to start a turn")
	}
}

func TestEnterWhileBusyBlocksSlashCommandsWithoutQueueing(t *testing.T) {
	for _, cmd := range []string{
		"/resume",
		"/clear",
		"/new scratch",
		"/fork",
		"/fork scratch",
		"/model",
		"/skills",
		"/stats bad",
		"/compact bad",
		"/plan show",
		"/ask inspect the parser",
		"/plan propose a fix",
		"/compact",
		"/init",
		"/unknown",
	} {
		t.Run(cmd, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.busy = true
			m.status = "running"
			m.input.SetValue(cmd)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)

			if len(*intents) != 0 {
				t.Fatalf("expected no submitted intent while busy, got %+v", *intents)
			}
			if len(m.queuedPrompts) != 0 {
				t.Fatalf("expected local command not to be queued, got %+v", m.queuedPrompts)
			}
			if got := m.input.Value(); got != cmd {
				t.Fatalf("expected command to remain editable, got %q", got)
			}
			if !m.busy {
				t.Fatal("expected active turn to remain busy")
			}
			if !strings.Contains(m.status, "disabled while working") {
				t.Fatalf("expected disabled status, got %q", m.status)
			}
			gotTranscript := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
			if strings.Contains(gotTranscript, "disabled while") {
				t.Fatalf("blocked-command guidance should not be inserted into chat messages:\n%s", gotTranscript)
			}
			m.width = 100
			m.height = 24
			if view := m.View(); !strings.Contains(view, "disabled while working") {
				t.Fatalf("expected blocked-command guidance in busy status line:\n%s", view)
			}
		})
	}
}

func TestLocalSubmitBarrierBlocksPromptUntilDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/new scratch")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/new scratch" {
		t.Fatalf("expected /new local submit intent, got %+v", *intents)
	}
	if m.localSubmitPending != 1 {
		t.Fatal("expected mutating local submit to block later prompts")
	}
	if m.busy {
		t.Fatal("local submit barrier should not start working state")
	}

	m.input.SetValue("start a turn")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected prompt not to dispatch before local submit done, got %+v", *intents)
	}
	if got := m.input.Value(); got != "start a turn" {
		t.Fatalf("expected prompt to remain editable, got %q", got)
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while local submit is pending, got %q", m.status)
	}
	if m.busy {
		t.Fatal("prompt should not start a turn while local submit is pending")
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitDone, Metadata: map[string]any{service.EventMetadataLocalSubmit: true}}))
	m = next.(model)
	if m.localSubmitPending != 0 {
		t.Fatal("expected local submit barrier to clear after done event")
	}
	if m.status != "ready" {
		t.Fatalf("expected wait status to clear after local submit done, got %q", m.status)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 2 || (*intents)[1].Kind != service.IntentSubmit || (*intents)[1].Input != "start a turn" {
		t.Fatalf("expected prompt to dispatch after local submit done, got %+v", *intents)
	}
	if !m.busy {
		t.Fatal("expected prompt to start a turn after local submit done")
	}
}

func TestReadOnlyLocalSubmitBlocksPromptUntilDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14
	m.input.SetValue("/stats all")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmitLocal || (*intents)[0].Input != "/stats all" {
		t.Fatalf("expected /stats all local submit intent, got %+v", *intents)
	}
	if m.localSubmitPending != 1 {
		t.Fatal("expected idle read-only local submit to block later prompts")
	}
	if m.busy {
		t.Fatal("read-only local submit should not start working state")
	}

	m.input.SetValue("start a turn")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected prompt not to overtake local submit, got %+v", *intents)
	}
	if got := m.input.Value(); got != "start a turn" {
		t.Fatalf("expected prompt to remain editable, got %q", got)
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while read-only local submit is pending, got %q", m.status)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "Stats\n\nslow usage summary",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind: service.EventLocalSubmitDone,
		Metadata: map[string]any{
			service.EventMetadataLocalSubmit: true,
		},
	}))
	m = next.(model)
	if m.localSubmitPending != 0 {
		t.Fatal("expected read-only local submit to clear after done event")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 2 || (*intents)[1].Kind != service.IntentSubmit || (*intents)[1].Input != "start a turn" {
		t.Fatalf("expected prompt to dispatch after read-only local submit done, got %+v", *intents)
	}

	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
	statsIx := strings.Index(rendered, "slow usage summary")
	promptIx := strings.Index(rendered, "start a turn")
	if statsIx < 0 || promptIx < 0 || statsIx > promptIx {
		t.Fatalf("expected local result before later prompt:\n%s", rendered)
	}
}

func TestEnterWhileBusyWithEmptyInputDoesNotAppendPersistentNotice(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.status = "running"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected no queued prompt, got %+v", m.queuedPrompts)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected no submitted intent while busy, got %+v", *intents)
	}
	if !m.busy {
		t.Fatal("expected turn to remain busy")
	}
	if m.status != "running" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	if got := strings.Join(tuirender.ChatLines(m.assembler.Snapshot(), 80), "\n"); strings.Contains(got, "Agent is working") {
		t.Fatalf("empty enter while busy should not write a persistent notice:\n%s", got)
	}
}

func TestTurnDoneSubmitsOneQueuedPrompt(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.queuedPrompts = []queuedPrompt{{Text: "first queued"}, {Text: "second queued"}}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "first queued" {
		t.Fatalf("expected first queued prompt submitted, got %+v", *intents)
	}
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0].Text != "second queued" {
		t.Fatalf("expected one queued prompt left, got %+v", m.queuedPrompts)
	}
	if !m.busy || m.status != "running" {
		t.Fatalf("expected queued turn running, busy=%v status=%q", m.busy, m.status)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "first queued") {
		t.Fatalf("expected submitted queued prompt in transcript:\n%s", got)
	}
}

func TestQueuedPromptsSubmitFIFOAcrossTurns(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.queuedPrompts = []queuedPrompt{{Text: "first queued"}, {Text: "second queued"}}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)

	if len(*intents) != 2 {
		t.Fatalf("expected two submitted intents, got %+v", *intents)
	}
	if (*intents)[0].Input != "first queued" || (*intents)[1].Input != "second queued" {
		t.Fatalf("expected FIFO submit order, got %+v", *intents)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected queue drained, got %+v", m.queuedPrompts)
	}
	if !m.busy {
		t.Fatal("expected second queued turn to be running")
	}
}

func TestStoppingTurnDoneRestoresQueuedPromptsToComposer(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.stopping = true
	m.queuedPrompts = []queuedPrompt{{Text: "first queued"}, {Text: "second queued"}}
	m.input.SetValue("current draft")

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)

	if len(*intents) != 0 {
		t.Fatalf("expected no submitted intents after stopping, got %+v", *intents)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected queue restored and cleared, got %+v", m.queuedPrompts)
	}
	if got := m.input.Value(); got != "first queued\nsecond queued\ncurrent draft" {
		t.Fatalf("expected queued prompts restored to composer, got %q", got)
	}
	if m.busy || m.stopping {
		t.Fatalf("expected stopped turn idle, busy=%v stopping=%v", m.busy, m.stopping)
	}
}

func TestStoppingTurnDoneRestoresQueuedPromptsWithPendingWindowsPaste(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.stopping = true
	m.queuedPrompts = []queuedPrompt{{Text: "older queued"}}
	m.setWindowsPasteBuffer("pasted follow up")
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)
	m.windowsPaste.busyInput = true
	m.windowsPaste.busyInputStop = true
	flushID := m.windowsPaste.burstID

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)

	if len(*intents) != 0 {
		t.Fatalf("expected no submitted intents after stopping, got %+v", *intents)
	}
	if len(m.queuedPrompts) != 0 {
		t.Fatalf("expected queue restored and cleared, got %+v", m.queuedPrompts)
	}
	if got := m.input.Value(); got != "older queued\npasted follow up" {
		t.Fatalf("expected queued prompt and pending paste restored to composer, got %q", got)
	}
	if m.windowsPasteBuffer() != "" || !m.windowsPaste.activeUntil.IsZero() || m.windowsPaste.busyInput || m.windowsPaste.busyInputStop {
		t.Fatalf("expected pending paste state cleared after restore, got %+v", m.windowsPaste)
	}

	next, _ = m.Update(windowsPasteBurstFlushMsg{id: flushID})
	m = next.(model)
	if got := m.input.Value(); got != "older queued\npasted follow up" {
		t.Fatalf("stale paste flush should not mutate restored composer, got %q", got)
	}
}

func TestQueuedPromptSuppressesPlanImplementationPicker(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.queuedPrompts = []queuedPrompt{{Text: "queued follow up"}}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("queued prompt should suppress plan implementation picker")
	}
	if len(*intents) != 1 || (*intents)[0].Input != "queued follow up" {
		t.Fatalf("expected queued follow-up submitted, got %+v", *intents)
	}
}

func TestPlanImplementationPickerDefersUntilLocalSubmitDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.localSubmitPending = 1

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("plan implementation picker should wait for pending local submit")
	}
	if !m.deferredPlanPicker {
		t.Fatal("expected plan implementation picker to be deferred")
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while plan picker is deferred, got %q", m.status)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("pending local submit should block implementation intent, got %+v", *intents)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	m = next.(model)
	if m.mode != modePlanImplementation {
		t.Fatalf("expected deferred implementation picker after local submit done, got mode %v", m.mode)
	}
	if m.deferredPlanPicker {
		t.Fatal("expected deferred picker flag to clear after opening")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentImplementPlan {
		t.Fatalf("expected implementation intent after pending local submit clears, got %+v", *intents)
	}
}

func TestQueuedPromptSuppressesDeferredPlanImplementationPicker(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.localSubmitPending = 1
	m.queuedPrompts = []queuedPrompt{{Text: "queued follow up"}}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("plan implementation picker should not open before local submit done")
	}
	if !m.deferredPlanPicker {
		t.Fatal("expected plan picker to be deferred while local submit is pending")
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("queued prompt should suppress deferred implementation picker")
	}
	if m.deferredPlanPicker {
		t.Fatal("expected queued prompt to clear deferred implementation picker")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "queued follow up" {
		t.Fatalf("expected queued follow-up submitted after local submit done, got %+v", *intents)
	}
}

func TestRenderQueuedPromptsShowsPreviewLimit(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.queuedPrompts = []queuedPrompt{
		{Text: "first queued"},
		{Text: "second queued"},
		{Text: "third queued"},
		{Text: "fourth queued"},
	}

	view := m.renderQueuedPrompts(80)
	for _, want := range []string{"queued (4)", "first queued", "second queued", "third queued", "... 1 more"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected queued preview to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "fourth queued") {
		t.Fatalf("expected queued preview to hide fourth prompt:\n%s", view)
	}
}

func TestApprovalNoticeTextUsesDecisionAndSummary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.approval.reason = "shell_run: go test ./..."
	if got := m.approvalNoticeText("allow"); !strings.Contains(got, "You approved whale to run go test ./... this time") {
		t.Fatalf("unexpected allow notice: %q", got)
	}
	if got := m.approvalNoticeText("allow_session"); !strings.Contains(got, "for this session") {
		t.Fatalf("unexpected session notice: %q", got)
	}
	if got := m.approvalNoticeText("deny"); !strings.Contains(got, "You canceled the request to run go test ./...") {
		t.Fatalf("unexpected deny notice: %q", got)
	}
	if got := m.approvalNoticeText("cancel"); !strings.Contains(got, "You canceled the request to run go test ./...") {
		t.Fatalf("unexpected cancel notice: %q", got)
	}
}

func TestApprovalEscCancelsInsteadOfDenying(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"

	cmd := m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc approval handling to return no command")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentCancelToolApproval || (*intents)[0].ToolCallID != "tool-1" {
		t.Fatalf("expected esc to cancel approval, got %+v", *intents)
	}
	if m.mode != modeChat || m.status != "canceled" {
		t.Fatalf("expected approval cancel to return to chat canceled state, got mode=%v status=%q", m.mode, m.status)
	}
}

func TestApprovalEscRemovesPendingToolCallBeforeTurnDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 24
	m.busy = true
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"
	m.assembler.AddToolCall("tool-1", "shell_run", "Running date")
	m.markToolCallPending("tool-1")
	m.sawReasoningThisTurn = true

	_ = m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentCancelToolApproval {
		t.Fatalf("expected esc to cancel approval, got %+v", *intents)
	}
	if got := m.assembler.ToolCallText("tool-1"); got != "" {
		t.Fatalf("cancel should remove pending tool call before turn done, got %q", got)
	}
	if m.hasPendingToolCalls() {
		t.Fatalf("cancel should clear pending tool call state: %+v", m.pendingToolCalls)
	}
	if !m.sawTerminalToolOutcomeThisTurn {
		t.Fatal("cancel should mark the turn as terminal to suppress reasoning-only fallback")
	}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(rendered, "Running date") {
		t.Fatalf("canceled approval committed pending running row:\n%s", rendered)
	}
	if strings.Contains(rendered, "Reasoning only") || strings.Contains(rendered, "did not produce a visible answer") {
		t.Fatalf("approval cancel should suppress reasoning-only fallback:\n%s", rendered)
	}
	if !strings.Contains(rendered, "You canceled the request to run date") {
		t.Fatalf("expected cancel notice in transcript:\n%s", rendered)
	}
}

func TestApprovalDStillDenies(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"

	cmd := m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Fatal("expected deny approval handling to return no command")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentDenyTool || (*intents)[0].ToolCallID != "tool-1" {
		t.Fatalf("expected d to deny approval, got %+v", *intents)
	}
}

func TestTurnInterruptedNoticeText(t *testing.T) {
	m := newModel(nil, "", "", "")
	got := m.turnInterruptedNoticeText()
	if !strings.Contains(got, "Conversation interrupted") {
		t.Fatalf("unexpected interrupt notice: %q", got)
	}
}

func TestEscWhileBusyKeepsTurnBusyUntilTurnDone(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if !m.busy {
		t.Fatal("expected interrupted turn to remain busy until EventTurnDone")
	}
	if !m.stopping {
		t.Fatal("expected stopping state after interrupt")
	}
	if m.status != "stopping" {
		t.Fatalf("unexpected status: %q", m.status)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !m.busy || !m.stopping {
		t.Fatalf("enter during stopping should not start another turn, busy=%v stopping=%v", m.busy, m.stopping)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.busy || m.stopping {
		t.Fatalf("expected turn done to clear busy/stopping, busy=%v stopping=%v", m.busy, m.stopping)
	}
}

func TestEscInterruptDuringThinkingDoesNotShowReasoningOnly(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}

	// Simulate receiving reasoning (thinking) content
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventReasoningDelta, Text: "thinking..."}))
	m = next.(model)

	// User presses Esc to interrupt
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if !m.stopping {
		t.Fatal("expected stopping state after Esc interrupt")
	}

	// Stream ends
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.busy || m.stopping {
		t.Fatalf("expected turn done to clear state, busy=%v stopping=%v", m.busy, m.stopping)
	}

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Conversation interrupted") {
		t.Fatalf("expected interrupted notice in transcript:\n%s", rendered)
	}
	if strings.Contains(rendered, "Reasoning only") || strings.Contains(rendered, "did not produce a visible answer") {
		t.Fatalf("should not show reasoning-only message after intentional Esc interrupt:\n%s", rendered)
	}
}

func TestCtrlCWhileBusyClearsDraftAndPendingWindowsEnter(t *testing.T) {
	// After PR 2's Ctrl+C-precedence change, a non-empty composer while busy
	// resolves Ctrl+C to "clear the draft" rather than "interrupt the turn".
	// The clear path still tears down the Windows deferred-enter state via
	// resetWindowsPasteFallbackInputState, so a stale deferred enter cannot
	// fire after the user abandons the queued draft. Esc remains the
	// unconditional busy interrupt for users who also want to cancel the
	// running turn.
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.windowsPaste.enabled = true
	m.busy = true
	m.input.SetValue("queued prompt")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected enter while busy to arm deferred submit")
	}
	deferredID := m.windowsPaste.pendingEnterID

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if m.windowsPaste.pendingEnter {
		t.Fatal("expected ctrl+c clear path to drop pending windows enter")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+C with non-empty composer to clear draft, got %q", got)
	}
	if len(*intents) != 0 {
		t.Fatalf("expected Ctrl+C with non-empty composer not to interrupt the turn, got intents %+v", *intents)
	}
	if m.stopping {
		t.Fatal("expected Ctrl+C with non-empty composer not to mark the turn stopping")
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter should not submit after clear, got %+v", *intents)
	}
}

func TestCtrlCWhileBusyInterruptsWithoutArmingQuit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.busy = true
	m.quitArmedUntil = time.Now().Add(time.Second)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if !m.busy {
		t.Fatal("expected interrupted turn to remain busy until EventTurnDone")
	}
	if !m.stopping {
		t.Fatal("expected stopping state after ctrl+c interrupt")
	}
	if m.status != "stopping" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	if !m.quitArmedUntil.IsZero() {
		t.Fatal("expected ctrl+c while busy not to arm quit")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentShutdown {
		t.Fatalf("expected ctrl+c while busy to dispatch shutdown intent, got %+v", *intents)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if !m.quitArmedUntil.IsZero() {
		t.Fatal("expected repeated ctrl+c while stopping not to arm quit")
	}
	if len(*intents) != 1 {
		t.Fatalf("expected repeated ctrl+c while stopping not to dispatch another intent, got %+v", *intents)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	if m.busy || m.stopping {
		t.Fatalf("expected turn done to clear busy/stopping, busy=%v stopping=%v", m.busy, m.stopping)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	if m.quitArmedUntil.IsZero() {
		t.Fatal("expected first idle ctrl+c after interrupt to arm quit")
	}
	if len(*intents) != 1 {
		t.Fatalf("expected idle ctrl+c to avoid shutdown after stale arm was cleared, got %+v", *intents)
	}
}

func TestCtrlCWhileBusyInterruptsBeforeApprovalMode(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.busy = true
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)

	if !m.stopping {
		t.Fatal("expected ctrl+c in busy approval mode to interrupt the turn")
	}
	if m.mode != modeChat {
		t.Fatalf("expected interrupt to leave approval mode, got %v", m.mode)
	}
	if len(*intents) != 2 ||
		(*intents)[0].Kind != service.IntentCancelToolApproval ||
		(*intents)[0].ToolCallID != "tool-1" ||
		(*intents)[1].Kind != service.IntentShutdown {
		t.Fatalf("expected cancel approval then shutdown intents, got %+v", *intents)
	}
}

func TestCtrlCWhileBusyInterruptsBeforeUserInputMode(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.busy = true
	m.mode = modeUserInput
	m.userInput.toolCallID = "tool-1"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)

	if !m.stopping {
		t.Fatal("expected ctrl+c in busy user-input mode to interrupt the turn")
	}
	if m.mode != modeChat {
		t.Fatalf("expected interrupt to leave user-input mode, got %v", m.mode)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentShutdown {
		t.Fatalf("expected user input interrupt to dispatch shutdown only, got %+v", *intents)
	}
}

func TestEscWhileBusyUserInputInterruptsTurn(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.busy = true
	m.mode = modeUserInput
	m.userInput.toolCallID = "tool-1"
	m.userInput.questions = []core.UserInputQuestion{{
		Header:   "Scope",
		ID:       "scope",
		Question: "Continue?",
		Options:  []core.UserInputOption{{Label: "Yes", Description: "Proceed."}},
	}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if !m.stopping {
		t.Fatal("expected esc in busy user-input mode to interrupt the turn")
	}
	if m.mode != modeChat {
		t.Fatalf("expected interrupt to leave user-input mode, got %v", m.mode)
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentShutdown {
		t.Fatalf("expected esc user input interrupt to dispatch shutdown only, got %+v", *intents)
	}
}

func TestCtrlCWhileStoppingClearsBlockingModalWithoutDuplicateShutdown(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*model)
	}{
		{
			name: "approval",
			setup: func(m *model) {
				m.mode = modeApproval
				m.approval.toolCallID = "approval-queued"
			},
		},
		{
			name: "user input",
			setup: func(m *model) {
				m.mode = modeUserInput
				m.userInput.toolCallID = "input-queued"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.svc = &service.Service{}
			m.width = 80
			m.height = 24
			m.busy = true
			m.stopping = true
			m.status = "stopping"
			tt.setup(&m)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			m = next.(model)

			if m.mode != modeChat {
				t.Fatalf("expected repeated ctrl+c while stopping to clear blocking mode, got %v", m.mode)
			}
			if !m.busy || !m.stopping {
				t.Fatalf("expected turn to keep stopping, busy=%v stopping=%v", m.busy, m.stopping)
			}
			if len(*intents) != 0 {
				t.Fatalf("expected no duplicate shutdown or modal intent while already stopping, got %+v", *intents)
			}
		})
	}
}

func TestStaleBlockingEventsWhileStoppingDoNotOpenModals(t *testing.T) {
	tests := []struct {
		name       string
		ev         service.Event
		wantIntent service.IntentKind
	}{
		{
			name: "approval",
			ev: service.Event{
				Kind:       service.EventApprovalRequired,
				ToolCallID: "approval-stale",
				ToolName:   "shell_run",
				Text:       "shell_run: sleep 30",
			},
			wantIntent: service.IntentCancelToolApproval,
		},
		{
			name: "user input",
			ev: service.Event{
				Kind:       service.EventUserInputRequired,
				ToolCallID: "input-stale",
				ToolName:   "request_user_input",
				Questions: []core.UserInputQuestion{{
					Header:   "Choice",
					ID:       "choice",
					Question: "Continue?",
				}},
			},
			wantIntent: service.IntentCancelUserInput,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, intents := newModelWithDispatchSpy()
			m.busy = true
			m.stopping = true
			m.mode = modeChat
			m.status = "stopping"

			next, _ := m.Update(svcMsg(tt.ev))
			m = next.(model)

			if m.mode != modeChat {
				t.Fatalf("expected stale event while stopping to leave chat mode, got %v", m.mode)
			}
			if m.status != "stopping" {
				t.Fatalf("expected stale event while stopping to preserve status, got %q", m.status)
			}
			if len(*intents) != 1 {
				t.Fatalf("expected stale event while stopping to resolve interaction, got %+v", *intents)
			}
			if got := (*intents)[0]; got.Kind != tt.wantIntent || got.ToolCallID != tt.ev.ToolCallID {
				t.Fatalf("unexpected stale interaction intent: %+v", got)
			}
		})
	}
}

func TestTurnDoneClearsStaleBlockingModal(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*model)
	}{
		{
			name: "approval",
			setup: func(m *model) {
				m.mode = modeApproval
				m.approval.toolCallID = "approval-stale"
			},
		},
		{
			name: "user input",
			setup: func(m *model) {
				m.mode = modeUserInput
				m.userInput.toolCallID = "input-stale"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(nil, "", "", "")
			m.busy = true
			m.stopping = true
			m.status = "stopping"
			tt.setup(&m)

			next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
			m = next.(model)

			if m.mode != modeChat {
				t.Fatalf("expected turn done to clear stale blocking mode, got %v", m.mode)
			}
			if m.busy || m.stopping {
				t.Fatalf("expected turn done to clear busy/stopping, busy=%v stopping=%v", m.busy, m.stopping)
			}
			if m.status != "ready" {
				t.Fatalf("expected ready status after turn done, got %q", m.status)
			}
		})
	}
}

func TestPlanCompletedReplacesPartialPlanAndTurnDoneShowsPicker(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      true,
		status:    "working",
	}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventPlanDelta, Text: "partial"}))
	m = next.(model)
	liveRendered := strings.Join(tuirender.ChatLines(m.assembler.Snapshot(), 80), "\n")
	if !strings.Contains(liveRendered, "Proposed Plan") || !strings.Contains(liveRendered, "partial") {
		t.Fatalf("expected live proposed plan render, got %q", liveRendered)
	}
	next, cmd := m.Update(svcMsg(service.Event{Kind: service.EventPlanCompleted, Text: "complete final plan"}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	if snap := m.assembler.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected completed plan to leave live assembler empty, got %+v", snap)
	}
	if len(m.transcript) != 1 || m.transcript[0].Kind != tuirender.KindPlan {
		t.Fatalf("expected completed plan in transcript, got %+v", m.transcript)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Proposed Plan") || !strings.Contains(rendered, "complete final plan") {
		t.Fatalf("expected rendered proposed plan, got %q", rendered)
	}
	if m.lastProposedPlan != "complete final plan" {
		t.Fatalf("expected last proposed plan to be captured, got %q", m.lastProposedPlan)
	}
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone, LastResponse: "done"}))
	m = next.(model)

	if m.mode != modePlanImplementation {
		t.Fatalf("expected implementation picker, got mode %v", m.mode)
	}
	if m.busy {
		t.Fatal("expected busy=false after turn done")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected completed turn to move plan out of live assembler, got %+v", snap)
	}
}

func TestPlanImplementationIntentDoesNotEmbedLastProposedPlan(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePlanImplementation
	m.planImplementation.index = 0
	m.lastProposedPlan = "# Plan\n- Patch it"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentImplementPlan {
		t.Fatalf("expected implement intent, got %+v", *intents)
	}
	if (*intents)[0].Input != "" {
		t.Fatalf("expected implement intent to avoid embedding plan text, got %q", (*intents)[0].Input)
	}
	if m.chatMode != "agent" {
		t.Fatalf("expected chat mode switched to agent, got %q", m.chatMode)
	}
}

func TestPlanImplementationNoDeclinesAndClearsPendingPlan(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePlanImplementation
	m.chatMode = "plan"
	m.planImplementation.index = 1
	m.lastProposedPlan = "# Plan\n- Patch it"
	m.sawPlanThisTurn = true
	m.deferredPlanPicker = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentDeclinePlan {
		t.Fatalf("expected decline intent, got %+v", *intents)
	}
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after decline popup, got %v", m.mode)
	}
	if m.chatMode != "plan" {
		t.Fatalf("decline should stay in plan chat mode, got %q", m.chatMode)
	}
	if m.lastProposedPlan != "" || m.sawPlanThisTurn || m.deferredPlanPicker || m.planImplementation.index != 0 {
		t.Fatalf("expected stale plan state cleared, last=%q saw=%v deferred=%v index=%d", m.lastProposedPlan, m.sawPlanThisTurn, m.deferredPlanPicker, m.planImplementation.index)
	}
}

func TestPlanImplementationEscDeclinesAndStaysInPlanMode(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePlanImplementation
	m.chatMode = "plan"
	m.lastProposedPlan = "# Plan\n- Patch it"
	m.sawPlanThisTurn = true
	m.deferredPlanPicker = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentDeclinePlan {
		t.Fatalf("expected decline intent, got %+v", *intents)
	}
	if m.mode != modeChat || m.chatMode != "plan" {
		t.Fatalf("expected esc decline to close popup and stay in plan mode, mode=%v chatMode=%q", m.mode, m.chatMode)
	}
	if m.lastProposedPlan != "" || m.sawPlanThisTurn || m.deferredPlanPicker {
		t.Fatalf("expected stale plan state cleared, last=%q saw=%v deferred=%v", m.lastProposedPlan, m.sawPlanThisTurn, m.deferredPlanPicker)
	}
}

func TestPlanUpdateEventRendersUpdatedPlan(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
	}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventPlanUpdate, Text: "[x] Inspect\n[~] Patch\n[ ] Test"}))
	m = next.(model)
	if len(m.transcript) != 0 {
		t.Fatalf("plan update should wait for tool result before committing transcript, got %+v", m.transcript)
	}
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "plan-1", ToolName: "update_plan", Text: `{"success":true}`}))
	m = next.(model)
	if len(m.transcript) != 1 || m.transcript[0].Kind != tuirender.KindPlanUpdate {
		t.Fatalf("expected plan update in transcript, got %+v", m.transcript)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Updated Plan") || !strings.Contains(rendered, "Patch") {
		t.Fatalf("expected rendered updated plan, got %q", rendered)
	}
}

func TestPlanUpdateDoesNotClearPendingToolCallsBeforeResult(t *testing.T) {
	m := model{
		assembler:        tuirender.NewAssembler(),
		mode:             modeChat,
		pendingToolCalls: map[string]struct{}{},
	}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventToolCall, ToolCallID: "read-1", ToolName: "read_file", Text: `read_file: docs/plugins.md`}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolCall, ToolCallID: "plan-1", ToolName: "update_plan", Text: `update_plan: 2 step(s)`}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventPlanUpdate, Text: "[x] Inspect\n[ ] Report"}))
	m = next.(model)

	if _, ok := m.pendingToolCalls["read-1"]; !ok {
		t.Fatalf("plan update event cleared unrelated pending tool calls: %+v", m.pendingToolCalls)
	}
	if _, ok := m.pendingToolCalls["plan-1"]; ok {
		t.Fatalf("update_plan should not create a pending tool row")
	}
	if len(m.transcript) != 0 {
		t.Fatalf("plan update should remain live while another tool is pending, got %+v", m.transcript)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "plan-1", ToolName: "update_plan", Text: `{"success":true}`}))
	m = next.(model)
	if len(m.transcript) != 0 {
		t.Fatalf("update_plan result should not commit while read tool is pending, got %+v", m.transcript)
	}

	readResult := `{"success":true,"data":{"content":"ok"},"metadata":{"duration_ms":1}}`
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "read-1", ToolName: "read_file", Text: readResult}))
	m = next.(model)
	if len(m.transcript) != 2 {
		t.Fatalf("expected read result and plan update committed together, got %+v", m.transcript)
	}
	if m.transcript[0].Text == "Updating plan" || strings.Contains(m.transcript[0].Text, "Updating plan") {
		t.Fatalf("stale update_plan tool row was committed: %+v", m.transcript)
	}
	if m.transcript[1].Kind != tuirender.KindPlanUpdate {
		t.Fatalf("expected plan update after pending tool resolves, got %+v", m.transcript)
	}
}

func TestStalePlanCompletionDoesNotOpenPickerOnLaterTurnDone(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      false,
		status:    "ready",
	}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventPlanCompleted, Text: "complete final plan"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone, LastResponse: "status"}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("stale plan completion opened implementation picker")
	}
	if m.sawPlanThisTurn {
		t.Fatal("expected stale plan flag to reset after turn done")
	}
}

func TestPlanCompletedWithoutDeltasStillRendersPlan(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      true,
	}
	plan := strings.Repeat("final plan\n", 100)
	next, cmd := m.Update(svcMsg(service.Event{Kind: service.EventPlanCompleted, Text: plan}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	if snap := m.assembler.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected final plan to leave live assembler empty, got %+v", snap)
	}
	if len(m.transcript) != 1 || m.transcript[0].Kind != tuirender.KindPlan {
		t.Fatalf("expected final plan in transcript, got %+v", m.transcript)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Proposed Plan") || !strings.Contains(rendered, "final plan") {
		t.Fatalf("expected rendered proposed plan, got %q", rendered)
	}
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone, LastResponse: "done"}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected final plan to be committed and live assembler cleared, got %+v", snap)
	}
	if m.mode != modePlanImplementation {
		t.Fatalf("expected implementation picker, got mode %v", m.mode)
	}
}

func TestHydrateSessionMessages_RestoresProposedPlanStyle(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "drafting...\n<proposed_plan>\n# Plan\n- Patch renderer\n</proposed_plan>",
		},
	}
	m.hydrateSessionMessages(msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 2 || snap[0].Kind != tuirender.KindText || snap[1].Kind != tuirender.KindPlan {
		t.Fatalf("expected assistant text and proposed plan, got %+v", snap)
	}
	rendered := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	for _, want := range []string{"drafting", "Proposed Plan", "Patch renderer"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in hydrated proposed plan:\n%s", want, rendered)
		}
	}
}

func TestScrollbackTextRendersUserMessage(t *testing.T) {
	m := model{width: 80, height: 24}
	got := m.scrollbackText([]tuirender.UIMessage{{
		Role: "you",
		Kind: tuirender.KindText,
		Text: "hello whale",
	}})
	if !strings.Contains(got, "hello whale") {
		t.Fatalf("expected user text in scrollback output, got %q", got)
	}
}

func TestCommitLiveTranscriptClearsAssembler(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), width: 80, height: 24}
	m.append("assistant", "streamed answer")
	m.commitLiveTranscript(true)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected live assembler cleared after commit, got %d entries", got)
	}
	if len(m.transcript) != 1 || m.transcript[0].Text != "streamed answer" {
		t.Fatalf("expected committed transcript entry, got %+v", m.transcript)
	}
}

func TestAssistantDeltaKeepsMultilineBlockLiveUntilBoundary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "stable line\nlive tail"}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 || snap[0].Text != "stable line\nlive tail" {
		t.Fatalf("expected newline-delimited assistant content to stay in one live message, got %+v", snap)
	}
	view := m.View()
	if !strings.Contains(view, "stable line") {
		t.Fatalf("expected first line to remain in the same live block:\n%s", view)
	}
	if !strings.Contains(view, "live tail") {
		t.Fatalf("expected tail to remain in the same live block:\n%s", view)
	}
}

func TestReasoningDeltaKeepsSingleThinkingCardAcrossNewlines(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventReasoningDelta, Text: "first thought\n\nsecond thought\nthird thought"}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 || snap[0].Role != "think" {
		t.Fatalf("expected reasoning content to stay in one live thinking message, got %+v", snap)
	}
	lines := m.renderChatLines(80)
	joined := strings.Join(lines, "\n")
	if got := strings.Count(joined, "Thinking"); got != 1 {
		t.Fatalf("expected one thinking card, got %d:\n%s", got, joined)
	}
	for _, want := range []string{"first thought", "second thought", "third thought"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in thinking card:\n%s", want, joined)
		}
	}
}

func TestSessionHydrationCommitsTranscriptAndClearsLiveAssembler(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24}
	next, cmd := m.Update(svcMsg(service.Event{
		Kind: service.EventSessionHydrated,
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "hi"},
			{Role: core.RoleAssistant, Text: "hello"},
		},
	}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected hydration to return wait-event command")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected hydrated transcript committed out of live assembler, got %d entries", got)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "hi") || !strings.Contains(got, "hello") {
		t.Fatalf("expected hydrated messages in transcript:\n%s", got)
	}
}

func TestChatIdleViewDoesNotRenderEmptyViewportFrame(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	view := m.View()
	if strings.Contains(view, "┌") {
		t.Fatalf("idle chat view should not render an empty bordered viewport:\n%s", view)
	}
	if !strings.Contains(view, "Type message or command") {
		t.Fatalf("expected composer placeholder in idle view:\n%s", view)
	}
	if strings.Contains(view, "status: ready") {
		t.Fatalf("idle view should not render ready status in footer:\n%s", view)
	}
	if strings.Contains(view, "Working (") || strings.Contains(view, "Stopping (") {
		t.Fatalf("idle view should not render busy status line:\n%s", view)
	}
}

func TestChatFooterFollowsContentAfterSlashSuggestionsClose(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "normal", "on")
	m.width = 80
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(model)
	withSlash := m.View()
	assertFooterLastLine(t, withSlash, "deepseek-v4-pro . normal")
	assertFooterLastLine(t, withSlash, "whale")
	assertFooterLastLineNotContains(t, withSlash, "dir:")
	if !strings.Contains(withSlash, "Tab/Enter pick") {
		t.Fatalf("expected slash suggestions while / is present:\n%s", withSlash)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	afterDelete := m.View()
	assertFooterLastLine(t, afterDelete, "deepseek-v4-pro . normal")
	assertFooterLastLine(t, afterDelete, "whale")
	assertFooterLastLineNotContains(t, afterDelete, "dir:")
	if strings.Contains(afterDelete, "Tab/Enter pick") {
		t.Fatalf("expected slash suggestions to disappear after deleting /:\n%s", afterDelete)
	}
	if got := countVisibleLines(afterDelete); got >= m.height {
		t.Fatalf("expected short chat view to use natural height below terminal height %d, got %d:\n%s", m.height, got, afterDelete)
	}
}

func TestChatFooterShowsWindowsDirectoryTail(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 80
	m.height = 24
	m.cwd = `C:\Users\goranka`

	view := m.View()
	assertFooterLastLine(t, view, `goranka`)
	assertFooterLastLineNotContains(t, view, ` ~`)
}

func TestChatFooterShowsAutoAcceptOnlyWhenEnabled(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale"

	view := m.View()
	assertFooterLastLineNotContains(t, view, "auto-accept on")

	m.autoAccept = true
	view = m.View()
	assertFooterLastLine(t, view, "auto-accept on")
}

func TestSessionHydratedUpdatesAutoAcceptFooterState(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 24
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventSessionHydrated, AutoAccept: true, AutoAcceptKnown: true}))
	m = next.(model)
	if !m.autoAccept {
		t.Fatal("expected hydrated auto-accept state")
	}
	assertFooterLastLine(t, m.View(), "auto-accept on")
}

func TestChatFooterShowsGitBranchInsteadOfScrollHint(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale-footer-branch-display"
	m.gitBranch = "feat/footer-branch"

	view := m.View()
	assertFooterLastLine(t, view, "footer-branch-display")
	assertFooterLastLine(t, view, "feat/footer-branch")
	assertFooterLastLineNotContains(t, view, "PgUp/PgDn scroll")
}

func TestChatFooterOmitsEmptyGitBranch(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale-footer-branch-display"

	view := m.View()
	assertFooterLastLine(t, view, "whale-footer-branch-display")
	assertFooterLastLineNotContains(t, view, "PgUp/PgDn scroll")
}

func TestChatFooterLongGitBranchDoesNotHideDirectory(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 80
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale-footer-branch-display"
	m.gitBranch = "feat/this-is-an-extremely-long-branch-name-that-cannot-fit-in-the-footer"

	view := m.View()
	assertFooterLastLine(t, view, "footer-branch-display")
	assertFooterLastLineNotContains(t, view, m.gitBranch)
	assertFooterLastLineNotContains(t, view, "PgUp/PgDn scroll")
}

func TestChatFooterKeepsFocusIndicatorWithGitBranch(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 24
	m.viewMode = app.ViewModeFocus
	m.cwd = "/Users/goranka/Engineer/ai/dsk/whale-output-mouse-copy"
	m.gitBranch = "feat/footer-branch"

	view := m.View()
	assertFooterLastLine(t, view, "focus")
	assertFooterLastLine(t, view, "feat/footer-branch")
	assertFooterLastLineNotContains(t, view, "PgUp/PgDn scroll")
}

func TestGitBranchUpdatedIgnoresStaleCwd(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.cwdPath = "/tmp/current"
	next, _ := m.Update(gitBranchUpdatedMsg{cwd: "/tmp/old", branch: "old"})
	m = next.(model)
	if m.gitBranch != "" {
		t.Fatalf("expected stale branch update to be ignored, got %q", m.gitBranch)
	}

	next, _ = m.Update(gitBranchUpdatedMsg{cwd: "/tmp/current", branch: "current"})
	m = next.(model)
	if m.gitBranch != "current" {
		t.Fatalf("expected current branch update, got %q", m.gitBranch)
	}
}

func TestDetectGitBranch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-b", "feat/footer-branch")

	if got := detectGitBranch(dir); got != "feat/footer-branch" {
		t.Fatalf("expected branch %q, got %q", "feat/footer-branch", got)
	}
}

func TestShellToolResultRefreshesGitBranch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-B", "whale-test-base")
	runGit(t, dir, "checkout", "-b", "feat/after-shell")

	m := newModel(nil, "", "", "")
	m.cwdPath = dir
	cmd, _, _ := m.handleServiceEvent(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-shell",
		ToolName:   "shell_run",
		Text:       `{"success":true,"code":"ok","data":{"status":"ok","metrics":{"exit_code":0},"payload":{"command":"git checkout -b feat/after-shell","stdout":"","stderr":""}}}`,
	})
	if cmd == nil {
		t.Fatal("expected shell tool result to schedule git branch refresh")
	}
	msg, ok := cmd().(gitBranchUpdatedMsg)
	if !ok {
		t.Fatalf("expected gitBranchUpdatedMsg, got %T", msg)
	}
	if msg.cwd != dir {
		t.Fatalf("expected cwd %q, got %q", dir, msg.cwd)
	}
	if msg.branch != "feat/after-shell" {
		t.Fatalf("expected refreshed branch %q, got %q", "feat/after-shell", msg.branch)
	}
}

func TestDetectGitBranchNonGitDirectory(t *testing.T) {
	requireGit(t)
	if got := detectGitBranch(t.TempDir()); got != "" {
		t.Fatalf("expected no branch outside git repo, got %q", got)
	}
}

func TestDetectGitBranchDetachedHead(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-B", "whale-test-base")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	runGit(t, dir, "checkout", "--detach", "HEAD")

	if got := detectGitBranch(dir); got != "" {
		t.Fatalf("expected no branch in detached HEAD, got %q", got)
	}
}

func TestChatTranscriptRetainsLocalCommandResultsAcrossSubmits(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14
	localInfo := func(text string) service.Event {
		return service.Event{Kind: service.EventLocalSubmitResult, Status: "info", Text: text}
	}
	localDone := func() service.Event {
		return service.Event{Kind: service.EventLocalSubmitDone, Metadata: map[string]any{service.EventMetadataLocalSubmit: true}}
	}
	next, _ := m.Update(svcMsg(localInfo("MCP\n\nconfig: /tmp/mcp.json servers: none")))
	m = next.(model)
	next, _ = m.Update(svcMsg(localDone()))
	m = next.(model)

	m.input.SetValue("/status")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(svcMsg(localInfo("Status\n\nsession: test-session")))
	m = next.(model)
	next, _ = m.Update(svcMsg(localDone()))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	for _, want := range []string{"config: /tmp/mcp.json", "/status", "session: test-session"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected transcript to retain %q:\n%s", want, got)
		}
	}
}

func TestLocalSlashCommandsEchoBeforeResults(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 18
	localInfo := func(text string) service.Event {
		return service.Event{Kind: service.EventLocalSubmitResult, Status: "info", Text: text}
	}
	localDone := func() service.Event {
		return service.Event{Kind: service.EventLocalSubmitDone, Metadata: map[string]any{service.EventMetadataLocalSubmit: true}}
	}

	m.input.SetValue("/mcp")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(svcMsg(localInfo("MCP\n\nconfig: /tmp/mcp.json servers: none")))
	m = next.(model)
	next, _ = m.Update(svcMsg(localDone()))
	m = next.(model)

	m.input.SetValue("/status")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(svcMsg(localInfo("Status\n\nsession: test-session")))
	m = next.(model)
	next, _ = m.Update(svcMsg(localDone()))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	for _, want := range []string{"/mcp", "config: /tmp/mcp.json", "/status", "session: test-session"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected transcript to contain %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "/mcp") > strings.Index(got, "config: /tmp/mcp.json") {
		t.Fatalf("expected /mcp before its result:\n%s", got)
	}
	if strings.Index(got, "/status") > strings.Index(got, "session: test-session") {
		t.Fatalf("expected /status before its result:\n%s", got)
	}
}

func TestHelpInfoRendersAsListInsteadOfCollapsedParagraph(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 100
	m.height = 30

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitResult, Status: "info", Text: app.BuildHelpText()}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(got, "/agent Switch to agent mode /ask") {
		t.Fatalf("expected help commands to render as a list, got:\n%s", got)
	}
	for _, want := range []string{"Whale help", "Browse default commands:", "/agent", "Switch to agent mode", "/feedback", "For more help:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected help output to contain %q:\n%s", want, got)
		}
	}
}

func TestNewSessionLocalResultRendersAsNotice(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14
	next, _ := m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "New session\n\nsession:  fresh\nprevious: old\nresume:   whale resume old\nmode:     agent",
	}))
	m = next.(model)
	if len(m.transcript) < 1 {
		t.Fatalf("expected session notice, got %+v", m.transcript)
	}
	got := m.transcript[len(m.transcript)-1]
	if got.Role != "notice" || got.Kind != tuirender.KindNotice {
		t.Fatalf("expected session notice kind, got role=%q kind=%q text=%q", got.Role, got.Kind, got.Text)
	}
}

func TestStatusLocalResultRendersAsStructuredTranscriptEntry(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14

	m.input.SetValue("/status")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "Status\n\n- session: test-session",
		LocalResult: &app.LocalResult{
			Kind:      "status",
			Title:     "Status",
			PlainText: "Status\n\n- session: test-session",
			Fields: []app.LocalResultField{
				{Label: "Session", Value: "test-session"},
				{Label: "Mode", Value: "agent", Tone: "info"},
			},
		},
	}))
	m = next.(model)

	if len(m.transcript) < 2 {
		t.Fatalf("expected command echo and status result, got %+v", m.transcript)
	}
	got := m.transcript[len(m.transcript)-1]
	if got.Kind != tuirender.KindLocalStatus || got.Role != "local_status" || got.Local == nil {
		t.Fatalf("expected local status transcript entry, got role=%q kind=%q local=%+v", got.Role, got.Kind, got.Local)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	for _, want := range []string{"/status", "Status", "Session", "test-session", "Mode", "agent"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered status transcript to contain %q:\n%s", want, rendered)
		}
	}
}

func TestBusyStatusLocalResultRendersAsStructuredLiveEntry(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14
	m.busy = true

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "working"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "Status\n\n- session: test-session\n- mode: agent",
		LocalResult: &app.LocalResult{
			Kind:      "status",
			Title:     "Status",
			PlainText: "Status\n\n- session: test-session\n- mode: agent",
			Fields: []app.LocalResultField{
				{Label: "Session", Value: "test-session"},
				{Label: "Mode", Value: "agent", Tone: "info"},
			},
		},
	}))
	m = next.(model)

	snap := m.assembler.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected assistant and local status in live assembler, got %+v", snap)
	}
	if got := snap[1]; got.Kind != tuirender.KindLocalStatus || got.Role != "local_status" || got.Local == nil {
		t.Fatalf("expected structured local status live entry, got role=%q kind=%q local=%+v", got.Role, got.Kind, got.Local)
	}
	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
	workingIx := strings.Index(rendered, "working")
	statusIx := strings.Index(rendered, "test-session")
	if workingIx < 0 || statusIx < 0 || workingIx > statusIx {
		t.Fatalf("expected live assistant output before structured status card:\n%s", rendered)
	}
}

func TestMCPLocalResultRendersAsStructuredTranscriptEntry(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14

	m.input.SetValue("/mcp")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "MCP\n\nconfig: /tmp/mcp.json\nservers: 1",
		LocalResult: &app.LocalResult{
			Kind:      "mcp",
			Title:     "MCP",
			PlainText: "MCP\n\nconfig: /tmp/mcp.json\nservers: 1",
			Fields: []app.LocalResultField{
				{Label: "Config", Value: "/tmp/mcp.json"},
				{Label: "Servers", Value: "1", Tone: "info"},
			},
			Sections: []app.LocalResultSection{{
				Title: "fs",
				Fields: []app.LocalResultField{
					{Label: "Status", Value: "failed", Tone: "error"},
					{Label: "Error", Value: "timeout", Tone: "error"},
				},
			}},
		},
	}))
	m = next.(model)

	if len(m.transcript) < 2 {
		t.Fatalf("expected command echo and mcp result, got %+v", m.transcript)
	}
	got := m.transcript[len(m.transcript)-1]
	if got.Kind != tuirender.KindLocalMCP || got.Role != "local_mcp" || got.Local == nil {
		t.Fatalf("expected local mcp transcript entry, got role=%q kind=%q local=%+v", got.Role, got.Kind, got.Local)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	for _, want := range []string{"/mcp", "MCP", "Config", "/tmp/mcp.json", "fs", "failed", "timeout"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered mcp transcript to contain %q:\n%s", want, rendered)
		}
	}
}

func TestBusyMCPLocalResultRendersAsStructuredLiveEntry(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14
	m.busy = true

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "working"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "MCP\n\nconfig: /tmp/mcp.json\nservers: 1",
		LocalResult: &app.LocalResult{
			Kind:      "mcp",
			Title:     "MCP",
			PlainText: "MCP\n\nconfig: /tmp/mcp.json\nservers: 1",
			Fields: []app.LocalResultField{
				{Label: "Config", Value: "/tmp/mcp.json"},
				{Label: "Servers", Value: "1", Tone: "info"},
			},
		},
	}))
	m = next.(model)

	snap := m.assembler.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected assistant and local mcp in live assembler, got %+v", snap)
	}
	if got := snap[1]; got.Kind != tuirender.KindLocalMCP || got.Role != "local_mcp" || got.Local == nil {
		t.Fatalf("expected structured local mcp live entry, got role=%q kind=%q local=%+v", got.Role, got.Kind, got.Local)
	}
	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
	workingIx := strings.Index(rendered, "working")
	mcpIx := strings.Index(rendered, "/tmp/mcp.json")
	if workingIx < 0 || mcpIx < 0 || workingIx > mcpIx {
		t.Fatalf("expected live assistant output before structured mcp card:\n%s", rendered)
	}
}

func TestLocalCommandResultCommitsIdleAssemblerBeforeNextPrompt(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14

	next, _ := m.Update(svcMsg(service.Event{
		Kind: service.EventInfo,
		Text: "Startup notice",
	}))
	m = next.(model)
	if got := len(m.assembler.Snapshot()); got == 0 {
		t.Fatal("expected idle info event to leave live assembler content")
	}

	m.input.SetValue("/status")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "Status\n\nsession: test-session",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind: service.EventLocalSubmitDone,
		Metadata: map[string]any{
			service.EventMetadataLocalSubmit: true,
		},
	}))
	m = next.(model)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected local result to commit idle live assembler, got %d live entries", got)
	}

	m.input.SetValue("next prompt")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
	noticeIx := strings.Index(rendered, "Startup notice")
	statusIx := strings.Index(rendered, "session: test-session")
	promptIx := strings.Index(rendered, "next prompt")
	if noticeIx < 0 || statusIx < 0 || promptIx < 0 || !(noticeIx < statusIx && statusIx < promptIx) {
		t.Fatalf("expected idle live content and local result before next prompt:\n%s", rendered)
	}
}

func TestLocalCommandResultPreservesLiveTurnOrder(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14
	localInfo := service.Event{Kind: service.EventLocalSubmitResult, Status: "info", Text: "Stats\n\nusage summary"}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "streamed answer"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(localInfo))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
	streamedIx := strings.Index(rendered, "streamed answer")
	statsIx := strings.Index(rendered, "usage summary")
	if streamedIx < 0 || statsIx < 0 || streamedIx > statsIx {
		t.Fatalf("expected live assistant output before local result:\n%s", rendered)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "streamed answer with final reconciliation",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)
	rendered = strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	streamedIx = strings.Index(rendered, "streamed answer")
	statsIx = strings.Index(rendered, "usage summary")
	tailIx := strings.Index(rendered, "with final reconciliation")
	if streamedIx < 0 || statsIx < 0 || tailIx < 0 || !(streamedIx < statsIx && statsIx < tailIx) {
		t.Fatalf("expected assistant prefix, local result, then recovered assistant tail:\n%s", rendered)
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected local result to leave live assembler empty after reconciliation, got %+v", m.assembler.Snapshot())
	}
}

func TestFinalAssistantDroppedTailPreservesToolOrder(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible assistant"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "visible assistant recovered tail",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	prefixIx := strings.Index(rendered, "visible assistant")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	tailIx := strings.Index(rendered, "recovered tail")
	if prefixIx < 0 || toolIx < 0 || tailIx < 0 || !(prefixIx < toolIx && toolIx < tailIx) {
		t.Fatalf("expected dropped assistant tail after already committed tool output:\n%s", rendered)
	}
	if earlyFullIx := strings.Index(rendered, "visible assistant recovered tail"); earlyFullIx >= 0 && earlyFullIx < toolIx {
		t.Fatalf("final assistant text should not be moved before tool output:\n%s", rendered)
	}
}

func TestFinalAssistantNonPrefixReconciliationStaysAfterToolOutput(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible pre "}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible later"}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "visible pre missing middle visible later final tail",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	finalIx := strings.Index(rendered, "visible pre missing middle visible later final tail")
	if toolIx < 0 || finalIx < 0 || toolIx > finalIx {
		t.Fatalf("expected non-prefix reconciled assistant after tool output:\n%s", rendered)
	}
	if earlyFullIx := strings.Index(rendered, "visible pre missing middle"); earlyFullIx >= 0 && earlyFullIx < toolIx {
		t.Fatalf("final assistant text should not replace the committed pre-tool assistant:\n%s", rendered)
	}
}

func TestFinalAssistantFailedCommittedReplacementDoesNotMutateTranscript(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible pre"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "different final response",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	prefixIx := strings.Index(rendered, "visible pre")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	finalIx := strings.Index(rendered, "different final response")
	if prefixIx < 0 || toolIx < 0 || finalIx < 0 || !(prefixIx < toolIx && toolIx < finalIx) {
		t.Fatalf("expected original assistant, tool, then appended final response:\n%s", rendered)
	}
	if strings.Count(rendered, "different final response") != 1 {
		t.Fatalf("expected final response to be appended exactly once:\n%s", rendered)
	}
}

func TestBusySlashWarningStaysOutOfLiveTurn(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 30
	m.busy = true
	m.status = "running"

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "already visible"}))
	m = next.(model)
	m.input.SetValue("/model")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("expected blocked slash not to dispatch, got %+v", *intents)
	}

	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 100), "\n")
	if !strings.Contains(rendered, "already visible") {
		t.Fatalf("expected existing live assistant output:\n%s", rendered)
	}
	if strings.Contains(rendered, "disabled while") {
		t.Fatalf("busy slash warning should not be inserted into live chat output:\n%s", rendered)
	}
	if view := m.View(); !strings.Contains(view, "/model disabled while working") {
		t.Fatalf("expected busy slash warning in status line:\n%s", view)
	}
	if view := m.View(); strings.Contains(view, "Enter to queue") {
		t.Fatalf("blocked slash draft should not show queue guidance:\n%s", view)
	}
	if view := m.View(); strings.Contains(view, "Esc/Ctrl+C to interrupt") {
		t.Fatalf("blocked slash draft should not claim Ctrl+C interrupts:\n%s", view)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "already visible recovered tail",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)
	rendered = strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	assistantIx := strings.Index(rendered, "already visible")
	tailIx := strings.Index(rendered, "recovered tail")
	if assistantIx < 0 || tailIx < 0 || !(assistantIx < tailIx) {
		t.Fatalf("expected committed order assistant then recovered tail:\n%s", rendered)
	}
	if strings.Contains(rendered, "disabled while") {
		t.Fatalf("busy slash warning should not be committed to transcript:\n%s", rendered)
	}
}

func TestBusySlashWarningDoesNotHideProviderRetryStatus(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 140
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("/model")
	m.status = "/model disabled while working"
	m.providerRetryStatus = "API rate limited, retrying in 1s (1/3)"
	m.providerRetryUntil = time.Now().Add(time.Second)

	view := m.View()
	if !strings.Contains(view, "API rate limited, retrying in 1s (1/3) (12s)") {
		t.Fatalf("expected retry status to take priority over blocked slash status:\n%s", view)
	}
	if strings.Contains(view, "/model disabled while working") {
		t.Fatalf("blocked slash status should not hide retry status:\n%s", view)
	}
	if strings.Contains(view, "Enter to queue") {
		t.Fatalf("blocked slash draft should not show queue guidance during retry:\n%s", view)
	}
}

func TestBusyLocalCommandResultKeepsPendingToolCallLive(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "Status\n\nsession: test-session",
	}))
	m = next.(model)
	if !m.hasPendingToolCalls() {
		t.Fatal("local result must not clear pending tool calls")
	}

	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)

	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected completed tool call and local result to commit together, got %+v", m.assembler.Snapshot())
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if !strings.Contains(rendered, "Read internal/tui/model.go") || !strings.Contains(rendered, "session: test-session") {
		t.Fatalf("expected completed tool cell and local result in transcript:\n%s", rendered)
	}
	if strings.Contains(rendered, "Running internal/tui/model.go") {
		t.Fatalf("local result should not leave stale running tool cell:\n%s", rendered)
	}
}

func TestBusyLocalCommandResultDoesNotDuplicateCompletedPlan(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, chatMode: "plan", width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventPlanDelta, Text: "partial plan"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "Status\n\nsession: test-session",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventPlanCompleted, Text: "complete final plan"}))
	m = next.(model)

	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected completed plan and local result to commit together, got %+v", m.assembler.Snapshot())
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	planIx := strings.Index(rendered, "complete final plan")
	localIx := strings.Index(rendered, "session: test-session")
	if planIx < 0 || localIx < 0 || planIx > localIx {
		t.Fatalf("expected completed plan before local result:\n%s", rendered)
	}
	if strings.Count(rendered, "complete final plan") != 1 || strings.Contains(rendered, "partial plan") {
		t.Fatalf("expected final plan to replace partial plan once:\n%s", rendered)
	}
}

func TestChatStartupHeaderPrintsCompactWhenShort(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = next.(model)
	if !m.startupHeaderPrinted {
		t.Fatal("expected window size update to mark startup header printed")
	}
	view := m.View()
	if strings.Contains(view, "WHALE") {
		t.Fatalf("expected printed startup header not to repeat in live viewport:\n%s", view)
	}
	header := m.startupHeaderText()
	if !strings.Contains(header, "WHALE") {
		t.Fatalf("expected compact startup header text:\n%s", header)
	}
	if strings.Contains(header, "██╗") {
		t.Fatalf("expected compact short-terminal header, got large logo:\n%s", header)
	}
	for _, want := range []string{"model: deepseek-v4-flash"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected startup header to contain %q:\n%s", want, header)
		}
	}
	if got := countVisibleLines(view); got >= m.height {
		t.Fatalf("expected compact header view to use natural height below terminal height %d, got %d:\n%s", m.height, got, view)
	}
}

func TestChatStartupHeaderLeavesGapAboveComposer(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	view := m.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	promptIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "Type message or command") {
			promptIdx = i
			break
		}
	}
	if promptIdx < 1 {
		t.Fatalf("expected composer after startup header:\n%s", view)
	}
	if strings.TrimSpace(lines[promptIdx-1]) != "" {
		t.Fatalf("expected blank line between startup header and composer, got %q in view:\n%s", lines[promptIdx-1], view)
	}
}

func TestChatStartupHeaderGapDoesNotOverflowConstrainedHeight(t *testing.T) {
	for _, height := range []int{5, 11} {
		m := newModel(nil, "deepseek-v4-flash", "max", "off")
		m.width = 80
		m.height = height
		view := m.View()
		if got := countVisibleLines(view); got > height {
			t.Fatalf("startup header view overflowed height %d with %d lines:\n%s", height, got, view)
		}
	}
}

func TestChatStartupHeaderPrintsLargeLogoWhenTall(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(model)
	if !m.startupHeaderPrinted {
		t.Fatal("expected window size update to mark startup header printed")
	}
	header := m.startupHeaderText()
	if !strings.Contains(header, "███████╗") {
		t.Fatalf("expected large startup header:\n%s", header)
	}
	for _, want := range []string{"model:     deepseek-v4-flash", "effort:    max", "thinking:  off"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected startup header to contain %q:\n%s", want, header)
		}
	}
}

func TestChatStartupHeaderPrintCommandIsOneShot(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	cmd := m.startupHeaderPrintCmd()
	if cmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}
	if !strings.Contains(fmt.Sprintf("%#v", cmd()), "███████") {
		t.Fatal("expected startup header print command to emit the banner")
	}
	if !m.startupHeaderPrinted {
		t.Fatal("expected startup header to be marked printed")
	}
	if cmd := m.startupHeaderPrintCmd(); cmd != nil {
		t.Fatal("expected startup header print command to be one-shot")
	}
}

func TestChatStartupHeaderStaysVisibleWithSmallTranscript(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(model)

	m.appendTranscript("info", tuirender.KindText, "first content")
	view := m.View()
	// Once printed to native scrollback the header must stay out of the
	// live viewport so resize ticks cannot repaint it into the conversation.
	if strings.Contains(view, "███████╗") {
		t.Fatalf("expected printed startup header not to repeat in live viewport:\n%s", view)
	}
	if !strings.Contains(view, "first content") {
		t.Fatalf("expected transcript content in view:\n%s", view)
	}
	if got := countVisibleLines(view); got >= m.height {
		t.Fatalf("expected short transcript view to use natural height below terminal height %d, got %d:\n%s", m.height, got, view)
	}
}

func TestChatStartupHeaderStaysOutOfViewportAfterFirstPrompt(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.model = "deepseek-v4-flash"
	m.effort = "max"
	m.thinking = "off"
	m.width = 80
	m.height = 24
	m.startupHeaderPrintCmd()
	m.input.SetValue("hi")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	view := m.View()
	if strings.Contains(view, "███████╗") {
		t.Fatalf("expected printed startup header not to repeat in live viewport after first prompt:\n%s", view)
	}
	if !strings.Contains(view, "hi") {
		t.Fatalf("expected first prompt in view:\n%s", view)
	}
}

func TestChatStartupHeaderReturnsAfterNewSessionNotice(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	m.startupHeaderPrinted = true
	m.appendTranscript("assistant", tuirender.KindText, "old content")

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventClearScreen}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind: service.EventInfo,
		Text: "New session\n\nsession: fresh",
	}))
	m = next.(model)

	if !m.startupHeaderPrinted {
		t.Fatal("expected clear screen to schedule a fresh startup header print")
	}
	view := m.View()
	if strings.Contains(view, "old content") {
		t.Fatalf("expected old content cleared after new session:\n%s", view)
	}
	if strings.Contains(view, "session: fresh") {
		t.Fatalf("expected printed new session notice not to repeat in tail viewport:\n%s", view)
	}
}

func TestSessionHydratedPreservesPrintedStartupHeaderForInitialEmptySession(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	if cmd := m.startupHeaderPrintCmd(); cmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}

	next, cmd := m.Update(svcMsg(service.Event{Kind: service.EventSessionHydrated, SessionID: "s1"}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait command after hydration")
	}
	if !m.startupHeaderPrinted || m.startupHeaderOnce == nil || !*m.startupHeaderOnce {
		t.Fatal("expected initial empty hydration to preserve printed startup header")
	}
}

func TestSessionHydratedResetsStartupHeaderForNewEmptySession(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	m.sessionID = "old"
	if cmd := m.startupHeaderPrintCmd(); cmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventSessionHydrated, SessionID: "new"}))
	m = next.(model)
	if !m.startupHeaderPrinted || m.startupHeaderOnce == nil || !*m.startupHeaderOnce {
		t.Fatal("expected new empty session hydration to schedule startup header print")
	}
	if m.sessionID != "new" {
		t.Fatalf("expected session id to update, got %q", m.sessionID)
	}
}

func TestChatHeaderOmittedWhenBodyTooShort(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = countVisibleLines(m.renderBottom(80)) + 2
	view := m.View()
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") || strings.Contains(view, "WHALE") {
		t.Fatalf("startup header should not render inside chat viewport:\n%s", view)
	}
	if got := countVisibleLines(view); got != countVisibleLines(m.renderBottom(80)) {
		t.Fatalf("expected body to collapse when header cannot fit, got %d lines:\n%s", got, view)
	}
}

func TestChatViewPinsBottomAfterContentExceedsScreen(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 12
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}

	view := m.View()
	if got := countVisibleLines(view); got != m.height {
		t.Fatalf("expected overflowing chat view to occupy terminal height %d, got %d:\n%s", m.height, got, view)
	}
	assertFooterLastLine(t, view, "deepseek-v4-flash . max")
	if !strings.Contains(view, "entry-39") {
		t.Fatalf("expected overflowing chat view to follow latest content:\n%s", view)
	}
	if strings.Contains(view, "entry-00") {
		t.Fatalf("expected overflowing chat view to scroll older content out of the visible frame:\n%s", view)
	}
}

func TestChatViewportScrollsTranscript(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 20; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	atBottom := m.View()
	if strings.Contains(atBottom, "entry-00") || !strings.Contains(atBottom, "entry-19") {
		t.Fatalf("expected bottom view to show tail only:\n%s", atBottom)
	}

	m.handleViewportScrollKey("home")
	atTop := m.View()
	if !strings.Contains(atTop, "WHALE") {
		t.Fatalf("expected home to scroll chat transcript to startup header:\n%s", atTop)
	}
}

func TestPgUpPgDownScrollTranscriptHomeEndStayOnComposer(t *testing.T) {
	// PgUp/PgDn remain the transcript-scroll keys. Home/End are now readline
	// line-start/end on the composer (they used to jump the transcript to
	// the extremes; that behavior moved to the explicit PgUp/PgDn flow).
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	bottomOffset := m.viewport.YOffset
	if bottomOffset == 0 {
		t.Fatal("expected transcript to be scrollable")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.viewport.YOffset >= bottomOffset {
		t.Fatalf("expected PageUp to scroll transcript up, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}
	scrolledOffset := m.viewport.YOffset

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewport.YOffset != bottomOffset {
		t.Fatalf("expected PageDown to return to bottom, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}

	// Scroll partway up so we can detect any accidental viewport movement
	// from Home/End — they should NOT touch the transcript anymore.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	preHomeOffset := m.viewport.YOffset
	preHomeFollow := m.followTail
	m.input.SetValue("draft")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if m.viewport.YOffset != preHomeOffset {
		t.Fatalf("expected Home not to scroll transcript, offset=%d want %d", m.viewport.YOffset, preHomeOffset)
	}
	if m.followTail != preHomeFollow {
		t.Fatalf("expected Home not to toggle followTail, got %v want %v", m.followTail, preHomeFollow)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if m.viewport.YOffset != preHomeOffset {
		t.Fatalf("expected End not to scroll transcript, offset=%d want %d", m.viewport.YOffset, preHomeOffset)
	}
	if m.followTail != preHomeFollow {
		t.Fatalf("expected End not to toggle followTail, got %v want %v", m.followTail, preHomeFollow)
	}
	if m.input.Value() != "draft" {
		t.Fatalf("expected composer value preserved across Home/End (cursor moves only), got %q", m.input.Value())
	}
	// Keep referencing scrolledOffset so the partially-scrolled state above
	// is acknowledged as the precondition for the no-scroll checks.
	_ = scrolledOffset
}

func TestMouseEventsDoNotDriveTerminalNativeTUI(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.input.SetValue("typed")
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	bottomOffset := m.viewport.YOffset
	if bottomOffset == 0 {
		t.Fatal("expected transcript to be scrollable")
	}

	next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = next.(model)
	if m.viewport.YOffset != bottomOffset {
		t.Fatalf("expected mouse wheel to be ignored by Whale, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}

	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 2, Y: 2})
	m = next.(model)
	if got := m.input.Value(); got != "typed" {
		t.Fatalf("expected mouse press not to mutate composer, got %q", got)
	}
}

func TestMouseCSIFragmentsDoNotEnterComposer(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.startBusy()

	for _, fragment := range []string{
		"[<65;69;14M",
		"[<65;54;25M[<65;54;25M",
		"\x1b[<64;10;10M",
	} {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(fragment)})
		m = next.(model)
		if got := m.input.Value(); got != "" {
			t.Fatalf("expected mouse CSI fragment %q not to enter composer, got %q", fragment, got)
		}
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	m = next.(model)
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("expected normal input to still enter composer, got %q", got)
	}
}

func TestSplitMouseCSIFragmentDoesNotEnterComposer(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.startBusy()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("["), Alt: true})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<65;69;14M")})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected split mouse CSI fragment not to enter composer, got %q", got)
	}
}

func TestWindowsPasteFallbackDoesNotCaptureMouseCSIFragments(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.windowsPaste.enabled = true
	m.startBusy()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\x1b[<64;10;10M")})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected mouse CSI fragment not to enter composer, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("expected mouse CSI fragment not to enter Windows paste buffer, got %q", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("["), Alt: true})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<65;69;14M")})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected split mouse CSI fragment not to enter composer, got %q", got)
	}
	if got := m.windowsPasteBuffer(); got != "" {
		t.Fatalf("expected split mouse CSI fragment not to enter Windows paste buffer, got %q", got)
	}
}

func TestChatViewportFreezesLiveOutputWhenScrolledDuringBusy(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if !m.viewportFrozen {
		t.Fatal("expected PageUp during busy output to freeze chat viewport")
	}
	if m.followTail {
		t.Fatal("expected PageUp to disable tail following")
	}
	frozenView := m.viewport.View()

	for i := 0; i < 30; i++ {
		m.append("assistant", fmt.Sprintf("live-tail-%02d\n", i))
	}
	if got := m.viewport.View(); got != frozenView {
		t.Fatalf("expected live deltas not to redraw scrolled viewport while frozen\nbefore:\n%s\n\nafter:\n%s", frozenView, got)
	}
	if strings.Contains(m.viewport.View(), "live-tail-29") {
		t.Fatalf("expected hidden live tail not to cover frozen viewport:\n%s", m.viewport.View())
	}
}

func TestChatViewportFrozenBatchDeltasDoNotRedrawKeyboardScrolledView(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if !m.viewportFrozen {
		t.Fatal("expected PageUp during busy output to freeze chat viewport")
	}
	frozenView := m.viewport.View()

	events := make([]service.Event, 0, 20)
	for i := 0; i < 20; i++ {
		events = append(events, service.Event{Kind: service.EventAssistantDelta, Text: fmt.Sprintf("batched-tail-%02d\n", i)})
	}
	next, _ = m.Update(svcBatchMsg(events))
	m = next.(model)
	if got := m.viewport.View(); got != frozenView {
		t.Fatalf("expected batched live deltas not to redraw scrolled viewport while frozen\nbefore:\n%s\n\nafter:\n%s", frozenView, got)
	}
	if strings.Contains(m.View(), "batched-tail-19") {
		t.Fatalf("expected hidden batched tail not to cover frozen viewport:\n%s", m.View())
	}
}

func TestAppendBatchedServiceEventMergesAdjacentDeltas(t *testing.T) {
	events := []service.Event{}
	events = appendBatchedServiceEvent(events, service.Event{Kind: service.EventAssistantDelta, Text: "a"})
	events = appendBatchedServiceEvent(events, service.Event{Kind: service.EventAssistantDelta, Text: "b"})
	events = appendBatchedServiceEvent(events, service.Event{Kind: service.EventReasoningDelta, Text: "c"})
	events = appendBatchedServiceEvent(events, service.Event{Kind: service.EventReasoningDelta, Text: "d"})
	events = appendBatchedServiceEvent(events, service.Event{Kind: service.EventTurnDone, Text: "done"})

	if len(events) != 3 {
		t.Fatalf("expected adjacent deltas to merge into 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Kind != service.EventAssistantDelta || events[0].Text != "ab" {
		t.Fatalf("unexpected merged assistant delta: %+v", events[0])
	}
	if events[1].Kind != service.EventReasoningDelta || events[1].Text != "cd" {
		t.Fatalf("unexpected merged reasoning delta: %+v", events[1])
	}
	if events[2].Kind != service.EventTurnDone {
		t.Fatalf("expected non-delta event to remain separate, got %+v", events[2])
	}
}

func TestChatViewportBusyFollowTailUsesTailRenderWindow(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(false)

	if lines := m.viewport.TotalLineCount(); lines > chatTailRenderLineFloor {
		t.Fatalf("expected busy tail-follow render to keep a bounded line window, got %d lines", lines)
	}
	view := m.View()
	if strings.Contains(view, "entry-000") {
		t.Fatalf("expected old transcript lines to be outside the busy tail render window:\n%s", view)
	}
	if !strings.Contains(view, "live-head") {
		t.Fatalf("expected live output to remain visible in tail render window:\n%s", view)
	}
}

func TestChatViewportIdleFollowTailUsesTailRenderWindow(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.refreshViewportContentFollow(false)

	lineLimit := max(chatTailRenderLineFloor, m.viewportBodyHeight(m.width)*4)
	if lines := m.viewport.TotalLineCount(); lines > lineLimit {
		t.Fatalf("expected idle tail-follow render to keep a bounded line window, got %d lines", lines)
	}
	view := m.View()
	if strings.Contains(view, "entry-000") {
		t.Fatalf("expected old transcript lines to be outside the idle tail render window:\n%s", view)
	}
	if !strings.Contains(view, "entry-199") {
		t.Fatalf("expected latest transcript lines to remain visible in idle tail render window:\n%s", view)
	}
}

func TestChatViewportTailRenderBoundedWithAlternatingToolAndAssistant(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("tool", tuirender.KindToolCall, fmt.Sprintf("tool-call-%03d", i))
		m.appendTranscript("tool", tuirender.KindToolResult, fmt.Sprintf("tool-result-%03d", i))
		m.appendTranscript("assistant", tuirender.KindText, fmt.Sprintf("assistant-reply-%03d", i))
	}
	m.refreshViewportContentFollow(false)

	lineLimit := max(chatTailRenderLineFloor, m.viewportBodyHeight(m.width)*4)
	if lines := m.viewport.TotalLineCount(); lines > lineLimit {
		t.Fatalf("alternating tool/assistant tail render should stay within %d lines (work separators included), got %d", lineLimit, lines)
	}
}

func TestChatViewportBusyFollowTailKeepsSingleLargeLiveMessageScrollable(t *testing.T) {
	for _, height := range []int{8, 10, 20} {
		t.Run(fmt.Sprintf("height_%d", height), func(t *testing.T) {
			m := newModel(nil, "", "", "")
			m.width = 80
			m.height = height
			m.transcript = nil
			m.beginTurnTranscript()
			m.startBusy()
			for i := 0; i < 400; i++ {
				m.append("assistant", fmt.Sprintf("single-live-%03d\n", i))
			}
			m.refreshViewportContentFollow(false)

			if lines := m.viewport.TotalLineCount(); lines <= m.viewportBodyHeight(m.width) {
				t.Fatalf("expected single coalesced live message to exceed viewport height, got %d lines", lines)
			}
			view := m.View()
			if !strings.Contains(view, "live-399") {
				t.Fatalf("expected single-message live tail to remain visible:\n%s", view)
			}
			m.handleViewportScrollKey("home")
			view = m.View()
			if !strings.Contains(view, "live-000") {
				t.Fatalf("expected full single-message live output to be scrollable to the top:\n%s", view)
			}
		})
	}
}

func TestChatViewportHomeFromIdleTailRenderRestoresFullHistory(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.refreshViewportContentFollow(false)
	tailLines := m.viewport.TotalLineCount()

	// handleViewportScrollKey is still the underlying scroll-to-top primitive
	// (used directly by the diff page and intended for future programmatic
	// callers). The Home key no longer routes here in chat mode after the
	// readline alignment, but the function's invariants must hold.
	m.handleViewportScrollKey("home")
	if m.followTail {
		t.Fatal("expected scroll-to-top from idle tail render to disable tail following")
	}
	if fullLines := m.viewport.TotalLineCount(); fullLines <= tailLines {
		t.Fatalf("expected scroll-to-top from idle tail render to restore full scrollable content, tail=%d full=%d", tailLines, fullLines)
	}
	view := m.View()
	if !strings.Contains(view, "entry-000") {
		t.Fatalf("expected scroll-to-top from idle tail render to restore early history:\n%s", view)
	}
	if strings.Contains(view, "entry-199") {
		t.Fatalf("expected scroll-to-top from idle tail render to move away from the latest tail:\n%s", view)
	}
}

func TestChatViewportPageUpRestoresFullRenderWindowDuringBusy(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 200; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(false)
	tailLines := m.viewport.TotalLineCount()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if !m.viewportFrozen {
		t.Fatal("expected PageUp during busy output to freeze chat viewport")
	}
	if fullLines := m.viewport.TotalLineCount(); fullLines <= tailLines {
		t.Fatalf("expected PageUp to restore full scrollable content, tail=%d full=%d", tailLines, fullLines)
	}
	if m.followTail {
		t.Fatal("expected PageUp to disable tail following")
	}
}

func TestChatViewportFirstPageUpDuringBusyAnchorsLiveTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	for i := 0; i < 12; i++ {
		m.append("assistant", fmt.Sprintf("live-%02d\n", i))
	}
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	view := m.View()
	if !strings.Contains(view, "live-11") {
		t.Fatalf("expected first PageUp during busy output to anchor current live tail:\n%s", view)
	}
}

func TestChatViewportEndUnfreezesLiveOutputAndReturnsToTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	for i := 0; i < 30; i++ {
		m.append("assistant", fmt.Sprintf("live-tail-%02d\n", i))
	}

	// handleViewportScrollKey is still the unfreeze-and-resume-tail primitive
	// for the chat page. The End key no longer routes here in chat mode after
	// the readline alignment, but the underlying viewport invariants —
	// unfreezing the frozen state and restoring tail-follow — must hold.
	m.handleViewportScrollKey("end")
	if m.viewportFrozen {
		t.Fatal("expected scroll-to-tail to unfreeze chat viewport")
	}
	if !m.followTail {
		t.Fatal("expected scroll-to-tail to re-enable tail following")
	}
	view := m.View()
	if !strings.Contains(view, "live-tail-29") {
		t.Fatalf("expected scroll-to-tail to reveal latest live tail:\n%s", view)
	}
}

func TestChatViewportTurnDoneUnfreezesScrolledLiveOutput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	m.append("assistant", "live-tail-after-scroll\n")

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone, LastResponse: "done"}))
	m = next.(model)
	if m.viewportFrozen {
		t.Fatal("expected turn completion to unfreeze chat viewport")
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "live-tail-after-scroll") {
		t.Fatalf("expected frozen live output to be committed on turn done:\n%s", got)
	}
}

func TestTurnDoneWhileScrolledDefersNativeScrollbackUntilTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	m.append("assistant", "tail while scrolled\n")
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone, LastResponse: "done"}))
	m = next.(model)

	if m.nativeScrollbackPrinted == len(m.transcript) {
		t.Fatal("expected turn completion while scrolled to defer native scrollback")
	}
	if m.followTail {
		t.Fatal("expected turn completion to preserve user-scrolled position")
	}

	cmd := m.resumeChatTail()
	if cmd == nil {
		t.Fatal("expected returning to tail to flush deferred turn output")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "tail while scrolled") {
		t.Fatalf("expected deferred native scrollback to include turn tail, got %s", got)
	}
}

func TestLongTurnDoneWhileScrolledPreservesViewportAndDefersDurationNotice(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.beginTurnTranscript()
	m.startBusy()
	m.busySince = time.Now().Add(-(3*time.Minute + 5*time.Second))
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	m.append("assistant", "tail while scrolled\n")
	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	if m.followTail {
		t.Fatal("expected long turn completion to preserve user-scrolled position")
	}
	if m.nativeScrollbackPrinted == len(m.transcript) {
		t.Fatal("expected long turn completion while scrolled to defer native scrollback")
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "✻ Worked for 3m ") {
		t.Fatalf("expected duration notice to be appended to transcript:\n%s", rendered)
	}
	// Scroll preservation intent: followTail must stay false (asserted above)
	// and the duration notice must remain deferred from native scrollback
	// (nativeScrollbackPrinted assertion above). A "view does not contain"
	// check here would be a coincidental coupling to which rows happen to
	// fit in the small test viewport, not a real scroll-position check.

	cmd := m.resumeChatTail()
	if cmd == nil {
		t.Fatal("expected returning to tail to flush deferred long-turn output")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "✻ Worked for 3m ") {
		t.Fatalf("expected deferred native scrollback to include duration notice, got %s", got)
	}
}

// When most of the transcript has been emitted to native scrollback
// (nativeScrollbackPrinted near len(transcript)), PgUp from the live tail
// must reveal the older transcript that is still semantically part of the
// session — not just the trimmed transcript[nativeScrollbackPrinted:]
// window. Previously PgUp scrolled within that trimmed view and landed at
// the startup banner (when start==0) or at very recent entries only.
func TestPageUpAtTailRevealsTranscriptAboveScrollbackBoundary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 20
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	// Simulate that all but the last two entries have already been written
	// to terminal native scrollback (the common state after a couple of
	// completed turns).
	m.nativeScrollbackPrinted = len(m.transcript) - 2
	m.followTail = true
	m.refreshViewportContentFollow(true)
	if !strings.Contains(m.View(), "entry-39") {
		t.Fatalf("precondition: expected tail view to show latest entry, got:\n%s", m.View())
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected PageUp to leave followTail mode")
	}
	view := m.View()
	// PageUp from the tail should expose entries above the scrollback
	// boundary (entries with index < 38), not just the two trimmed entries.
	if !regexp.MustCompile(`entry-3[0-7]`).MatchString(view) {
		t.Fatalf("expected PageUp from tail to expose transcript above the scrollback boundary, got:\n%s", view)
	}
	if strings.Contains(view, "WHALE") {
		t.Fatalf("expected PageUp from tail not to jump to the startup banner, got:\n%s", view)
	}
}

func TestTurnDoneReconciliationPreservesScrolledPosition(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.beginTurnTranscript()
	m.startBusy()

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible assistant"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolCall, ToolCallID: "tc-1", ToolName: "read_file", Text: `read_file: {"file_path":"internal/tui/model.go"}`}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "tc-1", ToolName: "read_file", Text: raw}))
	m = next.(model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if !m.viewportFrozen || m.followTail {
		t.Fatalf("expected user scroll to freeze away from tail, frozen=%v follow=%v", m.viewportFrozen, m.followTail)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "visible assistant with final reconciliation",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	if m.followTail {
		t.Fatal("final reconciliation should not force scrolled chat back to tail")
	}
	if m.nativeScrollbackPrinted == len(m.transcript) {
		t.Fatal("expected reconciled turn output to remain deferred for native scrollback")
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	prefixIx := strings.Index(rendered, "visible assistant")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	tailIx := strings.Index(rendered, "with final reconciliation")
	if prefixIx < 0 || toolIx < 0 || tailIx < 0 || !(prefixIx < toolIx && toolIx < tailIx) {
		t.Fatalf("expected final assistant tail after committed tool output:\n%s", rendered)
	}
}

func TestChatViewportResizeKeepsTailWhenFollowing(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 18
	m.sizeMsgReceived = true
	m.transcript = nil
	for i := 0; i < 50; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	if !m.followTail || !m.viewport.AtBottom() {
		t.Fatalf("expected chat to start following tail, follow=%v bottom=%v", m.followTail, m.viewport.AtBottom())
	}

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	// Resize wipes terminal scrollback (so it can't accumulate ghost frames
	// from terminal-side reflow). The replay command re-emits the header and
	// the full transcript into the now-clean scrollback so the user still
	// sees their history; the live View itself only carries the composer and
	// footer in this state.
	if cmd == nil {
		t.Fatal("expected resize to schedule a scrollback replay")
	}
	replay := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(replay, "entry-49") {
		t.Fatalf("expected resize scrollback replay to include tail, got %s", replay)
	}
	if !strings.Contains(replay, "entry-00") {
		t.Fatalf("expected resize scrollback replay to include head, got %s", replay)
	}
	if !m.followTail {
		t.Fatal("expected resize to keep follow-tail mode")
	}
}

func TestChatViewportResizePreservesUserScrollPosition(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 18
	m.sizeMsgReceived = true
	m.transcript = nil
	for i := 0; i < 50; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)

	m.handleViewportScrollKey("home")
	if m.followTail {
		t.Fatal("expected Home to disable tail following")
	}
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	// Even though followTail is false (scrolled up), the resize-wiped
	// scrollback must be replayed with the entire transcript so the user
	// does not lose history accessibility. flushNativeScrollbackCmd would
	// short-circuit here; replayNativeScrollbackCmd is the right path.
	if cmd == nil {
		t.Fatal("expected resize while scrolled up to schedule a scrollback replay")
	}
	replay := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(replay, "entry-00") || !strings.Contains(replay, "entry-49") {
		t.Fatalf("expected scrollback replay to include entire transcript, got %s", replay)
	}
	view := m.View()
	if !strings.Contains(view, "entry-00") {
		t.Fatalf("expected resized scrolled-up view to preserve top position at first transcript entry:\n%s", view)
	}
	if strings.Contains(view, "entry-49") {
		t.Fatalf("expected resized scrolled-up view not to jump to tail:\n%s", view)
	}

	m.handleViewportScrollKey("end")
	if !m.followTail {
		t.Fatal("expected End to re-enable tail following")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = next.(model)
	view = m.View()
	if strings.Contains(view, "entry-49") {
		t.Fatalf("expected printed tail content not to repeat in viewport after End:\n%s", view)
	}
}

func TestNativeScrollbackSkipsHeaderAndPrintsNewTranscriptOnce(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10

	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected startup chrome header not to enter native scrollback")
	}

	m.appendTranscript("you", tuirender.KindText, "hello native scrollback")
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected new transcript entry to produce a native scrollback print command")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "hello native scrollback") || !strings.Contains(got, "WHALE") {
		t.Fatalf("expected printed message to include startup header and transcript text, got %s", got)
	}
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected transcript entry not to be printed twice")
	}
}

func TestPrintedNativeScrollbackIsNotRepeatedInTailViewport(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.width = 80
	m.height = 24
	m.startupHeaderPrintCmd()
	m.appendTranscript("you", tuirender.KindText, "hi")
	m.appendTranscript("think", tuirender.KindThinking, "thinking once")
	m.appendTranscript("assistant", tuirender.KindText, "hello once")
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected committed turn to print to native scrollback")
	}

	view := m.View()
	for _, repeated := range []string{"thinking once", "hello once"} {
		if strings.Contains(view, repeated) {
			t.Fatalf("expected printed transcript %q not to repeat in tail viewport:\n%s", repeated, view)
		}
	}
	if strings.Contains(view, "███████╗") || strings.Contains(view, "WHALE") {
		t.Fatalf("expected printed startup header not to repeat in tail viewport:\n%s", view)
	}

	m.startBusy()
	m.append("assistant", "live tail")
	view = m.View()
	if !strings.Contains(view, "live tail") {
		t.Fatalf("expected uncommitted live output to remain visible:\n%s", view)
	}
}

func TestFirstNativeScrollbackFlushKeepsStartupHeaderVisibleOutsideViewport(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.width = 80
	m.height = 24
	headerCmd := m.startupHeaderPrintCmd()
	if headerCmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}
	headerPrinted := fmt.Sprintf("%#v", headerCmd())
	for _, want := range []string{"███████", "version:"} {
		if !strings.Contains(headerPrinted, want) {
			t.Fatalf("expected startup header print to include %q, got %s", want, headerPrinted)
		}
	}
	m.appendTranscript("you", tuirender.KindText, "hi")
	m.appendTranscript("assistant", tuirender.KindText, "hello once")

	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected first committed turn to print to native scrollback")
	}
	printed := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(printed, "hello once") {
		t.Fatalf("expected first native scrollback flush to include transcript content, got %s", printed)
	}
	if strings.Contains(printed, "███████") {
		t.Fatalf("expected startup header not to be reprinted in the transcript flush, got %s", printed)
	}

	view := m.View()
	for _, repeated := range []string{"███████", "hello once"} {
		if strings.Contains(view, repeated) {
			t.Fatalf("expected first native scrollback content %q not to repeat in tail viewport:\n%s", repeated, view)
		}
	}
}

func TestNativeScrollbackWaitsWhileChatIsScrolledUpAndFlushesAtTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	printed := m.nativeScrollbackPrinted
	m.appendTranscript("assistant", tuirender.KindText, "pending native scrollback")
	m.followTail = false

	cmd := m.flushNativeScrollbackCmd()
	if cmd != nil {
		t.Fatal("expected scrolled-up chat viewport not to print native scrollback")
	}
	if m.nativeScrollbackPrinted != printed {
		t.Fatalf("expected native scrollback cursor to remain at %d, got %d", printed, m.nativeScrollbackPrinted)
	}

	cmd = m.resumeChatTail()
	if cmd == nil {
		t.Fatal("expected returning to tail to flush delayed native scrollback")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "pending native scrollback") {
		t.Fatalf("expected delayed native scrollback output, got %s", got)
	}
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected delayed native scrollback not to be printed twice")
	}
}

func TestNativeScrollbackWaitsWhileChatViewportFrozenAndFlushesAtTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	printed := m.nativeScrollbackPrinted
	m.appendTranscript("assistant", tuirender.KindText, "pending frozen scrollback")
	m.viewportFrozen = true

	cmd := m.flushNativeScrollbackCmd()
	if cmd != nil {
		t.Fatal("expected frozen chat viewport not to print native scrollback")
	}
	if m.nativeScrollbackPrinted != printed {
		t.Fatalf("expected native scrollback cursor to remain at %d, got %d", printed, m.nativeScrollbackPrinted)
	}

	cmd = m.resumeChatTail()
	if cmd == nil {
		t.Fatal("expected returning to tail to flush delayed frozen native scrollback")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "pending frozen scrollback") {
		t.Fatalf("expected delayed native scrollback output, got %s", got)
	}
}

func TestCtrlUKillsSingleLineComposerDoesNotScrollTranscript(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	m.handleViewportScrollKey("home")
	m.input.SetValue("clear me")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer, got %q", got)
	}
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected Ctrl+U not to scroll transcript, offset=%d", m.viewport.YOffset)
	}
}

func TestCtrlDDeletesComposerCharDoesNotScrollTranscript(t *testing.T) {
	// PR 2 removed the chat-mode interception of Ctrl+D for transcript
	// half-page-down. Ctrl+D should now reach the textarea's
	// DeleteCharacterForward via the standard composer dispatch path.
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	m.handleViewportScrollKey("home")
	initialOffset := m.viewport.YOffset
	m.input.SetValue("abc")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA}) // cursor → line start
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(model)
	if got := m.input.Value(); got != "bc" {
		t.Fatalf("expected Ctrl+D to delete first char, got %q", got)
	}
	if m.viewport.YOffset != initialOffset {
		t.Fatalf("expected Ctrl+D not to scroll transcript, offset=%d want %d", m.viewport.YOffset, initialOffset)
	}
}

func TestCtrlCWhileBusyClearsNonEmptyComposer(t *testing.T) {
	// PR 2 promoted Ctrl+C to the canonical clear-all path. During a busy
	// turn the composer-clear path must still be reachable so users can
	// drop a queued draft mid-stream without canceling the running turn.
	// Esc remains the unconditional interrupt for those who want to cancel.
	m, _ := newModelWithDispatchSpy()
	m.busy = true
	m.input.SetValue("queued draft text")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+C to clear non-empty composer during busy, got %q", got)
	}
	if m.stopping {
		t.Fatal("expected Ctrl+C with non-empty composer not to interrupt the busy turn")
	}
}

func TestCtrlCWhileBusyEmptyComposerInterruptsTurn(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.busy = true
	m.input.SetValue("")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.stopping {
		t.Fatal("expected Ctrl+C with empty composer during busy to interrupt the turn")
	}
}

func TestCtrlCWhileBusyInBlockingModeAlwaysInterrupts(t *testing.T) {
	// The composer-clear precedence is scoped to modeChat. In blocking
	// modes (approval, user-input) Ctrl+C must interrupt the running turn
	// even with a queued draft — otherwise it would only dismiss the modal
	// via the mode-specific handler and leave the turn running.
	m, _ := newModelWithDispatchSpy()
	m.busy = true
	m.input.SetValue("queued draft kept while modal blocks")
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell"

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.stopping {
		t.Fatal("expected Ctrl+C in modeApproval during busy to interrupt the turn, not just dismiss the modal")
	}
	if got := m.input.Value(); got != "queued draft kept while modal blocks" {
		t.Fatalf("expected interrupt path not to touch the composer draft, got %q", got)
	}
}

func TestCtrlCClearsWhitespaceOnlyDraft(t *testing.T) {
	// After PR 2 made Ctrl+C the canonical clear-all, the path must accept
	// whitespace-only buffers too — otherwise a stray Enter / blank-line
	// paste leaves the user with no way to clear short of Ctrl+C ×2 quit.
	m, _ := newModelWithDispatchSpy()
	m.input.SetValue("   \n\n  ")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+C to clear whitespace-only draft, got %q", got)
	}
	if !m.quitArmedUntil.IsZero() {
		t.Fatal("expected Ctrl+C with whitespace draft to clear (not arm quit)")
	}
}

func TestCtrlCWhileBusyClearsWhitespaceOnlyDraft(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.busy = true
	m.input.SetValue("   ")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+C to clear whitespace-only draft during busy, got %q", got)
	}
	if m.stopping {
		t.Fatal("expected Ctrl+C with whitespace draft during busy to clear (not interrupt)")
	}
}

func TestCtrlCWhileBusyClearsPendingWindowsPasteBuffer(t *testing.T) {
	// Windows paste fallback buffers burst chunks in m.windowsPasteBuffer()
	// for windowsPasteQuietDelay (80ms) before flushing into the textarea.
	// In that window m.input.Value() is still empty, so the busy gate must
	// also consult hasWindowsPasteBuffer() to avoid interrupting the turn
	// when the user is really just trying to drop the pasted draft.
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.busy = true
	m.setWindowsPasteBuffer("buffered paste chunk")
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.hasWindowsPasteBuffer() {
		t.Fatalf("expected Ctrl+C to drop pending paste buffer, got %q", m.windowsPasteBuffer())
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected composer to stay empty after clearing pending paste, got %q", got)
	}
	if m.stopping {
		t.Fatal("expected Ctrl+C with pending paste buffer not to interrupt the busy turn")
	}
}

func TestCtrlCClearsPendingWindowsPasteBufferNotBusy(t *testing.T) {
	// Outside of busy, the same paste-buffer state must hit the clear path
	// rather than arming quit — otherwise users get a "Press Ctrl+C again
	// to quit" prompt while their just-pasted draft is still pending.
	m, _ := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.setWindowsPasteBuffer("buffered chunk")
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.hasWindowsPasteBuffer() {
		t.Fatalf("expected Ctrl+C to drop pending paste buffer outside busy, got %q", m.windowsPasteBuffer())
	}
	if !m.quitArmedUntil.IsZero() {
		t.Fatal("expected Ctrl+C with pending paste buffer to clear (not arm quit)")
	}
}

func TestComposerEditsDoNotRerenderChatWhenHeightIsStable(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	m.input.SetValue("seed\nline")
	for i := 0; i < 60; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	initialGeneration := m.chat.generation
	initialOffset := m.viewport.YOffset

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(model)
	if got := m.input.Value(); got != "seed\nlinea" {
		t.Fatalf("expected rune input to update composer, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected rune input not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if m.viewport.YOffset != initialOffset {
		t.Fatalf("expected rune input not to move chat offset, got %d want %d", m.viewport.YOffset, initialOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	if got := m.input.Value(); got != "seed\nline" {
		t.Fatalf("expected backspace to update composer, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected backspace not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if m.viewport.YOffset != initialOffset {
		t.Fatalf("expected backspace not to move chat offset, got %d want %d", m.viewport.YOffset, initialOffset)
	}
}

func TestLongHistoryComposerEditsStayIncrementalAtTail(t *testing.T) {
	m := newLongHistoryComposerModel(600, "seed")
	initialGeneration := m.chat.generation
	initialItems := len(m.chat.items)

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyRunes, Runes: []rune("b")},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyCtrlU},
	} {
		next, _ := m.Update(msg)
		m = next.(model)
		if m.chat.generation != initialGeneration {
			t.Fatalf("expected long-history edit %v not to rerender chat, gen=%d want=%d", msg.Type, m.chat.generation, initialGeneration)
		}
		if len(m.chat.items) != initialItems {
			t.Fatalf("expected long-history edit %v to keep chat item count stable, got %d want %d", msg.Type, len(m.chat.items), initialItems)
		}
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer after long-history edits, got %q", got)
	}
	if !m.followTail || !m.chat.AtBottom() || !m.viewport.AtBottom() {
		t.Fatalf("expected long-history tail edits to keep latest content visible, follow=%v chatBottom=%v viewportBottom=%v", m.followTail, m.chat.AtBottom(), m.viewport.AtBottom())
	}
	if view := m.View(); !strings.Contains(view, "entry-0599") {
		t.Fatalf("expected long-history tail view to keep latest entry visible:\n%s", view)
	}
}

func TestLongHistoryComposerEditsStayIncrementalOffTail(t *testing.T) {
	m := newLongHistoryComposerModel(600, "seed")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected PageUp to leave tail-follow mode for long history")
	}
	initialGeneration := m.chat.generation
	initialItems := len(m.chat.items)
	initialOffset := m.viewport.YOffset

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyCtrlU},
	} {
		next, _ = m.Update(msg)
		m = next.(model)
		if m.chat.generation != initialGeneration {
			t.Fatalf("expected off-tail long-history edit %v not to rerender chat, gen=%d want=%d", msg.Type, m.chat.generation, initialGeneration)
		}
		if len(m.chat.items) != initialItems {
			t.Fatalf("expected off-tail long-history edit %v to keep chat item count stable, got %d want %d", msg.Type, len(m.chat.items), initialItems)
		}
		if m.viewport.YOffset != initialOffset {
			t.Fatalf("expected off-tail long-history edit %v not to move chat offset, got %d want %d", msg.Type, m.viewport.YOffset, initialOffset)
		}
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer after off-tail long-history edits, got %q", got)
	}
	if m.followTail {
		t.Fatal("expected off-tail long-history edits not to resume tail following")
	}
	if view := m.View(); strings.Contains(view, "entry-0599") {
		t.Fatalf("expected off-tail long-history edits not to jump back to the latest tail:\n%s", view)
	}
}

func BenchmarkComposerEditCycleLongHistory(b *testing.B) {
	for _, historyCount := range []int{500, 1000, 2000} {
		b.Run(fmt.Sprintf("history-%d", historyCount), func(b *testing.B) {
			m := newLongHistoryComposerModel(historyCount, "seed")
			initialGeneration := m.chat.generation
			initialItems := len(m.chat.items)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
				m = next.(model)
				next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
				m = next.(model)
			}
			b.StopTimer()

			if m.chat.generation != initialGeneration {
				b.Fatalf("expected edit cycle not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
			}
			if len(m.chat.items) != initialItems {
				b.Fatalf("expected edit cycle to keep chat item count stable, got %d want %d", len(m.chat.items), initialItems)
			}
		})
	}
}

func TestComposerHeightGrowthAtTailUpdatesLayoutWithoutRerender(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	m.input.SetValue("seed")
	for i := 0; i < 60; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	mainWidth, _ := m.layoutDims()
	initialBodyHeight := m.viewportBodyHeight(mainWidth)
	initialGeneration := m.chat.generation

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = next.(model)
	mainWidth, _ = m.layoutDims()
	if got := m.viewportBodyHeight(mainWidth); got >= initialBodyHeight {
		t.Fatalf("expected composer growth to reduce chat body height, got %d want < %d", got, initialBodyHeight)
	}
	if got := m.input.Value(); got != "seed\n" {
		t.Fatalf("expected ctrl+j to add newline, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected composer growth not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if !m.followTail || !m.chat.AtBottom() || !m.viewport.AtBottom() {
		t.Fatalf("expected tail-follow layout sync to keep latest content visible, follow=%v chatBottom=%v viewportBottom=%v", m.followTail, m.chat.AtBottom(), m.viewport.AtBottom())
	}
	if view := m.View(); !strings.Contains(view, "entry-59") {
		t.Fatalf("expected tail view to keep latest entry visible after composer growth:\n%s", view)
	}
}

func TestWindowsFallbackTypedRuneHeightGrowthUpdatesLayout(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.windowsPaste.enabled = true
	m.width = 32
	m.height = 10
	m.transcript = nil
	for i := 0; i < 60; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.input.SetValue("seed")
	clock := newFakeClock()
	m.windowsPaste.nowFunc = clock.now
	m.refreshViewportContentFollow(true)
	mainWidth, _ := m.layoutDims()
	initialBodyHeight := m.viewportBodyHeight(mainWidth)
	initialGeneration := m.chat.generation

	for i := 0; i < 80; i++ {
		clock.advance(100 * time.Millisecond)
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
		m = next.(model)
	}
	mainWidth, _ = m.layoutDims()
	if got := m.input.Value(); got != "seed"+strings.Repeat("b", 80) {
		t.Fatalf("expected typed runes to enter composer, got %q", got)
	}
	if m.hasWindowsPasteBuffer() {
		t.Fatalf("ordinary typed runes should not enter Windows paste buffer, got %q", m.windowsPasteBuffer())
	}
	if got := m.viewportBodyHeight(mainWidth); got >= initialBodyHeight {
		t.Fatalf("expected typed rune wrapping to reduce chat body height, got %d want < %d", got, initialBodyHeight)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected typed rune height growth not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
}

func TestShiftEnterKeyInsertsNewline(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("seed")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")})
	m = next.(model)

	if got := m.input.Value(); got != "seed\n" {
		t.Fatalf("expected shift+enter to add newline, got %q", got)
	}
}

func TestComposerHeightShrinkOffTailClampsLayoutWithoutRerender(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	m.input.SetValue("seed\n")
	for i := 0; i < 120; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.refreshViewportContentFollow(true)
	tailOffset := m.viewport.YOffset

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected PageUp to leave tail-follow mode")
	}
	mainWidth, _ := m.layoutDims()
	initialBodyHeight := m.viewportBodyHeight(mainWidth)
	initialGeneration := m.chat.generation
	initialOffset := m.viewport.YOffset

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	mainWidth, _ = m.layoutDims()
	if got := m.viewportBodyHeight(mainWidth); got <= initialBodyHeight {
		t.Fatalf("expected composer shrink to increase chat body height, got %d want > %d", got, initialBodyHeight)
	}
	if got := m.input.Value(); got != "seed" {
		t.Fatalf("expected backspace to remove trailing newline, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected composer shrink not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if m.followTail {
		t.Fatal("expected off-tail layout sync not to resume tail following")
	}
	if m.viewport.YOffset > initialOffset {
		t.Fatalf("expected off-tail layout sync only to clamp offset, got %d want <= %d", m.viewport.YOffset, initialOffset)
	}
	if m.viewport.YOffset >= tailOffset {
		t.Fatalf("expected off-tail layout sync not to jump back to tail, got %d tail=%d", m.viewport.YOffset, tailOffset)
	}
}

func TestCtrlCClearsMultilineComposerWithLayoutSyncOnly(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	m.input.SetValue("alpha\nbeta")
	for i := 0; i < 60; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	mainWidth, _ := m.layoutDims()
	initialBodyHeight := m.viewportBodyHeight(mainWidth)
	initialGeneration := m.chat.generation

	// Ctrl+C is the canonical full-clear after PR 2 moved Ctrl+U to readline
	// kill-to-line-start (which would only kill the current line "beta").
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)
	mainWidth, _ = m.layoutDims()
	if got := m.viewportBodyHeight(mainWidth); got <= initialBodyHeight {
		t.Fatalf("expected Ctrl+C clear to free composer height, got %d want > %d", got, initialBodyHeight)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+C to clear multiline composer, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected Ctrl+C clear not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if !m.followTail || !m.chat.AtBottom() {
		t.Fatalf("expected Ctrl+C clear at tail to keep latest content visible, follow=%v chatBottom=%v", m.followTail, m.chat.AtBottom())
	}
}

func TestChatLiveViewRendersWithoutViewportFrame(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.append("assistant", "streamed answer")
	view := m.View()
	if !strings.Contains(view, "streamed answer") {
		t.Fatalf("expected live assistant text in view:\n%s", view)
	}
	if strings.Contains(view, "┌") {
		t.Fatalf("live chat view should not render bordered viewport:\n%s", view)
	}
}

func assertFooterLastLine(t *testing.T, view, want string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("empty view")
	}
	if got := lines[len(lines)-1]; !strings.Contains(got, want) {
		t.Fatalf("expected footer %q on last line, got %q in view:\n%s", want, got, view)
	}
}

func assertFooterLastLineNotContains(t *testing.T, view, unwanted string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("empty view")
	}
	if got := lines[len(lines)-1]; strings.Contains(got, unwanted) {
		t.Fatalf("expected footer not to contain %q, got %q in view:\n%s", unwanted, got, view)
	}
}

func TestChatBusyViewShowsWorkingAboveComposer(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	view := m.View()
	if !strings.Contains(view, "Working (12s) · Type follow-up, Enter to queue · Esc/Ctrl+C to interrupt") {
		t.Fatalf("expected working status line with elapsed time:\n%s", view)
	}
	if strings.Contains(view, "status: working") {
		t.Fatalf("busy view should not render footer status:\n%s", view)
	}
	if strings.Index(view, "Working (12s)") > strings.Index(view, "Type message or command") {
		t.Fatalf("working status line should appear above composer:\n%s", view)
	}
}

func TestChatBusyViewShowsDraftSpecificBusyHint(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("follow up")

	view := m.View()
	if !strings.Contains(view, "Working (12s) · Enter to queue · Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected draft-specific busy status line:\n%s", view)
	}
	if strings.Contains(view, "Esc/Ctrl+C to interrupt") {
		t.Fatalf("draft busy status should not claim Ctrl+C interrupts:\n%s", view)
	}
}

func TestChatBusyViewTreatsWhitespaceDraftAsEmpty(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("  \n\t  ")

	view := m.View()
	if !strings.Contains(view, "Working (12s) · Type follow-up · Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected whitespace draft to use draft-clearing busy guidance:\n%s", view)
	}
	for _, unexpected := range []string{"Enter to queue", "Esc/Ctrl+C to interrupt"} {
		if strings.Contains(view, unexpected) {
			t.Fatalf("whitespace-only draft should not show %q:\n%s", unexpected, view)
		}
	}
}

func TestChatBusyViewShowsBlockedSlashDraftHint(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("/model")
	m.status = "/model disabled while working"

	view := m.View()
	if !strings.Contains(view, "/model disabled while working (12s) · Edit command or press Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected blocked slash busy status line:\n%s", view)
	}
	if strings.Contains(view, "Enter to queue") {
		t.Fatalf("blocked slash draft should not show queue guidance:\n%s", view)
	}
	if strings.Contains(view, "Esc/Ctrl+C to interrupt") {
		t.Fatalf("blocked slash draft should not claim Ctrl+C interrupts:\n%s", view)
	}
}

func TestChatBusyViewShowsBlockedSlashPrefixDraftHint(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("/mo")
	m.status = "/model disabled while working"

	view := m.View()
	if !strings.Contains(view, "/model disabled while working (12s) · Edit command or press Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected expanded blocked slash prefix status line:\n%s", view)
	}
	if strings.Contains(view, "Enter to queue") {
		t.Fatalf("blocked slash prefix draft should not show queue guidance:\n%s", view)
	}
}

func TestChatBusyViewDoesNotQueueUnsentSlashDraft(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("/model")

	view := m.View()
	if !strings.Contains(view, "Working (12s) · Slash commands are disabled while working · Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected unsent slash draft busy guidance:\n%s", view)
	}
	if strings.Contains(view, "Enter to queue") {
		t.Fatalf("unsent slash draft should not show queue guidance:\n%s", view)
	}
}

func TestChatBusyViewShowsRunHintForBusyImmediateSlashDraft(t *testing.T) {
	for _, draft := range []string{"/status", "/btw remember this"} {
		t.Run(draft, func(t *testing.T) {
			m := newModel(nil, "", "", "")
			m.width = 120
			m.height = 24
			m.startBusy()
			m.busySince = time.Now().Add(-12 * time.Second)
			m.input.SetValue(draft)

			view := m.View()
			if !strings.Contains(view, "Working (12s) · Enter to run · Esc to interrupt · Ctrl+C clears draft") {
				t.Fatalf("expected busy-immediate slash draft run guidance:\n%s", view)
			}
			if strings.Contains(view, "Slash commands are disabled while working") {
				t.Fatalf("busy-immediate slash draft should not show disabled guidance:\n%s", view)
			}
			if strings.Contains(view, "Enter to queue") {
				t.Fatalf("busy-immediate slash draft should not show queue guidance:\n%s", view)
			}
		})
	}
}

func TestChatBusyViewDoesNotQueueEditedSlashDraft(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("/permissions")
	m.status = "/model disabled while working"

	view := m.View()
	if !strings.Contains(view, "Working (12s) · Slash commands are disabled while working · Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected edited slash draft busy guidance:\n%s", view)
	}
	if strings.Contains(view, "/model disabled while working") {
		t.Fatalf("edited slash draft should not show stale blocked status:\n%s", view)
	}
	if strings.Contains(view, "Enter to queue") {
		t.Fatalf("edited slash draft should not show queue guidance:\n%s", view)
	}
}

func TestChatBusyViewIgnoresStaleBlockedSlashStatusForNormalDraft(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.input.SetValue("normal follow up")
	m.status = "/model disabled while working"

	view := m.View()
	if strings.Contains(view, "/model disabled while working") {
		t.Fatalf("stale blocked slash status should not label normal drafts:\n%s", view)
	}
	if !strings.Contains(view, "Working (12s) · Enter to queue · Esc to interrupt · Ctrl+C clears draft") {
		t.Fatalf("expected normal draft queue guidance after slash edit:\n%s", view)
	}
}

func TestChatBusyViewShowsProviderRetryStatus(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 140
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.providerRetryStatus = "API rate limited, retrying in 1s (1/3)"
	m.providerRetryUntil = time.Now().Add(time.Second)

	view := m.View()
	if !strings.Contains(view, "API rate limited, retrying in 1s (1/3) (12s) · Type follow-up, Enter to queue · Esc/Ctrl+C to interrupt") {
		t.Fatalf("expected retry status in busy line:\n%s", view)
	}
}

func TestChatBusyViewIgnoresExpiredProviderRetryStatus(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.providerRetryStatus = "API rate limited, retrying in 1s (1/3)"
	m.providerRetryUntil = time.Now().Add(-time.Second)

	view := m.View()
	if strings.Contains(view, "API rate limited") {
		t.Fatalf("expired retry status should not render:\n%s", view)
	}
	if !strings.Contains(view, "Working (12s) · Type follow-up, Enter to queue · Esc/Ctrl+C to interrupt") {
		t.Fatalf("expected working status after retry expiry:\n%s", view)
	}
}

func TestApprovalBusyViewDoesNotDuplicatePromptInBusyStatus(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: sleep 30"

	view := m.View()
	if strings.Contains(view, "Approval required · shell command") {
		t.Fatalf("approval view should not duplicate the prompt in a busy status line:\n%s", view)
	}
	if strings.Contains(view, "Esc/Ctrl+C to interrupt") {
		t.Fatalf("approval busy status line should not advertise esc as interrupt:\n%s", view)
	}
	if count := strings.Count(view, "Approval required"); count != 1 {
		t.Fatalf("approval view should show one approval title, got %d:\n%s", count, view)
	}
}

func TestChatFooterShowsEffectiveThinkingAndEffort(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.width = 80
	m.height = 24

	view := m.View()
	assertFooterLastLine(t, view, "deepseek-v4-flash . high")
	assertFooterLastLine(t, view, "thinking: on")
}

func TestChatFooterUsesSemanticColorSegments(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := newModel(nil, "deepseek-v4-pro", "max", "on")
	m.width = 100
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale-theme-colors"
	m.viewMode = app.ViewModeFocus

	view := m.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "\x1b[") {
		t.Fatalf("expected styled footer segments, got %q in view:\n%s", footer, view)
	}
	plain := xansi.Strip(footer)
	for _, want := range []string{
		"deepseek-v4-pro . max",
		"thinking: on",
		"whale-theme-colors",
		"focus",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected stripped footer to contain %q, got %q", want, plain)
		}
	}
}

func TestModelSetRefreshesHeaderCache(t *testing.T) {
	m := newModel(nil, "old-model", "high", "on")
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback after first resize")
	}
	if header := m.startupHeaderText(); !strings.Contains(header, "model:     old-model") {
		t.Fatalf("expected initial header model:\n%s", header)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:   service.EventLocalSubmitResult,
		Status: "info",
		Text:   "model set: newer-model  effort: low  thinking: off",
	}))
	m = next.(model)

	header := m.startupHeaderText()
	if !strings.Contains(header, "model:     newer-model") {
		t.Fatalf("expected refreshed header after model set:\n%s", header)
	}
	if strings.Contains(header, "model:     old-model") {
		t.Fatalf("expected stale header model to disappear:\n%s", header)
	}
	view := m.View()
	if strings.Contains(view, "model set: newer-model") {
		t.Fatalf("expected printed model set result not to repeat in tail viewport:\n%s", view)
	}
	assertFooterLastLine(t, view, "newer-model . low")
	assertFooterLastLine(t, view, "thinking: off")
}

func TestChatStoppingViewShowsStoppingAboveComposer(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.startBusy()
	m.stopping = true
	m.busySince = time.Now().Add(-(time.Minute + 5*time.Second))
	view := m.View()
	if !strings.Contains(view, "Stopping (1m 05s)") {
		t.Fatalf("expected stopping status line with continued elapsed time:\n%s", view)
	}
	if strings.Contains(view, "to interrupt") {
		t.Fatalf("stopping view should not show interrupt hint:\n%s", view)
	}
}

func TestChatStoppingViewShowsBlockedSlashStatus(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.stopping = true
	m.busySince = time.Now().Add(-(time.Minute + 5*time.Second))
	m.input.SetValue("/model")
	m.status = "/model disabled while stopping"

	view := m.View()
	if !strings.Contains(view, "/model disabled while stopping (1m 05s)") {
		t.Fatalf("expected blocked slash stopping status line:\n%s", view)
	}
	if strings.Contains(view, "Enter to queue") {
		t.Fatalf("blocked slash while stopping should not show queue guidance:\n%s", view)
	}
	if strings.Contains(view, "to interrupt") {
		t.Fatalf("stopping view should not show interrupt hint:\n%s", view)
	}
}

func TestApprovalViewHidesToolCallID(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolCallID = "tc-123"
	m.approval.toolName = "edit"
	m.approval.reason = "edit: internal/tui/model.go"
	view := m.View()
	if !strings.Contains(view, "Approval required") || !strings.Contains(view, "edit") {
		t.Fatalf("expected approval header in view:\n%s", view)
	}
	if strings.Contains(view, "id: tc-123") {
		t.Fatalf("approval view should not expose tool call id:\n%s", view)
	}
}

func TestApprovalViewSeparatesToolNameFromDetail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"
	view := m.View()
	if strings.Contains(view, "shell_run: date") {
		t.Fatalf("approval view should not repeat tool name in body:\n%s", view)
	}
	if strings.Contains(view, "shell_run") {
		t.Fatalf("approval view should not expose internal shell tool name:\n%s", view)
	}
	if !strings.Contains(view, "Approval required") || !strings.Contains(view, "shell command") || !strings.Contains(xansi.Strip(view), "$ date") {
		t.Fatalf("expected separated approval tool and detail:\n%s", view)
	}
}

func TestApprovalViewHidesDuplicatePendingToolRow(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: git diff -- internal/tui/render.go | head -600"
	m.assembler.AddToolCall("tool-1", "shell_run", "Running git diff -- internal/tui/render.go | head -600")

	view := xansi.Strip(m.View())
	if strings.Contains(view, "Running git diff") {
		t.Fatalf("approval view should hide duplicate pending tool row:\n%s", view)
	}
	if count := strings.Count(view, "git diff -- internal/tui/render.go | head -600"); count != 1 {
		t.Fatalf("approval view should render the command exactly once, got %d:\n%s", count, view)
	}
	if !strings.Contains(view, "$ git diff -- internal/tui/render.go | head -600") {
		t.Fatalf("approval view should render a command body:\n%s", view)
	}
}

func TestApprovalViewShortensShellSessionScope(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"
	m.approval.metadata = map[string]any{"approval_session_scope": "this shell command"}

	view := xansi.Strip(m.View())
	if !strings.Contains(view, "Allow session (s) same command") {
		t.Fatalf("expected shortened shell session option:\n%s", view)
	}
	if strings.Contains(view, "Allow for session") || strings.Contains(view, "this shell command") {
		t.Fatalf("approval shell session option should stay compact:\n%s", view)
	}
}

func TestApprovalViewPreservesExactShellCommandText(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	cmd := "printf 'a  b'\n  echo \"c  d\" | head -1"
	m.approval.reason = "shell_run: " + cmd

	view := xansi.Strip(m.View())
	if !strings.Contains(view, "$ printf 'a  b'") {
		t.Fatalf("approval should preserve quoted repeated spaces:\n%s", view)
	}
	if !strings.Contains(view, "  echo \"c  d\" | head -1") {
		t.Fatalf("approval should preserve embedded newline and indentation:\n%s", view)
	}
	if strings.Contains(view, "printf 'a b'") || strings.Contains(view, "echo \"c d\"") {
		t.Fatalf("approval collapsed command whitespace:\n%s", view)
	}
}

func TestApprovalViewShowsDiffMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "edit"
	m.approval.reason = "edit: a.txt"
	m.approval.metadata = testFileDiffMetadata()
	view := m.View()
	for _, want := range []string{"a.txt (+1 -1)", "-world", "+whale"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval diff metadata to contain %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"--- a/a.txt", "+++ b/a.txt", "@@ -1 +1 @@"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("approval diff should hide raw diff header %q:\n%s", unwanted, view)
		}
	}
}

func TestApprovalViewShowsFileReviewSessionScope(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "apply_patch"
	m.approval.reason = "apply_patch: a.txt, b.txt"
	m.approval.metadata = testFileDiffMetadata()
	m.approval.metadata["approval_kind"] = "file_diff_review"
	m.approval.metadata["approval_session_scope"] = "these files: a.txt, b.txt"

	view := m.View()
	for _, want := range []string{
		"Approval required: file diff review",
		"Review file changes before Whale applies them.",
		"Allow session (s)",
		"a.txt (+1 -1)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Allow for session =") || strings.Contains(view, "these files: a.txt, b.txt") {
		t.Fatalf("approval view should not expose session scope detail:\n%s", view)
	}
}

func TestApprovalViewUsesSimilarCommandsLabelForShellFamily(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: go test ./internal/policy"
	m.approval.metadata = map[string]any{
		"approval_session_scope": "this bounded shell command family",
		"shell_approval_family":  true,
	}

	view := m.View()
	if !strings.Contains(view, "Allow similar commands") {
		t.Fatalf("expected similar-commands option:\n%s", view)
	}
	if strings.Contains(view, "this bounded shell command family") || strings.Contains(view, "Allow for session =") {
		t.Fatalf("approval view should not expose shell scope detail:\n%s", view)
	}
}

func TestApprovalViewKeepsLargeDiffPreviewBounded(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "apply_patch"
	m.approval.reason = "apply_patch: roadmap.md"
	m.approval.metadata = largeTranslationDiffMetadata(190, 190)
	m.approval.metadata["approval_kind"] = "file_diff_review"

	view := m.View()
	if !strings.Contains(view, "Allow once") || !strings.Contains(view, "Deny") {
		t.Fatalf("expected approval controls to remain visible:\n%s", view)
	}
	if !strings.Contains(view, "... diff truncated (") {
		t.Fatalf("expected approval diff preview to stay bounded:\n%s", view)
	}
	if strings.Contains(view, "+English 189") {
		t.Fatalf("approval preview should not render the full large diff:\n%s", view)
	}
}

func TestApprovalViewShowsMemoryWriteMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "remember"
	m.approval.reason = "remember: Writes long-term Whale memory."
	m.approval.metadata = map[string]any{
		"approval_kind":          "memory_write",
		"approval_session_scope": "global memory: response-style",
		"memory_scope":           "global",
		"memory_type":            "user",
		"memory_name":            "response-style",
		"memory_description":     "prefers concise Chinese answers",
		"memory_content_preview": "Answer in concise Chinese with repo evidence.",
		"memory_write_status":    "created",
	}

	view := m.View()
	for _, want := range []string{
		"Approval required: memory write",
		"Review memory before Whale saves it.",
		"Created memory: global/user",
		"Name: response-style",
		"Description: prefers concise Chinese answers",
		"Content:",
		"Answer in concise Chinese with repo evidence.",
		"Allow session (s)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Allow for session =") || strings.Contains(view, "global memory: response-style") {
		t.Fatalf("approval view should not expose memory session scope detail:\n%s", view)
	}
}

func TestApprovalViewShowsMemoryDeleteMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "forget"
	m.approval.reason = "forget: Deletes long-term Whale memory."
	m.approval.metadata = map[string]any{
		"approval_kind":          "memory_delete",
		"approval_session_scope": "project memory: roadmap",
		"memory_scope":           "project",
		"memory_type":            "project",
		"memory_name":            "roadmap",
		"memory_description":     "plugin-first memory",
		"memory_content_preview": "Memory is the first official plugin.",
	}

	view := m.View()
	for _, want := range []string{
		"Approval required: memory delete",
		"Review memory before Whale deletes it.",
		"Delete memory: project/project",
		"Name: roadmap",
		"Description: plugin-first memory",
		"Memory is the first official plugin.",
		"Allow session (s)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Allow for session =") || strings.Contains(view, "project memory: roadmap") {
		t.Fatalf("approval view should not expose memory session scope detail:\n%s", view)
	}
}

func TestApprovalDiffMetadataRendersMultipleFiles(t *testing.T) {
	metadata := map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new",
				"additions":    1,
				"deletions":    1,
			},
			map[string]any{
				"path":         "b.txt",
				"unified_diff": "--- a/b.txt\n+++ b/b.txt\n@@ -0,0 +1 @@\n+created",
				"additions":    1,
				"deletions":    0,
			},
		},
	}
	got := renderApprovalDiffMetadata(metadata, 80)
	for _, want := range []string{"a.txt (+1 -1)", "-old", "+new", "b.txt (+1 -0)", "+created"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected rendered approval diff to contain %q:\n%s", want, got)
		}
	}
}

func TestApprovalDiffMetadataPreviewErrorFallback(t *testing.T) {
	metadata := map[string]any{
		"kind":          "file_diff",
		"preview_error": "could not read file",
	}
	got := renderApprovalDiffMetadata(metadata, 80)
	if !strings.Contains(got, "diff preview unavailable: could not read file") {
		t.Fatalf("expected preview error fallback, got:\n%s", got)
	}
}

func TestApprovalDiffMetadataTruncatesLongPreview(t *testing.T) {
	metadata := map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1,4 +1,4 @@\n one\n-two\n+TWO\n three\n-four\n+FOUR",
				"additions":    2,
				"deletions":    2,
			},
		},
	}
	got := renderApprovalDiffMetadata(metadata, 4)
	if !strings.Contains(got, "... diff truncated (") {
		t.Fatalf("expected hidden-line truncation marker, got:\n%s", got)
	}
	if strings.Contains(got, "@@") {
		t.Fatalf("truncated approval diff should still hide hunk headers:\n%s", got)
	}
}

func TestFileDiffMetadataPreviewAllowsLargeTranslationDiff(t *testing.T) {
	metadata := largeTranslationDiffMetadata(190, 190)
	got := renderFileDiffMetadataPlain(metadata, fileDiffPreviewMaxLines)
	if !strings.Contains(got, "-中文 000") {
		t.Fatalf("expected diff preview to include deletions:\n%s", got)
	}
	if !strings.Contains(got, "+English 189") {
		t.Fatalf("expected 400-line diff preview to include additions:\n%s", got)
	}
	if strings.Contains(got, "... diff truncated (") {
		t.Fatalf("expected translation-size diff to fit in preview:\n%s", got)
	}
}

func largeTranslationDiffMetadata(deletions, additions int) map[string]any {
	lines := []string{
		"--- a/roadmap.md",
		"+++ b/roadmap.md",
		fmt.Sprintf("@@ -1,%d +1,%d @@", deletions, additions),
	}
	for i := 0; i < deletions; i++ {
		lines = append(lines, fmt.Sprintf("-中文 %03d", i))
	}
	for i := 0; i < additions; i++ {
		lines = append(lines, fmt.Sprintf("+English %03d", i))
	}
	return map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "roadmap.md",
				"unified_diff": strings.Join(lines, "\n"),
				"additions":    additions,
				"deletions":    deletions,
			},
		},
	}
}

func TestApprovalDiffMetadataShowsFileTruncatedMarker(t *testing.T) {
	metadata := map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new",
				"additions":    1,
				"deletions":    1,
				"truncated":    true,
			},
		},
	}
	got := renderApprovalDiffMetadata(metadata, 80)
	if !strings.Contains(got, "... diff truncated ...") {
		t.Fatalf("expected per-file truncation marker, got:\n%s", got)
	}
}

func TestToolResultShowsDiffMetadata(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-1",
		ToolName:   "edit",
		Text:       `edit: a.txt`,
	}))
	m = next.(model)
	next, cmd := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-1",
		ToolName:   "edit",
		Text:       `{"success":true,"data":{"payload":{"file_path":"a.txt","replacements":1}}}`,
		Metadata:   testFileDiffMetadata(),
	}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected completed tool cell to leave live assembler empty, got %+v", snap)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n"); !strings.Contains(got, "Edited a.txt") {
		t.Fatalf("expected completed tool cell in transcript:\n%s", got)
	}
	if got := strings.Join(m.renderDiffs(), "\n"); !strings.Contains(got, "+whale") {
		t.Fatalf("expected /diff content from metadata:\n%s", got)
	}
}

func TestToolResultShowsLargeTranslationDiffTailInChat(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 30
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-translation",
		ToolName:   "write",
		Text:       `write: roadmap.md`,
	}))
	m = next.(model)
	next, cmd := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-translation",
		ToolName:   "write",
		Text:       `{"success":true,"data":{"payload":{"file_path":"roadmap.md"}}}`,
		Metadata:   largeTranslationDiffMetadata(190, 190),
	}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 120), "\n")
	if !strings.Contains(got, "Edited roadmap.md") {
		t.Fatalf("expected completed tool cell in transcript:\n%s", got)
	}
	if !strings.Contains(got, "+English 189") {
		t.Fatalf("expected output box diff preview to include translated additions:\n%s", got)
	}
	if strings.Contains(got, "... diff truncated (") {
		t.Fatalf("expected translation-size diff to fit in output preview:\n%s", got)
	}
}

func testFileDiffMetadata() map[string]any {
	return map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-world\n+whale",
				"additions":    1,
				"deletions":    1,
			},
		},
	}
}

func TestChatLiveViewUsesViewportForLongOutput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.append("assistant", strings.Repeat("line\n", 80))
	view := m.View()
	if !strings.Contains(view, "Type message or command") {
		t.Fatalf("expected composer to remain visible with long live output:\n%s", view)
	}
	if got := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; got != m.height {
		t.Fatalf("expected view to keep terminal height %d, got %d:\n%s", m.height, got, view)
	}
}

func TestFormatElapsedCompact(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{elapsed: 0, want: "0s"},
		{elapsed: 12 * time.Second, want: "12s"},
		{elapsed: time.Minute + 5*time.Second, want: "1m 05s"},
		{elapsed: time.Hour + 2*time.Minute + 9*time.Second, want: "1h 02m 09s"},
	}
	for _, tc := range cases {
		if got := formatElapsedCompact(tc.elapsed); got != tc.want {
			t.Fatalf("formatElapsedCompact(%v) = %q, want %q", tc.elapsed, got, tc.want)
		}
	}
}

func TestBtwBusySubmitDispatchesLocalSubmit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.submitPromptWhileBusy("/btw what is happening?")
	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %d", len(*intents))
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmitLocal || got.Input != "/btw what is happening?" {
		t.Fatalf("unexpected intent: %+v", got)
	}
	if m.localSubmitPending != 1 {
		t.Fatalf("expected pending local submit, got %d", m.localSubmitPending)
	}
}

func TestBtwSlashSuggestionDoesNotAutoRunWithoutQuestion(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.input.SetValue("/bt")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/btw ")
	suggestion, ok := m.selectedSlashSuggestion()
	if !ok {
		t.Fatal("expected /btw slash suggestion")
	}
	if suggestion.AutoRun {
		t.Fatal("/btw should not auto-run from suggestions without a question")
	}
}

func TestBtwSlashSuggestionEnterCompletesWithSpace(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.input.SetValue("/bt")
	m.updateSlashMatches()

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.input.Value(); got != "/btw " {
		t.Fatalf("expected /btw completion with trailing space, got %q", got)
	}
	if m.hasSlashSuggestions() {
		t.Fatalf("expected suggestions hidden after required-arg completion, got %+v", m.slash.matches)
	}
}

func TestBtwExactSlashEnterShowsUsage(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/btw")
	m.updateSlashMatches()

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(*intents) != 1 {
		t.Fatalf("expected /btw usage local submit, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmitLocal || got.Input != "/btw" {
		t.Fatalf("unexpected intent: %+v", got)
	}
}

func TestBtwSecondSubmitWhileLoadingIsBlocked(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.btwPanel = btwPanelState{visible: true, id: 1, question: "first?", loading: true}
	m.input.SetValue("/btw second?")

	m.submitLocalNoTurn(appcommands.SubmitClassification{Line: "/btw second?", Class: appcommands.SubmitLocalReadOnly})

	if len(*intents) != 0 {
		t.Fatalf("expected no second /btw intent while loading, got %+v", *intents)
	}
	if got := m.input.Value(); got != "/btw second?" {
		t.Fatalf("expected input to remain editable, got %q", got)
	}
	if m.localSubmitPending != 0 {
		t.Fatalf("expected no pending local submit, got %d", m.localSubmitPending)
	}
	if m.status != "/btw is already answering" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestBtwDeltaEventsAreNotBatchableAcrossRequests(t *testing.T) {
	first := service.Event{Kind: service.EventBtwDelta, Text: "first", Count: 1}
	second := service.Event{Kind: service.EventBtwDelta, Text: "second", Count: 2}
	if shouldBatchServiceEvent(first) {
		t.Fatal("btw deltas should not be batched because request ids can differ")
	}
	events := appendBatchedServiceEvent(nil, first)
	events = appendBatchedServiceEvent(events, second)
	if len(events) != 2 {
		t.Fatalf("expected separate btw delta events, got %d: %+v", len(events), events)
	}
	if events[0].Text != "first" || events[0].Count != 1 || events[1].Text != "second" || events[1].Count != 2 {
		t.Fatalf("unexpected btw delta events: %+v", events)
	}
}

func TestBtwPanelRendersAndDoesNotAppendTranscript(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 24
	before := len(m.transcript)
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventBtwStarted, Text: "quick?", Count: 1}))
	if !m.btwPanel.visible || !m.btwPanel.loading {
		t.Fatalf("expected loading btw panel: %+v", m.btwPanel)
	}
	view := m.View()
	if !strings.Contains(view, "/btw") || !strings.Contains(view, "Answering...") {
		t.Fatalf("expected btw loading panel in view:\n%s", view)
	}
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventBtwDone, Text: "**answer**", Count: 1}))
	view = m.View()
	if !strings.Contains(view, "answer") || !strings.Contains(view, "Ctrl+P/Ctrl+N") {
		t.Fatalf("expected btw answer panel in view:\n%s", view)
	}
	if len(m.transcript) != before {
		t.Fatalf("btw answer should not append transcript, before=%d after=%d", before, len(m.transcript))
	}
}

func TestBtwPanelKeysDismissBeforeBusyInterrupt(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.btwPanel = btwPanelState{visible: true, id: 1, question: "quick?", response: "answer"}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.btwPanel.visible {
		t.Fatal("expected ctrl+c to dismiss btw panel")
	}
	if len(*intents) != 0 {
		t.Fatalf("ctrl+c with btw panel should not interrupt busy turn, got %+v", *intents)
	}
}

func TestBtwPanelDoesNotConsumeChatInputKeys(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.btwPanel = btwPanelState{visible: true, id: 1, question: "quick?", response: "answer"}
	m.input.SetValue("hello")

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if got := m.input.Value(); got != "hello " {
		t.Fatalf("expected space to reach composer, got %q", got)
	}

	m.input.SetValue("follow up")
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(*intents) != 1 {
		t.Fatalf("expected enter to submit prompt, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSubmit || got.Input != "follow up" {
		t.Fatalf("unexpected submit intent: %+v", got)
	}
	if !m.btwPanel.visible {
		t.Fatal("btw panel should remain visible after chat input submit")
	}
}

func TestBtwPanelScrollKeys(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.height = 9
	m.width = 80
	m.btwPanel = btwPanelState{
		visible:  true,
		id:       1,
		question: "quick?",
		response: strings.Repeat("line\n\n", 20),
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.btwPanel.scroll == 0 {
		t.Fatal("expected ctrl+n to scroll btw panel")
	}
	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.btwPanel.scroll != 0 {
		t.Fatalf("expected ctrl+p to scroll back to top, got %d", m.btwPanel.scroll)
	}
}

func TestSummarizeToolResultForChat_ShellRunSuccessShowsOutputSummary(t *testing.T) {
	raw := `{"success":true,"code":"ok","data":{"status":"ok","metrics":{"exit_code":0,"duration_ms":29},"payload":{"command":"date","stdout":"Sun May 3\n","stderr":""}}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_ok" {
		t.Fatalf("expected result_ok role, got %q", role)
	}
	want := "✓ · 29ms\nSun May 3"
	if got != want {
		t.Fatalf("unexpected summary text:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSummarizeToolResultForChat_ShellWaitExitedShowsSuccess(t *testing.T) {
	raw := `{"success":true,"code":"ok","data":{"status":"exited","metrics":{"exit_code":0},"payload":{"command":"sleep 1; echo whale-background-smoke","stdout":"whale-background-smoke\n","stderr":"","done":true}}}`
	role, got := summarizeToolResultForChat("shell_wait", raw)
	if role != "result_ok" {
		t.Fatalf("expected result_ok role, got %q: %s", role, got)
	}
	want := "✓\nwhale-background-smoke"
	if got != want {
		t.Fatalf("unexpected summary text:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestShellRunTranscriptKeepsStatusAndOutputSeparate(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-shell",
		ToolName:   "shell_run",
		Text:       `shell_run: {"command":"cd internal/tui && wc -l model.go model_events.go model_keys.go model_prompt.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"code":"ok","data":{"status":"ok","metrics":{"exit_code":0,"duration_ms":23},"payload":{"command":"cd internal/tui && wc -l model.go model_events.go model_keys.go model_prompt.go","stdout":"284 model.go\n202 model_events.go\n401 model_keys.go\n88 model_prompt.go\n975 total\n","stderr":""}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-shell",
		ToolName:   "shell_run",
		Text:       raw,
	}))
	m = next.(model)
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(rendered, "23ms 284 model.go") {
		t.Fatalf("status and shell output collapsed onto one line:\n%s", rendered)
	}
	for _, want := range []string{"Ran cd internal/tui && wc -l", "✓ · 23ms", "284 model.go", "202 model_events.go", "975 total"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered transcript to contain %q:\n%s", want, rendered)
		}
	}
}

func TestSummarizeToolResultForChat_ShellRunFailureShowsReason(t *testing.T) {
	raw := `{"success":false,"code":"exec_failed","message":"command failed","data":{"status":"error","summary":"command failed","metrics":{"exit_code":2,"duration_ms":1210},"payload":{"stderr":"ls: cannot access x: No such file or directory\n","stdout":""}}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_failed" {
		t.Fatalf("expected result_failed role, got %q", role)
	}
	want := "✗ (exit 2) · 1.2s\nls: cannot access x: No such file or directory"
	if got != want {
		t.Fatalf("unexpected summary text:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSummarizeToolResultForChat_RequestReplanHidesInternalRecoveryText(t *testing.T) {
	raw := `{"success":false,"code":"request_replan","error":"recovery exhausted, replan required","data":{"tool_name":"mcp__fs__search_files","last_error":"{\"success\":false,\"code\":\"mcp_tool_error\",\"error\":\"Error: Access denied - path outside allowed directories: /workspace not in /tmp\"}"}}`
	role, got := summarizeToolResultForChat("mcp__fs__search_files", raw)
	if role != "result_failed" {
		t.Fatalf("expected result_failed role, got %q", role)
	}
	if strings.Contains(got, "recovery exhausted") || strings.Contains(got, "replan required") {
		t.Fatalf("summary leaked internal recovery text: %q", got)
	}
	if !strings.Contains(got, "DENIED") || !strings.Contains(got, "outside allowed directories") {
		t.Fatalf("expected user-facing access denial, got %q", got)
	}
}

func TestSummarizeToolResultForChat_PermissionDeniedShowsDenied(t *testing.T) {
	raw := `{"success":false,"code":"permission_denied","message":"path outside MCP fs allowed directories: /workspace not in /tmp"}`
	role, got := summarizeToolResultForChat("mcp__fs__search_files", raw)
	if role != "result_denied" {
		t.Fatalf("expected result_denied role, got %q", role)
	}
	want := "DENIED · path outside MCP fs allowed directories: /workspace not in /tmp"
	if got != want {
		t.Fatalf("unexpected summary:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSummarizeToolResultForChat_NonShellSummarized(t *testing.T) {
	raw := `{"success":true,"data":{"metrics":{"total_matches":3},"payload":{"items":["a.go","b.go","c.go"]}}}`
	role, got := summarizeToolResultForChat("search_files", raw)
	if role != "result_ok" {
		t.Fatalf("expected result_ok role for non-shell, got: %q", role)
	}
	if got != "✓ · 3 matches" {
		t.Fatalf("expected summarized non-shell payload, got: %q", got)
	}
	if strings.Contains(got, "{") || strings.Contains(got, "payload") {
		t.Fatalf("summary must not expose raw json: %q", got)
	}
}

func TestSummarizeToolResultForChat_Denied(t *testing.T) {
	raw := `{"success":false,"code":"approval_denied","message":"tool approval denied"}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_denied" || got != "DENIED · tool approval denied" {
		t.Fatalf("unexpected denied summary: role=%q text=%q", role, got)
	}
}

func TestSummarizeToolResultForChat_AskModeBlockedShowsProductCommands(t *testing.T) {
	raw := `{"success":false,"code":"ask_mode_blocked","message":"tool unavailable in ask mode","summary":"Current mode: ask. Ask mode only allows read-only tools. To execute or modify files, switch to agent mode with /agent or Shift+Tab. To propose a reviewed approach first, switch to plan mode with /plan or Shift+Tab.","data":{"current_mode":"ask","suggested_modes":["/agent","/plan","Shift+Tab"]}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_failed" {
		t.Fatalf("expected result_failed role, got %q", role)
	}
	want := "✗ · Current mode: ask. Ask mode only allows read-only tools. To execute or modify files, switch to agent mode with /agent or Shift+Tab. To propose a reviewed approach first, switch to plan mode with /plan or Shift+Tab."
	if got != want {
		t.Fatalf("unexpected ask-mode summary:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSummarizeToolResultForChat_Timeout(t *testing.T) {
	raw := `{"success":false,"code":"timeout","message":"command timed out","data":{"metrics":{"duration_ms":15000}}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_timeout" || got != "TIMEOUT · 15s" {
		t.Fatalf("unexpected timeout summary: role=%q text=%q", role, got)
	}
}

func TestSummarizeToolResultForChat_Canceled(t *testing.T) {
	raw := `{"success":false,"code":"cancelled","message":"context canceled"}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_canceled" || got != "CANCELED" {
		t.Fatalf("unexpected canceled summary: role=%q text=%q", role, got)
	}
}

func TestToolDeniedDoesNotAddNoFinalAnswerNotice(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventReasoningDelta, Text: "I should edit the file."}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-1",
		ToolName:   "edit",
		Text:       `edit: internal/tui/model.go`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-1",
		ToolName:   "edit",
		Text:       `{"success":false,"code":"approval_denied","message":"tool approval denied"}`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	for _, entry := range snap {
		if strings.Contains(entry.Text, "did not produce a visible answer") {
			t.Fatalf("unexpected reasoning-only status after tool denial: %+v", snap)
		}
	}
}

func TestSummarizeToolResultForChat_FailedNoExitCodeDoesNotShowZero(t *testing.T) {
	raw := `{"success":false,"code":"exec_failed","message":"command failed","data":{"metrics":{"duration_ms":41},"payload":{"stderr":"unknown flag: --bad"}}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_failed" {
		t.Fatalf("expected result_failed role, got %q", role)
	}
	if got == "✗ (exit 0) · 41ms · unknown flag: --bad" {
		t.Fatalf("must not show fake exit 0: %q", got)
	}
	if got != "✗ · 41ms\nunknown flag: --bad" {
		t.Fatalf("unexpected failed summary: %q", got)
	}
}

func TestSummarizeToolResultForChat_OkWithoutSuccessField(t *testing.T) {
	raw := `{"code":"ok","data":{"status":"ok","metrics":{"exit_code":0,"duration_ms":237},"payload":{"stdout":"142.251.214.110","stderr":""}}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_ok" {
		t.Fatalf("expected result_ok role, got %q", role)
	}
	if got != "✓ · 237ms\n142.251.214.110" {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func TestSummarizeToolResultForChat_ShellOutputTruncated(t *testing.T) {
	stdout := strings.Join([]string{
		"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9", "l10", "l11", "l12", "l13", "l14",
	}, `\n`) + `\n`
	raw := `{"success":true,"data":{"status":"ok","payload":{"stdout":"` + stdout + `"}}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_ok" {
		t.Fatalf("expected result_ok role, got %q", role)
	}
	for _, want := range []string{"l1", "l2", "l13", "l14"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected compact output to keep %q, got: %q", want, got)
		}
	}
	if strings.Contains(got, "l3") || strings.Contains(got, "l12") {
		t.Fatalf("expected middle output to be omitted, got: %q", got)
	}
	if !strings.Contains(got, "10 lines omitted") {
		t.Fatalf("expected omitted output marker, got: %q", got)
	}
}

func TestToolResultUpdatesToolCellWithoutRawJSON(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventToolCall, ToolCallID: "tc-1", ToolName: "read_file", Text: `read_file: {"file_path":"internal/tui/model.go"}`}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, cmd := m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "tc-1", ToolName: "read_file", Text: raw}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected completed read cell to leave live assembler empty, got %+v", snap)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "Read internal/tui/model.go") {
		t.Fatalf("expected completed read cell in transcript:\n%s", got)
	}
}

func TestMultipleToolResultsWaitForPendingToolCallsBeforeCommit(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventToolCall, ToolCallID: "todo-1", ToolName: "todo_update", Text: `todo_update: Summarize findings with severity tags`}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolCall, ToolCallID: "todo-2", ToolName: "todo_update", Text: `todo_update: Perform structured file-by-file review`}))
	m = next.(model)

	raw := `{"success":true,"data":{"count":2,"items":[]}}`
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "todo-1", ToolName: "todo_update", Text: raw}))
	m = next.(model)
	if got := len(m.assembler.Snapshot()); got != 2 {
		t.Fatalf("expected pending tool calls to stay live until all results arrive, got %d", got)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n"); strings.Contains(got, "✓") {
		t.Fatalf("first result should not create a standalone checkmark:\n%s", got)
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventToolResult, ToolCallID: "todo-2", ToolName: "todo_update", Text: raw}))
	m = next.(model)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected completed tool cells to be committed, got %+v", m.assembler.Snapshot())
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	for _, want := range []string{"Todo updated", "Summarize findings with severity tags", "Perform structured file-by-file review"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered transcript to contain %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "\n┃ ✓") {
		t.Fatalf("todo results should not render as standalone checkmarks:\n%s", rendered)
	}
}

func TestUnmatchedToolResultRefreshesLiveViewportWhilePendingCallsRemain(t *testing.T) {
	m := model{
		assembler:  tuirender.NewAssembler(),
		mode:       modeChat,
		page:       pageChat,
		width:      100,
		height:     16,
		followTail: true,
	}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-1",
		ToolName:   "shell_run",
		Text:       `shell_run: {"command":"echo first"}`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-2",
		ToolName:   "shell_run",
		Text:       `shell_run: {"command":"echo second"}`,
	}))
	m = next.(model)

	beforeGeneration := m.chat.generation
	beforeView := m.View()

	raw := `{"success":true,"data":{"status":"ok","metrics":{"duration_ms":12},"payload":{"stdout":"visible output"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "missing-id",
		ToolName:   "shell_run",
		Text:       raw,
	}))
	m = next.(model)

	if m.chat.generation <= beforeGeneration {
		t.Fatalf("expected unmatched live tool result to refresh chat viewport, generation before=%d after=%d", beforeGeneration, m.chat.generation)
	}
	if got := m.View(); got == beforeView || !strings.Contains(got, "visible output") {
		t.Fatalf("expected unmatched live tool result to appear in chat viewport:\n%s", got)
	}
	if got := len(m.assembler.Snapshot()); got != 3 {
		t.Fatalf("expected pending tool calls plus unmatched result to remain live, got %d", got)
	}
}

func TestTaskToolResultSummaries(t *testing.T) {
	rawParallel := `{"ok":true,"success":true,"data":{"model":"deepseek-v4-flash","results":[{"index":0,"output":"a"},{"index":1,"output":"b"}]},"metadata":{"duration_ms":42}}`
	role, got := summarizeToolResultForChat("parallel_reason", rawParallel)
	if role != "result_ok" || got != "✓ · 42ms · 2 result(s)" {
		t.Fatalf("unexpected parallel summary: role=%q got=%q", role, got)
	}
	rawSubagent := `{"ok":true,"success":true,"data":{"role":"review","summary":"no permission bypass found"},"metadata":{"duration_ms":1500}}`
	role, got = summarizeToolResultForChat("spawn_subagent", rawSubagent)
	if role != "result_ok" || got != "✓ · 1.5s · review\nno permission bypass found" {
		t.Fatalf("unexpected subagent summary: role=%q got=%q", role, got)
	}
}

func TestTaskActivityEventsUpdateStatusOnly(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTaskStarted, ToolName: "spawn_subagent", Text: "spawn_subagent started · review"}))
	m = next.(model)
	if m.status != "spawn_subagent started · review" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	if len(m.assembler.Snapshot()) != 0 {
		t.Fatalf("task activity event should not add transcript rows: %+v", m.assembler.Snapshot())
	}
}

func TestMCPStatusFailureUpdatesStatusAndLogOnly(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventMCPStatus, Status: "failed", Text: "MCP startup failed: fs. Run /mcp for details."}))
	m = next.(model)
	if m.status != "MCP startup failed: fs. Run /mcp for details." {
		t.Fatalf("unexpected status: %q", m.status)
	}
	if snap := m.assembler.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected MCP failure to stay out of transcript, got: %+v", snap)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "mcp_status" || !strings.Contains(m.logs[0].Summary, "MCP startup failed: fs") {
		t.Fatalf("expected MCP failure log entry, got: %+v", m.logs)
	}
}

func TestTaskProgressUpdatesTaskToolRow(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent: review · inspect internal/tasks",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventTaskProgress,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent running · review · reading internal/tasks/runner.go",
		Metadata: map[string]any{
			"child_session_id": "parent--subagent-tc-task",
			"child_tool":       "read_file",
			"role":             "review",
		},
	}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one tool row, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindSubagent || snap[0].Role != "result_running" {
		t.Fatalf("expected running subagent row, got %+v", snap[0])
	}
	for _, want := range []string{"Subagent review running", "session: parent--subagent-tc-task", "current: read_file", "detail: reading internal/tasks/runner.go"} {
		if !strings.Contains(snap[0].Text, want) {
			t.Fatalf("expected %q in progress row: %+v", want, snap[0])
		}
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventTaskProgress,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       `spawn_subagent running · review · Searched "TaskProgress" in internal/tui (*.go) · 7 matches in 3 files`,
		Metadata: map[string]any{
			"child_session_id": "parent--subagent-tc-task",
			"child_tool":       "grep",
			"role":             "review",
		},
	}))
	m = next.(model)
	snap = m.assembler.Snapshot()
	if !strings.Contains(snap[0].Text, "current: grep") || !strings.Contains(snap[0].Text, `detail: Searched "TaskProgress" in internal/tui (*.go) · 7 matches in 3 files`) {
		t.Fatalf("expected child tool and progress metric to be preserved: %+v", snap[0])
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventTaskProgress,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent compacted · review · Compacted child context (10 -> 3 messages)",
		Status:     "compacted",
		Metadata: map[string]any{
			"child_session_id": "parent--subagent-tc-task",
			"role":             "review",
		},
	}))
	m = next.(model)
	snap = m.assembler.Snapshot()
	if snap[0].Role != "result_running" || !strings.Contains(snap[0].Text, "Subagent review compacted") || !strings.Contains(snap[0].Text, "current: grep") {
		t.Fatalf("expected non-running progress status to update subagent row without losing current tool: %+v", snap[0])
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventTaskProgress,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent completed · review · Child finished",
		Status:     "completed",
		Metadata: map[string]any{
			"child_session_id": "parent--subagent-tc-task",
			"role":             "review",
		},
	}))
	m = next.(model)
	snap = m.assembler.Snapshot()
	if snap[0].Role != "result_ok" || !strings.Contains(snap[0].Text, "Subagent review completed") {
		t.Fatalf("expected completed progress status to update subagent row: %+v", snap[0])
	}

	result := `{"ok":true,"success":true,"data":{"role":"review","child_session_id":"parent--subagent-tc-task","summary":"no permission bypass found"},"metadata":{"duration_ms":1500}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       result,
	}))
	m = next.(model)
	if len(m.transcript) == 0 {
		t.Fatalf("expected completed subagent row in transcript")
	}
	completed := m.transcript[len(m.transcript)-1]
	for _, want := range []string{"Subagent review completed", "session: parent--subagent-tc-task", "current: grep", "duration: 1.5s", "summary: no permission bypass found"} {
		if !strings.Contains(completed.Text, want) {
			t.Fatalf("expected %q in completed row: %+v", want, completed)
		}
	}
}

func TestSubagentFailureUpdatesDedicatedCell(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent: review · inspect internal/tasks",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventTaskProgress,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       "spawn_subagent tool_failed · review · Read internal/tasks/runner.go failed",
		Metadata: map[string]any{
			"child_session_id": "parent--subagent-tc-task",
			"child_tool":       "read_file",
			"role":             "review",
		},
	}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if !strings.Contains(snap[0].Text, "Subagent review failed") || !strings.Contains(snap[0].Text, "current: read_file") {
		t.Fatalf("unexpected progress row: %+v", snap[0])
	}

	result := `{"ok":false,"success":false,"code":"spawn_subagent_failed","error":"subagent failed","data":{"role":"review","child_session_id":"parent--subagent-tc-task"},"metadata":{"duration_ms":41}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       result,
	}))
	m = next.(model)
	if len(m.transcript) == 0 {
		t.Fatalf("expected failed subagent row in transcript")
	}
	failed := m.transcript[len(m.transcript)-1]
	for _, want := range []string{"Subagent review failed", "session: parent--subagent-tc-task", "duration: 41ms", "summary: subagent failed"} {
		if !strings.Contains(failed.Text, want) {
			t.Fatalf("expected %q in failed row: %+v", want, failed)
		}
	}
}

func TestToolCallShowsSearchPatternAndPath(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-search",
		ToolName:   "grep",
		Text:       `grep: assistant_delta in internal/tui (*.go)`,
	}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one tool cell, got %+v", snap)
	}
	want := "Exploring\nSearch assistant_delta in internal/tui (*.go)"
	if snap[0].Text != want {
		t.Fatalf("unexpected search tool call text:\nwant: %q\ngot:  %q", want, snap[0].Text)
	}
}

func TestToolResultKeepsSearchDetailAndAddsSummary(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-search",
		ToolName:   "grep",
		Text:       `grep: assistant_delta in internal/tui (*.go)`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"total_matches":1,"files_matched":1},"payload":{"matches":[]}}}`
	next, cmd := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-search",
		ToolName:   "grep",
		Text:       raw,
	}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected completed search cell to leave live assembler empty, got %+v", snap)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "Search assistant_delta in internal/tui") {
		t.Fatalf("expected completed search cell in transcript:\n%s", got)
	}
}

func TestToolCallShowsWebSearchQuery(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-web",
		ToolName:   "web_search",
		Text:       `web_search: F1 pit strategy tools`,
	}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one web search cell, got %+v", snap)
	}
	want := "Exploring\nSearch web for F1 pit strategy tools"
	if snap[0].Text != want {
		t.Fatalf("unexpected web search tool call text:\nwant: %q\ngot:  %q", want, snap[0].Text)
	}
}

func TestClearScreenResetsStateAndShowsHeader(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
		model:     "deepseek-v4-flash",
		effort:    "high",
		cwd:       "~/work",
		version:   "v0.1.0",
	}
	// Add some state
	m.assembler.AddNotice("old notice")
	m.logs = []logEntry{{Kind: "info", Summary: "old"}}
	m.diffs = []diffEntry{{Source: "x", Line: "old"}}
	m.status = "ready"

	next, cmd := m.Update(svcMsg(service.Event{Kind: service.EventClearScreen}))
	m2 := next.(model)

	if cmd == nil {
		t.Fatal("expected clear screen to return a command")
	}
	if m2.status != "terminal cleared" {
		t.Fatalf("expected status 'terminal cleared', got %q", m2.status)
	}
	if len(m2.logs) != 0 {
		t.Fatalf("expected logs cleared, got %d", len(m2.logs))
	}
	if len(m2.diffs) != 0 {
		t.Fatalf("expected diffs cleared, got %d", len(m2.diffs))
	}
	// The transcript is cleared; the header is rendered as the first chat item.
	snap := m2.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected empty live assembler, got %+v", snap)
	}
	if len(m2.transcript) != 0 {
		t.Fatalf("expected empty transcript, got %d: %+v", len(m2.transcript), m2.transcript)
	}
	view := m2.View()
	if strings.Contains(view, "WHALE") || strings.Contains(view, "██╗") {
		t.Fatalf("expected startup header to be printed to scrollback, not the live viewport:\n%s", view)
	}
	if !m2.startupHeaderPrinted {
		t.Fatal("expected clear screen to schedule startup header print")
	}
	if m2.nativeScrollbackPrinted != len(m2.transcript) {
		t.Fatalf("expected clear screen to reset native scrollback cursor, got cursor %d for %d transcript items", m2.nativeScrollbackPrinted, len(m2.transcript))
	}
}

func TestClearScreenInvalidatesRenderedChatCache(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "high", "on")
	m.width = 80
	m.height = 24
	m.appendTranscript("assistant", tuirender.KindText, "old cached content")
	if view := m.View(); !strings.Contains(view, "old cached content") {
		t.Fatalf("expected old content before clear:\n%s", view)
	}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventClearScreen}))
	m = next.(model)
	view := m.View()
	if strings.Contains(view, "old cached content") {
		t.Fatalf("expected first clear to remove cached content:\n%s", view)
	}
	if strings.Contains(view, "WHALE") || strings.Contains(view, "██╗") {
		t.Fatalf("expected startup header to land in scrollback after clear, not the live viewport:\n%s", view)
	}
	if !m.startupHeaderPrinted {
		t.Fatal("expected first clear to schedule startup header print")
	}
}

func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
