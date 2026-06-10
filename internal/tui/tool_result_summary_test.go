package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestSummarizeToolResultForChatPreservesShellANSI(t *testing.T) {
	raw := `{"success":true,"data":{"status":"ok","metrics":{"exit_code":0,"duration_ms":23},"payload":{"stdout":"\u001b[31mRED\u001b[0m\n\u001b[34mBLUE\u001b[0m\n","stderr":""}}}`
	role, text := summarizeToolResultForChat("shell_run", raw)
	if role != "result_ok" {
		t.Fatalf("role = %q, want result_ok", role)
	}
	for _, want := range []string{"\x1b[31mRED\x1b[0m", "\x1b[34mBLUE\x1b[0m"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected ANSI output %q preserved, got: %q", want, text)
		}
	}
	plain := xansi.Strip(text)
	if !strings.Contains(plain, "RED") || !strings.Contains(plain, "BLUE") {
		t.Fatalf("expected visible shell output preserved, got: %q", plain)
	}
}

func TestSummarizeShellOutputTruncatesColoredLineByVisibleWidth(t *testing.T) {
	input := "\x1b[31m" + strings.Repeat("x", shellOutputLineRunes+20) + "\x1b[0m"
	got := summarizeShellOutput(input)
	if xansi.StringWidth(got) > shellOutputLineRunes {
		t.Fatalf("visible width = %d, want <= %d: %q", xansi.StringWidth(got), shellOutputLineRunes, got)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("expected color sequence preserved, got: %q", got)
	}
	if !strings.Contains(xansi.Strip(got), "...") {
		t.Fatalf("expected truncation marker, got: %q", got)
	}
}

func TestSummarizeShellOutputPreservesColoredHeadTail(t *testing.T) {
	input := strings.Join([]string{
		"\x1b[31mone\x1b[0m",
		"\x1b[32mtwo\x1b[0m",
		"\x1b[33mthree\x1b[0m",
		"\x1b[34mfour\x1b[0m",
		"\x1b[35mfive\x1b[0m",
		"\x1b[36msix\x1b[0m",
		"\x1b[37mseven\x1b[0m",
	}, "\n")
	got := summarizeShellOutput(input)
	plain := xansi.Strip(got)
	for _, want := range []string{"one", "two", "... 3 lines omitted", "six", "seven"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected %q in summary, got: %q", want, got)
		}
	}
	if strings.Contains(plain, "three") || strings.Contains(plain, "four") || strings.Contains(plain, "five") {
		t.Fatalf("expected middle lines omitted, got: %q", got)
	}
	for _, want := range []string{"\x1b[31mone", "\x1b[32mtwo", "\x1b[36msix", "\x1b[37mseven"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected colored retained line %q, got: %q", want, got)
		}
	}
}

func TestSummarizeToolResultForChatIgnoresANSIOnlyShellOutput(t *testing.T) {
	raw := `{"success":true,"data":{"status":"ok","metrics":{"exit_code":0,"duration_ms":23},"payload":{"stdout":"\u001b[0m\n","stderr":""}}}`
	role, text := summarizeToolResultForChat("shell_run", raw)
	if role != "result_ok" {
		t.Fatalf("role = %q, want result_ok", role)
	}
	if strings.Contains(text, "\n") {
		t.Fatalf("expected no output line for ANSI-only output, got: %q", text)
	}
	if !strings.Contains(text, "✓") {
		t.Fatalf("expected success marker, got: %q", text)
	}
}
