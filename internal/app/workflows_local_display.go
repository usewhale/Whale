package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

func workflowTaskMetricsValue(task *workflowTaskSnapshot) string {
	if task == nil {
		return ""
	}
	parts := []string{}
	if tokens := workflowTaskTokenValue(task); tokens != "" {
		parts = append(parts, tokens)
	}
	if task.ToolCalls > 0 {
		label := "tools"
		if task.ToolCalls == 1 {
			label = "tool"
		}
		parts = append(parts, fmt.Sprintf("%d %s", task.ToolCalls, label))
	}
	if elapsed := workflowTaskElapsedValue(task); elapsed != "" {
		parts = append(parts, elapsed)
	}
	return strings.Join(parts, " · ")
}

func workflowTaskTokenValue(task *workflowTaskSnapshot) string {
	if task == nil {
		return ""
	}
	out := task.CompletionTokens
	if out <= 0 {
		return ""
	}
	return formatWorkflowCount(out) + " out"
}

func workflowTaskElapsedValue(task *workflowTaskSnapshot) string {
	if task == nil {
		return ""
	}
	if task.DurationMS > 0 {
		return formatWorkflowDuration(time.Duration(task.DurationMS) * time.Millisecond)
	}
	if task.Status == workflow.TaskStatusRunning && !task.StartedAt.IsZero() {
		elapsed := time.Since(task.StartedAt)
		if elapsed > 0 {
			return formatWorkflowDuration(elapsed)
		}
	}
	return ""
}

func workflowTasksForPhase(tasks []*workflowTaskSnapshot, phase string) []*workflowTaskSnapshot {
	out := []*workflowTaskSnapshot{}
	phase = normalizeWorkflowPhaseName(phase)
	for _, task := range tasks {
		taskPhase := normalizeWorkflowPhaseName(task.Phase)
		if taskPhase == phase || (phase == "Tasks" && taskPhase == "") {
			out = append(out, task)
		}
	}
	return out
}

func workflowDeclaredPhaseNames(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return workflowStringSlice(v)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch phase := item.(type) {
		case map[string]any:
			if title := strings.TrimSpace(workflowLocalString(phase["title"])); title != "" {
				out = append(out, title)
			}
		default:
			if title := strings.TrimSpace(workflowLocalString(phase)); title != "" {
				out = append(out, title)
			}
		}
	}
	return out
}

func workflowTaskStatusCounts(tasks []*workflowTaskSnapshot) (done, running, failed, cancelled, cached int) {
	for _, task := range tasks {
		if task.Cached {
			cached++
		}
		switch task.Status {
		case workflow.TaskStatusCompleted:
			done++
		case workflow.TaskStatusFailed:
			failed++
		case workflow.TaskStatusCancelled:
			cancelled++
		default:
			running++
		}
	}
	return done, running, failed, cancelled, cached
}

func workflowDisplayTaskStatus(task *workflowTaskSnapshot) string {
	if task == nil {
		return "running"
	}
	if task.Cached && task.Status == workflow.TaskStatusCompleted {
		return "cached"
	}
	switch task.Status {
	case workflow.TaskStatusCompleted:
		return "done"
	case workflow.TaskStatusFailed:
		return "failed"
	case workflow.TaskStatusCancelled:
		return "cancelled"
	default:
		return "running"
	}
}

func workflowTaskTone(status string) string {
	switch status {
	case workflow.TaskStatusCompleted:
		return "info"
	case workflow.TaskStatusFailed:
		return "error"
	case workflow.TaskStatusCancelled:
		return "warn"
	default:
		return ""
	}
}

func workflowTaskModel(ev workflow.RunEvent) string {
	if ev.Data == nil {
		return ""
	}
	return strings.TrimSpace(workflowLocalString(ev.Data["model"]))
}

func workflowTaskActorKind(ev workflow.RunEvent) string {
	if ev.Data == nil {
		return ""
	}
	return strings.TrimSpace(workflowLocalString(ev.Data["actor_kind"]))
}

func workflowEventLabel(ev workflow.RunEvent) string {
	base := strings.TrimSpace(ev.Type)
	if base == "" {
		base = "event"
	}
	if ev.WorkflowName != "" && (ev.Type == workflow.EventWorkflowStarted || ev.Type == workflow.EventWorkflowCompleted || ev.Type == workflow.EventWorkflowFailed) {
		return base + " · " + ev.WorkflowName
	}
	if ev.TaskID != "" {
		return base + " · " + string(ev.TaskID)
	}
	if ev.Phase != "" && ev.Type != workflow.EventPhaseStarted {
		return base + " · " + ev.Phase
	}
	return base
}

func workflowEventValue(ev workflow.RunEvent) string {
	parts := []string{}
	if !ev.Time.IsZero() {
		parts = append(parts, formatWorkflowTime(ev.Time))
	}
	if ev.Status != "" {
		parts = append(parts, ev.Status)
	}
	if ev.WorkflowName != "" {
		parts = append(parts, "workflow "+ev.WorkflowName)
	}
	if ev.ParentTaskID != "" {
		parts = append(parts, "parent "+string(ev.ParentTaskID))
	}
	if ev.Phase != "" {
		parts = append(parts, "phase "+ev.Phase)
	}
	if ev.Label != "" {
		parts = append(parts, ev.Label)
	}
	if workflowEventCached(ev) {
		parts = append(parts, "cached")
	}
	if msg := strings.TrimSpace(ev.Message); msg != "" {
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " · ")
}

func workflowEventCached(ev workflow.RunEvent) bool {
	if ev.Data == nil {
		return false
	}
	if cached, _ := ev.Data["cached"].(bool); cached {
		return true
	}
	resumeData, _ := ev.Data["resume"].(map[string]any)
	cached, _ := resumeData["cached"].(bool)
	return cached
}

func workflowEventTone(ev workflow.RunEvent) string {
	switch ev.Type {
	case workflow.EventRunFailed, workflow.EventTaskFailed, workflow.EventWorkflowFailed:
		return "error"
	case workflow.EventRunCancelled, workflow.EventTaskCancelled:
		return "warn"
	case workflow.EventRunCompleted, workflow.EventTaskCompleted, workflow.EventWorkflowCompleted, workflow.EventBudgetUpdated:
		return "info"
	default:
		return ""
	}
}

func workflowUsageValues(v any) workflowUsageSnapshot {
	data, ok := v.(map[string]any)
	if !ok || data == nil {
		return workflowUsageSnapshot{}
	}
	usage := workflowUsageSnapshot{}
	usage.PromptTokens, _ = workflowInt64Value(data["prompt_tokens"])
	usage.CompletionTokens, _ = workflowInt64Value(data["completion_tokens"])
	usage.TotalTokens, _ = workflowInt64Value(data["total_usage_tokens"])
	usage.PromptCacheHit, _ = workflowInt64Value(data["prompt_cache_hit_tokens"])
	usage.PromptCacheMiss, _ = workflowInt64Value(data["prompt_cache_miss_tokens"])
	usage.ReasoningReplay, _ = workflowInt64Value(data["reasoning_replay_tokens"])
	usage.ToolReplayTokens, _ = workflowInt64Value(data["tool_result_replay_tokens"])
	usage.ToolRawTokens, _ = workflowInt64Value(data["tool_result_raw_tokens"])
	usage.ToolTokensSaved, _ = workflowInt64Value(data["tool_result_tokens_saved"])
	usage.ToolCompacted, _ = workflowInt64Value(data["tool_results_compacted"])
	return usage
}
