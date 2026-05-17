package deepseek

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// syntheticHistory builds a realistic history of `nTurns` turns. Each turn
// is (user, assistant-with-tool-call, tool-result). Assistant messages carry
// `assistantBytes` of text and `reasoningBytes` of reasoning. Tool results
// carry `toolResultBytes` of content.
func syntheticHistory(nTurns, assistantBytes, reasoningBytes, toolResultBytes int) []core.Message {
	userText := "please continue with the next step in detail"
	asstText := strings.Repeat("x", assistantBytes)
	reasoning := strings.Repeat("r", reasoningBytes)
	toolContent := strings.Repeat("y", toolResultBytes)

	out := make([]core.Message, 0, nTurns*3)
	for i := 0; i < nTurns; i++ {
		out = append(out, core.Message{
			ID:   fmt.Sprintf("u-%d", i),
			Role: core.RoleUser,
			Text: userText,
		})
		callID := fmt.Sprintf("tc-%d", i)
		out = append(out, core.Message{
			ID:        fmt.Sprintf("a-%d", i),
			Role:      core.RoleAssistant,
			Text:      asstText,
			Reasoning: reasoning,
			ToolCalls: []core.ToolCall{{
				ID:    callID,
				Name:  "read_file",
				Input: `{"path":"some/file.go"}`,
			}},
		})
		out = append(out, core.Message{
			ID:   fmt.Sprintf("t-%d", i),
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{{
				ToolCallID: callID,
				Name:       "read_file",
				Content:    toolContent,
			}},
		})
	}
	return out
}

// BenchmarkToDeepSeekMessages measures the cost of one history conversion —
// the work done once per tool-loop iteration inside (*Client).stream.
func BenchmarkToDeepSeekMessages(b *testing.B) {
	cases := []struct {
		name string
		n    int
	}{
		{"50turns", 50},
		{"200turns", 200},
		{"500turns", 500},
		{"2000turns", 2000},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			hist := syntheticHistory(c.n, 2*1024, 256, 4*1024)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out := toDeepSeekMessages(hist)
				if len(out) == 0 {
					b.Fatal("empty")
				}
			}
		})
	}
}

// BenchmarkPayloadMarshal measures the toDeepSeekMessages + json.Marshal
// combination — the actual per-tool-loop work in (*Client).stream. This is
// what actually runs on every tool-call iteration.
func BenchmarkPayloadMarshal(b *testing.B) {
	cases := []struct {
		name string
		n    int
	}{
		{"50turns", 50},
		{"200turns", 200},
		{"500turns", 500},
		{"2000turns", 2000},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			hist := syntheticHistory(c.n, 2*1024, 256, 4*1024)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msgs := toDeepSeekMessages(hist)
				payload := map[string]any{
					"model":          "deepseek-v4-flash",
					"stream":         true,
					"stream_options": map[string]any{"include_usage": true},
					"messages":       msgs,
					"thinking":       map[string]any{"type": "disabled"},
				}
				body, err := json.Marshal(payload)
				if err != nil {
					b.Fatal(err)
				}
				if len(body) == 0 {
					b.Fatal("empty body")
				}
			}
		})
	}
}

// BenchmarkPayloadMarshalToolLoop simulates a single turn with K tool-loop
// iterations, each of which currently re-runs toDeepSeekMessages + Marshal
// over the SAME history (only the last 1-2 messages change between iters).
// One outer iter = one full turn of K provider calls.
func BenchmarkPayloadMarshalToolLoop(b *testing.B) {
	cases := []struct {
		name        string
		nTurns      int
		toolLoopRTs int
	}{
		{"200turns_x3loops", 200, 3},
		{"500turns_x5loops", 500, 5},
		{"2000turns_x3loops", 2000, 3},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			hist := syntheticHistory(c.nTurns, 2*1024, 256, 4*1024)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for k := 0; k < c.toolLoopRTs; k++ {
					msgs := toDeepSeekMessages(hist)
					payload := map[string]any{
						"model":          "deepseek-v4-flash",
						"stream":         true,
						"stream_options": map[string]any{"include_usage": true},
						"messages":       msgs,
						"thinking":       map[string]any{"type": "disabled"},
					}
					body, err := json.Marshal(payload)
					if err != nil {
						b.Fatal(err)
					}
					if len(body) == 0 {
						b.Fatal("empty")
					}
				}
			}
			b.ReportMetric(float64(c.toolLoopRTs), "iters/op")
		})
	}
}
