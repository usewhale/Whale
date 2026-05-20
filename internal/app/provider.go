package app

import (
	"strings"

	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/llm/deepseek"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
)

type providerOptions struct {
	APIKey            string
	BaseURL           string
	Model             string
	ReasoningEffort   string
	ThinkingEnabled   bool
	MaxTokens         int
	RetryPolicy       llmretry.Policy
	StreamMaxAttempts int
}

func newDeepSeekProvider(opts providerOptions) (llm.Provider, error) {
	dsOpts := []deepseek.Option{}
	if strings.TrimSpace(opts.APIKey) != "" {
		dsOpts = append(dsOpts, deepseek.WithAPIKey(opts.APIKey))
	}
	if strings.TrimSpace(opts.BaseURL) != "" {
		dsOpts = append(dsOpts, deepseek.WithBaseURL(opts.BaseURL))
	}
	if strings.TrimSpace(opts.Model) != "" {
		dsOpts = append(dsOpts, deepseek.WithModel(opts.Model))
	}
	dsOpts = append(dsOpts,
		deepseek.WithReasoningEffort(opts.ReasoningEffort),
		deepseek.WithThinking(opts.ThinkingEnabled),
	)
	if hasRetryPolicy(opts.RetryPolicy) {
		dsOpts = append(dsOpts, deepseek.WithRetryPolicy(opts.RetryPolicy))
	}
	if opts.StreamMaxAttempts > 0 {
		dsOpts = append(dsOpts, deepseek.WithStreamMaxAttempts(opts.StreamMaxAttempts))
	}
	if opts.MaxTokens > 0 {
		dsOpts = append(dsOpts, deepseek.WithMaxTokens(opts.MaxTokens))
	}
	return deepseek.New(dsOpts...)
}

func retryPolicyFromConfig(cfg Config) llmretry.Policy {
	policy := llmretry.DefaultPolicy()
	if cfg.RetryMaxAttempts > 0 {
		policy.MaxAttempts = cfg.RetryMaxAttempts
	}
	if cfg.RetryMaxDelay > 0 {
		policy.MaxDelay = cfg.RetryMaxDelay
	}
	return llmretry.NormalizePolicy(policy)
}

func hasRetryPolicy(policy llmretry.Policy) bool {
	return policy.MaxAttempts != 0 ||
		policy.BaseDelay != 0 ||
		policy.MaxDelay != 0 ||
		policy.Jitter != 0 ||
		policy.RespectRetryAfter ||
		policy.RetryNetwork ||
		policy.RetryStatusCodes != nil
}
