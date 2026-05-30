package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/tasks"
	"path/filepath"
	"strings"
)

func initAppRuntime(cfg Config, sessionInit appSessionInit, toolInit appToolInit, workspaceRoot string, parentSessionIDFunc func() string) (appRuntimeInit, error) {
	model := core.FirstNonEmpty(strings.TrimSpace(cfg.Model), defaults.DefaultModel)
	effort := normalizeEffort(core.FirstNonEmpty(strings.TrimSpace(cfg.ReasoningEffort), defaults.DefaultReasoningEffort))
	viewMode, err := NormalizeViewMode(cfg.ViewMode)
	if err != nil {
		return appRuntimeInit{}, err
	}
	cfg.ViewMode = viewMode
	thinking := cfg.ThinkingEnabled
	contextWindow := contextWindowForModel(model)
	apiKey, err := LoadDeepSeekAPIKey(cfg.DataDir)
	if err != nil {
		return appRuntimeInit{}, fmt.Errorf("load api key failed: %w", err)
	}
	toolInit.toolset.SetWebFetchExtractor(newDeepSeekWebFetchExtractor(webFetchExtractorOptions{
		APIKey:  apiKey,
		BaseURL: cfg.APIBaseURL,
	}))
	providerFactory := func(model string, maxTokens int) (llm.Provider, error) {
		if strings.TrimSpace(model) == "" {
			model = defaults.DefaultModel
		}
		return newDeepSeekProvider(providerOptions{
			APIKey:            apiKey,
			BaseURL:           cfg.APIBaseURL,
			Model:             model,
			ReasoningEffort:   effort,
			ThinkingEnabled:   thinking,
			MaxTokens:         maxTokens,
			RetryPolicy:       retryPolicyFromConfig(cfg),
			StreamMaxAttempts: cfg.RetryStreamMaxAttempts,
			StreamIdleTimeout: cfg.RetryStreamIdleTimeout,
		})
	}
	taskRunner := tasks.NewRunner(tasks.RunnerConfig{
		ProviderFactory:      providerFactory,
		ParentTools:          toolInit.baseToolRegistry,
		MessageStore:         sessionInit.msgStore,
		SessionsDir:          sessionInit.sessionsDir,
		ParentSessionID:      sessionInit.sessionID,
		ParentSessionIDFunc:  parentSessionIDFunc,
		WorkspaceRoot:        workspaceRoot,
		MemoryEnabled:        cfg.MemoryEnabled,
		MemoryMaxChars:       cfg.MemoryMaxChars,
		MemoryFileOrder:      parseCSVList(cfg.MemoryFileOrder),
		AutoCompact:          cfg.AutoCompact,
		AutoCompactThreshold: cfg.AutoCompactThreshold,
		DefaultModel:         defaults.DefaultModel,
		DefaultMaxTokens:     tasks.DefaultMaxTokens,
		DefaultMaxToolIters:  tasks.DefaultMaxToolIters,
		SummaryMaxChars:      tasks.DefaultSummaryMaxChar,
		UsageLogPath:         filepath.Join(cfg.DataDir, "usage.jsonl"),
	})
	taskTools := tasks.NewTools(taskRunner)
	registeredTools := append([]core.Tool{}, toolInit.baseTools...)
	registeredTools = append(registeredTools, toolInit.pluginTools...)
	registeredTools = append(registeredTools, taskTools...)
	toolRegistry, err := core.NewToolRegistryChecked(registeredTools)
	if err != nil {
		return appRuntimeInit{}, fmt.Errorf("init tool registry failed: %w", err)
	}
	return appRuntimeInit{
		cfg:           cfg,
		model:         model,
		effort:        effort,
		thinking:      thinking,
		contextWindow: contextWindow,
		apiKey:        apiKey,
		taskTools:     taskTools,
		toolRegistry:  toolRegistry,
	}, nil
}
