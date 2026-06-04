package main

import (
	"fmt"
	"sort"
	"strings"
)

type reportStats struct {
	Runs             int
	PassRate         float64
	CacheHitRatio    float64
	MeanCost         float64
	MeanTurns        float64
	MeanToolCalls    float64
	MeanFingerprints float64
}

func renderMarkdown(report benchReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Whale live prefix-cache benchmark\n\n")
	fmt.Fprintf(&b, "**Date:** %s\n", report.Meta.Date)
	fmt.Fprintf(&b, "**Model:** `%s`\n", report.Meta.Model)
	fmt.Fprintf(&b, "**Effort:** `%s`\n", report.Meta.Effort)
	fmt.Fprintf(&b, "**Modes:** `%s`\n", strings.Join(report.Meta.Modes, "`, `"))
	fmt.Fprintf(&b, "**Tasks:** %d, repeats x %d\n", report.Meta.TaskCount, report.Meta.RepeatsPerTask)
	fmt.Fprintf(&b, "**Whale version:** `%s`\n", report.Meta.WhaleVersion)
	if report.Meta.LiveDeepSeek {
		fmt.Fprintf(&b, "**Source:** live DeepSeek API usage, not mock usage\n\n")
	} else {
		fmt.Fprintf(&b, "**Source:** dry-run fixture validation, no DeepSeek API calls\n\n")
	}
	fmt.Fprintf(&b, "> Baseline is a cache-hostile Whale control with benchmark-only volatile system context. It is not an external product agent.\n\n")
	fmt.Fprintf(&b, "## Summary\n\n")
	if hasBothModes(report.Results) {
		renderComparisonSummary(&b, report.Results)
	} else {
		renderSingleSummary(&b, report.Results)
	}
	fmt.Fprintf(&b, "\n## Per-task breakdown\n\n")
	fmt.Fprintf(&b, "| mode | task | repeat | pass | turns | tools | prefixes | cache | cost |\n")
	fmt.Fprintf(&b, "|---|---|---:|:---:|---:|---:|---:|---:|---:|\n")
	rows := append([]runResult(nil), report.Results...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].TaskID != rows[j].TaskID {
			return rows[i].TaskID < rows[j].TaskID
		}
		if rows[i].Repeat != rows[j].Repeat {
			return rows[i].Repeat < rows[j].Repeat
		}
		return rows[i].Mode < rows[j].Mode
	})
	for _, r := range rows {
		pass := "no"
		if r.Pass {
			pass = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %d | %d | %d | %s | $%.6f |\n",
			r.Mode, r.TaskID, r.Repeat, pass, r.Turns, r.ToolCalls, r.PrefixFingerprints, pctFloat(r.CacheHitRatio), r.CostUSD)
	}
	fmt.Fprintf(&b, "\n## Reproduce\n\n")
	mode := "both"
	if len(report.Meta.Modes) == 1 {
		mode = report.Meta.Modes[0]
	}
	if report.Meta.LiveDeepSeek {
		fmt.Fprintf(&b, "```bash\nDEEPSEEK_API_KEY=sk-... scripts/bench/live_cache.sh --mode %s --repeats %d --model %s\n```\n", mode, report.Meta.RepeatsPerTask, report.Meta.Model)
	} else {
		fmt.Fprintf(&b, "```bash\nscripts/bench/live_cache.sh --dry --mode %s --repeats %d --model %s\n```\n", mode, report.Meta.RepeatsPerTask, report.Meta.Model)
	}
	return b.String()
}

func renderComparisonSummary(b *strings.Builder, results []runResult) {
	byMode := resultsByMode(results)
	baseline := summarizeResults(byMode["baseline"])
	whale := summarizeResults(byMode["whale"])
	fmt.Fprintf(b, "| metric | baseline | whale | delta |\n")
	fmt.Fprintf(b, "|---|---:|---:|---:|\n")
	fmt.Fprintf(b, "| runs | %d | %d | %d |\n", baseline.Runs, whale.Runs, whale.Runs-baseline.Runs)
	fmt.Fprintf(b, "| pass rate | %s | %s | %s |\n", pctFloat(baseline.PassRate), pctFloat(whale.PassRate), signedPctDelta(whale.PassRate-baseline.PassRate))
	fmt.Fprintf(b, "| cache hit | %s | %s | %s |\n", pctFloat(baseline.CacheHitRatio), pctFloat(whale.CacheHitRatio), signedPctDelta(whale.CacheHitRatio-baseline.CacheHitRatio))
	fmt.Fprintf(b, "| mean cost / task | $%.6f | $%.6f | %s |\n", baseline.MeanCost, whale.MeanCost, signedDollarDelta(whale.MeanCost-baseline.MeanCost))
	fmt.Fprintf(b, "| mean turns | %.1f | %.1f | %+.1f |\n", baseline.MeanTurns, whale.MeanTurns, whale.MeanTurns-baseline.MeanTurns)
	fmt.Fprintf(b, "| mean tool calls | %.1f | %.1f | %+.1f |\n", baseline.MeanToolCalls, whale.MeanToolCalls, whale.MeanToolCalls-baseline.MeanToolCalls)
	fmt.Fprintf(b, "| mean prefix fingerprints | %.1f | %.1f | %+.1f |\n", baseline.MeanFingerprints, whale.MeanFingerprints, whale.MeanFingerprints-baseline.MeanFingerprints)
}

func renderSingleSummary(b *strings.Builder, results []runResult) {
	mode := "whale"
	if len(results) > 0 && strings.TrimSpace(results[0].Mode) != "" {
		mode = results[0].Mode
	}
	stats := summarizeResults(results)
	fmt.Fprintf(b, "| metric | %s |\n", mode)
	fmt.Fprintf(b, "|---|---:|\n")
	fmt.Fprintf(b, "| runs | %d |\n", stats.Runs)
	fmt.Fprintf(b, "| pass rate | %s |\n", pctFloat(stats.PassRate))
	fmt.Fprintf(b, "| cache hit | %s |\n", pctFloat(stats.CacheHitRatio))
	fmt.Fprintf(b, "| mean cost / task | $%.6f |\n", stats.MeanCost)
	fmt.Fprintf(b, "| mean turns | %.1f |\n", stats.MeanTurns)
	fmt.Fprintf(b, "| mean tool calls | %.1f |\n", stats.MeanToolCalls)
	fmt.Fprintf(b, "| mean prefix fingerprints | %.1f |\n", stats.MeanFingerprints)
}

func hasBothModes(results []runResult) bool {
	byMode := resultsByMode(results)
	return len(byMode["baseline"]) > 0 && len(byMode["whale"]) > 0
}

func resultsByMode(results []runResult) map[string][]runResult {
	out := map[string][]runResult{}
	for _, r := range results {
		mode := strings.TrimSpace(r.Mode)
		if mode == "" {
			mode = "whale"
		}
		out[mode] = append(out[mode], r)
	}
	return out
}

func summarizeResults(results []runResult) reportStats {
	stats := reportStats{Runs: len(results)}
	if len(results) == 0 {
		return stats
	}
	stats.PassRate = float64(passCount(results)) / float64(len(results))
	stats.CacheHitRatio = aggregateUsage(results).CacheHitRatio()
	stats.MeanCost = meanCost(results)
	stats.MeanTurns = meanInt(results, func(r runResult) int { return r.Turns })
	stats.MeanToolCalls = meanInt(results, func(r runResult) int { return r.ToolCalls })
	stats.MeanFingerprints = meanInt(results, func(r runResult) int { return r.PrefixFingerprints })
	return stats
}

func aggregateUsage(results []runResult) usageTotals {
	out := usageTotals{PrefixFingerprints: map[string]bool{}}
	for _, r := range results {
		out.PromptTokens += r.PromptTokens
		out.CompletionTokens += r.CompletionTokens
		out.CacheHitTokens += r.CacheHitTokens
		out.CacheMissTokens += r.CacheMissTokens
		out.CostUSD += r.CostUSD
	}
	return out
}

func passCount(results []runResult) int {
	n := 0
	for _, r := range results {
		if r.Pass {
			n++
		}
	}
	return n
}

func meanCost(results []runResult) float64 {
	if len(results) == 0 {
		return 0
	}
	var sum float64
	for _, r := range results {
		sum += r.CostUSD
	}
	return sum / float64(len(results))
}

func meanInt(results []runResult, fn func(runResult) int) float64 {
	if len(results) == 0 {
		return 0
	}
	sum := 0
	for _, r := range results {
		sum += fn(r)
	}
	return float64(sum) / float64(len(results))
}

func pct(num, denom int) string {
	if denom == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", (float64(num)/float64(denom))*100)
}

func pctFloat(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}

func signedPctDelta(v float64) string {
	return fmt.Sprintf("%+.1f%%", v*100)
}

func signedDollarDelta(v float64) string {
	return fmt.Sprintf("%s$%.6f", sign(v), abs(v))
}

func sign(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
