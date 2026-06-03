package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/telemetry"
	"sort"
	"strings"
	"time"
)

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
		if len(sp.ProviderPrefixHashes) > 1 {
			add("provider prefix churn", sp.ID, profilePrefixChurnDetail(sp))
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

func profilePrefixChurnDetail(sp profileSessionStats) string {
	sources := make([]string, 0, 3)
	if len(sp.SystemHashes) > 1 {
		sources = append(sources, "system drift")
	}
	if len(sp.RuntimeHashes) > 1 {
		sources = append(sources, "runtime drift")
	}
	if len(sp.ToolsHashes) > 1 {
		sources = append(sources, "tool drift")
	}
	if len(sources) == 0 {
		sources = append(sources, "mixed or legacy drift")
	}
	detail := fmt.Sprintf("%d provider prefixes; %s", len(sp.ProviderPrefixHashes), strings.Join(sources, ", "))
	if changed := changedProfileShapeSegments(sp.ShapeSegments, 3); len(changed) > 0 {
		detail += "; changed segments: " + strings.Join(changed, ", ")
	}
	return detail
}

func changedProfileShapeSegments(segments map[string]map[string]bool, limit int) []string {
	if limit <= 0 || len(segments) == 0 {
		return nil
	}
	var names []string
	for name, hashes := range segments {
		if len(hashes) > 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > limit {
		names = names[:limit]
	}
	return names
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
