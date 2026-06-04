package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/llm"
)

func TestAppendUsage_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	err := AppendUsage(path, "s1", "deepseek-v4-flash", "abc123", llm.Usage{
		PromptTokens:           12,
		CompletionTokens:       3,
		PromptCacheHitTokens:   5,
		PromptCacheMissTokens:  7,
		ReasoningReplayTokens:  2,
		ToolResultRawChars:     1200,
		ToolResultReplayChars:  300,
		ToolResultRawTokens:    300,
		ToolResultReplayTokens: 75,
		ToolResultTokensSaved:  225,
		ToolResultsCompacted:   1,
	}, 0.1234, time.UnixMilli(1000), &CacheShape{
		RequestKind: "agent",
		SystemHash:  "sys",
		SystemSegments: []CacheShapeSegment{{
			Index:     0,
			Name:      "runtime_context",
			Stability: "dynamic",
			Hash:      "seg",
			Bytes:     42,
		}},
		SystemBytes:  42,
		ToolsHash:    "tools",
		ToolsBytes:   12,
		LogTailHash:  "tail",
		LogTailBytes: 20,
		RequestHash:  "req",
		LogMessages:  2,
		TailMessages: 2,
	})
	if err != nil {
		t.Fatalf("append usage failed: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read usage log failed: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"session":"s1"`) {
		t.Fatalf("missing session in log: %s", s)
	}
	if !strings.Contains(s, `"reasoning_replay_tokens":2`) {
		t.Fatalf("missing replay tokens in log: %s", s)
	}
	if !strings.Contains(s, `"tool_result_tokens_saved":225`) {
		t.Fatalf("missing tool replay savings in log: %s", s)
	}
	if !strings.Contains(s, `"prefix_fingerprint":"abc123"`) {
		t.Fatalf("missing prefix fingerprint in log: %s", s)
	}
	if !strings.Contains(s, `"cache_shape"`) || !strings.Contains(s, `"system_hash":"sys"`) {
		t.Fatalf("missing cache shape in log: %s", s)
	}
	if !strings.Contains(s, `"request_kind":"agent"`) {
		t.Fatalf("missing cache shape request kind in log: %s", s)
	}
	if !strings.Contains(s, `"system_segments"`) || !strings.Contains(s, `"name":"runtime_context"`) {
		t.Fatalf("missing cache shape system segments in log: %s", s)
	}
	if !strings.Contains(s, `"cache_hit_ratio"`) {
		t.Fatalf("missing cache hit ratio in log: %s", s)
	}
	if !strings.Contains(s, `"cache_hit_ratio":0.4166666666666667`) {
		t.Fatalf("unexpected cache hit ratio in log: %s", s)
	}
	if !strings.Contains(s, `"cache_savings_usd"`) {
		t.Fatalf("missing cache savings in log: %s", s)
	}
}

func TestAppendUsage_WritesSubagentMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	err := AppendUsage(path, "child", "deepseek-v4-flash", "", llm.Usage{PromptTokens: 100}, 0.0001, time.UnixMilli(1000), nil, UsageMetadata{
		Kind:                "subagent",
		ParentSessionID:     "parent",
		SubagentRole:        "reviewer",
		SubagentTaskPreview: "inspect replay use",
	})
	if err != nil {
		t.Fatalf("append usage failed: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read usage log failed: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"kind":"subagent"`,
		`"parent_session_id":"parent"`,
		`"subagent_role":"reviewer"`,
		`"subagent_task_preview":"inspect replay use"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in log: %s", want, s)
		}
	}
}

func TestAppendUsage_CompactionSerializesConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	now := time.UnixMilli(2_000_000_000_000)
	old := UsageRecord{
		TS:                  now.AddDate(0, 0, -usageLogRetentionDays-1).UnixMilli(),
		Session:             strings.Repeat("x", 1024),
		Model:               "deepseek-v4-flash",
		SubagentTaskPreview: strings.Repeat("y", 256),
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("seed usage log: %v", err)
	}
	for i := 0; i < 4500; i++ {
		old.Session = strings.Repeat("x", 1024) + string(rune('a'+i%26))
		b, err := json.Marshal(old)
		if err != nil {
			t.Fatalf("marshal old record: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("write old record: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close seed log: %v", err)
	}

	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- AppendUsage(path, "recent-"+string(rune('a'+i)), "deepseek-v4-flash", "", llm.Usage{PromptTokens: 10}, 0.0001, now.Add(time.Duration(i)*time.Millisecond), nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("append usage failed: %v", err)
		}
	}

	f, err = os.Open(path)
	if err != nil {
		t.Fatalf("open compacted log: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	gotRecent := map[string]bool{}
	var oldLines int
	for scanner.Scan() {
		var rec UsageRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("unmarshal compacted record: %v", err)
		}
		if strings.HasPrefix(rec.Session, "recent-") {
			gotRecent[rec.Session] = true
		}
		if rec.TS < now.AddDate(0, 0, -usageLogRetentionDays).UnixMilli() {
			oldLines++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan compacted log: %v", err)
	}
	if len(gotRecent) != writers {
		t.Fatalf("lost recent appends: got %d want %d (%v)", len(gotRecent), writers, gotRecent)
	}
	if oldLines != 0 {
		t.Fatalf("expected old lines compacted away, got %d", oldLines)
	}
}
