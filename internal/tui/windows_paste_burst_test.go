package tui

import "testing"

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
