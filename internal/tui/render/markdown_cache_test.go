package render

import (
	"strconv"
	"testing"
)

func TestMarkdownCache_HitProducesIdenticalOutput(t *testing.T) {
	resetMarkdownCacheForTest()
	input := "## Title\n\nParagraph with **bold** and `code`.\n\n```go\nfunc x() {}\n```\n"

	first := Markdown(input, 80, false)
	second := Markdown(input, 80, false)
	if first != second {
		t.Fatalf("cache hit produced different output\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if _, ok := markdownCacheGet(markdownCacheKey{input: normalizeMarkdownLinks(input, escapeAutolinksForRenderer), width: 80, quiet: false}); !ok {
		t.Fatalf("expected entry to be cached after Markdown() call")
	}
}

func TestMarkdownCache_WidthAndQuietAreSeparateKeys(t *testing.T) {
	resetMarkdownCacheForTest()
	input := "## Title\n\nParagraph with **bold**.\n"

	wide := Markdown(input, 120, false)
	narrow := Markdown(input, 40, false)
	if wide == narrow {
		t.Fatal("expected different output for different widths")
	}
	loud := Markdown(input, 80, false)
	quiet := Markdown(input, 80, true)
	if loud == quiet {
		t.Fatal("expected different output for quiet vs loud styles")
	}
}

func TestMarkdownCache_EvictsBeyondCap(t *testing.T) {
	resetMarkdownCacheForTest()
	for i := 0; i < markdownCacheCap+50; i++ {
		Markdown(uniqueMarkdown(i), 80, false)
	}
	markdownCacheMu.Lock()
	got := markdownCacheList.Len()
	markdownCacheMu.Unlock()
	if got > markdownCacheCap {
		t.Fatalf("cache exceeded cap: %d > %d", got, markdownCacheCap)
	}
}

func TestMarkdownCache_SkipsOversizedInputs(t *testing.T) {
	resetMarkdownCacheForTest()
	huge := make([]byte, markdownCacheMaxInputBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	// Bypass Markdown() (which would force a slow goldmark render of the huge
	// input) and verify the cache layer itself refuses to store the entry.
	markdownCachePut(markdownCacheKey{input: string(huge), width: 80}, "out")
	markdownCacheMu.Lock()
	got := markdownCacheList.Len()
	markdownCacheMu.Unlock()
	if got != 0 {
		t.Fatalf("expected no cache entry for oversized input, got %d", got)
	}
}

func uniqueMarkdown(i int) string {
	return "## entry\n\nthis is entry number " +
		string(rune('A'+(i%26))) +
		" with index " +
		strconv.Itoa(i) +
		" and *some* emphasis.\n"
}
