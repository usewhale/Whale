package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

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
		lines = append(lines, workflowTerminalResultLines(resultFields)...)
	} else if strings.TrimSpace(summary.Summary) != "" {
		lines = append(lines, summary.Summary)
	} else if strings.TrimSpace(run.Error) != "" {
		lines = append(lines, strings.TrimSpace(run.Error))
	}
	lines = append(lines, "", "Full run details: open /workflows")
	if runtime := workflowTerminalRuntimeLine(stats); runtime != "" {
		lines = append(lines, runtime)
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
	return &LocalResult{
		Kind:      "workflow-terminal",
		Title:     title,
		Fields:    fields,
		Sections:  sections,
		PlainText: strings.Join(lines, "\n"),
	}
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
