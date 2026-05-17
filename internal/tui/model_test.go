package tui

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1700000000, 0)} }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
		t.Fatalf("buffer after second pasted line = %q", got)
	}

	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after paste detection: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.windowsPaste.buffer; got != "line one\nline two\n" {
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
	if got := m.windowsPaste.buffer; got != firstLine {
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
	m.windowsPaste.buffer = "line one\nline two"
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)
	m.windowsPaste.burstID = 7

	m, _ = updateTestModel(t, m, struct{}{})
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
		t.Fatalf("unhandled message should not clear active paste buffer, got %q", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("unhandled message should not flush paste buffer early, got input %q", got)
	}

	m, _ = updateTestModel(t, m, windowsPasteBurstFlushMsg{id: 7})
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "" {
		t.Fatalf("ordinary typed character should not enter paste buffer, got %q", got)
	}

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected fast enter after typed character to defer submit")
	}
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("fast enter should keep typed prompt intact, got %q", got)
	}
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "hello\nwo" {
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
		t.Fatalf("post-Enter keystroke escalated into burst buffer: %q", m.windowsPaste.buffer)
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
			m.input.Value(), m.windowsPaste.buffer)
	}
	combined := m.input.Value() + m.windowsPaste.buffer
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
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "fix bug" {
		t.Fatalf("expected single-line paste to stay buffered, got %q", got)
	}

	m, cmd = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected enter after single-line paste to defer submit")
	}
	if got := m.input.Value(); got != "fix bug" {
		t.Fatalf("expected single-line paste to flush before deferred submit, got %q", got)
	}
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "" {
		t.Fatalf("expected buffer to flush while enter is deferred, got %q", got)
	}
	if !m.windowsPaste.pendingEnter {
		t.Fatal("expected pasted line enter to arm deferred submit")
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "foo\n" {
		t.Fatalf("buffer after CRLF pasted newline = %q", got)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bar")})
	if got := m.windowsPaste.buffer; got != "foo\nbar" {
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
	if got := m.windowsPaste.buffer; got != "a\n\nb" {
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
	if got := m.windowsPaste.buffer; got != "first\nblock\nsecond" {
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
	if got := m.windowsPaste.buffer; got != "foo\n    " {
		t.Fatalf("buffer after pasted tab indentation = %q", got)
	}
	m, _ = updateTestModel(t, m, windowsDeferredEnterMsg{id: deferredID})
	if len(*intents) != 0 {
		t.Fatalf("stale deferred enter submitted after pasted tab: %+v", *intents)
	}

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bar")})
	if got := m.windowsPaste.buffer; got != "foo\n    bar" {
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
	if got := m.windowsPaste.buffer; got != "" {
		t.Fatalf("buffer after tab-indented first pasted line = %q", got)
	}
	deferredID := m.windowsPaste.pendingEnterID

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bar")})
	if got := m.windowsPaste.buffer; got != "    foo\nbar" {
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
	if got := m.windowsPaste.buffer; got != "a\n\n" {
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
	if got := m.windowsPaste.buffer; got != "a\n\nb" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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

func TestWindowsPasteFallbackCtrlUResetsQuietWindow(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.windowsPaste.enabled = true
	m.input.SetValue("old")
	m.windowsPaste.buffer = "pending paste"
	m.windowsPaste.activeUntil = time.Now().Add(windowsPasteQuietDelay)

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer, got %q", got)
	}
	if m.windowsPaste.pendingEnter || m.windowsPaste.buffer != "" || !m.windowsPaste.activeUntil.IsZero() {
		t.Fatalf("expected Ctrl+U to reset paste fallback state, got %+v", m.windowsPaste)
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
		if m.status != "command disabled while working" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
		t.Fatalf("buffer after pasted continuation = %q", got)
	}

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "older queued" {
		t.Fatalf("expected older queued prompt to start, got %+v", *intents)
	}
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
		t.Fatalf("buffer after pasted continuation = %q", got)
	}

	m.localSubmitPending = 1
	m.queuedPrompts = []queuedPrompt{{Text: "older queued"}}
	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventTurnDone}))
	if len(*intents) != 0 {
		t.Fatalf("queued prompt should wait for local submit done, got %+v", *intents)
	}
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
		t.Fatalf("expected active Windows paste to remain while local submit is pending, got %q", got)
	}

	m, _ = updateTestModel(t, m, svcMsg(service.Event{Kind: service.EventLocalSubmitDone}))
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentSubmit || (*intents)[0].Input != "older queued" {
		t.Fatalf("expected older queued prompt to start after local submit done, got %+v", *intents)
	}
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "line one\nline two" {
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
	if got := m.windowsPaste.buffer; got != "" {
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
	if got := m.windowsPaste.buffer; got != "" {
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

func TestClearScreenCmdUsesBubbleTeaOnWindows(t *testing.T) {
	var out bytes.Buffer
	cmd := clearScreenCmdForOS("windows", &out)
	msg := cmd()
	if out.Len() != 0 {
		t.Fatalf("windows clear should not write raw ANSI directly, got %q", out.String())
	}
	if msg == nil {
		t.Fatal("windows clear should return a Bubble Tea clear-screen message")
	}
}

func TestClearScreenCmdPreservesUnixScrollbackClear(t *testing.T) {
	var out bytes.Buffer
	cmd := clearScreenCmdForOS("linux", &out)
	msg := cmd()
	if msg != nil {
		t.Fatalf("unix clear should write directly and return nil msg, got %T", msg)
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
		if cmd == want {
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
			name: "permissions picker",
			ev: service.Event{
				Kind:            service.EventPermissionsPicker,
				ApprovalChoices: []string{service.ApprovalChoiceAskFirst, service.ApprovalChoiceAutoApproveSession},
				CurrentApproval: service.ApprovalChoiceAskFirst,
			},
			mode: modePermissionsPicker,
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

func TestPermissionsPickerCopyClarifiesAutoApproveScope(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.permissionsPicker.choices = []string{service.ApprovalChoiceAskFirst, service.ApprovalChoiceAutoApproveSession}

	view := m.renderPermissionsPicker()
	for _, want := range []string{
		"Ask before tools run",
		"Prompt before write, patch, shell, or MCP tools run.",
		"Auto approve all tools for this session",
		"No approval prompts until Whale exits.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected permissions picker to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Never ask; auto-approve tool calls.") {
		t.Fatalf("permissions picker should not use ambiguous auto-approve copy:\n%s", view)
	}
}

func TestPermissionsPickerAutoApproveDispatchesNeverAsk(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePermissionsPicker
	m.permissionsPicker.choices = []string{service.ApprovalChoiceAskFirst, service.ApprovalChoiceAutoApproveSession}
	m.permissionsPicker.index = 1

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 {
		t.Fatalf("expected one intent, got %+v", *intents)
	}
	if got := (*intents)[0]; got.Kind != service.IntentSetApprovalMode || got.ApprovalMode != "never-ask" {
		t.Fatalf("unexpected approval intent: %+v", got)
	}
	if m.mode != modeChat {
		t.Fatalf("expected permissions picker to close, got mode %v", m.mode)
	}
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
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "No final answer was produced") {
		t.Fatalf("expected fallback notice in transcript:\n%s", got)
	}
	if m.sawReasoningThisTurn || m.sawAssistantThisTurn {
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
	if strings.Contains(got, "No final answer was produced") {
		t.Fatalf("did not expect no-final-answer fallback after LastResponse recovery:\n%s", got)
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
	if snap[0].Kind != tuirender.KindNotice || snap[0].Role != "notice" {
		t.Fatalf("expected notice entry, got %+v", snap[0])
	}
	if !strings.Contains(snap[0].Text, "No final answer was produced") {
		t.Fatalf("expected generic missing-answer notice, got %q", snap[0].Text)
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
	if snap[0].Kind != tuirender.KindNotice || snap[0].Role != "notice" {
		t.Fatalf("expected notice entry, got %+v", snap[0])
	}
	if !strings.Contains(snap[0].Text, "No plan was produced") {
		t.Fatalf("expected missing-plan notice, got %q", snap[0].Text)
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

func TestSlashSuggestionsHiddenForAbsolutePathInput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("/Users/goranka/Engineer/ai/dsk 里有好几个go项目的，你看看它们怎么做的")
	m.updateSlashMatches()
	if len(m.slash.matches) != 0 {
		t.Fatalf("expected slash suggestions hidden for absolute path prompt, got %+v", m.slash.matches)
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
		"/stats all",
		"/mcp",
		"/resume",
		"/clear",
		"/new",
		"/new scratch",
		"/model xxx",
		"/skills xxx",
		"/resume xxx",
		"/new a b",
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
	for _, cmd := range []string{"/status", "/stats usage", "/stats repair", "/mcp", "/exit"} {
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
			if !strings.Contains(m.status, "disabled") {
				t.Fatalf("expected disabled guidance, got %q", m.status)
			}
			gotTranscript := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
			if !strings.Contains(gotTranscript, "disabled while a turn is in progress") {
				t.Fatalf("expected visible blocked-command message, got:\n%s", gotTranscript)
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
	m.windowsPaste.buffer = "pasted follow up"
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
	if m.windowsPaste.buffer != "" || !m.windowsPaste.activeUntil.IsZero() || m.windowsPaste.busyInput || m.windowsPaste.busyInputStop {
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

func TestCtrlCWhileBusyCancelsPendingWindowsEnter(t *testing.T) {
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
		t.Fatal("expected ctrl+c interrupt to clear pending windows enter")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentShutdown {
		t.Fatalf("expected only the shutdown intent from interrupt, got %+v", *intents)
	}

	next, _ = m.Update(windowsDeferredEnterMsg{id: deferredID})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("stale deferred enter should not submit after interrupt, got %+v", *intents)
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
	if len(*intents) != 2 ||
		(*intents)[0].Kind != service.IntentCancelUserInput ||
		(*intents)[0].ToolCallID != "tool-1" ||
		(*intents)[1].Kind != service.IntentShutdown {
		t.Fatalf("expected cancel input then shutdown intents, got %+v", *intents)
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
	if strings.Contains(view, "┌") || strings.Contains(view, "│\n│") {
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

func TestChatFooterStaysPinnedAfterSlashSuggestionsClose(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "normal", "on")
	m.width = 80
	m.height = 24
	m.cwd = "~/Engineer/ai/dsk/whale"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(model)
	withSlash := m.View()
	assertFooterLastLine(t, withSlash, "model: deepseek-v4-pro")
	assertFooterLastLine(t, withSlash, "whale")
	assertFooterLastLineNotContains(t, withSlash, "dir:")
	if !strings.Contains(withSlash, "Tab/Enter pick") {
		t.Fatalf("expected slash suggestions while / is present:\n%s", withSlash)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	afterDelete := m.View()
	assertFooterLastLine(t, afterDelete, "model: deepseek-v4-pro")
	assertFooterLastLine(t, afterDelete, "whale")
	assertFooterLastLineNotContains(t, afterDelete, "dir:")
	if strings.Contains(afterDelete, "Tab/Enter pick") {
		t.Fatalf("expected slash suggestions to disappear after deleting /:\n%s", afterDelete)
	}
	if got := strings.Count(afterDelete, "\n") + 1; got != m.height {
		t.Fatalf("expected view to keep terminal height %d after slash closes, got %d:\n%s", m.height, got, afterDelete)
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
	for _, want := range []string{"config: /tmp/mcp.json", "session: test-session"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected transcript to retain %q:\n%s", want, got)
		}
	}
}

func TestLocalCommandResultCommitsIdleAssemblerBeforeNextPrompt(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 80
	m.height = 14

	next, _ := m.Update(svcMsg(service.Event{
		Kind:   service.EventMCPStatus,
		Status: "failed",
		Text:   "MCP startup failed: fs. Run /mcp for details.",
	}))
	m = next.(model)
	if got := len(m.assembler.Snapshot()); got == 0 {
		t.Fatal("expected idle MCP status to leave live assembler content")
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
	mcpIx := strings.Index(rendered, "MCP startup failed")
	statusIx := strings.Index(rendered, "session: test-session")
	promptIx := strings.Index(rendered, "next prompt")
	if mcpIx < 0 || statusIx < 0 || promptIx < 0 || !(mcpIx < statusIx && statusIx < promptIx) {
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

func TestBusySlashWarningPreservesLiveTurnOrder(t *testing.T) {
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
	assistantIx := strings.Index(rendered, "already visible")
	warningIx := strings.Index(rendered, "disabled while a turn is in progress")
	if assistantIx < 0 || warningIx < 0 || assistantIx > warningIx {
		t.Fatalf("expected busy slash warning after existing live assistant output:\n%s", rendered)
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "already visible recovered tail",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)
	rendered = strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	assistantIx = strings.Index(rendered, "already visible")
	warningIx = strings.Index(rendered, "disabled while a turn is in progress")
	tailIx := strings.Index(rendered, "recovered tail")
	if assistantIx < 0 || warningIx < 0 || tailIx < 0 || !(assistantIx < warningIx && warningIx < tailIx) {
		t.Fatalf("expected committed order assistant, warning, recovered tail:\n%s", rendered)
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

func TestChatStartupHeaderRendersInsideViewportHeight(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 10
	view := m.View()
	if !strings.Contains(view, "▸ Whale") {
		t.Fatalf("expected startup header in chat view:\n%s", view)
	}
	for _, want := range []string{"effort: max", "thinking: off"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected startup header to contain %q:\n%s", want, view)
		}
	}
	if got := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; got != m.height {
		t.Fatalf("expected view to keep terminal height %d, got %d:\n%s", m.height, got, view)
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
	if !strings.Contains(atTop, "entry-00") {
		t.Fatalf("expected home to scroll chat transcript to top:\n%s", atTop)
	}
}

func TestChatViewportScrollKeysUseTranscriptBeforeComposer(t *testing.T) {
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

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewport.YOffset != bottomOffset {
		t.Fatalf("expected PageDown to return to bottom, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected Home to jump to top, offset=%d", m.viewport.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if m.viewport.YOffset != bottomOffset {
		t.Fatalf("expected End to jump to bottom, offset=%d bottom=%d", m.viewport.YOffset, bottomOffset)
	}
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
	if got := m.windowsPaste.buffer; got != "" {
		t.Fatalf("expected mouse CSI fragment not to enter Windows paste buffer, got %q", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("["), Alt: true})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<65;69;14M")})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected split mouse CSI fragment not to enter composer, got %q", got)
	}
	if got := m.windowsPaste.buffer; got != "" {
		t.Fatalf("expected split mouse CSI fragment not to enter Windows paste buffer, got %q", got)
	}
}

func TestBusyMouseWheelFreezesLiveOutputAndScrollsChat(t *testing.T) {
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

	next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = next.(model)
	if !m.viewportFrozen {
		t.Fatal("expected wheel up during busy output to freeze chat viewport")
	}
	if m.followTail {
		t.Fatal("expected wheel up to disable tail following")
	}
	if !m.mouseCapture {
		t.Fatal("expected busy chat mode to enable mouse capture for wheel scrolling")
	}
	view := m.View()
	if !strings.Contains(view, "live-11") {
		t.Fatalf("expected small wheel scroll to keep current live output nearby:\n%s", view)
	}

	frozenView := m.View()
	events := make([]service.Event, 0, 10)
	for i := 0; i < 10; i++ {
		events = append(events, service.Event{Kind: service.EventAssistantDelta, Text: fmt.Sprintf("hidden-live-%02d\n", i)})
	}
	next, _ = m.Update(svcBatchMsg(events))
	m = next.(model)
	if got := m.View(); got != frozenView {
		t.Fatalf("expected wheel-scrolled live viewport to stay frozen\nbefore:\n%s\n\nafter:\n%s", frozenView, got)
	}
	if strings.Contains(m.View(), "hidden-live-09") {
		t.Fatalf("expected hidden live tail not to redraw frozen viewport:\n%s", m.View())
	}
}

func TestBusyMouseWheelDownResumesTail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 20; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	for i := 0; i < 12; i++ {
		m.append("assistant", fmt.Sprintf("live-%02d\n", i))
	}
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = next.(model)
	if !m.viewportFrozen {
		t.Fatal("expected wheel up to freeze chat viewport")
	}

	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(model)
	if m.viewportFrozen {
		t.Fatal("expected wheel down at tail to unfreeze chat viewport")
	}
	if !m.followTail {
		t.Fatal("expected wheel down at tail to resume following")
	}

	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "latest-after-tail\n"}))
	m = next.(model)
	if view := m.View(); !strings.Contains(view, "latest-after-tail") {
		t.Fatalf("expected resumed tail to render new live output:\n%s", view)
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

func TestChatViewportBusyFollowTailCropsSingleLargeLiveMessage(t *testing.T) {
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

			lineLimit := max(chatTailRenderLineFloor, m.viewportBodyHeight(m.width)*4)
			if lines := m.viewport.TotalLineCount(); lines > lineLimit {
				t.Fatalf("expected single coalesced live message to be cropped to %d lines, got %d", lineLimit, lines)
			}
			view := m.View()
			if strings.Contains(view, "single-live-000") {
				t.Fatalf("expected old single-message live lines to be cropped out:\n%s", view)
			}
			if !strings.Contains(view, "single-live-399") {
				t.Fatalf("expected cropped single-message live tail to remain visible:\n%s", view)
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

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected Home from idle tail render to disable tail following")
	}
	if fullLines := m.viewport.TotalLineCount(); fullLines <= tailLines {
		t.Fatalf("expected Home from idle tail render to restore full scrollable content, tail=%d full=%d", tailLines, fullLines)
	}
	view := m.View()
	if !strings.Contains(view, "entry-000") {
		t.Fatalf("expected Home from idle tail render to restore early history:\n%s", view)
	}
	if strings.Contains(view, "entry-199") {
		t.Fatalf("expected Home from idle tail render to move away from the latest tail:\n%s", view)
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

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if m.viewportFrozen {
		t.Fatal("expected End to unfreeze chat viewport")
	}
	if !m.followTail {
		t.Fatal("expected End to re-enable tail following")
	}
	view := m.View()
	if !strings.Contains(view, "live-tail-29") {
		t.Fatalf("expected End to reveal latest live tail:\n%s", view)
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
	m.transcript = nil
	for i := 0; i < 50; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	if !m.followTail || !m.viewport.AtBottom() {
		t.Fatalf("expected chat to start following tail, follow=%v bottom=%v", m.followTail, m.viewport.AtBottom())
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	view := m.View()
	if !strings.Contains(view, "entry-49") {
		t.Fatalf("expected resized following view to show tail:\n%s", view)
	}
	if strings.Contains(view, "entry-00") {
		t.Fatalf("expected resized following view to stay at tail, got top entry:\n%s", view)
	}
}

func TestChatViewportResizePreservesUserScrollPosition(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 18
	m.transcript = nil
	for i := 0; i < 50; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)

	m.handleViewportScrollKey("home")
	if m.followTail {
		t.Fatal("expected Home to disable tail following")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	view := m.View()
	if !strings.Contains(view, "entry-00") {
		t.Fatalf("expected resized scrolled-up view to preserve top position:\n%s", view)
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
	if !strings.Contains(view, "entry-49") {
		t.Fatalf("expected resized view to follow tail after End:\n%s", view)
	}
}

func TestNativeScrollbackSkipsHeaderAndPrintsNewTranscriptOnce(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10

	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected startup header to be marked as already printed")
	}

	m.appendTranscript("you", tuirender.KindText, "hello native scrollback")
	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected new transcript entry to produce a native scrollback print command")
	}
	msg := cmd()
	if got := reflect.TypeOf(msg).String(); got != "tea.printLineMessage" {
		t.Fatalf("expected Bubble Tea print message, got %s", got)
	}
	if got := fmt.Sprintf("%#v", msg); !strings.Contains(got, "hello native scrollback") {
		t.Fatalf("expected printed message to include transcript text, got %s", got)
	}
	if cmd := m.flushNativeScrollbackCmd(); cmd != nil {
		t.Fatal("expected transcript entry not to be printed twice")
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

func TestCtrlUStillClearsComposerInsteadOfScrollingTranscript(t *testing.T) {
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

func TestCtrlUClearsMultilineComposerWithLayoutSyncOnly(t *testing.T) {
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

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(model)
	mainWidth, _ = m.layoutDims()
	if got := m.viewportBodyHeight(mainWidth); got <= initialBodyHeight {
		t.Fatalf("expected Ctrl+U clear to free composer height, got %d want > %d", got, initialBodyHeight)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear multiline composer, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected Ctrl+U clear not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if !m.followTail || !m.chat.AtBottom() {
		t.Fatalf("expected Ctrl+U clear at tail to keep latest content visible, follow=%v chatBottom=%v", m.followTail, m.chat.AtBottom())
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
	if !strings.Contains(view, "Working (12s) · Esc/Ctrl+C to interrupt") {
		t.Fatalf("expected working status line with elapsed time:\n%s", view)
	}
	if strings.Contains(view, "status: working") {
		t.Fatalf("busy view should not render footer status:\n%s", view)
	}
	if strings.Index(view, "Working (12s)") > strings.Index(view, "Type message or command") {
		t.Fatalf("working status line should appear above composer:\n%s", view)
	}
}

func TestApprovalBusyViewShowsCtrlCOnlyInterruptHint(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: sleep 30"

	view := m.View()
	if !strings.Contains(view, "Working (12s) · Ctrl+C to interrupt") {
		t.Fatalf("expected approval busy status line to advertise ctrl+c interrupt:\n%s", view)
	}
	if strings.Contains(view, "Esc/Ctrl+C to interrupt") {
		t.Fatalf("approval busy status line should not advertise esc as interrupt:\n%s", view)
	}
}

func TestChatFooterShowsEffectiveThinkingAndEffort(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "max", "off")
	m.width = 80
	m.height = 24

	view := m.View()
	assertFooterLastLine(t, view, "model: deepseek-v4-pro")
	assertFooterLastLine(t, view, "effort: max")
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
	if !strings.Contains(view, "Approval required") || !strings.Contains(view, "shell command") || !strings.Contains(view, "  date") {
		t.Fatalf("expected separated approval tool and detail:\n%s", view)
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
		"Allow for session = these files: a.txt, b.txt",
		"a.txt (+1 -1)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
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
	raw := `{"success":false,"code":"ask_mode_blocked","message":"tool unavailable in ask mode","summary":"Current mode: ask. Ask mode only allows read-only tools. To execute or modify files, switch to agent mode. To propose a reviewed approach first, switch to plan mode.","data":{"current_mode":"ask","suggested_modes":["agent","plan"]}}`
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_failed" {
		t.Fatalf("expected result_failed role, got %q", role)
	}
	want := "✗ · Current mode: ask. Ask mode only allows read-only tools. To execute or modify files, switch to agent mode. To propose a reviewed approach first, switch to plan mode."
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
		if strings.Contains(entry.Text, "No final answer was produced") {
			t.Fatalf("unexpected no-final-answer notice after tool denial: %+v", snap)
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

func TestMCPStatusFailureAddsVisibleWarning(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventMCPStatus, Status: "failed", Text: "MCP startup failed: fs. Run /mcp for details."}))
	m = next.(model)
	if m.status != "MCP startup failed: fs. Run /mcp for details." {
		t.Fatalf("unexpected status: %q", m.status)
	}
	snap := m.assembler.Snapshot()
	if len(snap) == 0 || !strings.Contains(snap[0].Text, "MCP startup failed: fs") {
		t.Fatalf("expected visible warning, got: %+v", snap)
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
	}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one tool row, got %+v", snap)
	}
	if snap[0].Role != "result_running" || !strings.Contains(snap[0].Text, "reading internal/tasks/runner.go") {
		t.Fatalf("unexpected progress row: %+v", snap[0])
	}

	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventTaskProgress,
		ToolCallID: "tc-task",
		ToolName:   "spawn_subagent",
		Text:       `spawn_subagent running · review · Searched "TaskProgress" in internal/tui (*.go) · 7 matches in 3 files`,
	}))
	m = next.(model)
	snap = m.assembler.Snapshot()
	if !strings.Contains(snap[0].Text, `Searched "TaskProgress" in internal/tui (*.go) · 7 matches in 3 files`) {
		t.Fatalf("expected progress target and metric to be preserved: %+v", snap[0])
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
	// The transcript should keep only the header banner after clear.
	snap := m2.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected empty live assembler, got %+v", snap)
	}
	if len(m2.transcript) != 1 {
		t.Fatalf("expected 1 transcript header, got %d: %+v", len(m2.transcript), m2.transcript)
	}
	if m2.transcript[0].Role != "info" {
		t.Fatalf("expected info role, got %q", m2.transcript[0].Role)
	}
	if !strings.Contains(m2.transcript[0].Text, "▸ Whale") {
		t.Fatalf("expected header banner, got: %q", m2.transcript[0].Text)
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
