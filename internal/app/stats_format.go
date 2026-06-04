package app

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/defaults"
)

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
	if stats.PrefixCompletionRequests > 0 {
		lines = append(lines, fmt.Sprintf("- Prefix completion: %d requests", stats.PrefixCompletionRequests))
	}
	if summary := cacheDiagnosticsSummary(stats.CacheDiagnostics); summary != "" {
		lines = append(lines, "- cache diagnostics: "+summary)
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
			prefixDetail := ""
			if b.PrefixCompletionRequests > 0 {
				prefixDetail = fmt.Sprintf(" · Prefix completion %d", b.PrefixCompletionRequests)
			}
			lines = append(lines, fmt.Sprintf("- %s: %d turns · %s tokens · %.1f%% cache · $%.4f cost · $%.4f cache saved%s%s", b.Label, b.Turns, formatCount(b.PromptTokens+b.CompletionTokens), ratioPercent(b.CacheHit, b.CacheHit+b.CacheMiss), b.CostUSD, b.CacheSavingsUSD, prefixDetail, subagentDetail))
		}
	}
	if len(stats.ByModel) > 0 {
		lines = append(lines, "", "By model")
		for _, ms := range topUsageModels(stats.ByModel, statsRecentLimit) {
			replayDetail := ""
			if ms.ReasoningReplayTokens > 0 {
				replayDetail = fmt.Sprintf(" · %s reasoning replay", formatCount(ms.ReasoningReplayTokens))
			}
			prefixDetail := ""
			if ms.PrefixCompletionRequests > 0 {
				prefixDetail = fmt.Sprintf(" · Prefix completion %d", ms.PrefixCompletionRequests)
			}
			lines = append(lines, fmt.Sprintf("- %s: %d turns · %s tokens%s%s · %.1f%% cache · $%.4f", ms.Model, ms.Turns, formatCount(ms.Tokens), replayDetail, prefixDetail, ratioPercent(ms.CacheHit, ms.CacheHit+ms.CacheMiss), ms.CostUSD))
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
		fmt.Sprintf("- provider prefixes: %d distinct across %d usage sessions", len(stats.ProviderPrefixHashes), len(stats.PrefixShapeSessions)),
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
	if stats.ApprovalAuditEvents > 0 {
		lines = append(lines, fmt.Sprintf("- approvals: %d prompts · %d allow-once · %d allow-session · %d denied · %d canceled · %d reused/cached · %d policy/mode blocks · %d audit events",
			stats.ApprovalPrompts,
			stats.ApprovalAllowedOnce,
			stats.ApprovalAllowedForSession,
			stats.ApprovalDenied,
			stats.ApprovalCanceled,
			stats.ApprovalReused,
			stats.ApprovalPolicyBlocks+stats.ApprovalModeBlocks,
			stats.ApprovalAuditEvents,
		))
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
	if usage.PrefixCompletionRequests > 0 {
		lines = append(lines, fmt.Sprintf("- Prefix completion: %d requests", usage.PrefixCompletionRequests))
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
	lines = append(lines, "", "More: /stats usage, /stats cache, /stats tools, /stats repair, /stats recent, /stats profile, /stats all")
	return lines
}

func formatCacheDiagnostics(diag cacheDiagnostics) []string {
	lines := []string{fmt.Sprintf("- breaks: %d", totalCacheBreaks(diag))}
	if len(diag.Counts) > 0 {
		lines = append(lines, "", "Break causes")
		for _, kv := range topCounts(diag.Counts, statsRecentLimit) {
			lines = append(lines, fmt.Sprintf("- %s: %d", kv.Key, kv.Value))
		}
	}
	if len(diag.Breaks) > 0 {
		lines = append(lines, "", "Recent breaks")
		for _, br := range reverseCacheBreaks(diag.Breaks) {
			lines = append(lines, fmt.Sprintf("- %s · %s", formatTS(br.TS), formatCacheBreakDetail(br)))
		}
	}
	return lines
}

func formatCacheBreakDetail(br cacheBreak) string {
	parts := []string{
		nonEmpty(br.Session, "(unknown session)"),
		nonEmpty(br.Model, "(unknown model)"),
		nonEmpty(br.RequestKind, "agent"),
		fmt.Sprintf("cache hit %s -> %s", formatCount(br.PreviousHit), formatCount(br.CurrentHit)),
		fmt.Sprintf("miss %s", formatCount(br.CurrentMiss)),
		nonEmpty(br.Cause, "unknown"),
	}
	if detail := strings.TrimSpace(br.Details); detail != "" {
		parts = append(parts, detail)
	}
	return strings.Join(parts, " · ")
}

func reverseCacheBreaks(in []cacheBreak) []cacheBreak {
	out := make([]cacheBreak, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
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
