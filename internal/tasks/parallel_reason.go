package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	if r.providerFactory == nil && r.providerFactoryWithOptions == nil {
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
	for i, prompt := range prompts {
		results[i] = ParallelReasonResult{Index: i, Prompt: prompt}
	}

	// Workers report through a buffered channel (one slot per prompt) instead of
	// writing the shared results slice. The buffer guarantees a worker can always
	// send without blocking — even one whose provider stream hangs and ignores
	// ctx will not wedge on send — and confining all writes to results[] to this
	// goroutine keeps the collection data-race free. We then wait on the channel
	// with a ctx.Done() escape so a single hung worker can no longer deadlock the
	// whole call (the user's "关不掉"); the hung worker goroutine is abandoned.
	type workerResult struct {
		index  int
		output string
		usage  llm.Usage
		err    error
	}
	resCh := make(chan workerResult, len(prompts))
	for i, prompt := range prompts {
		i, prompt := i, prompt
		go func() {
			output, usage, err := r.runOneReasoningQuery(ctx, model, maxTokens, prompt)
			resCh <- workerResult{index: i, output: output, usage: usage, err: err}
		}()
	}

	done := make([]bool, len(prompts))
	collected := 0
	apply := func(res workerResult) {
		if done[res.index] {
			return
		}
		results[res.index].Output = res.output
		results[res.index].Usage = res.usage
		if res.err != nil {
			results[res.index].Error = res.err.Error()
		}
		done[res.index] = true
		collected++
	}

waitLoop:
	for collected < len(prompts) {
		select {
		case res := <-resCh:
			apply(res)
		case <-ctx.Done():
			break waitLoop
		}
	}
	// If ctx fired with workers still outstanding, take any results already
	// produced without blocking, then record per-result cancellation for the
	// rest. ParallelReason still returns a nil top-level error: cancellation is
	// reported per result, matching the existing contract.
	for collected < len(prompts) {
		select {
		case res := <-resCh:
			apply(res)
		default:
			msg := "canceled"
			if cerr := ctx.Err(); cerr != nil {
				msg = cerr.Error()
			}
			for i := range results {
				if !done[i] {
					results[i].Error = msg
					done[i] = true
					collected++
				}
			}
		}
	}

	var usage llm.Usage
	for _, res := range results {
		usage = addUsage(usage, res.Usage)
	}
	return ParallelReasonResponse{Model: model, Results: results, Usage: usage}, nil
}

func (r *Runner) runOneReasoningQuery(ctx context.Context, model string, maxTokens int, prompt string) (string, llm.Usage, error) {
	provider, err := r.newProvider(model, maxTokens, "")
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
