package app

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/telemetry"
)

func TestSessionUsageSummaryReportsSubagentShapeDriftOnlyWhenHashesChange(t *testing.T) {
	dir := t.TempDir()
	usagePath := filepath.Join(dir, "usage.jsonl")
	parentID := "parent-session"

	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:          parentID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     1000,
		CompletionTokens: 100,
		PromptCacheHit:   800,
		PromptCacheMiss:  200,
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:          "child-1",
		Model:            "deepseek-v4-flash",
		Kind:             "subagent",
		ParentSessionID:  parentID,
		PromptTokens:     2000,
		CompletionTokens: 200,
		PromptCacheHit:   1500,
		PromptCacheMiss:  500,
		CacheShape:       &telemetry.CacheShape{RequestHash: "request-a", SystemHash: "system-a", ToolsHash: "tools-a"},
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:          "child-2",
		Model:            "deepseek-v4-flash",
		Kind:             "subagent",
		ParentSessionID:  parentID,
		PromptTokens:     3000,
		CompletionTokens: 300,
		PromptCacheHit:   2500,
		PromptCacheMiss:  500,
		CacheShape:       &telemetry.CacheShape{RequestHash: "request-a", SystemHash: "system-a", ToolsHash: "tools-a"},
	})

	stable := formatSessionUsageSummary(readSessionUsageSummary(usagePath, parentID))
	if strings.Contains(stable, "subagent shape drift") {
		t.Fatalf("stable subagent shapes should not report drift: %s", stable)
	}

	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:          "child-3",
		Model:            "deepseek-v4-flash",
		Kind:             "subagent",
		ParentSessionID:  parentID,
		PromptTokens:     4000,
		CompletionTokens: 400,
		PromptCacheHit:   2500,
		PromptCacheMiss:  1500,
		CacheShape:       &telemetry.CacheShape{RequestHash: "request-b", SystemHash: "system-b", ToolsHash: "tools-a"},
	})

	drift := formatSessionUsageSummary(readSessionUsageSummary(usagePath, parentID))
	for _, want := range []string{
		"subagents 3 turns",
		"subagent shape drift 2 request/2 system",
	} {
		if !strings.Contains(drift, want) {
			t.Fatalf("summary missing %q:\n%s", want, drift)
		}
	}
	if strings.Contains(drift, "tools") {
		t.Fatalf("stable tools hash should not be reported as drift: %s", drift)
	}
}

func TestUsageStatsCacheDiagnosticsReportsToolShapeBreak(t *testing.T) {
	dir := t.TempDir()
	usagePath := filepath.Join(dir, "usage.jsonl")
	ts := time.Date(2026, 5, 12, 10, 0, 0, 0, time.Local)

	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		TS:              ts.UnixMilli(),
		Session:         "s-cache",
		Model:           "deepseek-v4-flash",
		PromptTokens:    12000,
		PromptCacheHit:  10000,
		PromptCacheMiss: 2000,
		CacheShape: &telemetry.CacheShape{
			RequestKind: "agent",
			SystemHash:  "system-a",
			RuntimeHash: "runtime-a",
			ToolsHash:   "tools-a",
			LogTailHash: "tail-a",
			RequestHash: "request-a",
			ToolSegments: []telemetry.CacheShapeSegment{
				{Name: "read_file", Hash: "read-a"},
				{Name: "mcp__fs__read", Hash: "mcp-a"},
			},
		},
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		TS:              ts.Add(time.Minute).UnixMilli(),
		Session:         "s-cache",
		Model:           "deepseek-v4-flash",
		PromptTokens:    12000,
		PromptCacheHit:  6000,
		PromptCacheMiss: 6000,
		CacheShape: &telemetry.CacheShape{
			RequestKind: "agent",
			SystemHash:  "system-a",
			RuntimeHash: "runtime-a",
			ToolsHash:   "tools-b",
			LogTailHash: "tail-a",
			RequestHash: "request-b",
			ToolSegments: []telemetry.CacheShapeSegment{
				{Name: "read_file", Hash: "read-b"},
				{Name: "mcp__fs__read", Hash: "mcp-b"},
			},
		},
	})

	stats := readUsageStats(usagePath, ts.Add(2*time.Minute))
	if got := totalCacheBreaks(stats.CacheDiagnostics); got != 1 {
		t.Fatalf("cache break count = %d, want 1", got)
	}
	if got := stats.CacheDiagnostics.Counts["tools changed"]; got != 1 {
		t.Fatalf("tools changed count = %d, want 1", got)
	}
	text := strings.Join(formatCacheDiagnostics(stats.CacheDiagnostics), "\n")
	for _, want := range []string{
		"Break causes",
		"tools changed: 1",
		"cache hit 10K -> 6K",
		"changed mcp, read_file",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cache diagnostics missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "mcp__fs__read") {
		t.Fatalf("cache diagnostics should sanitize MCP tool names:\n%s", text)
	}

	a := &App{cfg: Config{DataDir: dir}, sessionsDir: filepath.Join(dir, "sessions")}
	view := a.buildStatsViewAt("cache", ts.Add(2*time.Minute))
	for _, want := range []string{
		"Cache diagnostics",
		"Recent breaks",
		"tools changed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("stats cache view missing %q:\n%s", want, view)
		}
	}
}

func TestCacheBreakDetectorClassifiesUnchangedShapeAsServerSideOrTTL(t *testing.T) {
	detector := newCacheBreakDetector()
	shape := &telemetry.CacheShape{
		RequestKind: "agent",
		SystemHash:  "system-a",
		RuntimeHash: "runtime-a",
		ToolsHash:   "tools-a",
		LogTailHash: "tail-a",
		RequestHash: "request-a",
	}
	detector.Add(telemetry.UsageRecord{
		Session:        "s-cache",
		Model:          "deepseek-v4-flash",
		PromptCacheHit: 10000,
		CacheShape:     shape,
	})
	detector.Add(telemetry.UsageRecord{
		Session:         "s-cache",
		Model:           "deepseek-v4-flash",
		PromptCacheHit:  6000,
		PromptCacheMiss: 6000,
		CacheShape:      shape,
	})

	diag := detector.Diagnostics()
	if got := totalCacheBreaks(diag); got != 1 {
		t.Fatalf("cache break count = %d, want 1", got)
	}
	if got := diag.Breaks[0].Cause; got != "likely server-side or TTL" {
		t.Fatalf("cause = %q, want likely server-side or TTL", got)
	}
}

func TestAppendRecentCacheBreakKeepsNewestBreaks(t *testing.T) {
	var recent []cacheBreak
	for i := int64(1); i <= statsRecentLimit+2; i++ {
		recent = appendRecentCacheBreak(recent, cacheBreak{
			TS:      i,
			Session: "s" + strconv.FormatInt(i, 10),
		})
	}

	if len(recent) != statsRecentLimit {
		t.Fatalf("recent breaks len = %d, want %d", len(recent), statsRecentLimit)
	}
	if recent[0].Session != "s7" {
		t.Fatalf("newest break should be first, got %+v", recent)
	}
	for _, br := range recent {
		if br.Session == "s1" || br.Session == "s2" {
			t.Fatalf("old cache break was retained instead of newest tail: %+v", recent)
		}
	}
}
