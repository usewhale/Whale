package tui

import (
	"fmt"
	"strings"
	"testing"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func benchmarkChatListMessages(turns, assistantBytes int) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, turns*2)
	for i := 0; i < turns; i++ {
		out = append(out, tuirender.UIMessage{
			ID:   fmt.Sprintf("u-%d", i),
			Role: "you",
			Kind: tuirender.KindText,
			Text: fmt.Sprintf("question %d", i),
		})

		var body strings.Builder
		body.WriteString(fmt.Sprintf("## Step %d\n\n", i))
		for j := 0; body.Len() < assistantBytes; j++ {
			body.WriteString(fmt.Sprintf("Paragraph %d with **bold**, `code`, and enough text to wrap in the terminal viewport.\n\n", j))
			if j%4 == 0 {
				body.WriteString("```go\nfmt.Println(\"hello\")\n```\n\n")
			}
		}
		out = append(out, tuirender.UIMessage{
			ID:   fmt.Sprintf("a-%d", i),
			Role: "assistant",
			Kind: tuirender.KindText,
			Text: body.String(),
		})
	}
	return out
}

func BenchmarkChatListSetMessagesRepeated(b *testing.B) {
	messages := benchmarkChatListMessages(20, 2*1024)
	var l chatList
	l.SetSize(100, 30)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.SetMessages(messages, 100)
	}
}

func BenchmarkChatTailMessagesForView(b *testing.B) {
	cases := []struct {
		name   string
		turns  int
		bytes  int
		height int
	}{
		{"small_20turns_2k", 20, 2 * 1024, 30},
		{"medium_60turns_4k", 60, 4 * 1024, 30},
		{"large_200turns_4k", 200, 4 * 1024, 30},
	}
	for _, tc := range cases {
		messages := benchmarkChatListMessages(tc.turns, tc.bytes)
		renderWidth := 100
		b.Run(tc.name, func(b *testing.B) {
			var m model
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = m.chatTailMessagesForView(messages, renderWidth, tc.height)
			}
		})
	}
}

func BenchmarkChatListSetMessagesStreamingTail(b *testing.B) {
	base := benchmarkChatListMessages(20, 2*1024)
	var l chatList
	l.SetSize(100, 30)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		messages := append([]tuirender.UIMessage(nil), base...)
		messages = append(messages, tuirender.UIMessage{
			ID:   "live",
			Role: "assistant",
			Kind: tuirender.KindText,
			Text: strings.Repeat("streaming token ", i%200+1),
		})
		l.SetMessages(messages, 100)
	}
}
