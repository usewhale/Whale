package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

const workflowTerminalPlainTextLimit = 12000

func (a *App) BuildWorkflowTerminalLocalResult(runID string) *LocalResult {
	run, res, err := a.loadWorkflowRunForLocalResult(runID)
	if err != nil {
		text := "Workflow\n\nerror: " + err.Error()
		return &LocalResult{Kind: "workflow-terminal", Title: "Workflow", Fields: []LocalResultField{{Label: "Error", Value: err.Error(), Tone: "error"}}, PlainText: text}
	}
	if res != nil {
		res.Kind = "workflow-terminal"
		return res
	}
	if run.Status == "" || run.Status == workflow.RunStatusRunning {
		return nil
	}
	summary := workflowRunSummary(run)
	snapshot := buildWorkflowSnapshot(run)
	name := workflowRunDisplayName(run, snapshot)
	stats := workflowTerminalStats(run, snapshot)
	title := fmt.Sprintf("Dynamic workflow %q %s", name, summary.Status)
	lines := []string{title}
	if metrics := workflowTerminalHeaderMetrics(stats); metrics != "" {
		lines[0] += " · " + metrics
	}
	lines = append(lines, "")
	resultFields := workflowResultDisplayFields(summary.Result)
	if len(resultFields) > 0 {
		lines = append(lines, workflowTerminalResultLinesFor(summary.Result, resultFields)...)
	} else if strings.TrimSpace(summary.Summary) != "" {
		lines = append(lines, summary.Summary)
	} else if strings.TrimSpace(run.Error) != "" {
		lines = append(lines, strings.TrimSpace(run.Error))
	}
	resultPath := a.writeWorkflowResultFile(run.ID, summary.Result)
	if resultPath != "" {
		lines = append(lines, "", "Full result:", resultPath)
	} else {
		lines = append(lines, "", "Full run details: open /workflows")
	}
	if runtime := workflowTerminalRuntimeLine(stats); runtime != "" {
		lines = append(lines, "", "Runtime:", strings.TrimSpace(strings.TrimPrefix(runtime, "Runtime:")))
	}
	failedFields := workflowTerminalFailedFields(snapshot.Tasks, 3)
	if len(failedFields) > 0 {
		lines = append(lines, "Failed subagents:")
		for _, field := range failedFields {
			lines = append(lines, "- "+field.Label+": "+field.Value)
		}
	}
	fields := []LocalResultField{
		{Label: "Run", Value: string(run.ID)},
		{Label: "Workflow", Value: name},
		{Label: "Status", Value: summary.Status, Tone: workflowStatusTone(summary.Status)},
		{Label: "Details", Value: "/workflows"},
	}
	if shortSummary := workflowTerminalShortSummary(summary.Summary); shortSummary != "" {
		fields = append(fields, LocalResultField{Label: workflowRunSummaryLabel(summary.Status), Value: shortSummary, Tone: workflowRunSummaryTone(summary.Status)})
	}
	if stats.Duration != "" {
		fields = append(fields, LocalResultField{Label: "Duration", Value: stats.Duration})
	}
	if stats.CompletionTokens > 0 {
		fields = append(fields, LocalResultField{Label: "Output tokens", Value: formatWorkflowCount(stats.CompletionTokens)})
	}
	sections := []LocalResultSection{{
		Title: "Runtime",
		Fields: []LocalResultField{
			{Label: "Agents", Value: workflowTerminalAgentCount(stats)},
			{Label: "Tool calls", Value: fmt.Sprintf("%d", stats.ToolCalls)},
		},
	}}
	if len(resultFields) > 0 {
		sections = append([]LocalResultSection{{Title: "Result", Fields: resultFields}}, sections...)
	}
	if len(failedFields) > 0 {
		sections = append(sections, LocalResultSection{Title: "Failed subagents", Fields: failedFields})
	}
	plainText := workflowTerminalLimitPlainText(strings.Join(lines, "\n"))
	if resultPath != "" && !strings.Contains(plainText, resultPath) {
		plainText = strings.TrimSpace(plainText) + "\n\nFull result:\n" + resultPath
	}
	return &LocalResult{
		Kind:      "workflow-terminal",
		Title:     title,
		Fields:    fields,
		Sections:  sections,
		PlainText: plainText,
	}
}

type workflowRunDirStore interface {
	RunDir(workflow.RunID) (string, error)
}

func (a *App) writeWorkflowResultFile(runID workflow.RunID, result any) string {
	if result == nil || a == nil || a.workflowManager == nil || a.workflowManager.Store == nil {
		return ""
	}
	store, ok := a.workflowManager.Store.(workflowRunDirStore)
	if !ok {
		return ""
	}
	dir, err := store.RunDir(runID)
	if err != nil {
		return ""
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return ""
	}
	return path
}

func workflowTerminalLimitPlainText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= workflowTerminalPlainTextLimit {
		return text
	}
	return strings.TrimSpace(string(runes[:workflowTerminalPlainTextLimit])) + "\n\n... output truncated in chat; open /workflows or the full result file for the complete structured result."
}

type workflowTerminalStatsSummary struct {
	Total            int
	Completed        int
	Failed           int
	Cancelled        int
	Running          int
	ToolCalls        int
	CompletionTokens int64
	TotalTokens      int64
	Duration         string
}

func workflowRunDisplayName(run workflow.Run, snapshot workflowSnapshot) string {
	for _, ev := range run.Events {
		if ev.Type != workflow.EventScriptReady {
			continue
		}
		if ev.Data != nil {
			if name := strings.TrimSpace(workflowLocalString(ev.Data["name"])); name != "" {
				return name
			}
		}
		if name := strings.TrimSpace(ev.Message); name != "" {
			return name
		}
	}
	for _, task := range snapshot.Tasks {
		if task != nil && strings.TrimSpace(task.Label) != "" && task.IsChild {
			return strings.TrimSpace(task.Label)
		}
	}
	return string(run.ID)
}

func workflowTerminalShortSummary(summary string) string {
	const maxRunes = 240
	text := strings.Join(strings.Fields(summary), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func workflowTerminalStats(run workflow.Run, snapshot workflowSnapshot) workflowTerminalStatsSummary {
	stats := workflowTerminalStatsSummary{Total: len(snapshot.Tasks)}
	for _, task := range snapshot.Tasks {
		if task == nil {
			continue
		}
		switch task.Status {
		case workflow.TaskStatusCompleted:
			stats.Completed++
		case workflow.TaskStatusFailed:
			stats.Failed++
		case workflow.TaskStatusCancelled:
			stats.Cancelled++
		default:
			stats.Running++
		}
		stats.ToolCalls += task.ToolCalls
		stats.CompletionTokens += task.CompletionTokens
		stats.TotalTokens += task.TotalTokens
	}
	if !run.Started.IsZero() && !run.Ended.IsZero() {
		stats.Duration = formatWorkflowDuration(run.Ended.Sub(run.Started))
	} else if !run.Started.IsZero() {
		stats.Duration = formatWorkflowDuration(time.Since(run.Started))
	}
	return stats
}

func workflowTerminalHeaderMetrics(stats workflowTerminalStatsSummary) string {
	parts := []string{}
	if stats.Total > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d agents", stats.Completed, stats.Total))
	}
	if stats.Duration != "" {
		parts = append(parts, stats.Duration)
	}
	if stats.CompletionTokens > 0 {
		parts = append(parts, formatWorkflowCount(stats.CompletionTokens)+" out")
	}
	return strings.Join(parts, " · ")
}

func workflowTerminalRuntimeLine(stats workflowTerminalStatsSummary) string {
	parts := []string{}
	if stats.Total > 0 {
		parts = append(parts, workflowTerminalAgentCount(stats))
	}
	if stats.Duration != "" {
		parts = append(parts, stats.Duration)
	}
	if stats.CompletionTokens > 0 {
		parts = append(parts, formatWorkflowCount(stats.CompletionTokens)+" out")
	}
	if stats.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool calls", stats.ToolCalls))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Runtime: " + strings.Join(parts, " · ")
}

func workflowTerminalAgentCount(stats workflowTerminalStatsSummary) string {
	parts := []string{}
	if stats.Total > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d completed", stats.Completed, stats.Total))
	}
	if stats.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", stats.Failed))
	}
	if stats.Cancelled > 0 {
		parts = append(parts, fmt.Sprintf("%d cancelled", stats.Cancelled))
	}
	if stats.Running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", stats.Running))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " · ")
}

func workflowTerminalResultLines(fields []LocalResultField) []string {
	lines := []string{}
	for i, field := range fields {
		label := strings.TrimSpace(field.Label)
		value := strings.TrimSpace(field.Value)
		if value == "" {
			continue
		}
		if i == 0 && (label == "Answer" || label == "Summary") {
			lines = append(lines, value)
			continue
		}
		if label == "" {
			lines = append(lines, value)
			continue
		}
		lines = append(lines, label+":")
		for _, line := range strings.Split(value, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, "- "+line)
		}
	}
	return lines
}

func workflowTerminalResultLinesFor(result any, fields []LocalResultField) []string {
	obj, ok := result.(map[string]any)
	if !ok {
		return workflowTerminalResultLines(fields)
	}
	lines := []string{}
	used := map[string]bool{}
	if key, text := workflowTerminalPrimaryResultText(obj); text != "" {
		lines = append(lines, text)
		used[key] = true
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		if used[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return lines
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, "Result:")
	for _, key := range keys {
		preview := workflowTerminalResultPreview(obj[key])
		if preview == "" {
			continue
		}
		label := workflowTerminalHumanizeIdentifier(key)
		lines = append(lines, label+": "+preview)
		for _, item := range workflowTerminalCollectionItemPreviews(obj[key], 3) {
			lines = append(lines, "  "+item)
		}
	}
	return lines
}

func workflowTerminalPrimaryResultText(obj map[string]any) (string, string) {
	for _, key := range []string{"report", "summary", "answer", "decision", "verdict"} {
		if text := strings.TrimSpace(workflowLocalString(obj[key])); text != "" {
			return key, text
		}
	}
	return "", ""
}

func workflowTerminalResultPreview(value any) string {
	if value == nil {
		return "null"
	}
	switch v := value.(type) {
	case string:
		return workflowTerminalCompactText(v, 240)
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return workflowTerminalFormatNumber(v)
	case json.Number:
		return v.String()
	case []any:
		if len(v) == 0 {
			return "[]"
		}
		return fmt.Sprintf("%d items", len(v))
	case []string:
		if len(v) == 0 {
			return "[]"
		}
		return fmt.Sprintf("%d items", len(v))
	case map[string]any:
		if len(v) == 0 {
			return "{}"
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<%T>", value)
	}
	return workflowTerminalCompactText(string(data), 240)
}

func workflowTerminalCollectionItemPreviews(value any, limit int) []string {
	if limit <= 0 {
		return nil
	}
	var items []any
	switch v := value.(type) {
	case []any:
		items = v
	case []string:
		items = make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, item)
		}
	default:
		return nil
	}
	out := []string{}
	for i, item := range items {
		if i >= limit {
			break
		}
		out = append(out, fmt.Sprintf("%d. %s", i+1, workflowTerminalCollectionItemPreview(item)))
	}
	if len(items) > limit {
		out = append(out, fmt.Sprintf("... %d more", len(items)-limit))
	}
	return out
}

func workflowTerminalCollectionItemPreview(value any) string {
	obj, ok := value.(map[string]any)
	if !ok {
		return workflowTerminalResultPreview(value)
	}
	prefixes := []string{}
	for _, key := range []string{"severity", "confidence", "dimension", "category"} {
		if text := workflowTerminalCompactText(workflowLocalString(obj[key]), 40); text != "" {
			prefixes = append(prefixes, text)
		}
	}
	title := ""
	for _, key := range []string{"title", "name", "claim", "finding", "summary"} {
		if text := workflowTerminalCompactText(workflowLocalString(obj[key]), 160); text != "" {
			title = text
			break
		}
	}
	if title == "" {
		return workflowTerminalResultPreview(value)
	}
	if len(prefixes) > 0 {
		return "[" + strings.Join(prefixes, " · ") + "] " + title
	}
	return title
}

func workflowTerminalCompactText(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func workflowTerminalFormatNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func workflowTerminalHumanizeIdentifier(value string) string {
	var out []rune
	var prev rune
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' && ((prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')) {
			out = append(out, ' ')
		}
		if r == '_' || r == '-' {
			out = append(out, ' ')
		} else {
			out = append(out, r)
		}
		prev = r
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

func workflowTerminalFailedFields(tasks []*workflowTaskSnapshot, limit int) []LocalResultField {
	fields := []LocalResultField{}
	for _, task := range tasks {
		if task == nil || (task.Status != workflow.TaskStatusFailed && task.Status != workflow.TaskStatusCancelled) {
			continue
		}
		value := strings.TrimSpace(task.Error)
		if value == "" {
			value = strings.TrimSpace(task.Message)
		}
		if value == "" {
			value = task.Status
		}
		label := strings.TrimSpace(task.Label)
		if label == "" {
			label = string(task.ID)
		}
		fields = append(fields, LocalResultField{Label: label, Value: value, Tone: workflowTaskTone(task.Status)})
		if limit > 0 && len(fields) >= limit {
			break
		}
	}
	return fields
}
