package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

// buildDoctorLocalResult builds a structured local result for the /doctor command.
// It combines session storage information with the existing RunDoctor diagnostics.
func buildDoctorLocalResult(a *App) *LocalResult {
	sanitized := core.SanitizeSessionID(a.sessionID)

	// --- Session Storage info ---
	absSessionsDir := a.sessionsDir
	if abs, err := filepath.Abs(a.sessionsDir); err == nil {
		absSessionsDir = abs
	}

	totalSessions := countSessions(absSessionsDir)

	// Gather session file info
	type candidate struct {
		display string
		path    string
	}
	candidates := []candidate{
		{".jsonl", filepath.Join(absSessionsDir, sanitized+".jsonl")},
		{".meta.json", filepath.Join(absSessionsDir, sanitized+".meta.json")},
		{".state.json", filepath.Join(absSessionsDir, sanitized+".state.json")},
		{".todo.json", filepath.Join(absSessionsDir, sanitized+".todo.json")},
		{".user_input.json", filepath.Join(absSessionsDir, sanitized+".user_input.json")},
	}

	var fileFields []LocalResultField
	var fileLines []string
	for _, c := range candidates {
		info, err := os.Stat(c.path)
		if err != nil {
			// Skip non-existent files — they're optional (e.g. todo.json, user_input.json).
			continue
		}
		sz := formatFileSize(info.Size())
		mt := info.ModTime().Format("2006-01-02 15:04:05")
		fileFields = append(fileFields, LocalResultField{
			Label: c.display,
			Value: fmt.Sprintf("%s  %s", sz, mt),
			Tone:  "info",
		})
		fileLines = append(fileLines, fmt.Sprintf("  %s  %9s  %s", c.display, sz, mt))
	}

	// --- Diagnostics via RunDoctor (skip network checks — /doctor is synchronous) ---
	report, err := RunDoctor(a.ctx, a.cfg, a.workspaceRoot, DoctorOptions{SkipNetworkChecks: true})
	diagFields := make([]LocalResultField, 0)
	diagLines := make([]string, 0)
	if err == nil {
		for _, check := range report.Checks {
			if check.Level == "" {
				continue
			}
			// Skip noisy internal checks that aren't actionable.
			if strings.HasPrefix(check.Label, "legacy") {
				continue
			}
			tone := "info"
			symbol := "\u2713"
			switch check.Level {
			case DoctorOK:
				tone = "info"
				symbol = "\u2713"
			case DoctorWarn:
				tone = "warn"
				symbol = "\u26A0"
			case DoctorFail:
				tone = "fail"
				symbol = "\u2717"
			}
			diagFields = append(diagFields, LocalResultField{
				Label: check.Label,
				Value: check.Detail,
				Tone:  tone,
			})
			diagLines = append(diagLines, fmt.Sprintf("  %s %-18s  %s", symbol, check.Label, check.Detail))
		}
	} else {
		diagFields = append(diagFields, LocalResultField{
			Label: "diagnostics",
			Value: fmt.Sprintf("failed: %v", err),
			Tone:  "fail",
		})
		diagLines = append(diagLines, fmt.Sprintf("  \u2717 diagnostics  failed: %v", err))
	}

	// --- PlainText ---
	var b strings.Builder
	b.WriteString("\u2500\u2500 Session Storage \u2500\u2500\n")
	b.WriteString(fmt.Sprintf("  Directory:      %s\n", absSessionsDir))
	b.WriteString(fmt.Sprintf("  Session ID:     %s\n", a.sessionID))
	b.WriteString(fmt.Sprintf("  Total sessions: %d\n", totalSessions))
	b.WriteString("\n  Session files:\n")
	for _, l := range fileLines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\n\u2500\u2500 Diagnostics \u2500\u2500\n")
	for _, l := range diagLines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	plainText := b.String()

	// --- LocalResult ---
	return &LocalResult{
		Kind:  "doctor",
		Title: "Doctor Report",
		Fields: []LocalResultField{
			{Label: "Directory", Value: absSessionsDir, Tone: "info"},
			{Label: "Session ID", Value: a.sessionID},
			{Label: "Total sessions", Value: fmt.Sprintf("%d", totalSessions)},
		},
		Sections: []LocalResultSection{
			{
				Title:  "Session Files",
				Fields: fileFields,
			},
			{
				Title:  "Diagnostics",
				Fields: diagFields,
			},
		},
		PlainText: plainText,
	}
}

// countSessions returns the number of non-subagent session JSONL files in the directory.
func countSessions(sessionsDir string) int {
	summaries, err := session.ListSessions(sessionsDir, 0)
	if err != nil {
		return 0
	}
	return len(summaries)
}

// formatFileSize formats a byte count as a human-readable string.
func formatFileSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.0f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
