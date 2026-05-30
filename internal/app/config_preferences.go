package app

import (
	"fmt"
	"strings"
)

func SaveGlobalPreferences(dataDir, model, effort string, thinking bool) error {
	path := GlobalConfigPath(dataDir)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return err
	}
	cfg.Model = strings.TrimSpace(model)
	cfg.ReasoningEffort = strings.TrimSpace(effort)
	cfg.ThinkingEnabled = &thinking
	return SaveConfigFile(path, cfg)
}

func SaveGlobalViewMode(dataDir, mode string) error {
	mode, err := NormalizeViewMode(mode)
	if err != nil {
		return err
	}
	path := GlobalConfigPath(dataDir)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return err
	}
	cfg.UI.ViewMode = mode
	return SaveConfigFile(path, cfg)
}

func NormalizeViewMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ViewModeDefault:
		return ViewModeDefault, nil
	case ViewModeFocus:
		return ViewModeFocus, nil
	default:
		return "", fmt.Errorf("invalid ui.view_mode: %s (want %q or %q)", mode, ViewModeDefault, ViewModeFocus)
	}
}

func ConfigSources(loaded LoadedConfig) []string {
	out := make([]string, 0, 3)
	if loaded.ProjectLocalLoaded {
		out = append(out, loaded.ProjectLocalPath)
	}
	if loaded.ProjectLoaded {
		out = append(out, loaded.ProjectPath)
	}
	if loaded.GlobalLoaded {
		out = append(out, loaded.GlobalPath)
	}
	return out
}
