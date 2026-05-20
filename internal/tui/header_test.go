package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestHeaderBannerUsesLargeLogoWhenWide(t *testing.T) {
	got := buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 80, 24)
	for _, want := range []string{
		"╭",
		"██╗    ██╗██╗  ██╗ █████╗ ██╗     ███████╗",
		"model:     deepseek-chat   /model to change",
		"thinking:  on   /model to change",
		"directory: ~/work/whale",
		"╰",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected header to contain %q:\n%s", want, got)
		}
	}
}

func TestHeaderBannerKeepsWindowsDirectoryVisible(t *testing.T) {
	got := buildHeaderBanner("deepseek-chat", "medium", "on", `C:\Users\goranka`, "v0.1.0", 80, 24)
	if !strings.Contains(got, `directory: C:\Users\goranka`) {
		t.Fatalf("expected Windows directory in header:\n%s", got)
	}
	if strings.Contains(got, "directory: ~") {
		t.Fatalf("Windows header should not collapse directory to home shorthand:\n%s", got)
	}
}

func TestDisplayWorkingDirectoryDoesNotCollapseWindowsHome(t *testing.T) {
	got := displayWorkingDirectory(`C:\Users\goranka`, `C:\Users\goranka`, "windows")
	if got != `C:\Users\goranka` {
		t.Fatalf("expected absolute Windows home directory, got %q", got)
	}
}

func TestHeaderBannerFallsBackWhenNarrow(t *testing.T) {
	got := buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 40, 24)
	if strings.Contains(got, "██╗") {
		t.Fatalf("expected compact header without large logo:\n%s", got)
	}
	if !strings.Contains(got, "WHALE v0.1.0") {
		t.Fatalf("expected compact wordmark:\n%s", got)
	}
	if !strings.Contains(got, "╭") || !strings.Contains(got, "╰") {
		t.Fatalf("expected compact header to keep border:\n%s", got)
	}
}

func TestHeaderBannerUsesSemanticColorSegments(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	got := buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 80, 24)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected styled header segments, got:\n%s", got)
	}
	plain := xansi.Strip(got)
	for _, want := range []string{
		"model:     deepseek-chat   /model to change",
		"thinking:  on   /model to change",
		"directory: ~/work/whale",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected stripped header to contain %q:\n%s", want, plain)
		}
	}
}

func TestHeaderBannerConstrainedToRequestedWidth(t *testing.T) {
	got := buildHeaderBanner(
		"deepseek-reasoner-with-a-very-long-model-name",
		"extraordinarily-high-effort",
		"enabled-with-a-long-status",
		"~/Engineer/ai/dsk/whale-header-support/internal/tui",
		"v2026.05.19-long-version",
		40,
		24,
	)
	for _, line := range strings.Split(got, "\n") {
		if width := lipgloss.Width(line); width > 40 {
			t.Fatalf("expected line width <= 40, got %d for %q\n%s", width, line, got)
		}
	}
}

func TestHeaderBannerTruncatesStyledLinesSafely(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	got := buildHeaderBanner(
		"deepseek-reasoner-with-a-very-long-model-name-that-forces-truncation",
		"extraordinarily-high-effort-that-forces-truncation",
		"enabled-with-a-long-status-that-forces-truncation",
		"~/Engineer/ai/dsk/whale-header-support/internal/tui/with/a/very/long/path",
		"v2026.05.19-long-version-that-forces-truncation",
		28,
		24,
	)
	if strings.Contains(xansi.Strip(got), "\x1b") {
		t.Fatalf("header leaked malformed ANSI after stripping:\n%q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if width := lipgloss.Width(line); width > 28 {
			t.Fatalf("expected line width <= 28, got %d for %q\n%s", width, line, got)
		}
	}
}

func TestHeaderBannerUsesTinyModeWhenShort(t *testing.T) {
	got := buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 80, 8)
	if strings.Contains(got, "██╗") {
		t.Fatalf("expected short terminal header without large logo:\n%s", got)
	}
	if strings.Contains(got, "/model to change") || strings.Contains(got, "/thinking to change") {
		t.Fatalf("expected short terminal header to omit hint rows:\n%s", got)
	}
	if gotLines := strings.Count(got, "\n") + 1; gotLines > 8 {
		t.Fatalf("expected tiny header to fit in 8 rows, got %d:\n%s", gotLines, got)
	}
}

func TestHeaderBannerOmittedWhenTooShortForMiniHeader(t *testing.T) {
	for _, height := range []int{1, 2} {
		got := buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 80, height)
		if got != "" {
			t.Fatalf("expected no partial header for height %d, got:\n%s", height, got)
		}
	}
}

func TestHeaderBannerFitsHeightBudget(t *testing.T) {
	for _, height := range []int{0, 1, 2, 3, 5, 6, 8, 9, 13, 14, 24} {
		got := buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 80, height)
		if gotLines := countVisibleLines(got); height > 0 && gotLines > height {
			t.Fatalf("expected header to fit height %d, got %d lines:\n%s", height, gotLines, got)
		}
	}
}

func TestHeaderBannerDoesNotAdvertiseUnsupportedThinkingCommand(t *testing.T) {
	for _, got := range []string{
		buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 80, 24),
		buildHeaderBanner("deepseek-chat", "medium", "on", "~/work/whale", "v0.1.0", 40, 24),
	} {
		if strings.Contains(got, "/thinking") {
			t.Fatalf("header should not advertise unsupported /thinking command:\n%s", got)
		}
	}
}
