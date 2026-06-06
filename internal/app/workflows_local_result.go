package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

const workflowsRecentLimit = 10
const workflowsUsage = "usage: /workflows"

func (a *App) buildWorkflowsLocalResult(runID string) *LocalResult {
	runID = strings.TrimSpace(runID)
	if runID != "" {
		return a.buildWorkflowRunLocalResult(runID)
	}
	return a.buildWorkflowRunsLocalResult()
}

func (a *App) WorkflowPanelLocalResult(runID string) *LocalResult {
	return a.buildWorkflowsLocalResult(runID)
}

func (a *App) buildWorkflowRunsLocalResult() *LocalResult {
	runs := []workflow.Run{}
	var err error
	if a != nil && a.workflowManager != nil && a.workflowManager.Store != nil {
		if lister, ok := a.workflowManager.Store.(workflow.RunListStore); ok {
			runs, err = lister.ListRuns(context.Background(), 0)
			runs = filterWorkflowRunsForSession(runs, a.sessionID, workflowsRecentLimit)
		}
	}
	defs, defsErr := a.workflowDefinitions()
	if err != nil {
		text := "Workflows\n\nerror: " + err.Error()
		return &LocalResult{
			Kind:      "workflows",
			Title:     "Workflows",
			Fields:    []LocalResultField{{Label: "Error", Value: err.Error(), Tone: "error"}},
			PlainText: text,
		}
	}
	lines := []string{"Dynamic workflows", "", fmt.Sprintf("runs: %s", workflowRunCountValue(runs))}
	sections := make([]LocalResultSection, 0, len(runs)+len(defs)+1)
	for _, run := range runs {
		summary := workflowRunSummary(run)
		lines = append(lines, "", "- "+string(run.ID))
		lines = append(lines, "  status: "+summary.Status)
		if summary.Summary != "" {
			lines = append(lines, "  summary: "+summary.Summary)
		}
		if summary.CurrentPhase != "" {
			lines = append(lines, "  phase: "+summary.CurrentPhase)
		}
		if summary.Budget != "" {
			lines = append(lines, "  budget: "+summary.Budget)
		}
		lines = append(lines, "  tasks: "+workflowTaskCountValue(summary))
		fields := []LocalResultField{
			{Label: "Status", Value: summary.Status, Tone: workflowStatusTone(summary.Status)},
			{Label: "Tasks", Value: workflowTaskCountValue(summary)},
		}
		if summary.Summary != "" {
			fields = append(fields, LocalResultField{Label: "Summary", Value: summary.Summary})
		}
		if summary.CurrentPhase != "" {
			fields = append(fields, LocalResultField{Label: "Phase", Value: summary.CurrentPhase})
		}
		if summary.Budget != "" {
			fields = append(fields, LocalResultField{Label: "Budget", Value: summary.Budget})
		}
		if !summary.UpdatedAt.IsZero() {
			fields = append(fields, LocalResultField{Label: "Updated", Value: formatWorkflowTime(summary.UpdatedAt)})
		}
		sections = append(sections, LocalResultSection{Title: string(run.ID), Fields: fields})
	}
	if len(runs) == 0 {
		lines = append(lines, "", "No dynamic workflows in this session.")
	}
	if defsErr != nil {
		lines = append(lines, "", "available workflows: error: "+defsErr.Error())
		sections = append(sections, LocalResultSection{Title: "Available workflows", Fields: []LocalResultField{{Label: "Error", Value: defsErr.Error(), Tone: "error"}}})
	} else {
		lines = append(lines, "", fmt.Sprintf("available workflows: %s", workflowDefinitionCountValue(defs)))
		if len(defs) == 0 {
			lines = append(lines, "  none")
		}
		fields := make([]LocalResultField, 0, len(defs))
		for _, def := range defs {
			value := workflowDefinitionValue(def)
			lines = append(lines, "  - "+def.Name+": "+value)
			fields = append(fields, LocalResultField{Label: def.Name, Value: value, Tone: workflowDefinitionTone(def)})
		}
		if len(fields) > 0 {
			sections = append(sections, LocalResultSection{Title: "Available workflows", Fields: fields})
		}
	}
	availableField := LocalResultField{Label: "Available", Value: workflowDefinitionCountValue(defs), Tone: workflowCountTone(len(defs))}
	if defsErr != nil {
		availableField = LocalResultField{Label: "Available", Value: "error", Tone: "error"}
	}
	return &LocalResult{
		Kind:  "workflows",
		Title: "Workflows",
		Fields: []LocalResultField{
			{Label: "Runs", Value: workflowRunCountValue(runs), Tone: workflowCountTone(len(runs))},
			availableField,
		},
		Sections:  sections,
		PlainText: strings.Join(lines, "\n"),
	}
}

func (a *App) workflowDefinitions() ([]workflow.Definition, error) {
	if a == nil || a.workflowRunner == nil || a.workflowRunner.Library == nil {
		return nil, nil
	}
	return a.workflowRunner.Library.List(context.Background())
}

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

func workflowRunCountValue(runs []workflow.Run) string {
	if len(runs) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d recent", len(runs))
}

func workflowDefinitionCountValue(defs []workflow.Definition) string {
	if len(defs) == 0 {
		return "none"
	}
	ready := 0
	for _, def := range defs {
		if def.Status == workflow.DefinitionReady {
			ready++
		}
	}
	if ready == len(defs) {
		return fmt.Sprintf("%d ready", len(defs))
	}
	return fmt.Sprintf("%d ready · %d problem", ready, len(defs)-ready)
}

func workflowCountTone(count int) string {
	if count == 0 {
		return "muted"
	}
	return "info"
}

func workflowDefinitionValue(def workflow.Definition) string {
	status := strings.TrimSpace(string(def.Status))
	if status == "" {
		status = string(workflow.DefinitionReady)
	}
	parts := []string{status}
	if def.Source != "" {
		parts = append(parts, def.Source)
	}
	if def.Description != "" {
		parts = append(parts, def.Description)
	}
	if def.WhenToUse != "" {
		parts = append(parts, "when: "+def.WhenToUse)
	}
	if def.EstimatedAgents > 0 {
		parts = append(parts, fmt.Sprintf("%d agents", def.EstimatedAgents))
	}
	if def.Path != "" {
		parts = append(parts, def.Path)
	}
	if def.Error != "" {
		parts = append(parts, def.Error)
	}
	return strings.Join(parts, " · ")
}

func workflowDefinitionTone(def workflow.Definition) string {
	if def.Status == workflow.DefinitionProblem {
		return "error"
	}
	return "info"
}

func (a *App) buildWorkflowRunLocalResult(runID string) *LocalResult {
	return a.buildWorkflowRunSnapshotLocalResult(runID)
}

func (a *App) loadWorkflowRunForLocalResult(runID string) (workflow.Run, *LocalResult, error) {
	if a == nil || a.workflowManager == nil || a.workflowManager.Store == nil {
		text := "Workflow\n\nerror: workflow store unavailable"
		return workflow.Run{}, &LocalResult{
			Kind:      "workflow",
			Title:     "Workflow",
			Fields:    []LocalResultField{{Label: "Error", Value: "workflow store unavailable", Tone: "error"}},
			PlainText: text,
		}, nil
	}
	run, err := a.workflowManager.Store.LoadRun(context.Background(), workflow.RunID(runID))
	if err != nil {
		text := "Workflow\n\nerror: " + err.Error()
		return workflow.Run{}, &LocalResult{
			Kind:      "workflow",
			Title:     "Workflow",
			Fields:    []LocalResultField{{Label: "Error", Value: err.Error(), Tone: "error"}},
			PlainText: text,
		}, nil
	}
	if len(run.Events) == 0 {
		text := fmt.Sprintf("Workflow\n\nrun: %s\nstatus: not found", runID)
		return workflow.Run{}, &LocalResult{
			Kind:      "workflow",
			Title:     "Workflow",
			Fields:    []LocalResultField{{Label: "Run", Value: runID}, {Label: "Status", Value: "not found", Tone: "muted"}},
			PlainText: text,
		}, nil
	}
	return run, nil, nil
}

func (a *App) buildWorkflowRunSnapshotLocalResult(runID string) *LocalResult {
	run, res, err := a.loadWorkflowRunForLocalResult(runID)
	if err != nil {
		text := "Workflow\n\nerror: " + err.Error()
		return &LocalResult{Kind: "workflow", Title: "Workflow", Fields: []LocalResultField{{Label: "Error", Value: err.Error(), Tone: "error"}}, PlainText: text}
	}
	if res != nil {
		return res
	}
	summary := workflowRunSummary(run)
	snapshot := buildWorkflowSnapshot(run)
	panelSnapshot := buildWorkflowPanelSnapshot(run, summary, snapshot)
	lines := []string{"Workflow", "", "run: " + string(run.ID), "status: " + summary.Status, "tasks: " + workflowTaskCountValue(summary)}
	if summary.Summary != "" {
		lines = append(lines, "summary: "+summary.Summary)
	}
	if summary.CurrentPhase != "" {
		lines = append(lines, "phase: "+summary.CurrentPhase)
	}
	if summary.Budget != "" {
		lines = append(lines, "budget: "+summary.Budget)
	}
	resultFields := workflowResultDisplayFields(summary.Result)
	if len(resultFields) > 0 {
		lines = append(lines, "", "result:")
		for _, field := range resultFields {
			lines = append(lines, "  "+strings.ToLower(field.Label)+": "+field.Value)
		}
	}
	sections := []LocalResultSection{{
		Title: "Run",
		Fields: []LocalResultField{
			{Label: "Status", Value: summary.Status, Tone: workflowStatusTone(summary.Status)},
			{Label: "Tasks", Value: workflowTaskCountValue(summary)},
		},
	}}
	if summary.Summary != "" {
		sections[0].Fields = append(sections[0].Fields, LocalResultField{
			Label: workflowRunSummaryLabel(summary.Status),
			Value: summary.Summary,
			Tone:  workflowRunSummaryTone(summary.Status),
		})
	}
	if summary.Budget != "" {
		sections[0].Fields = append(sections[0].Fields, LocalResultField{Label: "Budget", Value: summary.Budget})
	}
	if len(resultFields) > 0 {
		sections = append(sections, LocalResultSection{Title: "Result", Fields: resultFields})
	}
	progressSections, progressLines := workflowSnapshotProgressDisplay(snapshot)
	if len(progressLines) > 0 {
		lines = append(lines, "", "progress:")
		lines = append(lines, progressLines...)
		sections = append(sections, progressSections...)
	}
	if len(snapshot.Logs) > 0 {
		lines = append(lines, "", "logs:")
		for _, log := range lastWorkflowStrings(snapshot.Logs, 3) {
			lines = append(lines, "  - "+log)
		}
		fields := make([]LocalResultField, 0, len(snapshot.Logs))
		for _, log := range lastWorkflowStrings(snapshot.Logs, 3) {
			fields = append(fields, LocalResultField{Label: "log", Value: log})
		}
		sections = append(sections, LocalResultSection{Title: "Recent logs", Fields: fields})
	}
	lines = append(lines, "", "panel: use /workflows for live progress and keyboard shortcuts")
	return &LocalResult{
		Kind:                  "workflow",
		Title:                 "Workflow " + string(run.ID),
		WorkflowPanelSnapshot: panelSnapshot,
		Fields: []LocalResultField{
			{Label: "Run", Value: string(run.ID)},
			{Label: "Status", Value: summary.Status, Tone: workflowStatusTone(summary.Status)},
			{Label: workflowRunSummaryLabel(summary.Status), Value: summary.Summary, Tone: workflowRunSummaryTone(summary.Status)},
		},
		Sections:  sections,
		PlainText: strings.Join(lines, "\n"),
	}
}

func workflowRunSummaryLabel(status string) string {
	if status == workflow.RunStatusFailed {
		return "Error"
	}
	return "Summary"
}

func workflowRunSummaryTone(status string) string {
	if status == workflow.RunStatusFailed {
		return "error"
	}
	return ""
}

func (a *App) buildWorkflowEventsLocalResult(runID string) *LocalResult {
	run, res, err := a.loadWorkflowRunForLocalResult(runID)
	if err != nil {
		text := "Workflow\n\nerror: " + err.Error()
		return &LocalResult{Kind: "workflow", Title: "Workflow", Fields: []LocalResultField{{Label: "Error", Value: err.Error(), Tone: "error"}}, PlainText: text}
	}
	if res != nil {
		return res
	}
	summary := workflowRunSummary(run)
	lines := []string{"Workflow events", "", "run: " + string(run.ID), "status: " + summary.Status, "", "events:"}
	eventFields := make([]LocalResultField, 0, len(run.Events))
	for _, ev := range run.Events {
		label := workflowEventLabel(ev)
		value := workflowEventValue(ev)
		lines = append(lines, "  - "+label+": "+value)
		eventFields = append(eventFields, LocalResultField{Label: label, Value: value, Tone: workflowEventTone(ev)})
	}
	return &LocalResult{
		Kind:      "workflow",
		Title:     "Workflow Events " + string(run.ID),
		Fields:    []LocalResultField{{Label: "Run", Value: string(run.ID)}, {Label: "Status", Value: summary.Status, Tone: workflowStatusTone(summary.Status)}},
		Sections:  []LocalResultSection{{Title: "Events", Fields: eventFields}},
		PlainText: strings.Join(lines, "\n"),
	}
}

func (a *App) CancelWorkflowRun(runID string) (*LocalResult, error) {
	return a.cancelWorkflowLocalResult(runID)
}

func (a *App) cancelWorkflowLocalResult(runID string) (*LocalResult, error) {
	if a == nil || a.workflowRunner == nil {
		return nil, errors.New("workflow runner is unavailable")
	}
	msg, err := a.workflowRunner.CancelRun(context.Background(), workflow.RunID(runID))
	if err != nil {
		return nil, err
	}
	lines := []string{"Workflow", "", "run: " + strings.TrimSpace(runID), "status: cancel requested", "message: " + msg}
	return &LocalResult{
		Kind:      "workflow",
		Title:     "Workflow " + strings.TrimSpace(runID),
		Fields:    []LocalResultField{{Label: "Run", Value: strings.TrimSpace(runID)}, {Label: "Status", Value: "cancel requested", Tone: "warn"}, {Label: "Message", Value: msg}},
		PlainText: strings.Join(lines, "\n"),
	}, nil
}

type workflowSummary struct {
	Status         string
	Summary        string
	Result         any
	CurrentPhase   string
	Budget         string
	TasksRunning   int
	TasksCompleted int
	TasksFailed    int
	TasksCancelled int
	TasksCached    int
	UpdatedAt      time.Time
}

func workflowRunSummary(run workflow.Run) workflowSummary {
	s := workflowSummary{Status: run.Status, Summary: strings.TrimSpace(run.Summary)}
	if s.Status == "" {
		s.Status = workflow.RunStatusRunning
	}
	tasks := map[workflow.TaskID]string{}
	for _, ev := range run.Events {
		if !ev.Time.IsZero() && ev.Time.After(s.UpdatedAt) {
			s.UpdatedAt = ev.Time
		}
		switch ev.Type {
		case workflow.EventRunStarted:
			if s.Summary == "" {
				s.Summary = strings.TrimSpace(ev.Message)
			}
		case workflow.EventScriptReady:
			if s.Summary == "" {
				if desc := strings.TrimSpace(workflowLocalString(ev.Data["description"])); desc != "" {
					s.Summary = desc
				}
			}
			if budget := workflowBudgetValueFromDataMap(ev.Data["budget"]); budget != "" {
				s.Budget = budget
			}
		case workflow.EventPhaseStarted:
			if strings.TrimSpace(ev.Phase) != "" {
				s.CurrentPhase = strings.TrimSpace(ev.Phase)
			} else if strings.TrimSpace(ev.Message) != "" {
				s.CurrentPhase = strings.TrimSpace(ev.Message)
			}
		case workflow.EventTaskStarted, workflow.EventTaskProgress:
			if ev.TaskID != "" {
				tasks[ev.TaskID] = workflowTaskStatusFromEvent(ev)
			}
		case workflow.EventTaskCompleted:
			if ev.TaskID != "" {
				tasks[ev.TaskID] = workflowTaskStatusFromEvent(ev)
			}
			if workflowEventCached(ev) {
				s.TasksCached++
			}
		case workflow.EventBudgetUpdated:
			if budget := workflowBudgetValue(ev.Data); budget != "" {
				s.Budget = budget
			}
		case workflow.EventRunCompleted:
			if ev.Data != nil {
				if result := ev.Data["result"]; result != nil {
					s.Result = result
				}
			}
		case workflow.EventTaskFailed:
			if ev.TaskID != "" {
				tasks[ev.TaskID] = workflow.TaskStatusFailed
			}
		case workflow.EventTaskCancelled:
			if ev.TaskID != "" {
				tasks[ev.TaskID] = workflow.TaskStatusCancelled
			}
		}
	}
	for _, st := range tasks {
		switch st {
		case workflow.TaskStatusCompleted:
			s.TasksCompleted++
		case workflow.TaskStatusFailed, workflow.TaskStatusCancelled:
			if st == workflow.TaskStatusCancelled {
				s.TasksCancelled++
			} else {
				s.TasksFailed++
			}
		default:
			s.TasksRunning++
		}
	}
	if strings.TrimSpace(run.Error) != "" {
		s.Summary = strings.TrimSpace(run.Error)
	}
	if msg := workflowResultAnswer(s.Result); msg != "" {
		s.Summary = msg
	}
	return s
}

func workflowTaskCountValue(summary workflowSummary) string {
	parts := []string{
		fmt.Sprintf("%d running", summary.TasksRunning),
		fmt.Sprintf("%d completed", summary.TasksCompleted),
		fmt.Sprintf("%d failed", summary.TasksFailed),
	}
	if summary.TasksCancelled > 0 {
		parts = append(parts, fmt.Sprintf("%d cancelled", summary.TasksCancelled))
	}
	if summary.TasksCached > 0 {
		parts = append(parts, fmt.Sprintf("%d cached", summary.TasksCached))
	}
	return strings.Join(parts, " · ")
}

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

func workflowResultAnswer(result any) string {
	obj, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	if answer := strings.TrimSpace(workflowLocalString(obj["answer"])); answer != "" {
		return answer
	}
	return strings.TrimSpace(workflowLocalString(obj["summary"]))
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
