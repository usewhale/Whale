package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "default", in: "", want: []string{"baseline", "whale"}},
		{name: "both", in: "both", want: []string{"baseline", "whale"}},
		{name: "baseline", in: "baseline", want: []string{"baseline"}},
		{name: "whale", in: "whale", want: []string{"whale"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModes(tt.in)
			if err != nil {
				t.Fatalf("parseModes(%q) error: %v", tt.in, err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("parseModes(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
	if _, err := parseModes("other"); err == nil {
		t.Fatal("parseModes(other) succeeded, want error")
	}
}

func TestUsageTotalsCacheHitRatioUsesHitPlusMiss(t *testing.T) {
	u := usageTotals{CacheHitTokens: 80, CacheMissTokens: 20}
	if got := u.CacheHitRatio(); got != 0.8 {
		t.Fatalf("cache ratio = %v, want 0.8", got)
	}
}

func TestReadUsageTotalsCountsDistinctPrefixFingerprints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	data := strings.Join([]string{
		`{"prompt_tokens":10,"completion_tokens":2,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":2,"cost_usd":0.1,"prefix_fingerprint":"fp-a"}`,
		`{"prompt_tokens":20,"completion_tokens":3,"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":10,"cost_usd":0.2,"prefix_fingerprint":"fp-a"}`,
		`{"prompt_tokens":30,"completion_tokens":4,"prompt_cache_hit_tokens":15,"prompt_cache_miss_tokens":15,"cost_usd":0.3,"prefix_fingerprint":"fp-b"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	totals, err := readUsageTotals(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(totals.PrefixFingerprints); got != 2 {
		t.Fatalf("prefix fingerprints = %d, want 2", got)
	}
	if totals.PromptTokens != 60 || totals.CompletionTokens != 9 {
		t.Fatalf("unexpected token totals: %+v", totals)
	}
}

func TestRenderMarkdownAggregatesWeightedCacheRatio(t *testing.T) {
	report := benchReport{
		Meta: benchMeta{Date: "2026-05-07T00:00:00Z", Model: "deepseek-v4-flash", Effort: "high", Modes: []string{"whale"}, TaskCount: 2, RepeatsPerTask: 1, WhaleVersion: "test", LiveDeepSeek: true},
		Results: []runResult{
			{Mode: "whale", TaskID: "a", Repeat: 1, Pass: true, Turns: 1, ToolCalls: 1, PrefixFingerprints: 1, CacheHitTokens: 90, CacheMissTokens: 10, CacheHitRatio: 0.9, CostUSD: 0.1},
			{Mode: "whale", TaskID: "b", Repeat: 1, Pass: false, Turns: 3, ToolCalls: 5, PrefixFingerprints: 1, CacheHitTokens: 10, CacheMissTokens: 90, CacheHitRatio: 0.1, CostUSD: 0.3},
		},
	}
	md := renderMarkdown(report)
	for _, want := range []string{"| runs | 2 |", "| pass rate | 50.0% |", "| cache hit | 50.0% |", "live DeepSeek API usage"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}
}

func TestRenderMarkdownComparesBaselineAndWhale(t *testing.T) {
	report := benchReport{
		Meta: benchMeta{Date: "2026-05-07T00:00:00Z", Model: "deepseek-v4-flash", Effort: "high", Modes: []string{"baseline", "whale"}, TaskCount: 1, RepeatsPerTask: 1, WhaleVersion: "test", LiveDeepSeek: true},
		Results: []runResult{
			{Mode: "baseline", TaskID: "a", Repeat: 1, Pass: true, Turns: 2, ToolCalls: 2, PrefixFingerprints: 2, CacheHitTokens: 20, CacheMissTokens: 80, CacheHitRatio: 0.2, CostUSD: 0.2},
			{Mode: "whale", TaskID: "a", Repeat: 1, Pass: true, Turns: 2, ToolCalls: 2, PrefixFingerprints: 1, CacheHitTokens: 80, CacheMissTokens: 20, CacheHitRatio: 0.8, CostUSD: 0.1},
		},
	}
	md := renderMarkdown(report)
	for _, want := range []string{
		"| metric | baseline | whale | delta |",
		"| cache hit | 20.0% | 80.0% | +60.0% |",
		"| mean prefix fingerprints | 2.0 | 1.0 | -1.0 |",
		"cache-hostile Whale control",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}
}

func TestTaskSetupsCreateExpectedFixtures(t *testing.T) {
	for _, task := range tasks {
		root := t.TempDir()
		if err := task.Setup(root); err != nil {
			t.Fatalf("%s setup failed: %v", task.ID, err)
		}
	}
}
