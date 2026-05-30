package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/plugins"
	"strings"
)

func (a *App) SetPluginEnabled(id string, enabled bool) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("plugin id must not be empty")
	}
	if a == nil || a.pluginManager == nil {
		return "", fmt.Errorf("plugins unavailable")
	}
	st, ok := a.pluginManager.Status(id)
	if !ok {
		return "", fmt.Errorf("plugin not found: %s", id)
	}
	id = st.Manifest.ID

	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return "", err
	}
	disabled := disabledNameSet(cfg.Plugins.Disabled)
	enabledSet := disabledNameSet(cfg.Plugins.Enabled)
	if enabled {
		delete(disabled, strings.ToLower(id))
		enabledSet[strings.ToLower(id)] = id
	} else {
		disabled[strings.ToLower(id)] = id
		delete(enabledSet, strings.ToLower(id))
	}
	cfg.Plugins.Disabled = sortedSkillNames(disabled)
	cfg.Plugins.Enabled = sortedSkillNames(enabledSet)
	if err := SaveConfigFile(path, cfg); err != nil {
		return "", err
	}
	if err := a.reloadPluginDisabledConfig(); err != nil {
		return "", err
	}
	pm := plugins.NewManager(plugins.Context{DataDir: a.cfg.DataDir, WorkspaceRoot: a.workspaceRoot}, a.cfg.PluginsDisabled)
	hookRunner := agent.NewHookRunner(a.hooks, a.workspaceRoot)
	hookRunner.AddHandlers(pm.Hooks()...)
	a.toolMu.Lock()
	a.pluginManager = pm
	a.pluginTools = pm.Tools()
	if a.toolset != nil {
		a.toolset.SetExtraSkills(pm.Skills())
	}
	a.hookRunner = hookRunner
	a.toolMu.Unlock()
	if err := a.refreshMCPTools(); err != nil {
		return "", err
	}
	a.a = nil
	if enabled {
		return fmt.Sprintf("enabled plugin: %s\nconfig: %s", id, path), nil
	}
	return fmt.Sprintf("disabled plugin: %s\nconfig: %s", id, path), nil
}
