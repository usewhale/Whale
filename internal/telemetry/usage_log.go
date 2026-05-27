package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/usewhale/whale/internal/llm"
)

type UsageRecord struct {
	TS                     int64   `json:"ts"`
	Session                string  `json:"session"`
	Model                  string  `json:"model"`
	PrefixFingerprint      string  `json:"prefix_fingerprint,omitempty"`
	PromptTokens           int     `json:"prompt_tokens"`
	CompletionTokens       int     `json:"completion_tokens"`
	PromptCacheHit         int     `json:"prompt_cache_hit_tokens"`
	PromptCacheMiss        int     `json:"prompt_cache_miss_tokens"`
	CacheHitRatio          float64 `json:"cache_hit_ratio,omitempty"`
	ReasoningReplayTok     int     `json:"reasoning_replay_tokens,omitempty"`
	ToolResultRawChars     int     `json:"tool_result_raw_chars,omitempty"`
	ToolResultReplayChars  int     `json:"tool_result_replay_chars,omitempty"`
	ToolResultRawTokens    int     `json:"tool_result_raw_tokens,omitempty"`
	ToolResultReplayTokens int     `json:"tool_result_replay_tokens,omitempty"`
	ToolResultTokensSaved  int     `json:"tool_result_tokens_saved,omitempty"`
	ToolResultsCompacted   int     `json:"tool_results_compacted,omitempty"`
	CostUSD                float64 `json:"cost_usd"`
}

func DefaultUsageLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "usage.jsonl"
	}
	return filepath.Join(home, ".whale", "usage.jsonl")
}

func AppendUsage(path, sessionID, model, prefixFingerprint string, usage llm.Usage, cost float64, now time.Time) error {
	if path == "" {
		path = DefaultUsageLogPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rec := UsageRecord{
		TS:                     now.UnixMilli(),
		Session:                sessionID,
		Model:                  model,
		PrefixFingerprint:      prefixFingerprint,
		PromptTokens:           usage.PromptTokens,
		CompletionTokens:       usage.CompletionTokens,
		PromptCacheHit:         usage.PromptCacheHitTokens,
		PromptCacheMiss:        usage.PromptCacheMissTokens,
		CacheHitRatio:          cacheHitRatio(usage),
		ReasoningReplayTok:     usage.ReasoningReplayTokens,
		ToolResultRawChars:     usage.ToolResultRawChars,
		ToolResultReplayChars:  usage.ToolResultReplayChars,
		ToolResultRawTokens:    usage.ToolResultRawTokens,
		ToolResultReplayTokens: usage.ToolResultReplayTokens,
		ToolResultTokensSaved:  usage.ToolResultTokensSaved,
		ToolResultsCompacted:   usage.ToolResultsCompacted,
		CostUSD:                cost,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func cacheHitRatio(usage llm.Usage) float64 {
	hit := usage.PromptCacheHitTokens
	miss := usage.PromptCacheMissTokens
	denom := hit + miss
	if denom <= 0 || hit <= 0 {
		return 0
	}
	return float64(hit) / float64(denom)
}
