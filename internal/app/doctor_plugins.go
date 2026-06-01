package app

import (
	"context"
	"fmt"
	"github.com/usewhale/whale/internal/plugins"
)

func doctorCheckPlugins(ctx context.Context, cfg Config, workspaceRoot string) []DoctorCheck {
	mgr := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspaceRoot}, cfg.Plugins)
	var checks []DoctorCheck
	statuses := mgr.Statuses()
	enabled := 0
	for _, st := range statuses {
		if st.Enabled {
			enabled++
		}
	}
	checks = append(checks, DoctorCheck{
		Label:  "plugins",
		Level:  DoctorOK,
		Detail: fmt.Sprintf("%d enabled, %d disabled", enabled, len(statuses)-enabled),
	})
	for _, diag := range mgr.Diagnostics(ctx) {
		level := DoctorOK
		switch diag.Level {
		case plugins.DiagnosticWarn:
			level = DoctorWarn
		case plugins.DiagnosticFail:
			level = DoctorFail
		}
		label := "plugin " + diag.PluginID
		if diag.Label != "" {
			label += " " + diag.Label
		}
		checks = append(checks, DoctorCheck{Label: label, Level: level, Detail: diag.Detail})
	}
	return checks
}
