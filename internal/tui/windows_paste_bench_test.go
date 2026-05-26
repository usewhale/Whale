package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func updateBenchModel(b testing.TB, m model, msg tea.Msg) model {
	b.Helper()
	next, _ := m.Update(msg)
	updated, ok := next.(model)
	if !ok {
		b.Fatalf("expected model update, got %T", next)
	}
	return updated
}

func BenchmarkWindowsPasteFallbackFastSingleRuneStream(b *testing.B) {
	for _, size := range []int{200, 1000, 5000, 20000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m, _ := newModelWithDispatchSpy()
				m.windowsPaste.enabled = true
				clock := newFakeClock()
				m.windowsPaste.nowFunc = clock.now
				for n := 0; n < size; n++ {
					clock.advance(2 * time.Millisecond)
					r := rune('a' + n%26)
					m = updateBenchModel(b, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				}
				if !m.hasWindowsPasteBuffer() {
					b.Fatalf("expected paste buffer for %d-rune stream", size)
				}
			}
		})
	}
}

func BenchmarkWindowsPasteFallbackFastSingleRuneStreamWithView(b *testing.B) {
	for _, size := range []int{200, 1000, 5000, 20000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m, _ := newModelWithDispatchSpy()
				m.windowsPaste.enabled = true
				clock := newFakeClock()
				m.windowsPaste.nowFunc = clock.now
				_ = m.View()
				for n := 0; n < size; n++ {
					clock.advance(2 * time.Millisecond)
					r := rune('a' + n%26)
					m = updateBenchModel(b, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					_ = m.View()
				}
				if !m.hasWindowsPasteBuffer() {
					b.Fatalf("expected paste buffer for %d-rune stream", size)
				}
			}
		})
	}
}
