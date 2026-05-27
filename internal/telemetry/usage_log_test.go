package telemetry

import (
	"os"
	"path/filepath"
	"strings"
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
	}, 0.1234, time.UnixMilli(1000))
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
	if !strings.Contains(s, `"cache_hit_ratio"`) {
		t.Fatalf("missing cache hit ratio in log: %s", s)
	}
	if !strings.Contains(s, `"cache_hit_ratio":0.4166666666666667`) {
		t.Fatalf("unexpected cache hit ratio in log: %s", s)
	}
}
