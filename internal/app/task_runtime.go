package app

import (
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/skills"
	"github.com/usewhale/whale/internal/tasks"
	"github.com/usewhale/whale/internal/tools"
	"github.com/usewhale/whale/internal/workflow"
)

func (a *App) rebuildTaskRuntimeLocked() error {
	if a == nil {
		return nil
	}
	effort := a.reasoningEffort
	thinking := a.thinkingEnabled
	apiKey := a.apiKey
	cfg := a.cfg
	providerFactory := func(model string, maxTokens int) (llm.Provider, error) {
		if strings.TrimSpace(model) == "" {
			model = defaults.DefaultModel
		}
		return newDeepSeekProvider(providerOptions{
			APIKey:                   apiKey,
			BaseURL:                  cfg.APIBaseURL,
			Model:                    model,
			ReasoningEffort:          effort,
			ThinkingEnabled:          thinking,
			MaxTokens:                maxTokens,
			RetryPolicy:              retryPolicyFromConfig(cfg),
			StreamMaxAttempts:        cfg.RetryStreamMaxAttempts,
			StreamIdleTimeout:        cfg.RetryStreamIdleTimeout,
			DeepSeekPrefixCompletion: cfg.DeepSeekPrefixCompletion,
			DeepSeekMultimodal:       cfg.DeepSeekMultimodal,
		})
	}
	providerFactoryWithOptions := func(req tasks.ProviderRequest) (llm.Provider, error) {
		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = defaults.DefaultModel
		}
		reqEffort := normalizeEffort(core.FirstNonEmpty(strings.TrimSpace(req.Effort), effort))
		return newDeepSeekProvider(providerOptions{
			APIKey:                   apiKey,
			BaseURL:                  cfg.APIBaseURL,
			Model:                    model,
			ReasoningEffort:          reqEffort,
			ThinkingEnabled:          thinking,
			MaxTokens:                req.MaxTokens,
			RetryPolicy:              retryPolicyFromConfig(cfg),
			StreamMaxAttempts:        cfg.RetryStreamMaxAttempts,
			StreamIdleTimeout:        cfg.RetryStreamIdleTimeout,
			DeepSeekPrefixCompletion: cfg.DeepSeekPrefixCompletion,
			DeepSeekMultimodal:       cfg.DeepSeekMultimodal,
		})
	}
	workspaceTools := func(workspace tasks.ToolWorkspace) (*core.ToolRegistry, error) {
		toolset, err := tools.NewToolset(workspace.WorkspaceRoot)
		if err != nil {
			return nil, err
		}
		toolset.SetWorktreeContext(workspace.WorktreeRoot, workspace.OriginalWorkspace)
		toolset.SetExecBoundaryPolicy(policy.RulePolicy{
			Default:       a.permissionPolicy.Default,
			Rules:         append([]policy.PermissionRule(nil), a.permissionPolicy.Rules...),
			WorkspaceRoot: workspace.WorkspaceRoot,
			WorktreeRoot:  workspace.WorktreeRoot,
		})
		toolset.SetExecBoundaryApproval(func() string { return a.sessionID }, func(req policy.ApprovalRequest) policy.ApprovalDecision {
			a.approvalMu.Lock()
			defer a.approvalMu.Unlock()
			if a.autoAcceptPermissions {
				return policy.ApprovalAllow
			}
			return a.approvalFn(req)
		})
		toolset.SetSkillDisabled(cfg.SkillsDisabled)
		if a.pluginManager != nil {
			toolset.SetExtraSkills(a.pluginManager.Skills())
		}
		toolset.SetWebFetchExtractor(newDeepSeekWebFetchExtractor(webFetchExtractorOptions{
			APIKey:  apiKey,
			BaseURL: cfg.APIBaseURL,
		}))
		return core.NewToolRegistryChecked(toolset.Tools())
	}
	var extraSkills []*skills.Skill
	if a.pluginManager != nil {
		extraSkills = a.pluginManager.Skills()
	}
	taskRunner := tasks.NewRunner(tasks.RunnerConfig{
		ProviderFactory:            providerFactory,
		ProviderFactoryWithOptions: providerFactoryWithOptions,
		ParentTools:                a.subagentToolRegistry,
		WorkspaceTools:             workspaceTools,
		AgentDefinitions:           tasks.NewAgentDefinitionLibraryWithDefinitions(a.workspaceRoot, taskAgentDefinitions(a.pluginAgents)),
		ParentPolicy:               a.permissionPolicy,
		MessageStore:               a.msgStore,
		SessionsDir:                a.sessionsDir,
		ParentSessionIDFunc:        func() string { return a.sessionID },
		WorkspaceRoot:              a.workspaceRoot,
		MemoryEnabled:              cfg.MemoryEnabled,
		MemoryMaxChars:             cfg.MemoryMaxChars,
		MemoryFileOrder:            parseCSVList(cfg.MemoryFileOrder),
		SkillsDisabled:             cfg.SkillsDisabled,
		ExtraSkills:                extraSkills,
		AutoCompact:                cfg.AutoCompact,
		AutoCompactThreshold:       cfg.AutoCompactThreshold,
		DefaultModel:               defaults.DefaultModel,
		DefaultMaxTokens:           tasks.DefaultMaxTokens,
		DefaultMaxToolIters:        tasks.DefaultMaxToolIters,
		SummaryMaxChars:            tasks.DefaultSummaryMaxChar,
		UsageLogPath:               filepath.Join(cfg.DataDir, "usage.jsonl"),
		ApprovalFunc: func(req policy.ApprovalRequest) policy.ApprovalDecision {
			a.approvalMu.Lock()
			defer a.approvalMu.Unlock()
			if a.autoAcceptPermissions {
				return policy.ApprovalAllow
			}
			return a.approvalFn(req)
		},
	})
	a.taskTools = tasks.NewTools(taskRunner)
	a.workflowManager = nil
	a.workflowRunner = nil
	a.workflowTools = nil
	if cfg.WorkflowsEnabled {
		workflowStore, err := workflow.NewFileRunEventStore(cfg.DataDir)
		if err != nil {
			return err
		}
		workflowScheduler := workflow.NewTaskScheduler(workflowStore, taskRunner)
		a.workflowManager = workflow.NewRunManager(workflowStore, workflowScheduler)
		a.workflowRunner = workflow.NewScriptRunner(cfg.DataDir, a.workflowManager)
		a.workflowRunner.Library = workflow.NewLibrary(a.workspaceRoot)
		a.workflowTools = []core.Tool{workflow.NewToolWithOptions(a.workflowRunner, workflow.ToolOptions{
			ParentSessionIDFunc:   func() string { return a.sessionID },
			KeywordTriggerEnabled: cfg.WorkflowKeywordTrigger,
		})}
	}
	return nil
}
