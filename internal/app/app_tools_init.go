package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	whalemcp "github.com/usewhale/whale/internal/mcp"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/tools"
	"strings"
)

func initAppTools(cfg Config, start StartOptions, workspaceRoot string) (appToolInit, error) {
	toolset, err := tools.NewToolset(workspaceRoot)
	if err != nil {
		return appToolInit{}, fmt.Errorf("init tools failed: %w", err)
	}
	toolset.SetWorktreeContext(start.Worktree.Path, start.Worktree.OriginalWorkspace)
	toolset.SetForegroundShellWait(cfg.ShellForegroundWaitDefaultMS, cfg.ShellForegroundWaitMaxMS)
	toolset.SetExecBoundaryPolicy(policy.RulePolicy{
		Default:       cfg.PermissionDefault,
		Rules:         append([]policy.PermissionRule(nil), cfg.PermissionRules...),
		WorkspaceRoot: workspaceRoot,
		WorktreeRoot:  start.Worktree.Path,
	})
	toolset.SetSkillDisabled(cfg.SkillsDisabled)
	mcpConfigPath := strings.TrimSpace(cfg.MCPConfigPath)
	if mcpConfigPath == "" {
		mcpConfigPath = whalemcp.DefaultConfigPath(cfg.DataDir)
	}
	mcpConfig, err := whalemcp.LoadConfig(mcpConfigPath)
	if err != nil {
		return appToolInit{}, fmt.Errorf("load mcp config: %w", err)
	}
	pluginManager := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspaceRoot}, cfg.Plugins)
	pluginOutcome := pluginManager.Outcome()
	mergePluginMCPServers(&mcpConfig, pluginOutcome.MCPServers)
	mcpManager := whalemcp.NewManager(mcpConfig, workspaceRoot)
	pluginTools := pluginOutcome.Tools
	toolset.SetExtraSkills(pluginOutcome.Skills)
	baseTools := append([]core.Tool{}, toolset.Tools()...)
	baseToolRegistry, err := core.NewToolRegistryChecked(baseTools)
	if err != nil {
		return appToolInit{}, fmt.Errorf("init base tool registry failed: %w", err)
	}
	subagentTools := append([]core.Tool{}, baseTools...)
	subagentTools = append(subagentTools, pluginTools...)
	subagentToolRegistry, err := core.NewToolRegistryChecked(subagentTools)
	if err != nil {
		return appToolInit{}, fmt.Errorf("init subagent tool registry failed: %w", err)
	}
	hooks, hookSources, hookLoadErr := agent.LoadHooks(workspaceRoot, cfg.DataDir)
	if hookLoadErr != nil {
		return appToolInit{}, fmt.Errorf("load hooks failed: %w", hookLoadErr)
	}
	hookStates, err := LoadHookStates(cfg.DataDir, workspaceRoot)
	if err != nil {
		return appToolInit{}, fmt.Errorf("load hook state failed: %w", err)
	}
	allHooks := append([]agent.ResolvedHook{}, hooks...)
	allHooks = append(allHooks, pluginOutcome.CommandHooks...)
	hookRunner := agent.NewHookRunnerWithState(allHooks, workspaceRoot, hookStates)
	hookRunner.AddHandlers(pluginOutcome.HookHandlers...)
	return appToolInit{
		toolset:              toolset,
		mcpManager:           mcpManager,
		pluginManager:        pluginManager,
		pluginTools:          pluginTools,
		pluginAgents:         pluginOutcome.Agents,
		baseTools:            baseTools,
		baseToolRegistry:     baseToolRegistry,
		subagentToolRegistry: subagentToolRegistry,
		hooks:                hooks,
		hookStates:           hookStates,
		hookRunner:           hookRunner,
		hookSources:          hookSources,
	}, nil
}

func mergePluginMCPServers(cfg *whalemcp.Config, servers map[string]whalemcp.ServerConfig) {
	if cfg == nil || len(servers) == 0 {
		return
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]whalemcp.ServerConfig{}
	}
	for name, srv := range servers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		srv.Name = name
		cfg.Servers[name] = srv
	}
}
