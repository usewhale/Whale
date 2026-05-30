package notification

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDetectTerminal_Fallback(t *testing.T) {
	// Without any terminal env vars, should fall back to BEL.
	clearEnv(t)
	kind := detectTerminal()
	if kind != termBEL {
		t.Errorf("expected termBEL, got %d", kind)
	}
}

func TestWriteSeq_Ghostty(t *testing.T) {
	var buf bytes.Buffer
	writeSeq(&buf, termGhostty, Notification{Title: "Whale", Message: "done", Kind: "turn_complete"})
	out := buf.String()
	if !strings.HasPrefix(out, "\x1b]777;notify;") {
		t.Errorf("expected Ghostty prefix, got %q", out)
	}
	if !strings.Contains(out, ";Whale;done\x07") {
		t.Errorf("expected title+message, got %q", out)
	}
}

func TestWriteSeq_Kitty(t *testing.T) {
	var buf bytes.Buffer
	writeSeq(&buf, termKitty, Notification{Title: "Whale", Message: "done", Kind: "turn_complete"})
	out := buf.String()
	if !strings.Contains(out, "\x1b]99;i=") {
		t.Errorf("expected Kitty OSC 99 prefix, got %q", out)
	}
	if !strings.Contains(out, ":d=0:p=title;Whale") {
		t.Errorf("expected title part, got %q", out)
	}
	if !strings.Contains(out, ":p=body;done") {
		t.Errorf("expected body part, got %q", out)
	}
	if !strings.Contains(out, ":d=1:a=focus;") {
		t.Errorf("expected fire part, got %q", out)
	}
}

func TestWriteSeq_OSC9(t *testing.T) {
	var buf bytes.Buffer
	writeSeq(&buf, termOSC9, Notification{Title: "Whale", Message: "test msg", Kind: "turn_complete"})
	out := buf.String()
	if !strings.HasPrefix(out, "\x1b]9;") {
		t.Errorf("expected OSC 9 prefix, got %q", out)
	}
	if !strings.Contains(out, "Whale: test msg\x07") {
		t.Errorf("expected title: message, got %q", out)
	}
}

func TestWriteSeq_BEL(t *testing.T) {
	var buf bytes.Buffer
	writeSeq(&buf, termBEL, Notification{Title: "Whale", Message: "x", Kind: "turn_complete"})
	if buf.String() != "\x07" {
		t.Errorf("expected BEL only, got %q", buf.String())
	}
}

func TestTruncate_Short(t *testing.T) {
	s := "hello world"
	got := truncate(s, 200)
	if got != s {
		t.Errorf("expected %q, got %q", s, got)
	}
}

func TestTruncate_Long(t *testing.T) {
	s := strings.Repeat("a", 300)
	got := truncate(s, 200)
	if len(got) > 203 {
		t.Errorf("truncated string too long (%d chars): %q", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestTruncate_Multibyte(t *testing.T) {
	s := "你好世界hello"
	got := truncate(s, 4)
	if got != "你好世界…" {
		t.Errorf("truncated multibyte incorrectly: got %q, want %q", got, "你好世界…")
	}
	// Verify output is valid UTF-8
	if !utf8.ValidString(got) {
		t.Errorf("truncated string is not valid UTF-8: %q", got)
	}
}

func TestTruncate_MultibyteWithSpace(t *testing.T) {
	s := "你好 世界 hello world"
	got := truncate(s, 3)
	if got != "你好…" {
		t.Errorf("expected space-boundary truncation, got %q", got)
	}
}

func TestTruncate_CJKByteVsRuneComparison(t *testing.T) {
	// 3 CJK runes (9 bytes) + space + more text, n=10.
	// Space is at rune index 3, which is <= n/2=5 → should NOT truncate at space.
	// Byte offset 9 > 5 would falsely trigger space truncation.
	s := "你我他 abcdefghij"
	got := truncate(s, 10)
	want := "你我他 abcdef…"
	if got != want {
		t.Errorf("expected %q (keep content after space), got %q", want, got)
	}
}

func TestEscapeOSC(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"a;b", "a\\;b"},
		{"a\\b", "a\\\\b"},
		{"a;b\\c;d", "a\\;b\\\\c\\;d"},
		{"\x07", ""},
		{"\x1b", ""},
		{"\x00", ""},
		{"\x7f", ""},
		{"hello\x07world", "helloworld"},
		{"a\x1bb\x07c", "abc"},
		{"\x01\x02\x03", ""},
		{"tab\tkept", "tab\tkept"},
		{"newline\nkept", "newline\nkept"},
		{"carriage\rreturn", "carriage\rreturn"},
	}
	for _, tt := range tests {
		got := escapeOSC(tt.input)
		if got != tt.expected {
			t.Errorf("escapeOSC(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSendTurnDone_RespectsMinInterval(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{
		enabled:        true,
		writer:         &buf,
		term:           termBEL,
		minInterval:    1 * time.Hour, // Very long interval
		lastNotifyTurn: time.Now(),
	}
	n.SendTurnDone("test")
	if buf.String() != "" {
		t.Errorf("expected no output due to minInterval, got %q", buf.String())
	}
}

func TestSendTurnDone_Disabled(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{
		enabled: false,
		writer:  &buf,
		term:    termBEL,
	}
	n.SendTurnDone("test")
	if buf.String() != "" {
		t.Errorf("expected no output when disabled, got %q", buf.String())
	}
}

func TestSendApprovalRequired_NotThrottledByTurnDone(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{
		enabled:        true,
		writer:         &buf,
		term:           termBEL,
		minInterval:    1 * time.Hour, // Very long interval
		lastNotifyTurn: time.Now(),    // Recent turn-complete notification
	}
	// Approval should NOT be throttled by lastNotifyTurn.
	n.SendApprovalRequired("edit", "needs review")
	if buf.String() == "" {
		t.Error("expected approval notification despite recent turn-complete")
	}
}

func TestSendTurnDone_NotThrottledByApproval(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{
		enabled:            true,
		writer:             &buf,
		term:               termBEL,
		minInterval:        1 * time.Hour, // Very long interval
		lastNotifyApproval: time.Now(),    // Recent approval notification
	}
	// Turn-done should NOT be throttled by lastNotifyApproval.
	n.SendTurnDone("task finished")
	if buf.String() == "" {
		t.Error("expected turn-done notification despite recent approval")
	}
}

// clearEnv removes terminal-related env vars for the duration of the test.
func clearEnv(t *testing.T) {
	t.Helper()
	vars := []string{"TERM_PROGRAM", "ITERM_SESSION_ID", "KITTY_WINDOW_ID", "TERM"}
	saved := make(map[string]string)
	for _, k := range vars {
		saved[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for k, v := range saved {
			if v != "" {
				os.Setenv(k, v)
			} else {
				os.Unsetenv(k)
			}
		}
	})
}
