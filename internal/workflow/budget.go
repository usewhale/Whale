package workflow

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/usewhale/whale/internal/llm"
)

type workflowBudget struct {
	total *int64
	spent atomic.Int64
}

func newWorkflowBudget(totalTokens *int) (*workflowBudget, error) {
	if totalTokens != nil && *totalTokens <= 0 {
		return nil, errors.New("budgetTokens must be a positive integer")
	}
	b := &workflowBudget{}
	if totalTokens != nil {
		total := int64(*totalTokens)
		b.total = &total
	}
	return b, nil
}

func (b *workflowBudget) totalValue() (int64, bool) {
	if b == nil || b.total == nil {
		return 0, false
	}
	return *b.total, true
}

func (b *workflowBudget) spentValue() int64 {
	if b == nil {
		return 0
	}
	return b.spent.Load()
}

func (b *workflowBudget) remainingValue() (int64, bool) {
	total, ok := b.totalValue()
	if !ok {
		return 0, false
	}
	spent := b.spentValue()
	if spent >= total {
		return 0, true
	}
	return total - spent, true
}

func (b *workflowBudget) checkCanStart() error {
	total, ok := b.totalValue()
	if !ok {
		return nil
	}
	spent := b.spentValue()
	if spent >= total {
		return fmt.Errorf("workflow budget exceeded: spent %d completion tokens >= budget %d", spent, total)
	}
	return nil
}

func (b *workflowBudget) addUsage(usage llm.Usage) int64 {
	if b == nil || usage.CompletionTokens <= 0 {
		return b.spentValue()
	}
	return b.spent.Add(int64(usage.CompletionTokens))
}

func (b *workflowBudget) eventData(usage llm.Usage) map[string]any {
	data := map[string]any{
		"spent_tokens":       b.spentValue(),
		"prompt_tokens":      usage.PromptTokens,
		"completion_tokens":  usage.CompletionTokens,
		"total_usage_tokens": usage.TotalTokens,
	}
	addUsageBreakdown(data, usage)
	if total, ok := b.totalValue(); ok {
		data["total_budget_tokens"] = total
		if remaining, _ := b.remainingValue(); remaining >= 0 {
			data["remaining_tokens"] = remaining
		}
	} else {
		data["total_budget_tokens"] = nil
		data["remaining_tokens"] = "Infinity"
	}
	return data
}

func (b *workflowBudget) scriptReadyData() map[string]any {
	return b.eventData(llm.Usage{})
}

func usageEventData(usage llm.Usage) map[string]any {
	data := map[string]any{
		"prompt_tokens":      usage.PromptTokens,
		"completion_tokens":  usage.CompletionTokens,
		"total_usage_tokens": usage.TotalTokens,
	}
	addUsageBreakdown(data, usage)
	return data
}

func addUsageBreakdown(data map[string]any, usage llm.Usage) {
	data["prompt_cache_hit_tokens"] = usage.PromptCacheHitTokens
	data["prompt_cache_miss_tokens"] = usage.PromptCacheMissTokens
	data["reasoning_replay_tokens"] = usage.ReasoningReplayTokens
	data["tool_result_raw_tokens"] = usage.ToolResultRawTokens
	data["tool_result_replay_tokens"] = usage.ToolResultReplayTokens
	data["tool_result_tokens_saved"] = usage.ToolResultTokensSaved
	data["tool_results_compacted"] = usage.ToolResultsCompacted
}
