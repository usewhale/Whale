package telemetry

import (
	"strings"

	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
)

type Pricing struct {
	InputMissUSDPerMTok float64
	InputHitUSDPerMTok  float64
	OutputUSDPerMTok    float64
}

var defaultPricing = map[string]Pricing{
	// Source: https://api-docs.deepseek.com/quick_start/pricing
	// Checked 2026-05-12. DeepSeek v4 pro prices below use the current 75%
	// discount, which DeepSeek says runs through 2026-05-31 15:59 UTC.
	defaults.DefaultModel: {InputMissUSDPerMTok: 0.14, InputHitUSDPerMTok: 0.0028, OutputUSDPerMTok: 0.28},
	defaults.ProModel:     {InputMissUSDPerMTok: 0.435, InputHitUSDPerMTok: 0.003625, OutputUSDPerMTok: 0.87},
}

func pricingForModel(model string) Pricing {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(m, "pro") {
		return defaultPricing[defaults.ProModel]
	}
	return defaultPricing[defaults.DefaultModel]
}

func EstimateTurnUSD(model string, usage llm.Usage) float64 {
	p := pricingForModel(model)
	promptNonCache := max(usage.PromptTokens-usage.PromptCacheHitTokens, 0)
	promptCache := max(usage.PromptCacheHitTokens, 0)
	completion := max(usage.CompletionTokens, 0)
	return (float64(promptNonCache)/1_000_000.0)*p.InputMissUSDPerMTok +
		(float64(promptCache)/1_000_000.0)*p.InputHitUSDPerMTok +
		(float64(completion)/1_000_000.0)*p.OutputUSDPerMTok
}

func EstimateUsageRecordUSD(rec UsageRecord) float64 {
	return EstimateTurnUSD(rec.Model, llm.Usage{
		PromptTokens:          rec.PromptTokens,
		CompletionTokens:      rec.CompletionTokens,
		PromptCacheHitTokens:  rec.PromptCacheHit,
		PromptCacheMissTokens: rec.PromptCacheMiss,
	})
}

func EstimateCacheSavingsUSD(model string, cacheHitTokens int) float64 {
	if cacheHitTokens <= 0 {
		return 0
	}
	p := pricingForModel(model)
	return (float64(cacheHitTokens) / 1_000_000.0) * (p.InputMissUSDPerMTok - p.InputHitUSDPerMTok)
}

func EstimateUsageRecordCacheSavingsUSD(rec UsageRecord) float64 {
	if rec.CacheSavingsUSD > 0 {
		return rec.CacheSavingsUSD
	}
	return EstimateCacheSavingsUSD(rec.Model, rec.PromptCacheHit)
}

func InputCostUSD(model string, usage llm.Usage) float64 {
	p := pricingForModel(model)
	promptNonCache := max(usage.PromptTokens-usage.PromptCacheHitTokens, 0)
	promptCache := max(usage.PromptCacheHitTokens, 0)
	return (float64(promptNonCache)/1_000_000.0)*p.InputMissUSDPerMTok +
		(float64(promptCache)/1_000_000.0)*p.InputHitUSDPerMTok
}

func OutputCostUSD(model string, usage llm.Usage) float64 {
	p := pricingForModel(model)
	return (float64(max(usage.CompletionTokens, 0)) / 1_000_000.0) * p.OutputUSDPerMTok
}

type TurnStats struct {
	Turn               int
	Model              string
	Usage              llm.Usage
	CostUSD            float64
	InputCostUSD       float64
	OutputCostUSD      float64
	CacheHitRatio      float64
	ReasoningReplayTok int
}

func BuildTurnStats(turn int, model string, usage llm.Usage) TurnStats {
	cost := EstimateTurnUSD(model, usage)
	inCost := InputCostUSD(model, usage)
	outCost := OutputCostUSD(model, usage)
	hit := max(usage.PromptCacheHitTokens, 0)
	miss := max(usage.PromptCacheMissTokens, 0)
	denom := hit + miss
	ratio := 0.0
	if denom > 0 {
		ratio = float64(hit) / float64(denom)
	}
	return TurnStats{
		Turn:               turn,
		Model:              model,
		Usage:              usage,
		CostUSD:            cost,
		InputCostUSD:       inCost,
		OutputCostUSD:      outCost,
		CacheHitRatio:      ratio,
		ReasoningReplayTok: max(usage.ReasoningReplayTokens, 0),
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
