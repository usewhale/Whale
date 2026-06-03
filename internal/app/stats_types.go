package app

import (
	"github.com/usewhale/whale/internal/telemetry"
	"time"
)

const statsRecentLimit = 5
const statsProfileSessionLimit = 50
const statsProfileToolHeavyChars = 12_000
const statsProfileInsightLimit = 5

type usageStats struct {
	Turns                    int
	Sessions                 map[string]bool
	PromptTokens             int
	CompletionTokens         int
	CacheHit                 int
	CacheMiss                int
	PrefixCompletionRequests int
	ReasoningReplayTokens    int
	CostUSD                  float64
	Last7CostUSD             float64
	CacheSavingsUSD          float64
	SubagentTurns            int
	SubagentCostUSD          float64
	SubagentPromptTokens     int
	SubagentOutputTokens     int
	Buckets                  []usageBucketStats
	ByModel                  map[string]*usageModelStats
	Recent                   []telemetry.UsageRecord
}

type usageBucketStats struct {
	Label                    string
	Cutoff                   time.Time
	Turns                    int
	PromptTokens             int
	CompletionTokens         int
	CacheHit                 int
	CacheMiss                int
	PrefixCompletionRequests int
	CostUSD                  float64
	CacheSavingsUSD          float64
	ReasoningReplay          int
	SubagentTurns            int
	SubagentCostUSD          float64
	SubagentTokens           int
}

type usageModelStats struct {
	Model                    string
	Turns                    int
	Tokens                   int
	CostUSD                  float64
	CacheHit                 int
	CacheMiss                int
	PrefixCompletionRequests int
	PromptTokens             int
	CompletionTokens         int
	ReasoningReplayTokens    int
}

type toolInputStats struct {
	Repaired     int
	Invalid      int
	ByRepairKind map[string]int
	ByTool       map[string]*toolInputToolStats
	ByModel      map[string]*toolInputModelStats
	ByErrorCode  map[string]int
	Recent       []telemetry.ToolInputEvent
}

type toolInputToolStats struct {
	Tool     string
	Repaired int
	Invalid  int
}

type toolInputModelStats struct {
	Model    string
	Repaired int
	Invalid  int
}

type profileStats struct {
	Limit                          int
	Sessions                       []profileSessionStats
	MainWorkSessions               int
	TrivialSessions                int
	ToolHeavySessions              int
	SubagentSessions               int
	PromptTokens                   int
	CompletionTokens               int
	CacheHit                       int
	CacheMiss                      int
	CostUSD                        float64
	MaxPromptTokens                int
	SubagentPromptTokens           int
	SubagentCompletionTokens       int
	SubagentCacheHit               int
	SubagentCacheMiss              int
	ReasoningReplayTokens          int
	SubagentReasoningReplay        int
	ToolResultRawChars             int
	ToolResultReplayChars          int
	ToolResultRawTokens            int
	ToolResultReplayTokens         int
	ToolResultTokensSaved          int
	ToolResultsCompacted           int
	SubagentToolResultRawChars     int
	SubagentToolResultReplayChars  int
	SubagentToolResultRawTokens    int
	SubagentToolResultReplayTokens int
	SubagentToolResultTokensSaved  int
	SubagentToolResultsCompacted   int
	SubagentCostUSD                float64
	SubagentMaxPromptTokens        int
	PrefixFingerprints             map[string]bool
	ProviderPrefixHashes           map[string]bool
	PrefixShapeSessions            map[string]bool
	ToolCalls                      int
	ToolResultChars                int
	ApprovalPrompts                int
	ApprovalAllowedOnce            int
	ApprovalAllowedForSession      int
	ApprovalDenied                 int
	ApprovalCanceled               int
	ApprovalReused                 int
	ApprovalPolicyBlocks           int
	ApprovalModeBlocks             int
	ApprovalAuditEvents            int
	ReasoningChars                 int
	VisibleTextChars               int
	ByTool                         map[string]*profileToolStats
	TopSessions                    []profileSessionStats
	UsageMatchedSessions           int
	Insights                       []profileInsight
}

type profileInsight struct {
	Kind      string
	SessionID string
	Detail    string
}

type profileSessionStats struct {
	ID                             string
	ModTime                        time.Time
	Messages                       int
	UserMessages                   int
	AssistantMessages              int
	ToolMessages                   int
	ToolCalls                      int
	ToolResultChars                int
	ApprovalPrompts                int
	ApprovalAllowedOnce            int
	ApprovalAllowedForSession      int
	ApprovalDenied                 int
	ApprovalCanceled               int
	ApprovalReused                 int
	ApprovalPolicyBlocks           int
	ApprovalModeBlocks             int
	ApprovalAuditEvents            int
	ReasoningChars                 int
	VisibleTextChars               int
	FirstUserText                  string
	HasHiddenUserTask              bool
	Trivial                        bool
	PromptTokens                   int
	CompletionTokens               int
	CacheHit                       int
	CacheMiss                      int
	CostUSD                        float64
	MaxPromptTokens                int
	SubagentSessions               int
	SubagentPromptTokens           int
	SubagentCompletionTokens       int
	SubagentCacheHit               int
	SubagentCacheMiss              int
	ReasoningReplayTokens          int
	SubagentReasoningReplay        int
	ToolResultRawChars             int
	ToolResultReplayChars          int
	ToolResultRawTokens            int
	ToolResultReplayTokens         int
	ToolResultTokensSaved          int
	ToolResultsCompacted           int
	SubagentToolResultRawChars     int
	SubagentToolResultReplayChars  int
	SubagentToolResultRawTokens    int
	SubagentToolResultReplayTokens int
	SubagentToolResultTokensSaved  int
	SubagentToolResultsCompacted   int
	SubagentCostUSD                float64
	SubagentMaxPromptTokens        int
	PrefixFingerprints             map[string]bool
	ProviderPrefixHashes           map[string]bool
	SystemHashes                   map[string]bool
	RuntimeHashes                  map[string]bool
	ToolsHashes                    map[string]bool
	RequestHashes                  map[string]bool
	ShapeSegments                  map[string]map[string]bool
	ByTool                         map[string]*profileToolStats
	approvalPromptKeys             map[string]bool
	approvalDecisionKeys           map[string]bool
}

type profileToolStats struct {
	Tool        string
	Calls       int
	ResultChars int
}

type sessionUsageSummary struct {
	Turns            int
	PromptTokens     int
	CompletionTokens int
	CacheHit         int
	CacheMiss        int
	CostUSD          float64
	CacheSavingsUSD  float64
	LastPromptTokens int
	LastTS           int64
	SubagentTurns    int
	SubagentTokens   int
	SubagentCostUSD  float64
}
