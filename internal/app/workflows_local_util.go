package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

func filterWorkflowRunsForSession(runs []workflow.Run, sessionID string, limit int) []workflow.Run {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		if limit > 0 && len(runs) > limit {
			return append([]workflow.Run(nil), runs[:limit]...)
		}
		return append([]workflow.Run(nil), runs...)
	}
	filtered := make([]workflow.Run, 0, len(runs))
	for _, run := range runs {
		if workflowRunSessionID(run) != sessionID {
			continue
		}
		filtered = append(filtered, run)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func workflowRunSessionID(run workflow.Run) string {
	for _, ev := range run.Events {
		if ev.Type == workflow.EventRunStarted {
			return strings.TrimSpace(ev.SessionID)
		}
	}
	return ""
}

func containsWorkflowString(values []string, value string) bool {
	value = normalizeWorkflowPhaseName(value)
	for _, candidate := range values {
		if normalizeWorkflowPhaseName(candidate) == value {
			return true
		}
	}
	return false
}

func normalizeWorkflowPhaseName(phase string) string {
	return strings.TrimSpace(phase)
}

func lastWorkflowStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[len(values)-limit:]...)
}

func workflowBudgetValueFromDataMap(v any) string {
	data, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	return workflowBudgetValue(data)
}

func workflowBudgetValue(data map[string]any) string {
	if data == nil {
		return ""
	}
	spent, ok := workflowNumberString(data["spent_tokens"])
	if !ok {
		return ""
	}
	total, totalOK := workflowNumberString(data["total_budget_tokens"])
	remaining, remainingOK := workflowNumberString(data["remaining_tokens"])
	if !remainingOK {
		if s := strings.TrimSpace(workflowLocalString(data["remaining_tokens"])); s != "" {
			remaining = s
			remainingOK = true
		}
	}
	if totalOK {
		if remainingOK {
			return fmt.Sprintf("%s/%s completion tokens · %s remaining", spent, total, remaining)
		}
		return fmt.Sprintf("%s/%s completion tokens", spent, total)
	}
	if remainingOK {
		return fmt.Sprintf("%s completion tokens · %s remaining", spent, remaining)
	}
	return spent + " completion tokens"
}

func workflowNumberString(v any) (string, bool) {
	switch x := v.(type) {
	case int:
		return fmt.Sprintf("%d", x), true
	case int64:
		return fmt.Sprintf("%d", x), true
	case int32:
		return fmt.Sprintf("%d", x), true
	case float64:
		return fmt.Sprintf("%.0f", x), true
	case float32:
		return fmt.Sprintf("%.0f", x), true
	default:
		return "", false
	}
}

func workflowInt64Value(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case int32:
		return int64(x), true
	case float64:
		return int64(x), true
	case float32:
		return int64(x), true
	default:
		return 0, false
	}
}

func workflowStringSliceLen(v any) int {
	return len(workflowStringSlice(v))
}

func workflowStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(workflowLocalString(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func formatWorkflowCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

func formatWorkflowDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	if d < time.Hour {
		minutes := int(d / time.Minute)
		seconds := int((d % time.Minute).Round(time.Second) / time.Second)
		if seconds == 60 {
			minutes++
			seconds = 0
		}
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := int(d / time.Hour)
	minutes := int((d % time.Hour).Round(time.Minute) / time.Minute)
	if minutes == 60 {
		hours++
		minutes = 0
	}
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

func workflowStatusTone(status string) string {
	switch status {
	case workflow.RunStatusCompleted:
		return "info"
	case workflow.RunStatusFailed, workflow.RunStatusCancelled:
		return "error"
	default:
		return ""
	}
}

func formatWorkflowTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func workflowLocalString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}
