package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) buildStatsLocalResult(view string) *LocalResult {
	return a.buildStatsLocalResultAt(view, time.Now())
}

func (a *App) buildStatsLocalResultAt(view string, now time.Time) *LocalResult {
	view = normalizeStatsView(view)
	plain := a.buildStatsViewAt(view, now)
	usage := readUsageStats(filepath.Join(a.cfg.DataDir, "usage.jsonl"), now)
	toolInput := readToolInputStats(a.sessionsDir)
	result := &LocalResult{
		Kind:      "stats",
		Title:     statsTitle(view),
		Fields:    statsSummaryFields(view, usage, toolInput),
		PlainText: plain,
	}
	switch view {
	case "usage":
		result.Sections = append(result.Sections, usageStatsSections(usage)...)
	case "tools", "repair":
		result.Sections = append(result.Sections, toolInputStatsSections(toolInput)...)
	case "recent":
		result.Sections = append(result.Sections, recentStatsSections(usage, toolInput)...)
	case "profile":
		profile := readProfileStats(a.sessionsDir, filepath.Join(a.cfg.DataDir, "usage.jsonl"), statsProfileSessionLimit)
		result.Fields = append(result.Fields, profileSummaryFields(profile)...)
		result.Sections = append(result.Sections, profileStatsSections(profile)...)
	case "all":
		result.Sections = append(result.Sections, usageStatsSections(usage)...)
		result.Sections = append(result.Sections, toolInputStatsSections(toolInput)...)
		result.Sections = append(result.Sections, recentStatsSections(usage, toolInput)...)
	default:
		result.Sections = append(result.Sections,
			LocalResultSection{Title: "Usage", Fields: usageOverviewFields(usage)},
			LocalResultSection{Title: "Tool input", Fields: toolInputOverviewFields(toolInput)},
		)
	}
	return result
}

func normalizeStatsView(view string) string {
	switch strings.TrimSpace(view) {
	case "usage", "tools", "repair", "recent", "profile", "all":
		return strings.TrimSpace(view)
	default:
		return "overview"
	}
}

func statsTitle(view string) string {
	if view == "overview" {
		return "Stats"
	}
	return "Stats: " + view
}

func statsSummaryFields(view string, usage usageStats, toolInput toolInputStats) []LocalResultField {
	totalTokens := usage.PromptTokens + usage.CompletionTokens
	return []LocalResultField{
		{Label: "View", Value: view, Tone: "info"},
		{Label: "Turns", Value: fmt.Sprintf("%d", usage.Turns)},
		{Label: "Sessions", Value: fmt.Sprintf("%d", len(usage.Sessions))},
		{Label: "Tokens", Value: formatCount(totalTokens)},
		{Label: "Cost", Value: fmt.Sprintf("$%.4f total · $%.4f last 7d", usage.CostUSD, usage.Last7CostUSD)},
		{Label: "Tool input", Value: fmt.Sprintf("%d repaired · %d invalid", toolInput.Repaired, toolInput.Invalid)},
	}
}

func usageOverviewFields(usage usageStats) []LocalResultField {
	totalTokens := usage.PromptTokens + usage.CompletionTokens
	fields := []LocalResultField{
		{Label: "Turns", Value: fmt.Sprintf("%d", usage.Turns)},
		{Label: "Tokens", Value: formatCount(totalTokens)},
		{Label: "Cost", Value: fmt.Sprintf("$%.4f total · $%.4f last 7d", usage.CostUSD, usage.Last7CostUSD)},
	}
	if model := topUsageModel(usage.ByModel); model != nil {
		fields = append(fields, LocalResultField{Label: "Top model", Value: fmt.Sprintf("%s · %d turns · $%.4f", model.Model, model.Turns, model.CostUSD)})
	}
	return fields
}

func toolInputOverviewFields(toolInput toolInputStats) []LocalResultField {
	total := toolInput.Repaired + toolInput.Invalid
	fields := []LocalResultField{
		{Label: "Repaired", Value: fmt.Sprintf("%d", toolInput.Repaired)},
		{Label: "Invalid", Value: fmt.Sprintf("%d", toolInput.Invalid), Tone: invalidTone(toolInput.Invalid)},
		{Label: "Repair rate", Value: fmt.Sprintf("%.1f%%", ratioPercent(toolInput.Repaired, total))},
	}
	if repair := topCount(toolInput.ByRepairKind); repair != nil {
		fields = append(fields, LocalResultField{Label: "Top repair", Value: fmt.Sprintf("%s · %d", repair.Key, repair.Value)})
	}
	if tool := topInvalidTool(toolInput.ByTool); tool != nil {
		fields = append(fields, LocalResultField{Label: "Top invalid tool", Value: fmt.Sprintf("%s · %d", tool.Tool, tool.Invalid), Tone: "warn"})
	}
	return fields
}

func usageStatsSections(stats usageStats) []LocalResultSection {
	sections := []LocalResultSection{{Title: "Usage", Fields: []LocalResultField{
		{Label: "Turns", Value: fmt.Sprintf("%d", stats.Turns)},
		{Label: "Sessions", Value: fmt.Sprintf("%d", len(stats.Sessions))},
		{Label: "Tokens", Value: fmt.Sprintf("%s total · %s input · %s output", formatCount(stats.PromptTokens+stats.CompletionTokens), formatCount(stats.PromptTokens), formatCount(stats.CompletionTokens))},
		{Label: "Cache", Value: fmt.Sprintf("%s hit · %s miss · %.1f%%", formatCount(stats.CacheHit), formatCount(stats.CacheMiss), ratioPercent(stats.CacheHit, stats.CacheHit+stats.CacheMiss))},
		{Label: "Estimated cost", Value: fmt.Sprintf("$%.4f total · $%.4f last 7d", stats.CostUSD, stats.Last7CostUSD)},
	}}}
	if len(stats.ByModel) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, ms := range topUsageModels(stats.ByModel, statsRecentLimit) {
			fields = append(fields, LocalResultField{Label: ms.Model, Value: fmt.Sprintf("%d turns · %s tokens · %.1f%% cache · $%.4f", ms.Turns, formatCount(ms.Tokens), ratioPercent(ms.CacheHit, ms.CacheHit+ms.CacheMiss), ms.CostUSD)})
		}
		sections = append(sections, LocalResultSection{Title: "By model", Fields: fields})
	}
	return sections
}

func toolInputStatsSections(stats toolInputStats) []LocalResultSection {
	total := stats.Repaired + stats.Invalid
	sections := []LocalResultSection{{Title: "Tool input", Fields: []LocalResultField{
		{Label: "Repaired", Value: fmt.Sprintf("%d", stats.Repaired)},
		{Label: "Invalid", Value: fmt.Sprintf("%d", stats.Invalid), Tone: invalidTone(stats.Invalid)},
		{Label: "Repair rate", Value: fmt.Sprintf("%.1f%%", ratioPercent(stats.Repaired, total))},
	}}}
	if len(stats.ByRepairKind) > 0 {
		sections = append(sections, LocalResultSection{Title: "Repair kinds", Fields: countFields(topCounts(stats.ByRepairKind, statsRecentLimit), "")})
	}
	if len(stats.ByErrorCode) > 0 {
		sections = append(sections, LocalResultSection{Title: "Invalid codes", Fields: countFields(topCounts(stats.ByErrorCode, statsRecentLimit), "warn")})
	}
	if len(stats.ByTool) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, ts := range topToolInputTools(stats.ByTool, statsRecentLimit) {
			fields = append(fields, LocalResultField{Label: ts.Tool, Value: fmt.Sprintf("%d repaired · %d invalid", ts.Repaired, ts.Invalid), Tone: invalidTone(ts.Invalid)})
		}
		sections = append(sections, LocalResultSection{Title: "Top tools", Fields: fields})
	}
	if len(stats.ByModel) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, ms := range topToolInputModels(stats.ByModel, statsRecentLimit) {
			fields = append(fields, LocalResultField{Label: ms.Model, Value: fmt.Sprintf("%d repaired · %d invalid", ms.Repaired, ms.Invalid), Tone: invalidTone(ms.Invalid)})
		}
		sections = append(sections, LocalResultSection{Title: "By model", Fields: fields})
	}
	return sections
}

func recentStatsSections(usage usageStats, toolInput toolInputStats) []LocalResultSection {
	sections := []LocalResultSection{}
	if len(usage.Recent) > 0 {
		fields := make([]LocalResultField, 0, len(usage.Recent))
		for _, rec := range reverseUsage(usage.Recent) {
			fields = append(fields, LocalResultField{Label: formatTS(rec.TS), Value: fmt.Sprintf("%s · %s · %s tokens · $%.4f · %.1f%% cache", nonEmpty(rec.Session, "(unknown)"), nonEmpty(rec.Model, "(unknown)"), formatCount(rec.PromptTokens+rec.CompletionTokens), rec.CostUSD, ratioPercent(rec.PromptCacheHit, rec.PromptCacheHit+rec.PromptCacheMiss))})
		}
		sections = append(sections, LocalResultSection{Title: "Recent turns", Fields: fields})
	}
	if len(toolInput.Recent) > 0 {
		fields := make([]LocalResultField, 0, len(toolInput.Recent))
		for _, rec := range reverseToolInput(toolInput.Recent) {
			detail := nonEmpty(rec.RepairKind, rec.ErrorCode)
			if rec.Path != "" {
				detail += " · " + rec.Path
			}
			fields = append(fields, LocalResultField{Label: formatTS(rec.TS), Value: fmt.Sprintf("%s · %s · %s · %s", nonEmpty(rec.Model, "(unknown)"), nonEmpty(rec.Tool, "(unknown)"), eventDisplay(rec.Event), detail)})
		}
		sections = append(sections, LocalResultSection{Title: "Recent tool-input events", Fields: fields})
	}
	if len(sections) == 0 {
		sections = append(sections, LocalResultSection{Title: "Recent", Fields: []LocalResultField{{Label: "Activity", Value: "no recent stats", Tone: "muted"}}})
	}
	return sections
}

func profileSummaryFields(stats profileStats) []LocalResultField {
	return []LocalResultField{
		{Label: "Scanned sessions", Value: fmt.Sprintf("%d latest main sessions (limit %d)", len(stats.Sessions), stats.Limit)},
		{Label: "Main work sessions", Value: fmt.Sprintf("%d", stats.MainWorkSessions)},
		{Label: "Trivial/local sessions", Value: fmt.Sprintf("%d", stats.TrivialSessions)},
		{Label: "Tool-heavy sessions", Value: fmt.Sprintf("%d", stats.ToolHeavySessions)},
		{Label: "Usage matched sessions", Value: fmt.Sprintf("%d", stats.UsageMatchedSessions)},
	}
}

func profileStatsSections(stats profileStats) []LocalResultSection {
	allInReasoningReplay := stats.ReasoningReplayTokens + stats.SubagentReasoningReplay
	allInToolReplayTokens := stats.ToolResultReplayTokens + stats.SubagentToolResultReplayTokens
	allInToolRawTokens := stats.ToolResultRawTokens + stats.SubagentToolResultRawTokens
	allInToolSavedTokens := stats.ToolResultTokensSaved + stats.SubagentToolResultTokensSaved
	allInToolCompacted := stats.ToolResultsCompacted + stats.SubagentToolResultsCompacted
	profileFields := []LocalResultField{
		{Label: "Tokens", Value: fmt.Sprintf("%s total · %s input · %s output", formatCount(stats.PromptTokens+stats.CompletionTokens), formatCount(stats.PromptTokens), formatCount(stats.CompletionTokens))},
		{Label: "Cache", Value: fmt.Sprintf("%s hit · %s miss · %.1f%%", formatCount(stats.CacheHit), formatCount(stats.CacheMiss), ratioPercent(stats.CacheHit, stats.CacheHit+stats.CacheMiss))},
		{Label: "Estimated cost", Value: fmt.Sprintf("$%.4f", stats.CostUSD)},
		{Label: "Max prompt", Value: formatCount(stats.MaxPromptTokens)},
		{Label: "Prefix fingerprints", Value: fmt.Sprintf("%d", len(stats.PrefixFingerprints))},
		{Label: "Tools", Value: fmt.Sprintf("%d calls · %s result chars", stats.ToolCalls, formatCount(stats.ToolResultChars))},
		{Label: "Reasoning/text", Value: fmt.Sprintf("%s reasoning chars · %s visible text chars", formatCount(stats.ReasoningChars), formatCount(stats.VisibleTextChars))},
	}
	if allInReasoningReplay > 0 {
		profileFields = append(profileFields, LocalResultField{Label: "Reasoning replay", Value: fmt.Sprintf("%s main · %s subagent · %s all-in", formatCount(stats.ReasoningReplayTokens), formatCount(stats.SubagentReasoningReplay), formatCount(allInReasoningReplay))})
	}
	if allInToolReplayTokens > 0 || allInToolRawTokens > 0 || allInToolSavedTokens > 0 || allInToolCompacted > 0 {
		profileFields = append(profileFields, LocalResultField{Label: "Tool replay", Value: fmt.Sprintf("%s sent · %s raw · %s saved · %d compacted", formatCount(allInToolReplayTokens), formatCount(allInToolRawTokens), formatCount(allInToolSavedTokens), allInToolCompacted)})
	}
	sections := []LocalResultSection{{Title: "Profile", Fields: profileFields}}
	if len(stats.ByTool) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, ts := range topProfileTools(stats.ByTool, statsRecentLimit) {
			fields = append(fields, LocalResultField{Label: ts.Tool, Value: fmt.Sprintf("%d calls · %s result chars", ts.Calls, formatCount(ts.ResultChars))})
		}
		sections = append(sections, LocalResultSection{Title: "Top tools", Fields: fields})
	}
	if top := topToolReplaySessions(stats.Sessions, statsRecentLimit); len(top) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, sp := range top {
			fields = append(fields, LocalResultField{Label: sp.ID, Value: fmt.Sprintf("%s sent · %s raw · %s saved · %d compacted · %s", formatCount(profileSessionToolReplayTokens(sp)), formatCount(profileSessionToolRawTokens(sp)), formatCount(profileSessionToolSavedTokens(sp)), profileSessionToolCompacted(sp), nonEmpty(sp.FirstUserText, "(no user text)"))})
		}
		sections = append(sections, LocalResultSection{Title: "Top tool replay sessions", Fields: fields})
	}
	if top := topReasoningReplaySessions(stats.Sessions, statsRecentLimit); len(top) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, sp := range top {
			childDetail := ""
			if sp.SubagentReasoningReplay > 0 {
				childDetail = fmt.Sprintf(" · +%s subagents", formatCount(sp.SubagentReasoningReplay))
			}
			fields = append(fields, LocalResultField{Label: sp.ID, Value: fmt.Sprintf("%s tokens%s · %.1f%% of input · %s", formatCount(sp.ReasoningReplayTokens+sp.SubagentReasoningReplay), childDetail, ratioPercent(sp.ReasoningReplayTokens+sp.SubagentReasoningReplay, sp.PromptTokens+sp.SubagentPromptTokens), nonEmpty(sp.FirstUserText, "(no user text)"))})
		}
		sections = append(sections, LocalResultSection{Title: "Top reasoning replay sessions", Fields: fields})
	}
	if len(stats.TopSessions) > 0 {
		fields := make([]LocalResultField, 0, statsRecentLimit)
		for _, sp := range stats.TopSessions {
			fields = append(fields, LocalResultField{Label: sp.ID, Value: fmt.Sprintf("$%.4f · max prompt %s · tools %s chars · %.1f%% cache · %s", sp.CostUSD, formatCount(sp.MaxPromptTokens), formatCount(sp.ToolResultChars), ratioPercent(sp.CacheHit, sp.CacheHit+sp.CacheMiss), nonEmpty(sp.FirstUserText, "(no user text)"))})
		}
		sections = append(sections, LocalResultSection{Title: "Top work sessions", Fields: fields})
	}
	return sections
}

func countFields(counts []countKV, tone string) []LocalResultField {
	fields := make([]LocalResultField, 0, len(counts))
	for _, kv := range counts {
		fields = append(fields, LocalResultField{Label: kv.Key, Value: fmt.Sprintf("%d", kv.Value), Tone: tone})
	}
	return fields
}

func invalidTone(count int) string {
	if count > 0 {
		return "warn"
	}
	return ""
}
