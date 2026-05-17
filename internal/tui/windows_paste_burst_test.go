package tui

import (
	"testing"
	"time"
)

func TestWindowsPasteBurstClassifier(t *testing.T) {
	tests := []struct {
		name string
		text string
		want windowsPasteChunkDecision
	}{
		{name: "single ascii typed char", text: "h", want: windowsPasteChunkTyped},
		{name: "short ascii typed pair", text: "hi", want: windowsPasteChunkTyped},
		{name: "short ascii chunk", text: "hello", want: windowsPasteChunkTyped},
		{name: "ascii chunk with whitespace", text: "hello world", want: windowsPasteChunkBurst},
		{name: "long ascii chunk", text: "abcdefghijklmnop", want: windowsPasteChunkBurst},
		{name: "short ime commit", text: "你好", want: windowsPasteChunkTyped},
		{name: "non ascii with whitespace", text: "你好 世界", want: windowsPasteChunkBurst},
		{name: "long non ascii chunk", text: "你好你好你好你好你好你好你好你好", want: windowsPasteChunkBurst},
		{name: "newline chunk", text: "a\nb", want: windowsPasteChunkBurst},
	}

	var classifier windowsPasteBurstClassifier
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifier.classifyChunk(tt.text); got != tt.want {
				t.Fatalf("classifyChunk(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// Issue #61(1): a paste delivered as a stream of small "typed-looking"
// chunks must be recognized via inter-chunk cadence, not content alone.
func TestWindowsPasteBurstClassifierEscalatesOnFastCadence(t *testing.T) {
	var c windowsPasteBurstClassifier
	t0 := time.Unix(0, 0)
	if got := c.classify(t0, "a"); got != windowsPasteChunkTyped {
		t.Fatalf("first chunk should be typed, got %v", got)
	}
	if got := c.classify(t0.Add(5*time.Millisecond), "b"); got != windowsPasteChunkTyped {
		t.Fatalf("second fast chunk should not escalate yet (below run threshold), got %v", got)
	}
	if got := c.classify(t0.Add(10*time.Millisecond), "c"); got != windowsPasteChunkBurst {
		t.Fatalf("third consecutive fast chunk should escalate to burst, got %v", got)
	}
	// A long pause resets the run — next keystroke is real typing again.
	if got := c.classify(t0.Add(500*time.Millisecond), "d"); got != windowsPasteChunkTyped {
		t.Fatalf("chunk after long gap should reset run, got %v", got)
	}
}

func TestWindowsPasteBurstClassifierToleratesDoubleTap(t *testing.T) {
	var c windowsPasteBurstClassifier
	t0 := time.Unix(0, 0)
	// Two rapid chars then a normal pause — the kind of pattern produced
	// by a fast double-tap or two-finger keystroke. Must NOT escalate.
	c.classify(t0, "a")
	if got := c.classify(t0.Add(5*time.Millisecond), "b"); got != windowsPasteChunkTyped {
		t.Fatalf("two-chunk burst should stay typed, got %v", got)
	}
	if got := c.classify(t0.Add(200*time.Millisecond), "c"); got != windowsPasteChunkTyped {
		t.Fatalf("post-pause chunk should reset to typed, got %v", got)
	}
}

// Issue #61(1) review: OS key autorepeat fires within the paste-cadence
// window but is the same single rune over and over. It must not escalate
// into the burst buffer, otherwise holding a printable key would make
// chars disappear into the paste flush queue.
func TestWindowsPasteBurstClassifierRefusesAutorepeat(t *testing.T) {
	var c windowsPasteBurstClassifier
	now := time.Unix(0, 0)
	for i := 0; i < 20; i++ {
		if got := c.classify(now, "a"); got != windowsPasteChunkTyped {
			t.Fatalf("autorepeat keystroke %d should be typed, got %v", i, got)
		}
		now = now.Add(32 * time.Millisecond) // Windows default repeat rate
	}
}

// Even when prior real-typing has already advanced the fast-run counter,
// switching to autorepeat must roll the counter back so the held key
// stays out of the burst buffer.
func TestWindowsPasteBurstClassifierAutorepeatClearsPriorRun(t *testing.T) {
	var c windowsPasteBurstClassifier
	t0 := time.Unix(0, 0)
	// Two real fast typed chars (counter -> 2).
	c.classify(t0, "x")
	c.classify(t0.Add(5*time.Millisecond), "y")
	// Hold "y" — autorepeat starts. Must NOT escalate even though we were
	// one chunk away from threshold before.
	for i := 0; i < 5; i++ {
		got := c.classify(t0.Add(time.Duration(10+i*32)*time.Millisecond), "y")
		if got != windowsPasteChunkTyped {
			t.Fatalf("autorepeat after prior run escalated at i=%d: %v", i, got)
		}
	}
}

func TestWindowsPasteBurstClassifierPreservesHumanTypingCadence(t *testing.T) {
	var c windowsPasteBurstClassifier
	now := time.Unix(0, 0)
	// Simulate 10 keystrokes at 100ms intervals — fast human typing.
	for i := 0; i < 10; i++ {
		if got := c.classify(now, "x"); got != windowsPasteChunkTyped {
			t.Fatalf("keystroke %d at human cadence should be typed, got %v", i, got)
		}
		now = now.Add(100 * time.Millisecond)
	}
}
