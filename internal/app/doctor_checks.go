package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/memory"
	"os"
	"path/filepath"
	"strings"
)

func doctorCheckAPIReach(ctx context.Context, key string) DoctorCheck {
	if strings.TrimSpace(key) == "" {
		return DoctorCheck{
			Label:  "api reach",
			Level:  DoctorWarn,
			Detail: "skipped — no API key configured",
		}
	}
	msg, err := CheckDeepSeekAPIReachability(ctx, key)
	if err != nil {
		level := DoctorFail
		if errors.Is(err, errDoctorAuth) {
			level = DoctorFail
		}
		return DoctorCheck{
			Label:  "api reach",
			Level:  level,
			Detail: msg,
		}
	}
	return DoctorCheck{
		Label:  "api reach",
		Level:  DoctorOK,
		Detail: msg,
	}
}

func doctorCheckMemory(workspaceRoot string, fileOrder []string, maxChars int) DoctorCheck {
	pm, ok := memory.ReadProjectMemory(workspaceRoot, fileOrder, maxChars)
	if !ok {
		return DoctorCheck{
			Label:  "project doc",
			Level:  DoctorWarn,
			Detail: fmt.Sprintf("no project doc file found (%s)", strings.Join(fileOrder, ", ")),
		}
	}
	detail := pm.Path
	if pm.Truncated {
		detail += " (truncated)"
	}
	return DoctorCheck{
		Label:  "project doc",
		Level:  DoctorOK,
		Detail: detail,
	}
}

func doctorCheckConfig(loaded LoadedConfig, err error) DoctorCheck {
	if err != nil {
		return DoctorCheck{
			Label:  "config",
			Level:  DoctorFail,
			Detail: err.Error(),
		}
	}
	sources := ConfigSources(loaded)
	if len(sources) == 0 {
		return DoctorCheck{
			Label:  "config",
			Level:  DoctorOK,
			Detail: "no config.toml or config.local.toml found — defaults will be used",
		}
	}
	return DoctorCheck{
		Label:  "config",
		Level:  DoctorOK,
		Detail: strings.Join(sources, ", "),
	}
}

func doctorCheckLegacyConfig(dataDir, workspaceRoot string, hasActiveConfig bool) DoctorCheck {
	paths := []string{
		preferencesPath(dataDir),
		filepath.Join(dataDir, "settings.json"),
		filepath.Join(workspaceRoot, ".whale", "settings.json"),
	}
	found := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		} else if err != nil && !os.IsNotExist(err) {
			return DoctorCheck{
				Label:  "legacy config",
				Level:  DoctorFail,
				Detail: fmt.Sprintf("%s unreadable — %v", path, err),
			}
		}
	}
	if len(found) == 0 {
		return DoctorCheck{
			Label:  "legacy config",
			Level:  DoctorOK,
			Detail: "no obsolete config files found",
		}
	}
	if hasActiveConfig {
		return DoctorCheck{
			Label:  "legacy config",
			Level:  DoctorWarn,
			Detail: fmt.Sprintf("%d obsolete Whale v0.1.8-or-earlier file(s) ignored — config.toml is active; no migration needed", len(found)),
		}
	}
	return DoctorCheck{
		Label:  "legacy config",
		Level:  DoctorWarn,
		Detail: fmt.Sprintf("%d obsolete Whale v0.1.8-or-earlier file(s) found — run `whale migrate-config` if you used those versions", len(found)),
	}
}

func doctorCheckHooks(dataDir, workspaceRoot string) DoctorCheck {
	loaded, err := LoadConfigFiles(dataDir, workspaceRoot)
	if err != nil {
		return DoctorCheck{
			Label:  "hooks",
			Level:  DoctorFail,
			Detail: err.Error(),
		}
	}
	totalHooks := 0
	loadedFiles := 0
	if loaded.ProjectLoaded {
		totalHooks += countFileConfigHooks(loaded.Project)
		loadedFiles++
	}
	if loaded.ProjectLocalLoaded {
		totalHooks += countFileConfigHooks(loaded.ProjectLocal)
		loadedFiles++
	}
	if loaded.GlobalLoaded {
		totalHooks += countFileConfigHooks(loaded.Global)
		loadedFiles++
	}
	if totalHooks == 0 {
		return DoctorCheck{}
	}
	return DoctorCheck{
		Label:  "hooks",
		Level:  DoctorOK,
		Detail: fmt.Sprintf("%d hook(s) from %d file(s)", totalHooks, loadedFiles),
	}
}

func countHooks(st agent.HookSettings) int {
	n := 0
	for _, hooks := range st.Hooks {
		for _, hook := range hooks {
			if strings.TrimSpace(hook.Command) != "" {
				n++
			}
		}
	}
	return n
}
