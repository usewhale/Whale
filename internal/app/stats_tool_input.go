package app

import (
	"bufio"
	"encoding/json"
	"github.com/usewhale/whale/internal/telemetry"
	"os"
	"path/filepath"
	"strings"
)

func readToolInputStats(sessionsDir string) toolInputStats {
	stats := toolInputStats{
		ByRepairKind: map[string]int{},
		ByTool:       map[string]*toolInputToolStats{},
		ByModel:      map[string]*toolInputModelStats{},
		ByErrorCode:  map[string]int{},
	}
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return stats
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), telemetry.ToolInputEventsSuffix) {
			continue
		}
		readToolInputEventFile(filepath.Join(sessionsDir, entry.Name()), &stats)
	}
	return stats
}

func readToolInputEventFile(path string, stats *toolInputStats) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.ToolInputEvent
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		switch rec.Event {
		case "tool_input_repaired":
			stats.Repaired++
			if rec.RepairKind != "" {
				stats.ByRepairKind[rec.RepairKind]++
			}
			updateToolInputToolStats(stats, rec.Tool, true)
			updateToolInputModelStats(stats, rec.Model, true)
		case "tool_input_invalid":
			stats.Invalid++
			if rec.ErrorCode != "" {
				stats.ByErrorCode[rec.ErrorCode]++
			}
			updateToolInputToolStats(stats, rec.Tool, false)
			updateToolInputModelStats(stats, rec.Model, false)
		default:
			continue
		}
		stats.Recent = appendRecentToolInput(stats.Recent, rec)
	}
}

func updateToolInputToolStats(stats *toolInputStats, tool string, repaired bool) {
	tool = nonEmpty(tool, "(unknown)")
	ts := stats.ByTool[tool]
	if ts == nil {
		ts = &toolInputToolStats{Tool: tool}
		stats.ByTool[tool] = ts
	}
	if repaired {
		ts.Repaired++
	} else {
		ts.Invalid++
	}
}

func updateToolInputModelStats(stats *toolInputStats, model string, repaired bool) {
	model = nonEmpty(model, "(unknown)")
	ms := stats.ByModel[model]
	if ms == nil {
		ms = &toolInputModelStats{Model: model}
		stats.ByModel[model] = ms
	}
	if repaired {
		ms.Repaired++
	} else {
		ms.Invalid++
	}
}
