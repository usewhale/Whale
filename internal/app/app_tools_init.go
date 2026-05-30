package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	whalemcp "github.com/usewhale/whale/internal/mcp"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/tools"
	"strings"
)

func initAppTools(cfg Config, start StartOptions, workspaceRoot string) (appToolInit, error) {
	toolset, err := tools.NewToolset(workspaceRoot)
	if err != nil {
		return appToolInit{}, fmt.Errorf("init tools failed: %w", err)
	}
	toolset.SetWorktreeContext(start.Worktree.Path, start.Worktree.OriginalWorkspace)
	toolset.SetSkillDisabled(cfg.SkillsDisabled)
	mcpConfigPath := strings.TrimSpace(cfg.MCPConfigPath)
	if mcpConfigPath == "" {
		mcpConfigPath = whalemcp.DefaultConfigPath(cfg.DataDir)
	}
	mcpConfig, err := whalemcp.LoadConfig(mcpConfigPath)
	if err != nil {
		return appToolInit{}, fmt.Errorf("load mcp config: %w", err)
	}
	mcpManager := whalemcp.NewManager(mcpConfig, workspaceRoot)
	pluginManager := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspaceRoot}, cfg.PluginsDisabled)
	pluginTools := pluginManager.Tools()
	toolset.SetExtraSkills(pluginManager.Skills())
	baseTools := append([]core.Tool{}, toolset.Tools()...)
	baseToolRegistry, err := core.NewToolRegistryChecked(baseTools)
	if err != nil {
		return appToolInit{}, fmt.Errorf("init base tool registry failed: %w", err)
	}
	hooks, hookSources, hookLoadErr := agent.LoadHooks(workspaceRoot, cfg.DataDir)
	if hookLoadErr != nil {
		return appToolInit{}, fmt.Errorf("load hooks failed: %w", hookLoadErr)
	}
	hookRunner := agent.NewHookRunner(hooks, workspaceRoot)
	hookRunner.AddHandlers(pluginManager.Hooks()...)
	return appToolInit{
		toolset:          toolset,
		mcpManager:       mcpManager,
		pluginManager:    pluginManager,
		pluginTools:      pluginTools,
		baseTools:        baseTools,
		baseToolRegistry: baseToolRegistry,
		hooks:            hooks,
		hookRunner:       hookRunner,
		hookSources:      hookSources,
	}, nil
}
