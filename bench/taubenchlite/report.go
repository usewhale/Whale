package main

import (
	"fmt"
	"sort"
	"strings"
)

type reportStats struct {
	Runs                       int
	PassRate                   float64
	MeanRunCacheHitRatio       float64
	TokenWeightedCacheHitRatio float64
	WarmCacheHitRatio          float64
	MeanCost                   float64
	MeanCacheSavings           float64
	MeanUncachedCost           float64
	MeanTurns                  float64
	MeanToolCalls              float64
	MeanFingerprints           float64
	GlobalFingerprints         int
	MeanShapePrefixHashes      float64
	GlobalShapePrefixHashes    int
	MeanShapeRuntimeHashes     float64
	GlobalShapeRuntimeHashes   int
	MeanShapeToolsHashes       float64
	GlobalShapeToolsHashes     int
	MeanShapeRequestHashes     float64
	GlobalShapeRequestHashes   int
	Truncated                  int
}

func renderMarkdown(report benchReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Whale tau-bench-lite retail cache benchmark\n\n")
	fmt.Fprintf(&b, "**Date:** %s\n", report.Meta.Date)
	fmt.Fprintf(&b, "**Agent model:** `%s`\n", report.Meta.Model)
	fmt.Fprintf(&b, "**User-simulator model:** `%s`\n", report.Meta.UserModel)
	fmt.Fprintf(&b, "**Effort:** `%s`\n", report.Meta.Effort)
	fmt.Fprintf(&b, "**Modes:** `%s`\n", strings.Join(report.Meta.Modes, "`, `"))
	fmt.Fprintf(&b, "**Tasks:** %d, repeats x %d\n", report.Meta.TaskCount, report.Meta.RepeatsPerTask)
	fmt.Fprintf(&b, "**Whale version:** `%s`\n", report.Meta.WhaleVersion)
	if report.Meta.LiveDeepSeek {
		fmt.Fprintf(&b, "**Source:** live DeepSeek API usage, not mock usage\n\n")
	} else {
		fmt.Fprintf(&b, "**Source:** dry-run task/tool wiring, no DeepSeek API calls\n\n")
	}
	fmt.Fprintf(&b, "> Baseline is the same Whale agent and retail tools with benchmark-only volatile system context. It is not an external product agent.\n\n")
	fmt.Fprintf(&b, "## Summary\n\n")
	if hasBothModes(report.Results) {
		renderComparisonSummary(&b, report.Results)
	} else {
		renderSingleSummary(&b, report.Results)
	}
	fmt.Fprintf(&b, "\n## Per-task breakdown\n\n")
	fmt.Fprintf(&b, "| task | mode | repeat | pass | turns | tools | imm fp | shape prefix | runtime | tool shape | request | truncated | cache | cost |\n")
	fmt.Fprintf(&b, "|---|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|:---:|---:|---:|\n")
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
		truncated := "no"
		if r.Truncated {
			truncated = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %d | %d | %d | %d | %d | %d | %d | %s | %s | $%.6f |\n",
			r.TaskID, r.Mode, r.Repeat, pass, r.Turns, r.ToolCalls, r.PrefixFingerprints, r.ShapePrefixHashes, r.ShapeRuntimeHashes, r.ShapeToolsHashes, r.ShapeRequestHashes, truncated, pctFloat(r.CacheHitRatio), r.CostUSD)
	}
	fmt.Fprintf(&b, "\n## Reproduce\n\n")
	mode := "both"
	if len(report.Meta.Modes) == 1 {
		mode = report.Meta.Modes[0]
	}
	if report.Meta.LiveDeepSeek {
		fmt.Fprintf(&b, "```bash\nDEEPSEEK_API_KEY=sk-... scripts/bench/tau_bench_lite.sh --mode %s --repeats %d --model %s --user-model %s\n```\n", mode, report.Meta.RepeatsPerTask, report.Meta.Model, report.Meta.UserModel)
	} else {
		fmt.Fprintf(&b, "```bash\nscripts/bench/tau_bench_lite.sh --dry --mode %s --repeats %d\n```\n", mode, report.Meta.RepeatsPerTask)
	}
	fmt.Fprintf(&b, "\n## Scope & caveats\n\n")
	fmt.Fprintf(&b, "- This is tau-bench-lite, not upstream tau-bench.\n")
	fmt.Fprintf(&b, "- Tasks are hand-authored retail tasks with stateful tools, an LLM user simulator, and deterministic DB end-state checks.\n")
	fmt.Fprintf(&b, "- Baseline is a cache-hostile control: fresh timestamp in system context plus deterministic shuffled tool order. It is not an external product agent.\n")
	fmt.Fprintf(&b, "- User simulator drift is expected; use `--repeats N` for tighter means.\n")
	fmt.Fprintf(&b, "- Cost and cache numbers are live DeepSeek usage when `live_deepseek=true`.\n")
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
	fmt.Fprintf(b, "| cache hit | %s | %s | **%s** |\n", pctFloat(baseline.MeanRunCacheHitRatio), pctFloat(whale.MeanRunCacheHitRatio), signedPctDelta(whale.MeanRunCacheHitRatio-baseline.MeanRunCacheHitRatio))
	fmt.Fprintf(b, "| token-weighted cache hit | %s | %s | %s |\n", pctFloat(baseline.TokenWeightedCacheHitRatio), pctFloat(whale.TokenWeightedCacheHitRatio), signedPctDelta(whale.TokenWeightedCacheHitRatio-baseline.TokenWeightedCacheHitRatio))
	fmt.Fprintf(b, "| warm cache hit | %s | %s | %s |\n", pctFloat(baseline.WarmCacheHitRatio), pctFloat(whale.WarmCacheHitRatio), signedPctDelta(whale.WarmCacheHitRatio-baseline.WarmCacheHitRatio))
	costRatio := 0.0
	if baseline.MeanCost > 0 {
		costRatio = whale.MeanCost / baseline.MeanCost
	}
	fmt.Fprintf(b, "| mean cost / task | $%.6f | $%.6f | %s |\n", baseline.MeanCost, whale.MeanCost, costRatioText(costRatio))
	fmt.Fprintf(b, "| mean cache savings / task | $%.6f | $%.6f | %s |\n", baseline.MeanCacheSavings, whale.MeanCacheSavings, signedDollarDelta(whale.MeanCacheSavings-baseline.MeanCacheSavings))
	fmt.Fprintf(b, "| mean uncached cost / task | $%.6f | $%.6f | %s |\n", baseline.MeanUncachedCost, whale.MeanUncachedCost, signedDollarDelta(whale.MeanUncachedCost-baseline.MeanUncachedCost))
	fmt.Fprintf(b, "| mean turns | %.1f | %.1f | %+.1f |\n", baseline.MeanTurns, whale.MeanTurns, whale.MeanTurns-baseline.MeanTurns)
	fmt.Fprintf(b, "| mean tool calls | %.1f | %.1f | %+.1f |\n", baseline.MeanToolCalls, whale.MeanToolCalls, whale.MeanToolCalls-baseline.MeanToolCalls)
	fmt.Fprintf(b, "| mean immutable prefix fingerprints | %.1f | %.1f | %+.1f |\n", baseline.MeanFingerprints, whale.MeanFingerprints, whale.MeanFingerprints-baseline.MeanFingerprints)
	fmt.Fprintf(b, "| global immutable prefix fingerprints | %d | %d | %+d |\n", baseline.GlobalFingerprints, whale.GlobalFingerprints, whale.GlobalFingerprints-baseline.GlobalFingerprints)
	fmt.Fprintf(b, "| mean cache-shape prefix hashes | %.1f | %.1f | %+.1f |\n", baseline.MeanShapePrefixHashes, whale.MeanShapePrefixHashes, whale.MeanShapePrefixHashes-baseline.MeanShapePrefixHashes)
	fmt.Fprintf(b, "| global cache-shape prefix hashes | %d | %d | %+d |\n", baseline.GlobalShapePrefixHashes, whale.GlobalShapePrefixHashes, whale.GlobalShapePrefixHashes-baseline.GlobalShapePrefixHashes)
	fmt.Fprintf(b, "| mean runtime hashes | %.1f | %.1f | %+.1f |\n", baseline.MeanShapeRuntimeHashes, whale.MeanShapeRuntimeHashes, whale.MeanShapeRuntimeHashes-baseline.MeanShapeRuntimeHashes)
	fmt.Fprintf(b, "| global runtime hashes | %d | %d | %+d |\n", baseline.GlobalShapeRuntimeHashes, whale.GlobalShapeRuntimeHashes, whale.GlobalShapeRuntimeHashes-baseline.GlobalShapeRuntimeHashes)
	fmt.Fprintf(b, "| mean tool-shape hashes | %.1f | %.1f | %+.1f |\n", baseline.MeanShapeToolsHashes, whale.MeanShapeToolsHashes, whale.MeanShapeToolsHashes-baseline.MeanShapeToolsHashes)
	fmt.Fprintf(b, "| global tool-shape hashes | %d | %d | %+d |\n", baseline.GlobalShapeToolsHashes, whale.GlobalShapeToolsHashes, whale.GlobalShapeToolsHashes-baseline.GlobalShapeToolsHashes)
	fmt.Fprintf(b, "| mean request hashes | %.1f | %.1f | %+.1f |\n", baseline.MeanShapeRequestHashes, whale.MeanShapeRequestHashes, whale.MeanShapeRequestHashes-baseline.MeanShapeRequestHashes)
	fmt.Fprintf(b, "| truncated runs | %d | %d | %+d |\n", baseline.Truncated, whale.Truncated, whale.Truncated-baseline.Truncated)
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
	fmt.Fprintf(b, "| cache hit | %s |\n", pctFloat(stats.MeanRunCacheHitRatio))
	fmt.Fprintf(b, "| token-weighted cache hit | %s |\n", pctFloat(stats.TokenWeightedCacheHitRatio))
	fmt.Fprintf(b, "| warm cache hit | %s |\n", pctFloat(stats.WarmCacheHitRatio))
	fmt.Fprintf(b, "| mean cost / task | $%.6f |\n", stats.MeanCost)
	fmt.Fprintf(b, "| mean cache savings / task | $%.6f |\n", stats.MeanCacheSavings)
	fmt.Fprintf(b, "| mean uncached cost / task | $%.6f |\n", stats.MeanUncachedCost)
	fmt.Fprintf(b, "| mean turns | %.1f |\n", stats.MeanTurns)
	fmt.Fprintf(b, "| mean tool calls | %.1f |\n", stats.MeanToolCalls)
	fmt.Fprintf(b, "| mean immutable prefix fingerprints | %.1f |\n", stats.MeanFingerprints)
	fmt.Fprintf(b, "| global immutable prefix fingerprints | %d |\n", stats.GlobalFingerprints)
	fmt.Fprintf(b, "| mean cache-shape prefix hashes | %.1f |\n", stats.MeanShapePrefixHashes)
	fmt.Fprintf(b, "| global cache-shape prefix hashes | %d |\n", stats.GlobalShapePrefixHashes)
	fmt.Fprintf(b, "| mean runtime hashes | %.1f |\n", stats.MeanShapeRuntimeHashes)
	fmt.Fprintf(b, "| global runtime hashes | %d |\n", stats.GlobalShapeRuntimeHashes)
	fmt.Fprintf(b, "| mean tool-shape hashes | %.1f |\n", stats.MeanShapeToolsHashes)
	fmt.Fprintf(b, "| global tool-shape hashes | %d |\n", stats.GlobalShapeToolsHashes)
	fmt.Fprintf(b, "| mean request hashes | %.1f |\n", stats.MeanShapeRequestHashes)
	fmt.Fprintf(b, "| truncated runs | %d |\n", stats.Truncated)
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
	stats.MeanRunCacheHitRatio = meanFloat(results, func(r runResult) float64 { return r.CacheHitRatio })
	totals := aggregateUsage(results)
	stats.TokenWeightedCacheHitRatio = totals.CacheHitRatio()
	stats.WarmCacheHitRatio = totals.WarmCacheHitRatio()
	stats.MeanCost = meanCost(results)
	stats.MeanCacheSavings = meanFloat(results, func(r runResult) float64 { return r.CacheSavingsUSD })
	stats.MeanUncachedCost = meanFloat(results, func(r runResult) float64 { return r.UncachedCostUSD })
	stats.MeanTurns = meanInt(results, func(r runResult) int { return r.Turns })
	stats.MeanToolCalls = meanInt(results, func(r runResult) int { return r.ToolCalls })
	stats.MeanFingerprints = meanInt(results, func(r runResult) int { return r.PrefixFingerprints })
	stats.GlobalFingerprints = len(totals.PrefixFingerprints)
	stats.MeanShapePrefixHashes = meanInt(results, func(r runResult) int { return r.ShapePrefixHashes })
	stats.GlobalShapePrefixHashes = len(totals.ShapePrefixHashes)
	stats.MeanShapeRuntimeHashes = meanInt(results, func(r runResult) int { return r.ShapeRuntimeHashes })
	stats.GlobalShapeRuntimeHashes = len(totals.ShapeRuntimeHashes)
	stats.MeanShapeToolsHashes = meanInt(results, func(r runResult) int { return r.ShapeToolsHashes })
	stats.GlobalShapeToolsHashes = len(totals.ShapeToolsHashes)
	stats.MeanShapeRequestHashes = meanInt(results, func(r runResult) int { return r.ShapeRequestHashes })
	stats.GlobalShapeRequestHashes = len(totals.ShapeRequestHashes)
	for _, r := range results {
		if r.Truncated {
			stats.Truncated++
		}
	}
	return stats
}

func aggregateUsage(results []runResult) usageTotals {
	out := newUsageTotals()
	for _, r := range results {
		out.PromptTokens += r.PromptTokens
		out.CompletionTokens += r.CompletionTokens
		out.CacheHitTokens += r.CacheHitTokens
		out.CacheMissTokens += r.CacheMissTokens
		out.WarmCacheHitTokens += r.WarmCacheHitTokens
		out.WarmCacheMissTokens += r.WarmCacheMissTokens
		out.CostUSD += r.CostUSD
		out.CacheSavingsUSD += r.CacheSavingsUSD
		recordHashes(out.PrefixFingerprints, r.PrefixFingerprintValues)
		recordHashes(out.ShapePrefixHashes, r.ShapePrefixHashValues)
		recordHashes(out.ShapeRuntimeHashes, r.ShapeRuntimeHashValues)
		recordHashes(out.ShapeToolsHashes, r.ShapeToolsHashValues)
		recordHashes(out.ShapeRequestHashes, r.ShapeRequestHashValues)
	}
	return out
}

func recordHashes(dst map[string]bool, values []string) {
	for _, value := range values {
		recordHash(dst, value)
	}
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

func meanFloat(results []runResult, fn func(runResult) float64) float64 {
	if len(results) == 0 {
		return 0
	}
	var sum float64
	for _, r := range results {
		sum += fn(r)
	}
	return sum / float64(len(results))
}

func pctFloat(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}

func signedPctDelta(v float64) string {
	return fmt.Sprintf("%+.1fpp", v*100)
}

func signedDollarDelta(v float64) string {
	return fmt.Sprintf("%s$%.6f", sign(v), abs(v))
}

func costRatioText(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("x%.2f", v)
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
