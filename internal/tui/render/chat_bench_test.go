package render

import (
	"fmt"
	"strings"
	"testing"
)

// syntheticTranscript builds a realistic-looking transcript of `nTurns` turns
// (user + assistant per turn), where each assistant message carries a mix of
// markdown features that exercise the goldmark/glamour render path: headings,
// fenced code blocks, lists, inline emphasis, and paragraphs.
func syntheticTranscript(nTurns int, assistantBytesApprox int) []UIMessage {
	codeBlock := "```go\nfunc Example(x int) int {\n\t// inline comment\n\treturn x * 2\n}\n```\n"
	paraTemplate := "This is paragraph %d explaining the design with **bold** and *italic* and `code` spans. "
	listItem := "- bullet point %d with some descriptive trailing text\n"

	out := make([]UIMessage, 0, nTurns*2)
	for i := 0; i < nTurns; i++ {
		out = append(out, UIMessage{
			ID:   fmt.Sprintf("u-%d", i),
			Role: "you",
			Kind: KindText,
			Text: fmt.Sprintf("user question %d: please explain step %d in detail", i, i),
		})

		var b strings.Builder
		b.WriteString(fmt.Sprintf("## Step %d\n\n", i))
		for j := 0; b.Len() < assistantBytesApprox; j++ {
			b.WriteString(fmt.Sprintf(paraTemplate, j))
			if j%3 == 0 {
				b.WriteString("\n\n")
				b.WriteString(codeBlock)
			}
			if j%5 == 0 {
				b.WriteString("\n")
				for k := 0; k < 3; k++ {
					b.WriteString(fmt.Sprintf(listItem, k))
				}
			}
			b.WriteString("\n")
		}
		out = append(out, UIMessage{
			ID:   fmt.Sprintf("a-%d", i),
			Role: "assistant",
			Kind: KindText,
			Text: b.String(),
		})
	}
	return out
}

// BenchmarkChatLines_Single measures cost of one full ChatLines() call across
// transcript sizes — the cost paid on every TUI re-render today.
func BenchmarkChatLines_Single(b *testing.B) {
	cases := []struct {
		name      string
		nTurns    int
		bytesEach int
	}{
		{"fresh_5turns_1KB", 5, 1024},
		{"mid_20turns_2KB", 20, 2 * 1024},
		{"long_50turns_4KB", 50, 4 * 1024},
		{"huge_100turns_8KB", 100, 8 * 1024},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			msgs := syntheticTranscript(c.nTurns, c.bytesEach)
			width := 100
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out := ChatLines(msgs, width)
				if len(out) == 0 {
					b.Fatal("empty render")
				}
			}
		})
	}
}

// benchStreamingRerender simulates `deltas` consecutive re-renders triggered
// by streaming token arrivals: only the trailing assistant message text grows,
// but ChatLines() re-renders the *entire* transcript every call. One outer
// iteration = one streaming response.
func benchStreamingRerender(b *testing.B, nTurns, bytesEach, deltas int) {
	tokenChunk := strings.Repeat("token ", 4) // ~24 bytes per delta
	seed := syntheticTranscript(nTurns, bytesEach)
	width := 100

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		msgs := append([]UIMessage(nil), seed...)
		msgs = append(msgs, UIMessage{
			ID:   "live",
			Role: "assistant",
			Kind: KindText,
			Text: "",
		})
		liveIdx := len(msgs) - 1
		var liveText strings.Builder
		b.StartTimer()

		for d := 0; d < deltas; d++ {
			liveText.WriteString(tokenChunk)
			msgs[liveIdx].Text = liveText.String()
			out := ChatLines(msgs, width)
			if len(out) == 0 {
				b.Fatal("empty render")
			}
		}
	}
	b.ReportMetric(float64(deltas), "deltas/op")
}

// 20 turns × 2KB ≈ 40KB transcript, 200 deltas. Mid-session.
func BenchmarkChatLines_StreamingMid_200deltas(b *testing.B) {
	benchStreamingRerender(b, 20, 2*1024, 200)
}

// 50 turns × 4KB ≈ 200KB transcript, 500 deltas. Long session like issue #22.
func BenchmarkChatLines_StreamingLong_500deltas(b *testing.B) {
	benchStreamingRerender(b, 50, 4*1024, 500)
}

// 100 turns × 8KB ≈ 800KB transcript, 1000 deltas. Pathological.
func BenchmarkChatLines_StreamingHuge_1000deltas(b *testing.B) {
	benchStreamingRerender(b, 100, 8*1024, 1000)
}
