package app

import (
	"path/filepath"
	"strings"
	"time"
)

func (a *App) buildStats() string {
	return a.buildStatsViewAt("overview", time.Now())
}

func (a *App) buildStatsView(view string) string {
	return a.buildStatsViewAt(view, time.Now())
}

func (a *App) buildStatsViewAt(view string, now time.Time) string {
	usage := readUsageStats(filepath.Join(a.cfg.DataDir, "usage"), now)
	toolInput := readToolInputStats(a.sessionsDir)

	var lines []string
	switch view {
	case "usage":
		lines = []string{"Stats", "", "Usage"}
		lines = append(lines, formatUsageStats(usage)...)
	case "cache":
		lines = []string{"Stats", "", "Cache diagnostics"}
		lines = append(lines, formatCacheDiagnostics(usage.CacheDiagnostics)...)
	case "tools", "repair":
		lines = []string{"Stats", "", "Tool input"}
		lines = append(lines, formatToolInputStats(toolInput)...)
	case "recent":
		lines = []string{"Stats"}
		lines = append(lines, formatRecentStats(usage, toolInput)...)
	case "profile":
		profile := readProfileStats(a.sessionsDir, filepath.Join(a.cfg.DataDir, "usage"), statsProfileSessionLimit)
		lines = []string{"Stats", "", "Profile"}
		lines = append(lines, formatProfileStats(profile)...)
	case "all":
		lines = []string{"Stats", "", "Usage"}
		lines = append(lines, formatUsageStats(usage)...)
		lines = append(lines, "", "Cache diagnostics")
		lines = append(lines, formatCacheDiagnostics(usage.CacheDiagnostics)...)
		lines = append(lines, "", "Tool input")
		lines = append(lines, formatToolInputStats(toolInput)...)
		lines = append(lines, formatRecentStats(usage, toolInput)...)
	default:
		lines = []string{"Stats"}
		lines = append(lines, formatStatsOverview(usage, toolInput)...)
	}
	return strings.Join(lines, "\n")
}
