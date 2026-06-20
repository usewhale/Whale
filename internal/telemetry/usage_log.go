package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/llm"
)

type UsageRecord struct {
	TS                       int64       `json:"ts"`
	Session                  string      `json:"session"`
	Model                    string      `json:"model"`
	PrefixFingerprint        string      `json:"prefix_fingerprint,omitempty"`
	CacheShape               *CacheShape `json:"cache_shape,omitempty"`
	PromptTokens             int         `json:"prompt_tokens"`
	CompletionTokens         int         `json:"completion_tokens"`
	PromptCacheHit           int         `json:"prompt_cache_hit_tokens"`
	PromptCacheMiss          int         `json:"prompt_cache_miss_tokens"`
	PrefixCompletionRequests int         `json:"prefix_completion_requests,omitempty"`
	CacheHitRatio            float64     `json:"cache_hit_ratio,omitempty"`
	ReasoningReplayTok       int         `json:"reasoning_replay_tokens,omitempty"`
	ToolResultRawChars       int         `json:"tool_result_raw_chars,omitempty"`
	ToolResultReplayChars    int         `json:"tool_result_replay_chars,omitempty"`
	ToolResultRawTokens      int         `json:"tool_result_raw_tokens,omitempty"`
	ToolResultReplayTokens   int         `json:"tool_result_replay_tokens,omitempty"`
	ToolResultTokensSaved    int         `json:"tool_result_tokens_saved,omitempty"`
	ToolResultsCompacted     int         `json:"tool_results_compacted,omitempty"`
	Kind                     string      `json:"kind,omitempty"`
	ParentSessionID          string      `json:"parent_session_id,omitempty"`
	SubagentRole             string      `json:"subagent_role,omitempty"`
	SubagentTaskPreview      string      `json:"subagent_task_preview,omitempty"`
	CacheSavingsUSD          float64     `json:"cache_savings_usd,omitempty"`
	CostUSD                  float64     `json:"cost_usd"`
}

type CacheShape struct {
	RequestKind          string              `json:"request_kind,omitempty"`
	PrefixHash           string              `json:"prefix_hash,omitempty"`
	PrefixBytes          int                 `json:"prefix_bytes,omitempty"`
	SystemHash           string              `json:"system_hash,omitempty"`
	SystemSegments       []CacheShapeSegment `json:"system_segments,omitempty"`
	SystemBytes          int                 `json:"system_bytes,omitempty"`
	RuntimeHash          string              `json:"runtime_hash,omitempty"`
	RuntimeSegments      []CacheShapeSegment `json:"runtime_segments,omitempty"`
	RuntimeBytes         int                 `json:"runtime_bytes,omitempty"`
	ToolsHash            string              `json:"tools_hash,omitempty"`
	ToolSegments         []CacheShapeSegment `json:"tool_segments,omitempty"`
	ToolsBytes           int                 `json:"tools_bytes,omitempty"`
	FewShotHash          string              `json:"fewshot_hash,omitempty"`
	AssistantPrefixHash  string              `json:"assistant_prefix_hash,omitempty"`
	AssistantPrefixBytes int                 `json:"assistant_prefix_bytes,omitempty"`
	LogHeadHash          string              `json:"log_head_hash,omitempty"`
	LogHeadBytes         int                 `json:"log_head_bytes,omitempty"`
	LogTailHash          string              `json:"log_tail_hash,omitempty"`
	LogTailBytes         int                 `json:"log_tail_bytes,omitempty"`
	RequestHash          string              `json:"request_hash,omitempty"`
	LogMessages          int                 `json:"log_messages,omitempty"`
	TailMessages         int                 `json:"tail_messages,omitempty"`
}

type CacheShapeSegment struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Stability string `json:"stability"`
	Hash      string `json:"hash"`
	Bytes     int    `json:"bytes"`
}

type UsageMetadata struct {
	Kind                string
	ParentSessionID     string
	SubagentRole        string
	SubagentTaskPreview string
}

const (
	usageLogRetentionDays = 365
)

var usageLogLocks sync.Map

func DefaultUsageLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "usage"
	}
	return filepath.Join(home, ".whale", "usage")
}

func AppendUsage(dir, sessionID, model, prefixFingerprint string, usage llm.Usage, cost float64, now time.Time, cacheShape *CacheShape, metadata ...UsageMetadata) error {
	if dir == "" {
		dir = DefaultUsageLogDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}

	rec := UsageRecord{
		TS:                       now.UnixMilli(),
		Session:                  sessionID,
		Model:                    model,
		PrefixFingerprint:        prefixFingerprint,
		CacheShape:               CloneCacheShape(cacheShape),
		PromptTokens:             usage.PromptTokens,
		CompletionTokens:         usage.CompletionTokens,
		PromptCacheHit:           usage.PromptCacheHitTokens,
		PromptCacheMiss:          usage.PromptCacheMissTokens,
		PrefixCompletionRequests: usage.PrefixCompletionRequests,
		CacheHitRatio:            cacheHitRatio(usage),
		ReasoningReplayTok:       usage.ReasoningReplayTokens,
		ToolResultRawChars:       usage.ToolResultRawChars,
		ToolResultReplayChars:    usage.ToolResultReplayChars,
		ToolResultRawTokens:      usage.ToolResultRawTokens,
		ToolResultReplayTokens:   usage.ToolResultReplayTokens,
		ToolResultTokensSaved:    usage.ToolResultTokensSaved,
		ToolResultsCompacted:     usage.ToolResultsCompacted,
		CacheSavingsUSD:          EstimateCacheSavingsUSD(model, usage.PromptCacheHitTokens),
		CostUSD:                  cost,
	}
	if len(metadata) > 0 {
		rec.Kind = metadata[0].Kind
		rec.ParentSessionID = metadata[0].ParentSessionID
		rec.SubagentRole = metadata[0].SubagentRole
		rec.SubagentTaskPreview = metadata[0].SubagentTaskPreview
	}

	// Co-locate subagent records in the parent session's file so that
	// /status on the parent reads a single per-session file.
	fileSessionID := sessionID
	if strings.EqualFold(strings.TrimSpace(rec.Kind), "subagent") {
		if pid := strings.TrimSpace(rec.ParentSessionID); pid != "" {
			fileSessionID = pid
		}
	}
	filePath := filepath.Join(dir, fileSessionID+".jsonl")
	unlock, err := lockUsageLogPath(filePath)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return compactUsageDir(dir, now)
}

func CloneCacheShape(in *CacheShape) *CacheShape {
	if in == nil {
		return nil
	}
	out := *in
	out.SystemSegments = append([]CacheShapeSegment(nil), in.SystemSegments...)
	out.RuntimeSegments = append([]CacheShapeSegment(nil), in.RuntimeSegments...)
	out.ToolSegments = append([]CacheShapeSegment(nil), in.ToolSegments...)
	return &out
}

func lockUsageLogPath(path string) (func(), error) {
	key := filepath.Clean(path)
	muAny, _ := usageLogLocks.LoadOrStore(key, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()

	fileUnlock, err := lockUsageLogFile(key + ".lock")
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		fileUnlock()
		mu.Unlock()
	}, nil
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

func compactUsageDir(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, -usageLogRetentionDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}
