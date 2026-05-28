package agent

import (
	"context"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/telemetry"
)

func (a *Agent) emitBudgetWarningIfNeeded(ctx context.Context, sessionID string, turnCost float64, events chan<- AgentEvent) bool {
	if a.budgetWarningUSD <= 0 {
		return true
	}
	if turnCost <= 0 {
		return true
	}
	meta, err := a.sessionRuntime.LoadMeta(sessionID)
	if err != nil {
		return true
	}
	spent := meta.TotalCostUSD
	percent := int((spent / a.budgetWarningUSD) * 100)
	if percent >= 100 {
		return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeBudgetWarning, Budget: &BudgetWarningInfo{CapUSD: a.budgetWarningUSD, SpentUSD: spent, Percent: 100, TurnCostUSD: turnCost}})
	} else if percent >= 80 {
		if _, loaded := a.budgetWarned80.LoadOrStore(sessionID, true); loaded {
			return true
		}
		return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeBudgetWarning, Budget: &BudgetWarningInfo{CapUSD: a.budgetWarningUSD, SpentUSD: spent, Percent: 80, TurnCostUSD: turnCost}})
	}
	return true
}

func (a *Agent) recordTurnCost(sessionID string, usage llm.Usage, modelName, prefixFingerprint string) float64 {
	cost := telemetry.EstimateTurnUSD(modelName, usage)
	if cost <= 0 {
		return 0
	}

	if a.sessionRuntime != nil && a.sessionRuntime.Enabled() {
		_, _ = a.sessionRuntime.UpdateMeta(sessionID, func(meta *session.SessionMeta) {
			meta.TotalCostUSD += cost
		})
	}
	_ = telemetry.AppendUsage(a.usageLogPath, sessionID, modelName, prefixFingerprint, usage, cost, time.Now(), a.usageMetadata(sessionID))
	return cost
}

func (a *Agent) usageMetadata(sessionID string) telemetry.UsageMetadata {
	if a == nil || a.sessionRuntime == nil || !a.sessionRuntime.Enabled() {
		return telemetry.UsageMetadata{}
	}
	meta, err := a.sessionRuntime.LoadMeta(sessionID)
	if err != nil || !strings.EqualFold(strings.TrimSpace(meta.Kind), "subagent") {
		return telemetry.UsageMetadata{}
	}
	return telemetry.UsageMetadata{
		Kind:                "subagent",
		ParentSessionID:     strings.TrimSpace(meta.ParentSessionID),
		SubagentRole:        strings.TrimSpace(meta.Role),
		SubagentTaskPreview: previewUsageTask(meta.Task, 80),
	}
}

func previewUsageTask(task string, maxRunes int) string {
	task = strings.Join(strings.Fields(task), " ")
	if maxRunes <= 0 || len([]rune(task)) <= maxRunes {
		return task
	}
	runes := []rune(task)
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func buildPrefixCacheMetrics(model string, usage llm.Usage, fingerprint string) *PrefixCacheMetricsInfo {
	prompt := usage.PromptTokens
	cached := usage.PromptCacheHitTokens
	missed := usage.PromptCacheMissTokens
	if prompt <= 0 && cached <= 0 && missed <= 0 {
		return nil
	}
	ratio := 0.0
	if denom := cached + missed; denom > 0 && cached > 0 {
		ratio = float64(cached) / float64(denom)
	}
	return &PrefixCacheMetricsInfo{
		Model:             model,
		PrefixFingerprint: fingerprint,
		PromptTokens:      prompt,
		CachedTokens:      cached,
		CacheHitRatio:     ratio,
	}
}

func (a *Agent) budgetExceeded(sessionID string) (float64, bool) {
	if a.budgetWarningUSD <= 0 || a.sessionRuntime == nil || !a.sessionRuntime.Enabled() {
		return 0, false
	}
	meta, err := a.sessionRuntime.LoadMeta(sessionID)
	if err != nil {
		return 0, false
	}
	return meta.TotalCostUSD, meta.TotalCostUSD >= a.budgetWarningUSD
}
