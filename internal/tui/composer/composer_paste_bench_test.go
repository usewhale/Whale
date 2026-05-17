package composer

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Issue #61(1): Windows paste latency. The composer's fast path collapses
// >1000-rune pastes into a placeholder; the slow path happens when input
// arrives as a stream of per-rune KeyMsgs (bracketed paste off, or Windows
// burst classifier misses). These benchmarks measure both, plus the View()
// cost that gates visual paste-ready latency.

var pasteSizes = []int{200, 1000, 5000, 20000}

func makePasteText(n int) string {
	// Mix of ASCII words and newlines, similar to real source-code paste.
	var b strings.Builder
	b.Grow(n + n/20)
	const word = "lorem ipsum dolor sit amet "
	for b.Len() < n {
		b.WriteString(word)
		if b.Len()%80 < len(word) {
			b.WriteByte('\n')
		}
	}
	s := b.String()
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func BenchmarkHandlePasteSingleCall(b *testing.B) {
	for _, n := range pasteSizes {
		text := makePasteText(n)
		b.Run(sizeLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c := New()
				c.SetWidth(120)
				c.HandlePaste(text)
				_ = c.View()
			}
		})
	}
}

// BenchmarkUpdatePerRune simulates the worst case: paste arrives as
// individual KeyMsgs, so every rune triggers textarea.Update + reflow +
// (in real TUI) a full re-render. Cost should scale super-linearly because
// reflow walks the whole buffer on each keystroke.
func BenchmarkUpdatePerRune(b *testing.B) {
	for _, n := range pasteSizes {
		text := makePasteText(n)
		runes := []rune(text)
		b.Run(sizeLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c := New()
				c.SetWidth(120)
				for _, r := range runes {
					c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				}
				_ = c.View()
			}
		})
	}
}

// BenchmarkViewAfterPaste isolates the cost of one View() render after a
// paste of size N has already landed in the composer. This is what gates
// "paste-ready" latency once the paste itself is in the buffer.
func BenchmarkViewAfterPaste(b *testing.B) {
	for _, n := range pasteSizes {
		text := makePasteText(n)
		b.Run(sizeLabel(n), func(b *testing.B) {
			c := New()
			c.SetWidth(120)
			c.HandlePaste(text)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.View()
			}
		})
	}
}

func sizeLabel(n int) string {
	switch {
	case n >= 1000:
		return itoa(n/1000) + "k"
	default:
		return itoa(n)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
