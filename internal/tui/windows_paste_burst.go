package tui

import (
	"time"
	"unicode"
)

const (
	windowsPasteBurstLongChunkRunes = 16
	// Two consecutive key chunks arriving within this window are treated
	// as paste-cadence rather than typing-cadence. Human typing is rarely
	// faster than ~80ms between keystrokes; terminals stream pasted bytes
	// at sub-millisecond cadence. 60ms matches the Windows-conhost
	// coalescing window used by Codex / DeepSeek-TUI.
	windowsPasteBurstFastChunkInterval = 60 * time.Millisecond
	// Number of consecutive paste-cadence chunks before we escalate to
	// burst mode. Tolerates incidental double-taps and key autorepeat
	// without misclassifying a typing session.
	windowsPasteBurstFastRunThreshold = 3
)

type windowsPasteChunkDecision int

const (
	windowsPasteChunkTyped windowsPasteChunkDecision = iota
	windowsPasteChunkBurst
)

type windowsPasteBurstClassifier struct {
	lastChunkAt   time.Time
	lastChunkText string
	fastRunLen    int
}

// classify decides whether a chunk should start (or extend) a paste burst.
// It escalates a content-typed chunk to a burst once we have seen a run of
// windowsPasteBurstFastRunThreshold chunks arriving within
// windowsPasteBurstFastChunkInterval of each other — the cadence signature
// of a terminal-streamed paste rather than a user keystroke.
//
// OS key autorepeat (Windows default ~32ms) also fires within this window,
// but it produces the *same* single rune over and over. We detect that
// pattern and refuse to advance the fast-run counter so a held-down key
// stays in the typed path instead of getting buffered.
func (c *windowsPasteBurstClassifier) classify(now time.Time, text string) windowsPasteChunkDecision {
	decision := c.classifyChunk(text)
	fastCadence := !c.lastChunkAt.IsZero() &&
		now.Sub(c.lastChunkAt) <= windowsPasteBurstFastChunkInterval
	autorepeat := fastCadence && isSingleRune(text) && text == c.lastChunkText
	switch {
	case autorepeat:
		// OS autorepeat is paste-cadence but isn't a paste. Reset the run
		// so a held-down key can never escalate into the burst buffer, and
		// so any stale count from earlier real typing is cleared too.
		c.fastRunLen = 1
	case fastCadence:
		c.fastRunLen++
	default:
		c.fastRunLen = 1
	}
	c.lastChunkAt = now
	c.lastChunkText = text
	if decision == windowsPasteChunkTyped && c.fastRunLen >= windowsPasteBurstFastRunThreshold {
		decision = windowsPasteChunkBurst
	}
	return decision
}

// reset clears the cadence state so the next rune chunk starts a fresh
// run. Callers invoke this on non-rune key events (Enter, Tab, arrows,
// backspace, etc.) so editing actions segment cadence detection — a
// keystroke arriving 30ms after Enter is not "the third paste chunk".
func (c *windowsPasteBurstClassifier) reset() {
	c.lastChunkAt = time.Time{}
	c.lastChunkText = ""
	c.fastRunLen = 0
}

func isSingleRune(text string) bool {
	count := 0
	for range text {
		count++
		if count > 1 {
			return false
		}
	}
	return count == 1
}

func (windowsPasteBurstClassifier) classifyChunk(text string) windowsPasteChunkDecision {
	if text == "" {
		return windowsPasteChunkTyped
	}
	runes := []rune(text)
	if len(runes) <= 1 {
		return windowsPasteChunkTyped
	}
	if containsLineBreak(runes) {
		return windowsPasteChunkBurst
	}
	if containsWhitespace(runes) || len(runes) >= windowsPasteBurstLongChunkRunes {
		return windowsPasteChunkBurst
	}
	return windowsPasteChunkTyped
}

func containsLineBreak(runes []rune) bool {
	for _, r := range runes {
		if r == '\n' || r == '\r' {
			return true
		}
	}
	return false
}

func containsWhitespace(runes []rune) bool {
	for _, r := range runes {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func isASCIIMultiRune(text string) bool {
	runes := []rune(text)
	if len(runes) <= 1 {
		return false
	}
	for _, r := range runes {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
