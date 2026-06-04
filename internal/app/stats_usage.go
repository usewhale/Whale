package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/usewhale/whale/internal/telemetry"
	"os"
	"strings"
	"time"
)

func readUsageStats(path string, now time.Time) usageStats {
	stats := usageStats{
		Sessions: map[string]bool{},
		ByModel:  map[string]*usageModelStats{},
		Buckets:  usageBuckets(now),
	}
	cacheBreaks := newCacheBreakDetector()
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
		cacheBreaks.Add(rec)
		stats.Turns++
		if rec.Session != "" {
			stats.Sessions[rec.Session] = true
		}
		stats.PromptTokens += rec.PromptTokens
		stats.CompletionTokens += rec.CompletionTokens
		stats.CacheHit += rec.PromptCacheHit
		stats.CacheMiss += rec.PromptCacheMiss
		stats.PrefixCompletionRequests += rec.PrefixCompletionRequests
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
		ms.PrefixCompletionRequests += rec.PrefixCompletionRequests
		ms.ReasoningReplayTokens += rec.ReasoningReplayTok
		ms.CostUSD += cost
		ms.CacheHit += rec.PromptCacheHit
		ms.CacheMiss += rec.PromptCacheMiss
		rec.CostUSD = cost
		stats.Recent = appendRecentUsage(stats.Recent, rec)
	}
	stats.CacheDiagnostics = cacheBreaks.Diagnostics()
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
			out.addSubagentCacheShape(rec.CacheShape)
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
		if drift := summary.subagentCacheShapeDrift(); drift != "" {
			parts = append(parts, "subagent shape drift "+drift)
		}
	}
	return strings.Join(parts, " · ")
}

func (s *sessionUsageSummary) addSubagentCacheShape(shape *telemetry.CacheShape) {
	if shape == nil {
		return
	}
	addSessionShapeHash(&s.SubagentRequestHashes, shape.RequestHash)
	addSessionShapeHash(&s.SubagentSystemHashes, shape.SystemHash)
	addSessionShapeHash(&s.SubagentToolsHashes, shape.ToolsHash)
}

func addSessionShapeHash(dst *map[string]bool, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if *dst == nil {
		*dst = map[string]bool{}
	}
	(*dst)[value] = true
}

func (s sessionUsageSummary) subagentCacheShapeDrift() string {
	var parts []string
	if n := len(s.SubagentRequestHashes); n > 1 {
		parts = append(parts, fmt.Sprintf("%d request", n))
	}
	if n := len(s.SubagentSystemHashes); n > 1 {
		parts = append(parts, fmt.Sprintf("%d system", n))
	}
	if n := len(s.SubagentToolsHashes); n > 1 {
		parts = append(parts, fmt.Sprintf("%d tools", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/")
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
		buckets[i].PrefixCompletionRequests += rec.PrefixCompletionRequests
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
