package main

import "time"

type benchMeta struct {
	Date           string   `json:"date"`
	Model          string   `json:"model"`
	Effort         string   `json:"effort"`
	Modes          []string `json:"modes"`
	TaskCount      int      `json:"task_count"`
	RepeatsPerTask int      `json:"repeats_per_task"`
	WhaleVersion   string   `json:"whale_version"`
	LiveDeepSeek   bool     `json:"live_deepseek"`
}

type benchReport struct {
	Meta    benchMeta   `json:"meta"`
	Results []runResult `json:"results"`
}

type runResult struct {
	Mode               string  `json:"mode"`
	TaskID             string  `json:"task_id"`
	Repeat             int     `json:"repeat"`
	Pass               bool    `json:"pass"`
	Turns              int     `json:"turns"`
	ToolCalls          int     `json:"tool_calls"`
	PromptTokens       int     `json:"prompt_tokens"`
	CompletionTokens   int     `json:"completion_tokens"`
	CacheHitTokens     int     `json:"prompt_cache_hit_tokens"`
	CacheMissTokens    int     `json:"prompt_cache_miss_tokens"`
	CacheHitRatio      float64 `json:"cache_hit_ratio"`
	CostUSD            float64 `json:"cost_usd"`
	PrefixFingerprints int     `json:"prefix_fingerprints"`
	FinalOutput        string  `json:"final_output,omitempty"`
	Error              string  `json:"error,omitempty"`
	DurationMS         int64   `json:"duration_ms"`
	Workspace          string  `json:"workspace,omitempty"`
	TranscriptPath     string  `json:"transcript_path,omitempty"`
}

type usageTotals struct {
	PromptTokens       int
	CompletionTokens   int
	CacheHitTokens     int
	CacheMissTokens    int
	CostUSD            float64
	PrefixFingerprints map[string]bool
}

func (u usageTotals) CacheHitRatio() float64 {
	denom := u.CacheHitTokens + u.CacheMissTokens
	if denom <= 0 {
		return 0
	}
	return float64(u.CacheHitTokens) / float64(denom)
}

type taskSpec struct {
	ID          string
	Description string
	Prompts     []string
	Setup       func(root string) error
	Check       func(root string, transcript []transcriptRecord, finalOutput string) error
}

type transcriptRecord struct {
	TS            string         `json:"ts"`
	Turn          int            `json:"turn,omitempty"`
	Role          string         `json:"role,omitempty"`
	Event         string         `json:"event,omitempty"`
	Content       string         `json:"content,omitempty"`
	Tool          string         `json:"tool,omitempty"`
	Success       *bool          `json:"success,omitempty"`
	Model         string         `json:"model,omitempty"`
	PrefixHash    string         `json:"prefix_hash,omitempty"`
	CacheShape    map[string]any `json:"cache_shape,omitempty"`
	PromptTokens  int            `json:"prompt_tokens,omitempty"`
	CachedTokens  int            `json:"cached_tokens,omitempty"`
	CacheHitRatio float64        `json:"cache_hit_ratio,omitempty"`
	Error         string         `json:"error,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
