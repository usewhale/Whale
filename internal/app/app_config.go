package app

import (
	"time"

	"github.com/usewhale/whale/internal/defaults"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/store"
)

func DefaultConfig() Config {
	return Config{
		DataDir:                 store.DefaultDataDir(),
		PermissionDefault:       policy.PermissionAllow,
		PermissionRules:         policy.DefaultRules(),
		AutoCompact:             true,
		AutoCompactThreshold:    defaults.DefaultAutoCompactThreshold,
		MemoryEnabled:           true,
		MemoryMaxChars:          defaults.DefaultMemoryMaxChars,
		MemoryFileOrder:         defaults.DefaultMemoryFileOrderCSV,
		Model:                   defaults.DefaultModel,
		ReasoningEffort:         defaults.DefaultReasoningEffort,
		ThinkingEnabled:         defaults.DefaultThinkingEnabled,
		CheckForUpdateOnStartup: true,
		ViewMode:                ViewModeDefault,
		ShowReasoning:           false,
		RetryMaxAttempts:        llmretry.DefaultPolicy().MaxAttempts,
		RetryStreamMaxAttempts:  6,
		RetryStreamIdleTimeout:  90 * time.Second,
		RetryMaxDelay:           llmretry.DefaultPolicy().MaxDelay,
		WorkflowsEnabled:        false,
		WorkflowKeywordTrigger:  true,
		configDefaulted:         true,
	}
}
