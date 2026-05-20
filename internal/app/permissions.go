package app

import (
	"strings"

	"github.com/usewhale/whale/internal/policy"
)

func (a *App) SetProjectApprovalMode(mode policy.ApprovalMode) (string, error) {
	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return "", err
	}
	cfg.Permissions.Mode = string(mode)
	if err := SaveConfigFile(path, cfg); err != nil {
		return "", err
	}
	a.approvalMode = mode
	a.cfg.ApprovalMode = string(mode)
	a.a = nil
	return path, nil
}

func (a *App) ClearProjectApprovalMode() (policy.ApprovalMode, string, error) {
	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, loaded, err := LoadConfigFile(path)
	if err != nil {
		return "", "", err
	}
	if loaded {
		cfg.Permissions.Mode = ""
		if err := SaveConfigFile(path, cfg); err != nil {
			return "", "", err
		}
	}

	loadedCfg, err := LoadConfigFiles(a.cfg.DataDir, a.workspaceRoot)
	if err != nil {
		return "", "", err
	}
	effective := DefaultConfig()
	if err := ApplyLoadedConfig(&effective, loadedCfg); err != nil {
		return "", "", err
	}
	mode, err := policy.ParseApprovalMode(strings.TrimSpace(effective.ApprovalMode))
	if err != nil {
		return "", "", err
	}
	a.approvalMode = mode
	a.cfg.ApprovalMode = string(mode)
	a.a = nil
	return mode, path, nil
}
