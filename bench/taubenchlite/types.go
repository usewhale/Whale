package main

import "time"

const retailSystemPrompt = `You are a retail support agent. Use the tools to help the user.
Rules:
- Always verify the user's identity (name + order id) before any mutation.
- Never invent order ids or emails.
- If a request is outside your tools, say so honestly.
- Be concise.`

type cliArgs struct {
	taskFilter  string
	mode        string
	repeats     int
	model       string
	userModel   string
	effort      string
	outDir      string
	timeout     time.Duration
	transcripts bool
	verbose     bool
	dry         bool
}

type benchMeta struct {
	Date           string   `json:"date"`
	Model          string   `json:"model"`
	UserModel      string   `json:"user_model"`
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
	Mode                    string   `json:"mode"`
	TaskID                  string   `json:"task_id"`
	Repeat                  int      `json:"repeat"`
	Pass                    bool     `json:"pass"`
	Turns                   int      `json:"turns"`
	ToolCalls               int      `json:"tool_calls"`
	PromptTokens            int      `json:"prompt_tokens"`
	CompletionTokens        int      `json:"completion_tokens"`
	CacheHitTokens          int      `json:"prompt_cache_hit_tokens"`
	CacheMissTokens         int      `json:"prompt_cache_miss_tokens"`
	CacheHitRatio           float64  `json:"cache_hit_ratio"`
	WarmCacheHitTokens      int      `json:"warm_prompt_cache_hit_tokens"`
	WarmCacheMissTokens     int      `json:"warm_prompt_cache_miss_tokens"`
	WarmCacheHitRatio       float64  `json:"warm_cache_hit_ratio"`
	CostUSD                 float64  `json:"cost_usd"`
	CacheSavingsUSD         float64  `json:"cache_savings_usd"`
	UncachedCostUSD         float64  `json:"uncached_equivalent_cost_usd"`
	PrefixFingerprints      int      `json:"immutable_prefix_fingerprints"`
	PrefixFingerprintValues []string `json:"immutable_prefix_fingerprint_values,omitempty"`
	ShapePrefixHashes       int      `json:"shape_prefix_hashes"`
	ShapePrefixHashValues   []string `json:"shape_prefix_hash_values,omitempty"`
	ShapeRuntimeHashes      int      `json:"shape_runtime_hashes"`
	ShapeRuntimeHashValues  []string `json:"shape_runtime_hash_values,omitempty"`
	ShapeToolsHashes        int      `json:"shape_tools_hashes"`
	ShapeToolsHashValues    []string `json:"shape_tools_hash_values,omitempty"`
	ShapeRequestHashes      int      `json:"shape_request_hashes"`
	ShapeRequestHashValues  []string `json:"shape_request_hash_values,omitempty"`
	Truncated               bool     `json:"truncated"`
	FinalOutput             string   `json:"final_output,omitempty"`
	Error                   string   `json:"error,omitempty"`
	DurationMS              int64    `json:"duration_ms"`
	TranscriptPath          string   `json:"transcript_path,omitempty"`
}

type usageTotals struct {
	PromptTokens        int
	CompletionTokens    int
	CacheHitTokens      int
	CacheMissTokens     int
	WarmCacheHitTokens  int
	WarmCacheMissTokens int
	CostUSD             float64
	CacheSavingsUSD     float64
	PrefixFingerprints  map[string]bool
	ShapePrefixHashes   map[string]bool
	ShapeRuntimeHashes  map[string]bool
	ShapeToolsHashes    map[string]bool
	ShapeRequestHashes  map[string]bool
}

func (u usageTotals) CacheHitRatio() float64 {
	denom := u.CacheHitTokens + u.CacheMissTokens
	if denom <= 0 {
		return 0
	}
	return float64(u.CacheHitTokens) / float64(denom)
}

func (u usageTotals) WarmCacheHitRatio() float64 {
	denom := u.WarmCacheHitTokens + u.WarmCacheMissTokens
	if denom <= 0 {
		return 0
	}
	return float64(u.WarmCacheHitTokens) / float64(denom)
}

type taskSpec struct {
	ID          string
	Description string
	User        userPersona
	InitialDB   worldState
	MaxTurns    int
	Check       func(runCheckContext) bool
}

type userPersona struct {
	Style  string
	Goal   string
	Knowns map[string]string
}

type runCheckContext struct {
	DB                worldState
	FinalAgentMessage string
	Transcript        []turn
}

type turn struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ToolName string `json:"tool_name,omitempty"`
}

type transcriptRecord struct {
	TS            string         `json:"ts"`
	Turn          int            `json:"turn,omitempty"`
	Role          string         `json:"role,omitempty"`
	Event         string         `json:"event,omitempty"`
	Content       string         `json:"content,omitempty"`
	Tool          string         `json:"tool,omitempty"`
	Args          string         `json:"args,omitempty"`
	Success       *bool          `json:"success,omitempty"`
	Model         string         `json:"model,omitempty"`
	PrefixHash    string         `json:"prefix_hash,omitempty"`
	PromptTokens  int            `json:"prompt_tokens,omitempty"`
	CachedTokens  int            `json:"cached_tokens,omitempty"`
	CacheHitRatio float64        `json:"cache_hit_ratio,omitempty"`
	Error         string         `json:"error,omitempty"`
	Usage         map[string]int `json:"usage,omitempty"`
	CostUSD       float64        `json:"cost_usd,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
