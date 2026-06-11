package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateOnRuneBoundaryNeverProducesInvalidUTF8(t *testing.T) {
	// CJK (3 bytes), emoji (4 bytes), and mixed content: every possible
	// byte-slice boundary must come back valid after trimming.
	samples := []string{
		strings.Repeat("汉字输出", 8),
		strings.Repeat("a汉🐳b", 6),
		"plain ascii only",
	}
	for _, s := range samples {
		for cut := 0; cut <= len(s); cut++ {
			head := truncateOnRuneBoundary(s[:cut], false)
			if !utf8.ValidString(head) {
				t.Fatalf("head trim produced invalid UTF-8 at cut %d of %q: %q", cut, s, head)
			}
			tail := truncateOnRuneBoundary(s[cut:], true)
			if !utf8.ValidString(tail) {
				t.Fatalf("tail trim produced invalid UTF-8 at cut %d of %q: %q", cut, s, tail)
			}
		}
	}
}

func TestRenderTruncatedToolTextValidUTF8AtCJKBoundary(t *testing.T) {
	text := strings.Repeat("中文内容很长需要截断处理", 200)
	out := RenderTruncatedToolText(text, 1500, "")
	if len(out) > 1500 {
		t.Fatalf("truncated text too long: %d", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("truncated model text is not valid UTF-8: %q", out[:80])
	}
	payload := BoundedTruncationPayload(text, len(text), "ok", "")
	if head, _ := payload["head"].(string); !utf8.ValidString(head) {
		t.Fatalf("bounded payload head is not valid UTF-8")
	}
	if tail, _ := payload["tail"].(string); !utf8.ValidString(tail) {
		t.Fatalf("bounded payload tail is not valid UTF-8")
	}
}

func TestRenderTruncatedToolTextSmallBudgetKeepsMarker(t *testing.T) {
	text := strings.Repeat("output line\n", 300)
	for _, maxChars := range []int{600, 200, 64} {
		out := RenderTruncatedToolText(text, maxChars, "")
		if len(out) > maxChars {
			t.Fatalf("maxChars=%d: result too long: %d", maxChars, len(out))
		}
		if !strings.Contains(out, "...[truncated") && !strings.Contains(out, "...[output truncated") {
			t.Fatalf("maxChars=%d: truncation must never be silent, got %q", maxChars, out)
		}
		if !utf8.ValidString(out) {
			t.Fatalf("maxChars=%d: invalid UTF-8", maxChars)
		}
	}
	// Untruncated input passes through whole.
	if got := RenderTruncatedToolText("short", 600, ""); got != "short" {
		t.Fatalf("short input must pass through, got %q", got)
	}
}
