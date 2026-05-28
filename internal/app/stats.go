package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/telemetry"
)

const statsRecentLimit = 5
const statsProfileSessionLimit = 50
const statsProfileToolHeavyChars = 12_000
const statsProfileInsightLimit = 5

type usageStats struct {
	Turns                 int
	Sessions              map[string]bool
	PromptTokens          int
	CompletionTokens      int
	CacheHit              int
	CacheMiss             int
	ReasoningReplayTokens int
	CostUSD               float64
	Last7CostUSD          float64
	CacheSavingsUSD       float64
	SubagentTurns         int
	SubagentCostUSD       float64
	SubagentPromptTokens  int
	SubagentOutputTokens  int
	Buckets               []usageBucketStats
	ByModel               map[string]*usageModelStats
	Recent                []telemetry.UsageRecord
}

type usageBucketStats struct {
	Label            string
	Cutoff           time.Time
	Turns            int
	PromptTokens     int
	CompletionTokens int
	CacheHit         int
	CacheMiss        int
	CostUSD          float64
	CacheSavingsUSD  float64
	ReasoningReplay  int
	SubagentTurns    int
	SubagentCostUSD  float64
	SubagentTokens   int
}

type usageModelStats struct {
	Model                 string
	Turns                 int
	Tokens                int
	CostUSD               float64
	CacheHit              int
	CacheMiss             int
	PromptTokens          int
	CompletionTokens      int
	ReasoningReplayTokens int
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
	ToolCalls                      int
	ToolResultChars                int
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
	ByTool                         map[string]*profileToolStats
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

func (a *App) buildStats() string {
	return a.buildStatsViewAt("overview", time.Now())
}

func (a *App) buildStatsView(view string) string {
	return a.buildStatsViewAt(view, time.Now())
}

func (a *App) buildStatsViewAt(view string, now time.Time) string {
	usage := readUsageStats(filepath.Join(a.cfg.DataDir, "usage.jsonl"), now)
	toolInput := readToolInputStats(a.sessionsDir)

	var lines []string
	switch view {
	case "usage":
		lines = []string{"Stats", "", "Usage"}
		lines = append(lines, formatUsageStats(usage)...)
	case "tools", "repair":
		lines = []string{"Stats", "", "Tool input"}
		lines = append(lines, formatToolInputStats(toolInput)...)
	case "recent":
		lines = []string{"Stats"}
		lines = append(lines, formatRecentStats(usage, toolInput)...)
	case "profile":
		profile := readProfileStats(a.sessionsDir, filepath.Join(a.cfg.DataDir, "usage.jsonl"), statsProfileSessionLimit)
		lines = []string{"Stats", "", "Profile"}
		lines = append(lines, formatProfileStats(profile)...)
	case "all":
		lines = []string{"Stats", "", "Usage"}
		lines = append(lines, formatUsageStats(usage)...)
		lines = append(lines, "", "Tool input")
		lines = append(lines, formatToolInputStats(toolInput)...)
		lines = append(lines, formatRecentStats(usage, toolInput)...)
	default:
		lines = []string{"Stats"}
		lines = append(lines, formatStatsOverview(usage, toolInput)...)
	}
	return strings.Join(lines, "\n")
}

func readUsageStats(path string, now time.Time) usageStats {
	stats := usageStats{
		Sessions: map[string]bool{},
		ByModel:  map[string]*usageModelStats{},
		Buckets:  usageBuckets(now),
	}
	f, err := os.Open(path)
	if err != nil {
		return stats
	}
	defer f.Close()

	cutoff := now.Add(-7 * 24 * time.Hour).UnixMilli()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.UsageRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if !isSupportedUsageModel(rec.Model) {
			continue
		}
		stats.Turns++
		if rec.Session != "" {
			stats.Sessions[rec.Session] = true
		}
		stats.PromptTokens += rec.PromptTokens
		stats.CompletionTokens += rec.CompletionTokens
		stats.CacheHit += rec.PromptCacheHit
		stats.CacheMiss += rec.PromptCacheMiss
		stats.ReasoningReplayTokens += rec.ReasoningReplayTok
		cost := telemetry.EstimateUsageRecordUSD(rec)
		cacheSavings := telemetry.EstimateUsageRecordCacheSavingsUSD(rec)
		stats.CostUSD += cost
		stats.CacheSavingsUSD += cacheSavings
		if rec.TS >= cutoff {
			stats.Last7CostUSD += cost
		}
		if isUsageSubagentRecord(rec) {
			stats.SubagentTurns++
			stats.SubagentCostUSD += cost
			stats.SubagentPromptTokens += rec.PromptTokens
			stats.SubagentOutputTokens += rec.CompletionTokens
		}
		addUsageBuckets(stats.Buckets, rec, cost, cacheSavings)
		model := strings.TrimSpace(rec.Model)
		if model == "" {
			model = "(unknown)"
		}
		ms := stats.ByModel[model]
		if ms == nil {
			ms = &usageModelStats{Model: model}
			stats.ByModel[model] = ms
		}
		ms.Turns++
		ms.PromptTokens += rec.PromptTokens
		ms.CompletionTokens += rec.CompletionTokens
		ms.Tokens += rec.PromptTokens + rec.CompletionTokens
		ms.ReasoningReplayTokens += rec.ReasoningReplayTok
		ms.CostUSD += cost
		ms.CacheHit += rec.PromptCacheHit
		ms.CacheMiss += rec.PromptCacheMiss
		rec.CostUSD = cost
		stats.Recent = appendRecentUsage(stats.Recent, rec)
	}
	return stats
}

func readSessionUsageSummary(path, sessionID string) sessionUsageSummary {
	var out sessionUsageSummary
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.UsageRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if !isSupportedUsageModel(rec.Model) {
			continue
		}
		isMain := rec.Session == sessionID && !strings.EqualFold(strings.TrimSpace(rec.Kind), "subagent")
		isSubagent := strings.EqualFold(strings.TrimSpace(rec.Kind), "subagent") && strings.TrimSpace(rec.ParentSessionID) == sessionID
		if !isSubagent && strings.HasPrefix(rec.Session, sessionID+"--subagent-") {
			isSubagent = true
		}
		if !isMain && !isSubagent {
			continue
		}
		cost := telemetry.EstimateUsageRecordUSD(rec)
		cacheSavings := telemetry.EstimateUsageRecordCacheSavingsUSD(rec)
		out.Turns++
		out.PromptTokens += rec.PromptTokens
		out.CompletionTokens += rec.CompletionTokens
		out.CacheHit += rec.PromptCacheHit
		out.CacheMiss += rec.PromptCacheMiss
		out.CostUSD += cost
		out.CacheSavingsUSD += cacheSavings
		if rec.TS >= out.LastTS {
			out.LastTS = rec.TS
			out.LastPromptTokens = rec.PromptTokens
		}
		if isSubagent {
			out.SubagentTurns++
			out.SubagentTokens += rec.PromptTokens + rec.CompletionTokens
			out.SubagentCostUSD += cost
		}
	}
	return out
}

func formatSessionUsageSummary(summary sessionUsageSummary) string {
	if summary.Turns == 0 {
		return "none"
	}
	parts := []string{
		fmt.Sprintf("%d turns", summary.Turns),
		fmt.Sprintf("$%.4f", summary.CostUSD),
		fmt.Sprintf("%.1f%% cache", ratioPercent(summary.CacheHit, summary.CacheHit+summary.CacheMiss)),
		fmt.Sprintf("last prompt %s", formatCount(summary.LastPromptTokens)),
	}
	if summary.CacheSavingsUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f cache saved", summary.CacheSavingsUSD))
	}
	if summary.SubagentTurns > 0 {
		parts = append(parts, fmt.Sprintf("subagents %d turns/%s/$%.4f", summary.SubagentTurns, formatCount(summary.SubagentTokens), summary.SubagentCostUSD))
	}
	return strings.Join(parts, " · ")
}

func usageBuckets(now time.Time) []usageBucketStats {
	return []usageBucketStats{
		{Label: "24h", Cutoff: now.Add(-24 * time.Hour)},
		{Label: "7d", Cutoff: now.Add(-7 * 24 * time.Hour)},
		{Label: "30d", Cutoff: now.Add(-30 * 24 * time.Hour)},
		{Label: "all-time"},
	}
}

func addUsageBuckets(buckets []usageBucketStats, rec telemetry.UsageRecord, cost, cacheSavings float64) {
	for i := range buckets {
		if !buckets[i].Cutoff.IsZero() && rec.TS < buckets[i].Cutoff.UnixMilli() {
			continue
		}
		buckets[i].Turns++
		buckets[i].PromptTokens += rec.PromptTokens
		buckets[i].CompletionTokens += rec.CompletionTokens
		buckets[i].CacheHit += rec.PromptCacheHit
		buckets[i].CacheMiss += rec.PromptCacheMiss
		buckets[i].CostUSD += cost
		buckets[i].CacheSavingsUSD += cacheSavings
		buckets[i].ReasoningReplay += rec.ReasoningReplayTok
		if isUsageSubagentRecord(rec) {
			buckets[i].SubagentTurns++
			buckets[i].SubagentCostUSD += cost
			buckets[i].SubagentTokens += rec.PromptTokens + rec.CompletionTokens
		}
	}
}

func isUsageSubagentRecord(rec telemetry.UsageRecord) bool {
	if strings.EqualFold(strings.TrimSpace(rec.Kind), "subagent") {
		return true
	}
	return isLegacyUsageSubagentSessionID(rec.Session)
}

func isLegacyUsageSubagentSessionID(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	return strings.Contains(sessionID, "--subagent-") || strings.HasPrefix(sessionID, "subagent-")
}

func readProfileStats(sessionsDir, usagePath string, limit int) profileStats {
	if limit <= 0 {
		limit = statsProfileSessionLimit
	}
	stats := profileStats{
		Limit:              limit,
		PrefixFingerprints: map[string]bool{},
		ByTool:             map[string]*profileToolStats{},
	}
	files := latestProfileSessionFiles(sessionsDir, limit)
	sessionIndex := map[string]int{}
	for _, file := range files {
		sp := readProfileSessionFile(file.Path, file.ID, file.ModTime)
		stats.Sessions = append(stats.Sessions, sp)
		sessionIndex[sp.ID] = len(stats.Sessions) - 1
		stats.ToolCalls += sp.ToolCalls
		stats.ToolResultChars += sp.ToolResultChars
		stats.ReasoningChars += sp.ReasoningChars
		stats.VisibleTextChars += sp.VisibleTextChars
		for _, tool := range sp.ByTool {
			dst := stats.ByTool[tool.Tool]
			if dst == nil {
				dst = &profileToolStats{Tool: tool.Tool}
				stats.ByTool[tool.Tool] = dst
			}
			dst.Calls += tool.Calls
			dst.ResultChars += tool.ResultChars
		}
		if sp.Trivial {
			stats.TrivialSessions++
		}
		if sp.ToolResultChars >= statsProfileToolHeavyChars {
			stats.ToolHeavySessions++
		}
	}

	childSessionIndex := profileChildSessionIndex(sessionsDir, sessionIndex)
	for _, parentIdx := range childSessionIndex {
		stats.Sessions[parentIdx].SubagentSessions++
		stats.SubagentSessions++
	}

	for _, path := range profileUsagePaths(usagePath) {
		readProfileUsage(path, sessionIndex, childSessionIndex, &stats)
	}
	for _, sp := range stats.Sessions {
		for fp := range sp.PrefixFingerprints {
			stats.PrefixFingerprints[fp] = true
		}
		if sp.MaxPromptTokens > stats.MaxPromptTokens {
			stats.MaxPromptTokens = sp.MaxPromptTokens
		}
		stats.PromptTokens += sp.PromptTokens
		stats.CompletionTokens += sp.CompletionTokens
		stats.CacheHit += sp.CacheHit
		stats.CacheMiss += sp.CacheMiss
		stats.CostUSD += sp.CostUSD
		if sp.PromptTokens > 0 || sp.CompletionTokens > 0 || sp.CostUSD > 0 {
			stats.UsageMatchedSessions++
		}
		if sp.SubagentMaxPromptTokens > stats.SubagentMaxPromptTokens {
			stats.SubagentMaxPromptTokens = sp.SubagentMaxPromptTokens
		}
		stats.SubagentPromptTokens += sp.SubagentPromptTokens
		stats.SubagentCompletionTokens += sp.SubagentCompletionTokens
		stats.SubagentCacheHit += sp.SubagentCacheHit
		stats.SubagentCacheMiss += sp.SubagentCacheMiss
		stats.SubagentCostUSD += sp.SubagentCostUSD
	}
	stats.MainWorkSessions = len(stats.Sessions) - stats.TrivialSessions
	stats.TopSessions = topProfileSessions(stats.Sessions, statsRecentLimit, false)
	stats.Insights = buildProfileInsights(stats.Sessions, statsProfileInsightLimit)
	return stats
}

type profileSessionFile struct {
	ID      string
	Path    string
	ModTime time.Time
}

func latestProfileSessionFiles(sessionsDir string, limit int) []profileSessionFile {
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return nil
	}
	files := make([]profileSessionFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isProfileSessionJSONL(entry.Name()) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if isSubagentSession(sessionsDir, id) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, profileSessionFile{
			ID:      id,
			Path:    filepath.Join(sessionsDir, entry.Name()),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].ID > files[j].ID
		}
		return files[i].ModTime.After(files[j].ModTime)
	})
	return limitSlice(files, limit)
}

func isProfileSessionJSONL(name string) bool {
	if !strings.HasSuffix(name, ".jsonl") ||
		strings.HasSuffix(name, telemetry.ToolInputEventsSuffix) ||
		strings.HasSuffix(name, telemetry.ApprovalEventsSuffix) {
		return false
	}
	id := strings.TrimSuffix(name, ".jsonl")
	return !strings.Contains(id, "--subagent-") && !strings.HasPrefix(id, "e2e-") && !strings.HasPrefix(id, "rt-")
}

func isSubagentSession(sessionsDir, id string) bool {
	meta, err := session.LoadSessionMeta(sessionsDir, id)
	return err == nil && strings.EqualFold(strings.TrimSpace(meta.Kind), "subagent")
}

func profileChildSessionIndex(sessionsDir string, parentIndex map[string]int) map[string]int {
	out := map[string]int{}
	if len(parentIndex) == 0 {
		return out
	}
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".jsonl") ||
			strings.HasSuffix(entry.Name(), telemetry.ToolInputEventsSuffix) ||
			strings.HasSuffix(entry.Name(), telemetry.ApprovalEventsSuffix) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		parentID := ""
		if meta, err := session.LoadSessionMeta(sessionsDir, id); err == nil && strings.EqualFold(strings.TrimSpace(meta.Kind), "subagent") {
			parentID = strings.TrimSpace(meta.ParentSessionID)
		}
		if parentID == "" {
			if before, _, ok := strings.Cut(id, "--subagent-"); ok {
				parentID = before
			}
		}
		if parentID == "" || parentID == id {
			continue
		}
		if idx, ok := parentIndex[parentID]; ok {
			out[id] = idx
		}
	}
	return out
}

func readProfileSessionFile(path, id string, modTime time.Time) profileSessionStats {
	stats := profileSessionStats{
		ID:                 id,
		ModTime:            modTime,
		PrefixFingerprints: map[string]bool{},
		ByTool:             map[string]*profileToolStats{},
	}
	f, err := os.Open(path)
	if err != nil {
		return stats
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	var messages []core.Message
	for scanner.Scan() {
		var msg core.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
		stats.Messages++
		stats.VisibleTextChars += len(msg.Text)
		stats.ReasoningChars += len(msg.Reasoning)
		if msg.Role == core.RoleUser {
			stats.UserMessages++
			if msg.Hidden && strings.TrimSpace(msg.Text) != "" {
				stats.HasHiddenUserTask = true
			}
			if stats.FirstUserText == "" && !msg.Hidden {
				stats.FirstUserText = previewText(msg.Text, 80)
			}
		}
		if msg.Role == core.RoleAssistant {
			stats.AssistantMessages++
		}
		if msg.Role == core.RoleTool {
			stats.ToolMessages++
		}
		stats.ToolCalls += len(msg.ToolCalls)
		for _, tc := range msg.ToolCalls {
			addProfileToolCall(stats.ByTool, tc.Name)
		}
		for _, tr := range msg.ToolResults {
			stats.ToolResultChars += len(tr.Content)
			addProfileToolResult(stats.ByTool, tr.Name, len(tr.Content))
		}
	}
	if stats.HasHiddenUserTask && isTrivialProfileUserText(stats.FirstUserText) {
		stats.FirstUserText = "(hidden user task)"
	}
	stats.Trivial = isTrivialProfileSession(messages)
	return stats
}

func readProfileUsage(path string, sessionIndex map[string]int, childSessionIndex map[string]int, stats *profileStats) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.UsageRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if !isSupportedUsageModel(rec.Model) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(rec.Kind), "subagent") && strings.TrimSpace(rec.ParentSessionID) != "" {
			if idx, ok := sessionIndex[strings.TrimSpace(rec.ParentSessionID)]; ok {
				addProfileSubagentUsage(&stats.Sessions[idx], stats, rec)
				continue
			}
		}
		if idx, ok := sessionIndex[rec.Session]; ok {
			cost := telemetry.EstimateUsageRecordUSD(rec)
			sp := &stats.Sessions[idx]
			sp.PromptTokens += rec.PromptTokens
			sp.CompletionTokens += rec.CompletionTokens
			sp.CacheHit += rec.PromptCacheHit
			sp.CacheMiss += rec.PromptCacheMiss
			sp.ReasoningReplayTokens += rec.ReasoningReplayTok
			stats.ReasoningReplayTokens += rec.ReasoningReplayTok
			sp.ToolResultRawChars += rec.ToolResultRawChars
			sp.ToolResultReplayChars += rec.ToolResultReplayChars
			sp.ToolResultRawTokens += rec.ToolResultRawTokens
			sp.ToolResultReplayTokens += rec.ToolResultReplayTokens
			sp.ToolResultTokensSaved += rec.ToolResultTokensSaved
			sp.ToolResultsCompacted += rec.ToolResultsCompacted
			stats.ToolResultRawChars += rec.ToolResultRawChars
			stats.ToolResultReplayChars += rec.ToolResultReplayChars
			stats.ToolResultRawTokens += rec.ToolResultRawTokens
			stats.ToolResultReplayTokens += rec.ToolResultReplayTokens
			stats.ToolResultTokensSaved += rec.ToolResultTokensSaved
			stats.ToolResultsCompacted += rec.ToolResultsCompacted
			sp.CostUSD += cost
			if rec.PromptTokens > sp.MaxPromptTokens {
				sp.MaxPromptTokens = rec.PromptTokens
			}
			if fp := strings.TrimSpace(rec.PrefixFingerprint); fp != "" {
				sp.PrefixFingerprints[fp] = true
			}
			continue
		}
		if idx, ok := childSessionIndex[rec.Session]; ok {
			addProfileSubagentUsage(&stats.Sessions[idx], stats, rec)
		}
	}
}

func addProfileSubagentUsage(sp *profileSessionStats, stats *profileStats, rec telemetry.UsageRecord) {
	cost := telemetry.EstimateUsageRecordUSD(rec)
	sp.SubagentPromptTokens += rec.PromptTokens
	sp.SubagentCompletionTokens += rec.CompletionTokens
	sp.SubagentCacheHit += rec.PromptCacheHit
	sp.SubagentCacheMiss += rec.PromptCacheMiss
	sp.SubagentReasoningReplay += rec.ReasoningReplayTok
	stats.SubagentReasoningReplay += rec.ReasoningReplayTok
	sp.SubagentToolResultRawChars += rec.ToolResultRawChars
	sp.SubagentToolResultReplayChars += rec.ToolResultReplayChars
	sp.SubagentToolResultRawTokens += rec.ToolResultRawTokens
	sp.SubagentToolResultReplayTokens += rec.ToolResultReplayTokens
	sp.SubagentToolResultTokensSaved += rec.ToolResultTokensSaved
	sp.SubagentToolResultsCompacted += rec.ToolResultsCompacted
	stats.SubagentToolResultRawChars += rec.ToolResultRawChars
	stats.SubagentToolResultReplayChars += rec.ToolResultReplayChars
	stats.SubagentToolResultRawTokens += rec.ToolResultRawTokens
	stats.SubagentToolResultReplayTokens += rec.ToolResultReplayTokens
	stats.SubagentToolResultTokensSaved += rec.ToolResultTokensSaved
	stats.SubagentToolResultsCompacted += rec.ToolResultsCompacted
	sp.SubagentCostUSD += cost
	if rec.PromptTokens > sp.SubagentMaxPromptTokens {
		sp.SubagentMaxPromptTokens = rec.PromptTokens
	}
}

func profileUsagePaths(primary string) []string {
	paths := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, path := range []string{primary, telemetry.DefaultUsageLogPath()} {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		key := filepath.Clean(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		paths = append(paths, path)
	}
	return paths
}

func isTrivialProfileSession(messages []core.Message) bool {
	visible := make([]core.Message, 0, len(messages))
	for _, msg := range messages {
		if !msg.Hidden {
			visible = append(visible, msg)
		}
		if strings.TrimSpace(msg.Reasoning) != "" || len(msg.ToolCalls)+len(msg.ToolResults) > 0 {
			return false
		}
		if msg.Role == core.RoleTool {
			return false
		}
	}
	if len(visible) == 0 || len(visible) > 2 {
		return false
	}
	user := visible[0]
	if user.Role != core.RoleUser {
		return false
	}
	if len(visible) == 2 {
		assistant := visible[1]
		if assistant.Role != core.RoleAssistant || len([]rune(strings.TrimSpace(assistant.Text))) > 200 {
			return false
		}
	}
	return isTrivialProfileUserText(user.Text)
}

func isTrivialProfileUserText(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(text, "/") {
		return true
	}
	switch text {
	case "hi", "hello", "hey", "几点了", "现在几点", "现在几点了", "what time is it", "time":
		return true
	default:
		return false
	}
}

func addProfileToolCall(stats map[string]*profileToolStats, tool string) {
	tool = nonEmpty(tool, "(unknown)")
	ts := stats[tool]
	if ts == nil {
		ts = &profileToolStats{Tool: tool}
		stats[tool] = ts
	}
	ts.Calls++
}

func addProfileToolResult(stats map[string]*profileToolStats, tool string, resultChars int) {
	tool = nonEmpty(tool, "(unknown)")
	ts := stats[tool]
	if ts == nil {
		ts = &profileToolStats{Tool: tool}
		stats[tool] = ts
	}
	ts.ResultChars += resultChars
}

func readToolInputStats(sessionsDir string) toolInputStats {
	stats := toolInputStats{
		ByRepairKind: map[string]int{},
		ByTool:       map[string]*toolInputToolStats{},
		ByModel:      map[string]*toolInputModelStats{},
		ByErrorCode:  map[string]int{},
	}
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return stats
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), telemetry.ToolInputEventsSuffix) {
			continue
		}
		readToolInputEventFile(filepath.Join(sessionsDir, entry.Name()), &stats)
	}
	return stats
}

func readToolInputEventFile(path string, stats *toolInputStats) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.ToolInputEvent
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		switch rec.Event {
		case "tool_input_repaired":
			stats.Repaired++
			if rec.RepairKind != "" {
				stats.ByRepairKind[rec.RepairKind]++
			}
			updateToolInputToolStats(stats, rec.Tool, true)
			updateToolInputModelStats(stats, rec.Model, true)
		case "tool_input_invalid":
			stats.Invalid++
			if rec.ErrorCode != "" {
				stats.ByErrorCode[rec.ErrorCode]++
			}
			updateToolInputToolStats(stats, rec.Tool, false)
			updateToolInputModelStats(stats, rec.Model, false)
		default:
			continue
		}
		stats.Recent = appendRecentToolInput(stats.Recent, rec)
	}
}

func updateToolInputToolStats(stats *toolInputStats, tool string, repaired bool) {
	tool = nonEmpty(tool, "(unknown)")
	ts := stats.ByTool[tool]
	if ts == nil {
		ts = &toolInputToolStats{Tool: tool}
		stats.ByTool[tool] = ts
	}
	if repaired {
		ts.Repaired++
	} else {
		ts.Invalid++
	}
}

func updateToolInputModelStats(stats *toolInputStats, model string, repaired bool) {
	model = nonEmpty(model, "(unknown)")
	ms := stats.ByModel[model]
	if ms == nil {
		ms = &toolInputModelStats{Model: model}
		stats.ByModel[model] = ms
	}
	if repaired {
		ms.Repaired++
	} else {
		ms.Invalid++
	}
}

func formatUsageStats(stats usageStats) []string {
	totalTokens := stats.PromptTokens + stats.CompletionTokens
	lines := []string{
		fmt.Sprintf("- turns: %d", stats.Turns),
		fmt.Sprintf("- sessions: %d", len(stats.Sessions)),
		fmt.Sprintf("- tokens: %s total · %s input · %s output", formatCount(totalTokens), formatCount(stats.PromptTokens), formatCount(stats.CompletionTokens)),
		fmt.Sprintf("- cache: %s hit · %s miss · %.1f%%", formatCount(stats.CacheHit), formatCount(stats.CacheMiss), ratioPercent(stats.CacheHit, stats.CacheHit+stats.CacheMiss)),
		fmt.Sprintf("- estimated cost: $%.4f total · $%.4f last 7d · $%.4f cache saved", stats.CostUSD, stats.Last7CostUSD, stats.CacheSavingsUSD),
	}
	if stats.ReasoningReplayTokens > 0 {
		lines = append(lines, fmt.Sprintf("- reasoning replay: %s tokens · %.1f%% of input", formatCount(stats.ReasoningReplayTokens), ratioPercent(stats.ReasoningReplayTokens, stats.PromptTokens)))
	}
	if stats.SubagentTurns > 0 {
		lines = append(lines, fmt.Sprintf("- subagents: %d turns · %s tokens · $%.4f", stats.SubagentTurns, formatCount(stats.SubagentPromptTokens+stats.SubagentOutputTokens), stats.SubagentCostUSD))
	}
	if len(stats.Buckets) > 0 {
		lines = append(lines, "", "By window")
		for _, b := range stats.Buckets {
			subagentDetail := ""
			if b.SubagentTurns > 0 {
				subagentDetail = fmt.Sprintf(" · subagents %d/$%.4f", b.SubagentTurns, b.SubagentCostUSD)
			}
			lines = append(lines, fmt.Sprintf("- %s: %d turns · %s tokens · %.1f%% cache · $%.4f cost · $%.4f cache saved%s", b.Label, b.Turns, formatCount(b.PromptTokens+b.CompletionTokens), ratioPercent(b.CacheHit, b.CacheHit+b.CacheMiss), b.CostUSD, b.CacheSavingsUSD, subagentDetail))
		}
	}
	if len(stats.ByModel) > 0 {
		lines = append(lines, "", "By model")
		for _, ms := range topUsageModels(stats.ByModel, statsRecentLimit) {
			replayDetail := ""
			if ms.ReasoningReplayTokens > 0 {
				replayDetail = fmt.Sprintf(" · %s reasoning replay", formatCount(ms.ReasoningReplayTokens))
			}
			lines = append(lines, fmt.Sprintf("- %s: %d turns · %s tokens%s · %.1f%% cache · $%.4f", ms.Model, ms.Turns, formatCount(ms.Tokens), replayDetail, ratioPercent(ms.CacheHit, ms.CacheHit+ms.CacheMiss), ms.CostUSD))
		}
	}
	return lines
}

func isSupportedUsageModel(model string) bool {
	return defaults.IsSupportedModel(model)
}

func formatProfileStats(stats profileStats) []string {
	totalTokens := stats.PromptTokens + stats.CompletionTokens
	subagentTokens := stats.SubagentPromptTokens + stats.SubagentCompletionTokens
	allInTokens := totalTokens + subagentTokens
	allInReasoningReplay := stats.ReasoningReplayTokens + stats.SubagentReasoningReplay
	lines := []string{
		fmt.Sprintf("- scanned sessions: %d latest main sessions (limit %d)", len(stats.Sessions), stats.Limit),
		fmt.Sprintf("- main work sessions: %d", stats.MainWorkSessions),
		fmt.Sprintf("- trivial/local sessions: %d", stats.TrivialSessions),
		fmt.Sprintf("- tool-heavy sessions: %d (>= %s tool-result chars)", stats.ToolHeavySessions, formatCount(statsProfileToolHeavyChars)),
		fmt.Sprintf("- usage matched sessions: %d", stats.UsageMatchedSessions),
		fmt.Sprintf("- tokens: %s total · %s input · %s output", formatCount(totalTokens), formatCount(stats.PromptTokens), formatCount(stats.CompletionTokens)),
		fmt.Sprintf("- cache: %s hit · %s miss · %.1f%%", formatCount(stats.CacheHit), formatCount(stats.CacheMiss), ratioPercent(stats.CacheHit, stats.CacheHit+stats.CacheMiss)),
		fmt.Sprintf("- estimated cost: $%.4f", stats.CostUSD),
		fmt.Sprintf("- max prompt: %s", formatCount(stats.MaxPromptTokens)),
		fmt.Sprintf("- subagents: %d child sessions · %s total · %s input · %s output · $%.4f · max prompt %s · %.1f%% cache", stats.SubagentSessions, formatCount(subagentTokens), formatCount(stats.SubagentPromptTokens), formatCount(stats.SubagentCompletionTokens), stats.SubagentCostUSD, formatCount(stats.SubagentMaxPromptTokens), ratioPercent(stats.SubagentCacheHit, stats.SubagentCacheHit+stats.SubagentCacheMiss)),
		fmt.Sprintf("- all-in tokens: %s total · $%.4f", formatCount(allInTokens), stats.CostUSD+stats.SubagentCostUSD),
		fmt.Sprintf("- prefix fingerprints: %d", len(stats.PrefixFingerprints)),
		fmt.Sprintf("- tools: %d calls · %s result chars", stats.ToolCalls, formatCount(stats.ToolResultChars)),
		fmt.Sprintf("- reasoning/text: %s reasoning chars · %s visible text chars", formatCount(stats.ReasoningChars), formatCount(stats.VisibleTextChars)),
	}
	if allInReasoningReplay > 0 {
		lines = append(lines, fmt.Sprintf("- reasoning replay: %s main · %s subagent · %s all-in · %.1f%% of main input", formatCount(stats.ReasoningReplayTokens), formatCount(stats.SubagentReasoningReplay), formatCount(allInReasoningReplay), ratioPercent(stats.ReasoningReplayTokens, stats.PromptTokens)))
	}
	allInToolReplayTokens := stats.ToolResultReplayTokens + stats.SubagentToolResultReplayTokens
	allInToolRawTokens := stats.ToolResultRawTokens + stats.SubagentToolResultRawTokens
	allInToolSavedTokens := stats.ToolResultTokensSaved + stats.SubagentToolResultTokensSaved
	allInToolCompacted := stats.ToolResultsCompacted + stats.SubagentToolResultsCompacted
	if allInToolReplayTokens > 0 || allInToolRawTokens > 0 || allInToolSavedTokens > 0 || allInToolCompacted > 0 {
		lines = append(lines, fmt.Sprintf("- tool replay: %s sent · %s raw · %s saved · %d compacted", formatCount(allInToolReplayTokens), formatCount(allInToolRawTokens), formatCount(allInToolSavedTokens), allInToolCompacted))
	}
	if len(stats.Insights) > 0 {
		lines = append(lines, "", "Insights")
		for _, insight := range stats.Insights {
			lines = append(lines, fmt.Sprintf("- %s · %s: %s", insight.Kind, insight.SessionID, insight.Detail))
		}
	}
	if len(stats.ByTool) > 0 {
		lines = append(lines, "", "Top tools")
		for _, ts := range topProfileTools(stats.ByTool, statsRecentLimit) {
			lines = append(lines, fmt.Sprintf("- %s: %d calls · %s result chars", ts.Tool, ts.Calls, formatCount(ts.ResultChars)))
		}
	}
	if top := topToolReplaySessions(stats.Sessions, statsRecentLimit); len(top) > 0 {
		lines = append(lines, "", "Top tool replay sessions")
		for _, sp := range top {
			lines = append(lines, fmt.Sprintf("- %s: %s sent · %s raw · %s saved · %d compacted · %s", sp.ID, formatCount(profileSessionToolReplayTokens(sp)), formatCount(profileSessionToolRawTokens(sp)), formatCount(profileSessionToolSavedTokens(sp)), profileSessionToolCompacted(sp), nonEmpty(sp.FirstUserText, "(no user text)")))
		}
	}
	if top := topReasoningReplaySessions(stats.Sessions, statsRecentLimit); len(top) > 0 {
		lines = append(lines, "", "Top reasoning replay sessions")
		for _, sp := range top {
			childDetail := ""
			if sp.SubagentReasoningReplay > 0 {
				childDetail = fmt.Sprintf(" · +%s subagents", formatCount(sp.SubagentReasoningReplay))
			}
			replayTokens := profileSessionReasoningReplayTokens(sp)
			inputTokens := sp.PromptTokens + sp.SubagentPromptTokens
			lines = append(lines, fmt.Sprintf("- %s: %s tokens%s · %.1f%% of input · %s", sp.ID, formatCount(replayTokens), childDetail, ratioPercent(replayTokens, inputTokens), nonEmpty(sp.FirstUserText, "(no user text)")))
		}
	}
	if len(stats.TopSessions) > 0 {
		lines = append(lines, "", "Top work sessions")
		for _, sp := range stats.TopSessions {
			childDetail := ""
			if sp.SubagentSessions > 0 || sp.SubagentCostUSD > 0 || sp.SubagentPromptTokens+sp.SubagentCompletionTokens > 0 {
				childDetail = fmt.Sprintf(" · subagents %d · +$%.4f · +%s tokens", sp.SubagentSessions, sp.SubagentCostUSD, formatCount(sp.SubagentPromptTokens+sp.SubagentCompletionTokens))
			}
			lines = append(lines, fmt.Sprintf(
				"- %s: $%.4f%s · max prompt %s · tools %s chars · %.1f%% cache · %s",
				sp.ID,
				sp.CostUSD,
				childDetail,
				formatCount(sp.MaxPromptTokens),
				formatCount(sp.ToolResultChars),
				ratioPercent(sp.CacheHit, sp.CacheHit+sp.CacheMiss),
				nonEmpty(sp.FirstUserText, "(no user text)"),
			))
		}
	}
	return lines
}

func formatToolInputStats(stats toolInputStats) []string {
	total := stats.Repaired + stats.Invalid
	lines := []string{
		fmt.Sprintf("- repaired: %d", stats.Repaired),
		fmt.Sprintf("- invalid: %d", stats.Invalid),
		fmt.Sprintf("- repair rate: %.1f%%", ratioPercent(stats.Repaired, total)),
	}
	if len(stats.ByRepairKind) > 0 {
		lines = append(lines, "", "Repair kinds")
		for _, kv := range topCounts(stats.ByRepairKind, statsRecentLimit) {
			lines = append(lines, fmt.Sprintf("- %s: %d", kv.Key, kv.Value))
		}
	}
	if len(stats.ByErrorCode) > 0 {
		lines = append(lines, "", "Invalid codes")
		for _, kv := range topCounts(stats.ByErrorCode, statsRecentLimit) {
			lines = append(lines, fmt.Sprintf("- %s: %d", kv.Key, kv.Value))
		}
	}
	if len(stats.ByTool) > 0 {
		lines = append(lines, "", "Top tools")
		for _, ts := range topToolInputTools(stats.ByTool, statsRecentLimit) {
			lines = append(lines, fmt.Sprintf("- %s: %d repaired · %d invalid", ts.Tool, ts.Repaired, ts.Invalid))
		}
	}
	if len(stats.ByModel) > 0 {
		lines = append(lines, "", "By model")
		for _, ms := range topToolInputModels(stats.ByModel, statsRecentLimit) {
			lines = append(lines, fmt.Sprintf("- %s: %d repaired · %d invalid", ms.Model, ms.Repaired, ms.Invalid))
		}
	}
	return lines
}

func formatStatsOverview(usage usageStats, toolInput toolInputStats) []string {
	totalTokens := usage.PromptTokens + usage.CompletionTokens
	lines := []string{
		"",
		"Usage",
		fmt.Sprintf("- turns: %d", usage.Turns),
		fmt.Sprintf("- tokens: %s total", formatCount(totalTokens)),
		fmt.Sprintf("- estimated cost: $%.4f total · $%.4f last 7d", usage.CostUSD, usage.Last7CostUSD),
	}
	if usage.ReasoningReplayTokens > 0 {
		lines = append(lines, fmt.Sprintf("- reasoning replay: %s tokens", formatCount(usage.ReasoningReplayTokens)))
	}
	if model := topUsageModel(usage.ByModel); model != nil {
		lines = append(lines, fmt.Sprintf("- top model: %s · %d turns · $%.4f", model.Model, model.Turns, model.CostUSD))
	}

	totalToolInput := toolInput.Repaired + toolInput.Invalid
	lines = append(lines,
		"",
		"Tool input",
		fmt.Sprintf("- repaired: %d", toolInput.Repaired),
		fmt.Sprintf("- invalid: %d", toolInput.Invalid),
		fmt.Sprintf("- repair rate: %.1f%%", ratioPercent(toolInput.Repaired, totalToolInput)),
	)
	if repair := topCount(toolInput.ByRepairKind); repair != nil {
		lines = append(lines, fmt.Sprintf("- top repair: %s · %d", repair.Key, repair.Value))
	}
	if tool := topInvalidTool(toolInput.ByTool); tool != nil {
		lines = append(lines, fmt.Sprintf("- top invalid tool: %s · %d", tool.Tool, tool.Invalid))
	}
	lines = append(lines, "", "More: /stats usage, /stats tools, /stats repair, /stats recent, /stats profile, /stats all")
	return lines
}

func formatRecentStats(usage usageStats, toolInput toolInputStats) []string {
	lines := []string{}
	if len(usage.Recent) > 0 {
		lines = append(lines, "", "Recent turns")
		for _, rec := range reverseUsage(usage.Recent) {
			lines = append(lines, fmt.Sprintf("- %s · %s · %s · %s tokens · $%.4f · %.1f%% cache", formatTS(rec.TS), nonEmpty(rec.Session, "(unknown)"), nonEmpty(rec.Model, "(unknown)"), formatCount(rec.PromptTokens+rec.CompletionTokens), rec.CostUSD, ratioPercent(rec.PromptCacheHit, rec.PromptCacheHit+rec.PromptCacheMiss)))
		}
	}
	if len(toolInput.Recent) > 0 {
		lines = append(lines, "", "Recent tool-input events")
		for _, rec := range reverseToolInput(toolInput.Recent) {
			detail := nonEmpty(rec.RepairKind, rec.ErrorCode)
			if rec.Path != "" {
				detail += " · " + rec.Path
			}
			lines = append(lines, fmt.Sprintf("- %s · %s · %s · %s · %s", formatTS(rec.TS), nonEmpty(rec.Model, "(unknown)"), nonEmpty(rec.Tool, "(unknown)"), eventDisplay(rec.Event), detail))
		}
	}
	if len(lines) == 0 {
		return []string{"", "Recent", "- no recent stats"}
	}
	return lines
}

type countKV struct {
	Key   string
	Value int
}

func topCounts(in map[string]int, limit int) []countKV {
	out := make([]countKV, 0, len(in))
	for k, v := range in {
		out = append(out, countKV{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value == out[j].Value {
			return out[i].Key < out[j].Key
		}
		return out[i].Value > out[j].Value
	})
	return limitSlice(out, limit)
}

func topCount(in map[string]int) *countKV {
	top := topCounts(in, 1)
	if len(top) == 0 {
		return nil
	}
	return &top[0]
}

func topUsageModels(in map[string]*usageModelStats, limit int) []*usageModelStats {
	out := make([]*usageModelStats, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD == out[j].CostUSD {
			return out[i].Model < out[j].Model
		}
		return out[i].CostUSD > out[j].CostUSD
	})
	return limitSlice(out, limit)
}

func topUsageModel(in map[string]*usageModelStats) *usageModelStats {
	top := topUsageModels(in, 1)
	if len(top) == 0 {
		return nil
	}
	return top[0]
}

func topProfileTools(in map[string]*profileToolStats, limit int) []*profileToolStats {
	out := make([]*profileToolStats, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ResultChars == out[j].ResultChars {
			if out[i].Calls == out[j].Calls {
				return out[i].Tool < out[j].Tool
			}
			return out[i].Calls > out[j].Calls
		}
		return out[i].ResultChars > out[j].ResultChars
	})
	return limitSlice(out, limit)
}

func topProfileSessions(in []profileSessionStats, limit int, includeTrivial bool) []profileSessionStats {
	out := make([]profileSessionStats, 0, len(in))
	for _, sp := range in {
		if !includeTrivial && sp.Trivial {
			continue
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD == out[j].CostUSD {
			if out[i].ToolResultChars == out[j].ToolResultChars {
				return out[i].ID < out[j].ID
			}
			return out[i].ToolResultChars > out[j].ToolResultChars
		}
		return out[i].CostUSD > out[j].CostUSD
	})
	return limitSlice(out, limit)
}

func topReasoningReplaySessions(in []profileSessionStats, limit int) []profileSessionStats {
	out := make([]profileSessionStats, 0, len(in))
	for _, sp := range in {
		if sp.Trivial || profileSessionReasoningReplayTokens(sp) <= 0 {
			continue
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool {
		left := profileSessionReasoningReplayTokens(out[i])
		right := profileSessionReasoningReplayTokens(out[j])
		if left == right {
			leftCost := out[i].CostUSD + out[i].SubagentCostUSD
			rightCost := out[j].CostUSD + out[j].SubagentCostUSD
			if leftCost == rightCost {
				return out[i].ID < out[j].ID
			}
			return leftCost > rightCost
		}
		return left > right
	})
	return limitSlice(out, limit)
}

func topToolReplaySessions(in []profileSessionStats, limit int) []profileSessionStats {
	out := make([]profileSessionStats, 0, len(in))
	for _, sp := range in {
		if sp.Trivial || (profileSessionToolReplayTokens(sp) <= 0 && profileSessionToolRawTokens(sp) <= 0) {
			continue
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool {
		left := profileSessionToolReplayTokens(out[i])
		right := profileSessionToolReplayTokens(out[j])
		if left == right {
			leftRaw := profileSessionToolRawTokens(out[i])
			rightRaw := profileSessionToolRawTokens(out[j])
			if leftRaw == rightRaw {
				return out[i].ID < out[j].ID
			}
			return leftRaw > rightRaw
		}
		return left > right
	})
	return limitSlice(out, limit)
}

func profileSessionReasoningReplayTokens(sp profileSessionStats) int {
	return sp.ReasoningReplayTokens + sp.SubagentReasoningReplay
}

func profileSessionToolReplayTokens(sp profileSessionStats) int {
	return sp.ToolResultReplayTokens + sp.SubagentToolResultReplayTokens
}

func profileSessionToolRawTokens(sp profileSessionStats) int {
	return sp.ToolResultRawTokens + sp.SubagentToolResultRawTokens
}

func profileSessionToolSavedTokens(sp profileSessionStats) int {
	return sp.ToolResultTokensSaved + sp.SubagentToolResultTokensSaved
}

func profileSessionToolCompacted(sp profileSessionStats) int {
	return sp.ToolResultsCompacted + sp.SubagentToolResultsCompacted
}

func buildProfileInsights(sessions []profileSessionStats, limit int) []profileInsight {
	if limit <= 0 {
		return nil
	}
	var out []profileInsight
	add := func(kind, sessionID, detail string) {
		if len(out) < limit {
			out = append(out, profileInsight{Kind: kind, SessionID: sessionID, Detail: detail})
		}
	}
	for _, sp := range sessions {
		if sp.Trivial {
			continue
		}
		if input := sp.PromptTokens; input >= 8_000 {
			cachePct := ratioPercent(sp.CacheHit, sp.CacheHit+sp.CacheMiss)
			if cachePct < 80 {
				add("low cache", sp.ID, fmt.Sprintf("%.1f%% cache on %s input; inspect prefix and tool-schema churn", cachePct, formatCount(input)))
			}
		}
		if len(sp.PrefixFingerprints) > 1 {
			add("prefix churn", sp.ID, fmt.Sprintf("%d prefix fingerprints; check system blocks, memory, and tool schemas", len(sp.PrefixFingerprints)))
		}
		reasoningReplay := profileSessionReasoningReplayTokens(sp)
		input := sp.PromptTokens + sp.SubagentPromptTokens
		if reasoningReplay >= 1_000 || ratioPercent(reasoningReplay, input) >= 5 {
			add("reasoning replay", sp.ID, fmt.Sprintf("%s tokens replayed; %.1f%% of input", formatCount(reasoningReplay), ratioPercent(reasoningReplay, input)))
		}
		toolReplay := profileSessionToolReplayTokens(sp)
		if toolReplay >= 4_000 {
			add("tool replay", sp.ID, fmt.Sprintf("%s tool-result tokens resent; tune replay caps or summaries", formatCount(toolReplay)))
		}
		toolRaw := profileSessionToolRawTokens(sp)
		toolSaved := profileSessionToolSavedTokens(sp)
		if toolRaw >= 4_000 && ratioPercent(toolSaved, toolRaw) < 25 {
			add("weak tool compaction", sp.ID, fmt.Sprintf("%s saved from %s raw; lower replay caps or summarize earlier", formatCount(toolSaved), formatCount(toolRaw)))
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func topToolInputTools(in map[string]*toolInputToolStats, limit int) []*toolInputToolStats {
	out := make([]*toolInputToolStats, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].Repaired + out[i].Invalid
		right := out[j].Repaired + out[j].Invalid
		if left == right {
			return out[i].Tool < out[j].Tool
		}
		return left > right
	})
	return limitSlice(out, limit)
}

func topInvalidTool(in map[string]*toolInputToolStats) *toolInputToolStats {
	out := make([]*toolInputToolStats, 0, len(in))
	for _, v := range in {
		if v.Invalid > 0 {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Invalid == out[j].Invalid {
			return out[i].Tool < out[j].Tool
		}
		return out[i].Invalid > out[j].Invalid
	})
	if len(out) == 0 {
		return nil
	}
	return out[0]
}

func topToolInputModels(in map[string]*toolInputModelStats, limit int) []*toolInputModelStats {
	out := make([]*toolInputModelStats, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].Repaired + out[i].Invalid
		right := out[j].Repaired + out[j].Invalid
		if left == right {
			return out[i].Model < out[j].Model
		}
		return left > right
	})
	return limitSlice(out, limit)
}

func appendRecentUsage(recent []telemetry.UsageRecord, rec telemetry.UsageRecord) []telemetry.UsageRecord {
	recent = append(recent, rec)
	sort.Slice(recent, func(i, j int) bool { return recent[i].TS > recent[j].TS })
	return limitSlice(recent, statsRecentLimit)
}

func appendRecentToolInput(recent []telemetry.ToolInputEvent, rec telemetry.ToolInputEvent) []telemetry.ToolInputEvent {
	recent = append(recent, rec)
	sort.Slice(recent, func(i, j int) bool { return recent[i].TS > recent[j].TS })
	return limitSlice(recent, statsRecentLimit)
}

func reverseUsage(in []telemetry.UsageRecord) []telemetry.UsageRecord {
	out := append([]telemetry.UsageRecord(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out
}

func reverseToolInput(in []telemetry.ToolInputEvent) []telemetry.ToolInputEvent {
	out := append([]telemetry.ToolInputEvent(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out
}

func limitSlice[T any](in []T, limit int) []T {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}

func ratioPercent(num, denom int) float64 {
	if denom <= 0 || num <= 0 {
		return 0
	}
	return float64(num) * 100 / float64(denom)
}

func formatCount(v int) string {
	switch {
	case v >= 1_000_000:
		return trimFloat(float64(v)/1_000_000) + "M"
	case v >= 1_000:
		return trimFloat(float64(v)/1_000) + "K"
	default:
		return fmt.Sprintf("%d", v)
	}
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

func formatTS(ts int64) string {
	if ts <= 0 {
		return "(unknown time)"
	}
	return time.UnixMilli(ts).Format("2006-01-02 15:04")
}

func previewText(v string, maxRunes int) string {
	v = strings.Join(strings.Fields(v), " ")
	if maxRunes <= 0 {
		return v
	}
	runes := []rune(v)
	if len(runes) <= maxRunes {
		return v
	}
	if maxRunes <= 1 {
		return string(runes[:1]) + "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func eventDisplay(event string) string {
	switch event {
	case "tool_input_repaired":
		return "repaired"
	case "tool_input_invalid":
		return "invalid"
	default:
		return nonEmpty(event, "event")
	}
}
