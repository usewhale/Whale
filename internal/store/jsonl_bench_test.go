package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// seedSession creates a session with `nPrior` prior messages, each carrying
// `payloadBytes` of text, to simulate a long-context transcript on disk.
func seedSession(b *testing.B, st *JSONLStore, sessionID string, nPrior, payloadBytes int) {
	b.Helper()
	ctx := context.Background()
	payload := strings.Repeat("x", payloadBytes)
	for i := 0; i < nPrior; i++ {
		role := core.RoleAssistant
		if i%2 == 0 {
			role = core.RoleUser
		}
		if _, err := st.Create(ctx, core.Message{
			SessionID: sessionID,
			Role:      role,
			Text:      payload,
		}); err != nil {
			b.Fatalf("seed create: %v", err)
		}
	}
}

// benchUpdateStreaming simulates one streaming response: create an assistant
// message, then call Update() `deltas` times appending a token's worth of
// text each iteration. Each outer iteration is one operation. The session
// file is reset to the seeded state at the start of every operation so the
// timed work is independent of prior iterations.
func benchUpdateStreaming(b *testing.B, nPrior, priorPayloadBytes, deltas, tokenBytes int) {
	dir := b.TempDir()
	st, err := NewJSONLStore(dir)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	sessionID := "s"
	seedSession(b, st, sessionID, nPrior, priorPayloadBytes)
	ctx := context.Background()
	seeded, _, err := st.readSessionLocked(sessionID)
	if err != nil {
		b.Fatalf("read seeded: %v", err)
	}
	token := strings.Repeat("y", tokenBytes)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Reset back to the seeded transcript so every iteration measures the
		// same streaming response cost rather than an ever-growing file.
		if err := st.RewriteSession(ctx, sessionID, append([]core.Message(nil), seeded...)); err != nil {
			b.Fatalf("reset session: %v", err)
		}
		asst, err := st.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleAssistant})
		if err != nil {
			b.Fatalf("create asst: %v", err)
		}
		b.StartTimer()

		for d := 0; d < deltas; d++ {
			asst.Text += token
			if err := st.Update(ctx, asst); err != nil {
				b.Fatalf("update: %v", err)
			}
		}
	}
	b.ReportMetric(float64(deltas), "deltas/op")
}

// Short history, short response — should be fast.
func BenchmarkUpdate_FreshSession_50deltas(b *testing.B) {
	benchUpdateStreaming(b, 0, 0, 50, 4)
}

// Realistic mid-session: 20 prior turns, ~2KB each (~40KB file), 200 deltas.
func BenchmarkUpdate_MidSession_200deltas(b *testing.B) {
	benchUpdateStreaming(b, 20, 2*1024, 200, 4)
}

// Long-context like the issue describes: 100 prior turns, ~20KB each (~2MB file),
// 500 deltas in the streaming response.
func BenchmarkUpdate_LongSession_500deltas(b *testing.B) {
	benchUpdateStreaming(b, 100, 20*1024, 500, 4)
}

// Pathological case from the issue: 200 prior turns × 30KB (~6MB file), 1000 deltas.
func BenchmarkUpdate_HugeSession_1000deltas(b *testing.B) {
	benchUpdateStreaming(b, 200, 30*1024, 1000, 4)
}

// benchPostFixStreaming models the post-fix path in internal/agent/stream.go:
// one Create at the start of the assistant turn, the in-memory assistant
// accumulates `deltas` tokens with no disk I/O, and a single Update at
// EventComplete. This is the cost the issue #22 fix targets.
func benchPostFixStreaming(b *testing.B, nPrior, priorPayloadBytes, deltas, tokenBytes int) {
	dir := b.TempDir()
	st, err := NewJSONLStore(dir)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	sessionID := "s"
	seedSession(b, st, sessionID, nPrior, priorPayloadBytes)
	ctx := context.Background()
	seeded, _, err := st.readSessionLocked(sessionID)
	if err != nil {
		b.Fatalf("read seeded: %v", err)
	}
	token := strings.Repeat("y", tokenBytes)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := st.RewriteSession(ctx, sessionID, append([]core.Message(nil), seeded...)); err != nil {
			b.Fatalf("reset session: %v", err)
		}
		b.StartTimer()
		asst, err := st.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleAssistant})
		if err != nil {
			b.Fatalf("create: %v", err)
		}
		for d := 0; d < deltas; d++ {
			asst.Text += token // in-memory only, no disk
		}
		if err := st.Update(ctx, asst); err != nil {
			b.Fatalf("update: %v", err)
		}
	}
	b.ReportMetric(float64(deltas), "deltas/op")
}

// Mirrors BenchmarkUpdate_LongSession_500deltas (2MB file, 500 deltas) under
// the post-fix call pattern. Expect ≥100× speedup vs. the pre-fix bench.
func BenchmarkPostFix_LongSession_500deltas(b *testing.B) {
	benchPostFixStreaming(b, 100, 20*1024, 500, 4)
}

// Mirrors BenchmarkUpdate_HugeSession_1000deltas (6MB file, 1000 deltas).
func BenchmarkPostFix_HugeSession_1000deltas(b *testing.B) {
	benchPostFixStreaming(b, 200, 30*1024, 1000, 4)
}

// Single-Update microbenchmark across session sizes, to expose the O(file size)
// cost of each Update call independent of delta count.
func BenchmarkUpdate_Single(b *testing.B) {
	cases := []struct {
		name              string
		nPrior            int
		priorPayloadBytes int
	}{
		{"fresh", 0, 0},
		{"40KB_file", 20, 2 * 1024},
		{"2MB_file", 100, 20 * 1024},
		{"6MB_file", 200, 30 * 1024},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			dir := b.TempDir()
			st, err := NewJSONLStore(dir)
			if err != nil {
				b.Fatalf("new store: %v", err)
			}
			sessionID := "s"
			seedSession(b, st, sessionID, c.nPrior, c.priorPayloadBytes)
			ctx := context.Background()
			asst, err := st.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleAssistant})
			if err != nil {
				b.Fatalf("create: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				asst.Text = fmt.Sprintf("token-%d", i)
				if err := st.Update(ctx, asst); err != nil {
					b.Fatalf("update: %v", err)
				}
			}
		})
	}
}
