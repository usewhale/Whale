package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
	usageLogCompactionThresholdBytes = 5 * 1024 * 1024
	usageLogRetentionDays            = 365
)

var usageLogLocks sync.Map

func DefaultUsageLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "usage.jsonl"
	}
	return filepath.Join(home, ".whale", "usage.jsonl")
}

func AppendUsage(path, sessionID, model, prefixFingerprint string, usage llm.Usage, cost float64, now time.Time, cacheShape *CacheShape, metadata ...UsageMetadata) error {
	if path == "" {
		path = DefaultUsageLogPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	unlock, err := lockUsageLogPath(path)
	if err != nil {
		return err
	}
	defer unlock()

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
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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
	return compactUsageLogIfLarge(path, now)
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

func compactUsageLogIfLarge(path string, now time.Time) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() < usageLogCompactionThresholdBytes {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	cutoff := now.AddDate(0, 0, -usageLogRetentionDays).UnixMilli()
	tmp := path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		_ = f.Close()
		return err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmp)
		}
	}()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var rec UsageRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.TS > 0 && rec.TS < cutoff {
			continue
		}
		if _, err := out.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			_ = out.Close()
			_ = f.Close()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = out.Close()
		_ = f.Close()
		return err
	}
	if err := out.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}
