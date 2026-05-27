package composer

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Issue #5 (bug list): visualLineCount scans every line + every rune of the
// composer buffer via uniseg.StringWidth. It is called from reflow() on
// nearly every Update(), so steady-state per-keystroke cost scales with the
// already-in-buffer size. These benchmarks measure that steady state — what
// the user actually feels after pasting N chars and then continuing to type.

var reflowSizes = []int{1000, 5000, 20000, 100000}

// fillRealBuffer loads the composer with ~n chars of real text. HandlePaste
// replaces any single paste >1000 runes with a placeholder, so we feed
// sub-threshold chunks to keep actual content in rawValue().
func fillRealBuffer(c *Composer, n int) {
	chunk := makePasteText(largePasteCharThreshold - 1)
	for filled := 0; filled < n; {
		c.HandlePaste(chunk)
		filled += len(chunk)
	}
}

// BenchmarkVisualLineCountSteadyState is the most direct micro-bench of the
// suspected hot function. Pre-fills the composer with N chars of real text
// at width 120, then times visualLineCount in isolation.
func BenchmarkVisualLineCountSteadyState(b *testing.B) {
	for _, n := range reflowSizes {
		b.Run(sizeLabel(n), func(b *testing.B) {
			c := New()
			c.SetWidth(120)
			fillRealBuffer(&c, n)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.visualLineCount()
			}
		})
	}
}

// BenchmarkUpdateSingleKeystrokeAfterFill is the end-user-felt cost: the
// composer is already loaded with N chars of real text, and one more rune
// arrives via Update. This is what governs typing latency after a fill.
func BenchmarkUpdateSingleKeystrokeAfterFill(b *testing.B) {
	for _, n := range reflowSizes {
		b.Run(sizeLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				c := New()
				c.SetWidth(120)
				fillRealBuffer(&c, n)
				b.StartTimer()
				c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
			}
		})
	}
}

// BenchmarkVisualLineCountNarrowTerminal measures the same micro-bench at a
// 40-col width, where each logical line wraps several times so the rune
// width-walking loop in wrappedLineCount dominates more strongly.
func BenchmarkVisualLineCountNarrowTerminal(b *testing.B) {
	for _, n := range reflowSizes {
		b.Run(sizeLabel(n), func(b *testing.B) {
			c := New()
			c.SetWidth(40)
			// Use long-line content so each logical line wraps several times.
			longLineChunk := strings.Repeat("the quick brown fox jumps over the lazy dog ", (largePasteCharThreshold-1)/44)
			for filled := 0; filled < n; {
				c.HandlePaste(longLineChunk)
				filled += len(longLineChunk)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.visualLineCount()
			}
		})
	}
}
