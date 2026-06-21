package render

import (
	"strings"
	"testing"
)

func TestInjectHyperlinks_WrapsFullURLEvenWhenSplitByWrap(t *testing.T) {
	input := "PR created: https://github.com/usewhale/Whale/pull/134 — ready."
	out := Markdown(input, 40, false)
	const want = "\x1b]8;;https://github.com/usewhale/Whale/pull/134\a"
	if !strings.Contains(out, want) {
		t.Fatalf("expected OSC 8 wrapper with full URL\n got: %q", out)
	}
	if !strings.Contains(out, "\x1b]8;;\a") {
		t.Fatalf("expected OSC 8 close sequence\n got: %q", out)
	}
}

func TestInjectHyperlinks_DoesNotJoinWrappedProseAfterURL(t *testing.T) {
	out := Markdown("go https://example.com next", 24, false)
	if strings.Contains(out, "\x1b]8;;https://example.comnext\a") {
		t.Fatalf("ordinary wrapped prose leaked into URL target: %q", out)
	}
	if !strings.Contains(out, "\x1b]8;;https://example.com\a") {
		t.Fatalf("expected exact URL target\n got: %q", out)
	}
}

func TestInjectHyperlinks_TrimsTrailingPunctuation(t *testing.T) {
	// In prose, a URL followed by ")." or "." should not include the
	// punctuation in the click target.
	got := injectHyperlinks("see (https://example.com/path).")
	const wantTarget = "\x1b]8;;https://example.com/path\a"
	if !strings.Contains(got, wantTarget) {
		t.Fatalf("expected target without trailing punctuation\n got: %q", got)
	}
	if strings.Contains(got, "\x1b]8;;https://example.com/path).\a") {
		t.Fatalf("punctuation leaked into click target: %q", got)
	}
}

func TestInjectHyperlinks_PreservesBalancedClosingDelimiters(t *testing.T) {
	cases := []string{
		"https://example.com/path(foo)",
		"https://example.com/a[b]",
	}
	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			out := injectHyperlinks("see " + url)
			if !strings.Contains(out, "\x1b]8;;"+url+"\a") {
				t.Fatalf("expected balanced delimiter in target\n got: %q", out)
			}
		})
	}
}

func TestInjectHyperlinks_LeavesTextWithoutURLsUntouched(t *testing.T) {
	in := "just some prose with no link"
	if got := injectHyperlinks(in); got != in {
		t.Fatalf("unexpected mutation: %q", got)
	}
}

func TestInjectHyperlinks_HandlesMultipleURLs(t *testing.T) {
	in := "see https://a.example/one and https://b.example/two."
	out := injectHyperlinks(in)
	if !strings.Contains(out, "\x1b]8;;https://a.example/one\a") {
		t.Fatalf("missing first URL wrap: %q", out)
	}
	if !strings.Contains(out, "\x1b]8;;https://b.example/two\a") {
		t.Fatalf("missing second URL wrap: %q", out)
	}
}
