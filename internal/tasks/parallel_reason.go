package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

type ParallelReasonRequest struct {
	Prompts   []string `json:"prompts"`
	Model     string   `json:"model,omitempty"`
	MaxTokens int      `json:"max_tokens,omitempty"`
}

type ParallelReasonResult struct {
	Index  int       `json:"index"`
	Prompt string    `json:"prompt"`
	Output string    `json:"output,omitempty"`
	Error  string    `json:"error,omitempty"`
	Usage  llm.Usage `json:"usage,omitempty"`
}

type ParallelReasonResponse struct {
	Model   string                 `json:"model"`
	Results []ParallelReasonResult `json:"results"`
	Usage   llm.Usage              `json:"usage"`
}

func (r *Runner) ParallelReason(ctx context.Context, req ParallelReasonRequest) (ParallelReasonResponse, error) {
	prompts := compactPrompts(req.Prompts)
	if len(prompts) == 0 {
		return ParallelReasonResponse{}, errors.New("prompts is required")
	}
	if len(prompts) > MaxParallelPrompts {
		return ParallelReasonResponse{}, fmt.Errorf("prompts supports at most %d items", MaxParallelPrompts)
	}
	if r.providerFactory == nil {
		return ParallelReasonResponse{}, errors.New("provider factory is not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = r.defaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = r.defaultMaxTokens
	}
	results := make([]ParallelReasonResult, len(prompts))
	var wg sync.WaitGroup
	for i, prompt := range prompts {
		i, prompt := i, prompt
		results[i] = ParallelReasonResult{Index: i, Prompt: prompt}
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, usage, err := r.runOneReasoningQuery(ctx, model, maxTokens, prompt)
			results[i].Output = output
			results[i].Usage = usage
			if err != nil {
				results[i].Error = err.Error()
			}
		}()
	}
	wg.Wait()
	var usage llm.Usage
	for _, res := range results {
		usage = addUsage(usage, res.Usage)
	}
	return ParallelReasonResponse{Model: model, Results: results, Usage: usage}, nil
}

func (r *Runner) runOneReasoningQuery(ctx context.Context, model string, maxTokens int, prompt string) (string, llm.Usage, error) {
	provider, err := r.providerFactory(model, r.defaultEffort, maxTokens)
	if err != nil {
		return "", llm.Usage{}, err
	}
	history := []core.Message{
		{Role: core.RoleSystem, Text: "You are a cheap parallel reasoning worker. Answer only the assigned subquery, be concise, and do not use tools."},
		{Role: core.RoleUser, Text: prompt},
	}
	var b strings.Builder
	var final string
	var usage llm.Usage
	for ev := range provider.StreamResponse(ctx, history, nil) {
		switch ev.Type {
		case llm.EventContentDelta:
			b.WriteString(ev.Content)
		case llm.EventComplete:
			if ev.Response != nil {
				final = ev.Response.Content
				usage = ev.Response.Usage
			}
		case llm.EventError:
			if ev.Err != nil {
				return strings.TrimSpace(core.FirstNonEmpty(final, b.String())), usage, ev.Err
			}
			return strings.TrimSpace(core.FirstNonEmpty(final, b.String())), usage, errors.New("provider error")
		}
	}
	return strings.TrimSpace(core.FirstNonEmpty(final, b.String())), usage, ctx.Err()
}
