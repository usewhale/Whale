package tui

import "unicode"

const windowsPasteBurstLongChunkRunes = 16

type windowsPasteChunkDecision int

const (
	windowsPasteChunkTyped windowsPasteChunkDecision = iota
	windowsPasteChunkBurst
)

type windowsPasteBurstClassifier struct{}

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
