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
				tasks[ev.TaskID] = workflow.TaskStatusRunning
			}
		case workflow.EventTaskCompleted:
			if ev.TaskID != "" {
				tasks[ev.TaskID] = workflow.TaskStatusCompleted
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

type workflowSnapshot struct {
	Phases         []string
	DeclaredPhases []string
	CurrentPhase   string
	Tasks          []*workflowTaskSnapshot
	Logs           []string
}

type workflowTaskSnapshot struct {
	ID               workflow.TaskID
	Label            string
	Phase            string
	Status           string
	Message          string
	Prompt           string
	Outcome          string
	Error            string
	Model            string
	ActorKind        string
	Cached           bool
	IsChild          bool
	Sequence         int
	StartedAt        time.Time
	CompletedAt      time.Time
	DurationMS       int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	PromptCacheHit   int64
	PromptCacheMiss  int64
	ReasoningReplay  int64
	ToolReplayTokens int64
	ToolRawTokens    int64
	ToolTokensSaved  int64
	ToolCompacted    int64
	ToolCalls        int
	ToolCallNames    []string
	Activity         []WorkflowPanelActivity
}

type WorkflowPanelSnapshot struct {
	RunID        string
	Status       string
	Summary      string
	Error        string
	Budget       string
	CurrentPhase string
	StartedAt    time.Time
	EndedAt      time.Time
	ElapsedMS    int64
	Phases       []WorkflowPanelPhase
	Logs         []string
	Result       any
}

type WorkflowPanelPhase struct {
	Name      string
	Status    string
	Done      int
	Running   int
	Failed    int
	Cancelled int
	Cached    int
	Total     int
	Tasks     []WorkflowPanelTask
}

type WorkflowPanelTask struct {
	ID               string
	Sequence         int
	Phase            string
	Label            string
	Status           string
	Model            string
	ActorKind        string
	Prompt           string
	Outcome          string
	Error            string
	Message          string
	Cached           bool
	IsChild          bool
	StartedAt        time.Time
	CompletedAt      time.Time
	DurationMS       int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	PromptCacheHit   int64
	PromptCacheMiss  int64
	ReasoningReplay  int64
	ToolReplayTokens int64
	ToolRawTokens    int64
	ToolTokensSaved  int64
	ToolCompacted    int64
	ToolCalls        int
	ToolCallNames    []string
	Activity         []WorkflowPanelActivity
}

type WorkflowPanelActivity struct {
	Time     time.Time
	Message  string
	ToolName string
}

type workflowUsageSnapshot struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	PromptCacheHit   int64
	PromptCacheMiss  int64
	ReasoningReplay  int64
	ToolReplayTokens int64
	ToolRawTokens    int64
	ToolTokensSaved  int64
	ToolCompacted    int64
}

func (task *workflowTaskSnapshot) applyUsage(usage workflowUsageSnapshot) {
	if task == nil {
		return
	}
	if usage.PromptTokens > 0 {
		task.PromptTokens = usage.PromptTokens
	}
	if usage.CompletionTokens > 0 {
		task.CompletionTokens = usage.CompletionTokens
	}
	if usage.TotalTokens > 0 {
		task.TotalTokens = usage.TotalTokens
	}
	if usage.PromptCacheHit > 0 {
		task.PromptCacheHit = usage.PromptCacheHit
	}
	if usage.PromptCacheMiss > 0 {
		task.PromptCacheMiss = usage.PromptCacheMiss
	}
	if usage.ReasoningReplay > 0 {
		task.ReasoningReplay = usage.ReasoningReplay
	}
	if usage.ToolReplayTokens > 0 {
		task.ToolReplayTokens = usage.ToolReplayTokens
	}
	if usage.ToolRawTokens > 0 {
		task.ToolRawTokens = usage.ToolRawTokens
	}
	if usage.ToolTokensSaved > 0 {
		task.ToolTokensSaved = usage.ToolTokensSaved
	}
	if usage.ToolCompacted > 0 {
		task.ToolCompacted = usage.ToolCompacted
	}
}

func buildWorkflowSnapshot(run workflow.Run) workflowSnapshot {
	out := workflowSnapshot{}
	phaseSeen := map[string]bool{}
	tasks := map[workflow.TaskID]*workflowTaskSnapshot{}
	addPhase := func(phase string) {
		phase = strings.TrimSpace(phase)
		if phase == "" || phaseSeen[phase] {
			return
		}
		phaseSeen[phase] = true
		out.Phases = append(out.Phases, phase)
	}
	taskFor := func(ev workflow.RunEvent, isChild bool) *workflowTaskSnapshot {
		taskID := ev.TaskID
		if taskID == "" {
			taskID = workflow.TaskID(fmt.Sprintf("event-%d", len(out.Tasks)+1))
		}
		task := tasks[taskID]
		if task == nil {
			task = &workflowTaskSnapshot{ID: taskID, Sequence: len(out.Tasks) + 1}
			tasks[taskID] = task
			out.Tasks = append(out.Tasks, task)
		}
		task.IsChild = task.IsChild || isChild
		if ev.Label != "" {
			task.Label = ev.Label
		} else if ev.WorkflowName != "" {
			task.Label = ev.WorkflowName
		} else if task.Label == "" {
			task.Label = string(taskID)
		}
		if ev.Phase != "" {
			task.Phase = ev.Phase
		}
		if task.Phase == "" && out.CurrentPhase != "" {
			task.Phase = out.CurrentPhase
		}
		if workflowEventCached(ev) {
			task.Cached = true
		}
		switch ev.Type {
		case workflow.EventTaskStarted, workflow.EventWorkflowStarted:
			task.Prompt = strings.TrimSpace(ev.Message)
			task.Message = task.Prompt
			task.Model = workflowTaskModel(ev)
			task.ActorKind = workflowTaskActorKind(ev)
			if !ev.Time.IsZero() && task.StartedAt.IsZero() {
				task.StartedAt = ev.Time
			}
		case workflow.EventTaskProgress:
			if msg := strings.TrimSpace(ev.Message); msg != "" {
				task.Message = msg
				task.Activity = append(task.Activity, WorkflowPanelActivity{
					Time:     ev.Time,
					Message:  msg,
					ToolName: workflowLocalString(ev.Data["tool_name"]),
				})
			}
		case workflow.EventTaskCompleted, workflow.EventTaskFailed, workflow.EventTaskCancelled, workflow.EventWorkflowCompleted, workflow.EventWorkflowFailed:
			msg := strings.TrimSpace(ev.Message)
			task.Message = msg
			switch ev.Type {
			case workflow.EventTaskFailed, workflow.EventWorkflowFailed:
				task.Error = msg
			default:
				task.Outcome = msg
			}
			if !ev.Time.IsZero() {
				task.CompletedAt = ev.Time
			}
			if ms, ok := workflowInt64Value(ev.Data["duration_ms"]); ok {
				task.DurationMS = ms
			}
			if names := workflowStringSlice(ev.Data["tool_calls"]); len(names) > 0 {
				task.ToolCallNames = names
				task.ToolCalls = len(names)
			}
			usage := workflowUsageValues(ev.Data["usage"])
			task.applyUsage(usage)
		}
		if task.DurationMS <= 0 && !task.StartedAt.IsZero() && !task.CompletedAt.IsZero() {
			task.DurationMS = task.CompletedAt.Sub(task.StartedAt).Milliseconds()
		}
		return task
	}
	for _, ev := range run.Events {
		addPhase(ev.Phase)
		switch ev.Type {
		case workflow.EventScriptReady:
			for _, phase := range workflowDeclaredPhaseNames(ev.Data["phases"]) {
				addPhase(phase)
				if !containsWorkflowString(out.DeclaredPhases, phase) {
					out.DeclaredPhases = append(out.DeclaredPhases, phase)
				}
			}
		case workflow.EventPhaseStarted:
			if ev.Phase != "" {
				addPhase(ev.Phase)
				out.CurrentPhase = ev.Phase
			} else {
				addPhase(ev.Message)
				out.CurrentPhase = strings.TrimSpace(ev.Message)
			}
		case workflow.EventLog:
			if msg := strings.TrimSpace(ev.Message); msg != "" {
				out.Logs = append(out.Logs, msg)
			}
		case workflow.EventTaskStarted, workflow.EventTaskProgress, workflow.EventTaskCompleted, workflow.EventTaskFailed, workflow.EventTaskCancelled:
			task := taskFor(ev, false)
			task.Status = workflowTaskStatusFromEvent(ev)
		case workflow.EventWorkflowStarted, workflow.EventWorkflowCompleted, workflow.EventWorkflowFailed:
			task := taskFor(ev, true)
			task.Status = workflowTaskStatusFromEvent(ev)
		}
	}
	for _, task := range out.Tasks {
		if task.Status == "" {
			task.Status = workflow.TaskStatusRunning
		}
		if task.Label == "" {
			task.Label = string(task.ID)
		}
	}
	return out
}

func buildWorkflowPanelSnapshot(run workflow.Run, summary workflowSummary, snapshot workflowSnapshot) *WorkflowPanelSnapshot {
	panel := &WorkflowPanelSnapshot{
		RunID:        string(run.ID),
		Status:       summary.Status,
		Summary:      summary.Summary,
		Error:        strings.TrimSpace(run.Error),
		Budget:       summary.Budget,
		CurrentPhase: summary.CurrentPhase,
		StartedAt:    run.Started,
		EndedAt:      run.Ended,
		Logs:         append([]string(nil), snapshot.Logs...),
		Result:       summary.Result,
	}
	if panel.StartedAt.IsZero() {
		for _, ev := range run.Events {
			if !ev.Time.IsZero() {
				panel.StartedAt = ev.Time
				break
			}
		}
	}
	if panel.EndedAt.IsZero() {
		for i := len(run.Events) - 1; i >= 0; i-- {
			ev := run.Events[i]
			if ev.Type == workflow.EventRunCompleted || ev.Type == workflow.EventRunFailed || ev.Type == workflow.EventRunCancelled {
				panel.EndedAt = ev.Time
				break
			}
		}
	}
	if !panel.StartedAt.IsZero() {
		end := panel.EndedAt
		if end.IsZero() {
			end = time.Now()
		}
		if end.After(panel.StartedAt) {
			panel.ElapsedMS = end.Sub(panel.StartedAt).Milliseconds()
		}
	}

	phaseNames := append([]string(nil), snapshot.DeclaredPhases...)
	for _, phase := range snapshot.Phases {
		if strings.TrimSpace(phase) != "" && !containsWorkflowString(phaseNames, phase) {
			phaseNames = append(phaseNames, phase)
		}
	}
	for _, task := range snapshot.Tasks {
		if strings.TrimSpace(task.Phase) == "" || containsWorkflowString(phaseNames, task.Phase) {
			continue
		}
		phaseNames = append(phaseNames, task.Phase)
	}
	if len(phaseNames) == 0 && len(snapshot.Tasks) > 0 {
		phaseNames = []string{"Tasks"}
	}
	seen := map[*workflowTaskSnapshot]bool{}
	for _, name := range phaseNames {
		tasks := workflowTasksForPhase(snapshot.Tasks, name)
		for _, task := range tasks {
			seen[task] = true
		}
		panel.Phases = append(panel.Phases, workflowPanelPhaseFromTasks(name, tasks))
	}
	unphased := []*workflowTaskSnapshot{}
	for _, task := range snapshot.Tasks {
		if !seen[task] {
			unphased = append(unphased, task)
		}
	}
	if len(unphased) > 0 {
		panel.Phases = append(panel.Phases, workflowPanelPhaseFromTasks("Unphased", unphased))
	}
	return panel
}

func workflowPanelPhaseFromTasks(name string, tasks []*workflowTaskSnapshot) WorkflowPanelPhase {
	done, running, failed, cancelled, cached := workflowTaskStatusCounts(tasks)
	phase := WorkflowPanelPhase{
		Name:      name,
		Status:    workflowPanelPhaseStatus(running, failed, cancelled, done, len(tasks)),
		Done:      done,
		Running:   running,
		Failed:    failed,
		Cancelled: cancelled,
		Cached:    cached,
		Total:     len(tasks),
		Tasks:     make([]WorkflowPanelTask, 0, len(tasks)),
	}
	for _, task := range tasks {
		phase.Tasks = append(phase.Tasks, workflowPanelTaskFromSnapshot(task))
	}
	return phase
}

func workflowPanelPhaseStatus(running, failed, cancelled, done, total int) string {
	if failed > 0 {
		return workflow.TaskStatusFailed
	}
	if running > 0 {
		return workflow.TaskStatusRunning
	}
	if cancelled > 0 {
		return workflow.TaskStatusCancelled
	}
	if total > 0 && done == total {
		return workflow.TaskStatusCompleted
	}
	return ""
}

func workflowPanelTaskFromSnapshot(task *workflowTaskSnapshot) WorkflowPanelTask {
	if task == nil {
		return WorkflowPanelTask{}
	}
	return WorkflowPanelTask{
		ID:               string(task.ID),
		Sequence:         task.Sequence,
		Phase:            task.Phase,
		Label:            task.Label,
		Status:           task.Status,
		Model:            task.Model,
		ActorKind:        task.ActorKind,
		Prompt:           task.Prompt,
		Outcome:          task.Outcome,
		Error:            task.Error,
		Message:          task.Message,
		Cached:           task.Cached,
		IsChild:          task.IsChild,
		StartedAt:        task.StartedAt,
		CompletedAt:      task.CompletedAt,
		DurationMS:       task.DurationMS,
		PromptTokens:     task.PromptTokens,
		CompletionTokens: task.CompletionTokens,
		TotalTokens:      task.TotalTokens,
		PromptCacheHit:   task.PromptCacheHit,
		PromptCacheMiss:  task.PromptCacheMiss,
		ReasoningReplay:  task.ReasoningReplay,
		ToolReplayTokens: task.ToolReplayTokens,
		ToolRawTokens:    task.ToolRawTokens,
		ToolTokensSaved:  task.ToolTokensSaved,
		ToolCompacted:    task.ToolCompacted,
		ToolCalls:        task.ToolCalls,
		ToolCallNames:    append([]string(nil), task.ToolCallNames...),
		Activity:         append([]WorkflowPanelActivity(nil), task.Activity...),
	}
}

func workflowTaskStatusFromEvent(ev workflow.RunEvent) string {
	switch ev.Type {
	case workflow.EventTaskCompleted, workflow.EventWorkflowCompleted:
		return workflow.TaskStatusCompleted
	case workflow.EventTaskFailed, workflow.EventWorkflowFailed:
		return workflow.TaskStatusFailed
	case workflow.EventTaskCancelled:
		return workflow.TaskStatusCancelled
	case workflow.EventTaskStarted, workflow.EventTaskProgress, workflow.EventWorkflowStarted:
		if ev.Status != "" {
			return ev.Status
		}
		return workflow.TaskStatusRunning
	default:
		return ev.Status
	}
}

func workflowSnapshotProgressDisplay(snapshot workflowSnapshot) ([]LocalResultSection, []string) {
	if len(snapshot.Tasks) == 0 {
		return nil, nil
	}
	phaseNames := append([]string(nil), snapshot.DeclaredPhases...)
	for _, phase := range snapshot.Phases {
		if strings.TrimSpace(phase) != "" && !containsWorkflowString(phaseNames, phase) {
			phaseNames = append(phaseNames, phase)
		}
	}
	for _, task := range snapshot.Tasks {
		if strings.TrimSpace(task.Phase) == "" || containsWorkflowString(phaseNames, task.Phase) {
			continue
		}
		phaseNames = append(phaseNames, task.Phase)
	}
	if len(phaseNames) == 0 {
		phaseNames = []string{"Tasks"}
	}
	sections := []LocalResultSection{}
	lines := []string{}
	seen := map[*workflowTaskSnapshot]bool{}
	for _, phase := range phaseNames {
		tasks := workflowTasksForPhase(snapshot.Tasks, phase)
		for _, task := range tasks {
			seen[task] = true
		}
		if len(tasks) == 0 && phase != "Tasks" {
			lines = append(lines, "  "+phase+" 0/0")
			sections = append(sections, LocalResultSection{Title: phase, Fields: []LocalResultField{{Label: "Tasks", Value: "0"}}})
			continue
		}
		done, running, failed, cancelled, cached := workflowTaskStatusCounts(tasks)
		line := fmt.Sprintf("  %s %d/%d done", phase, done, len(tasks))
		if running > 0 {
			line += fmt.Sprintf(" · %d running", running)
		}
		if failed > 0 {
			line += fmt.Sprintf(" · %d failed", failed)
		}
		if cancelled > 0 {
			line += fmt.Sprintf(" · %d cancelled", cancelled)
		}
		if cached > 0 {
			line += fmt.Sprintf(" · %d cached", cached)
		}
		lines = append(lines, line)
		fields := []LocalResultField{{Label: "Tasks", Value: strings.TrimSpace(strings.TrimPrefix(line, "  "+phase))}}
		for _, task := range tasks {
			label := fmt.Sprintf("#%d %s %s", task.Sequence, workflowDisplayTaskStatus(task), task.Label)
			value := string(task.ID)
			if metrics := workflowTaskMetricsValue(task); metrics != "" {
				value += " · " + metrics
			}
			if task.Message != "" {
				value += " · " + task.Message
			}
			lines = append(lines, "    "+label+" · "+value)
			fields = append(fields, LocalResultField{Label: label, Value: value, Tone: workflowTaskTone(task.Status)})
		}
		sections = append(sections, LocalResultSection{Title: phase, Fields: fields})
	}
	unphased := []*workflowTaskSnapshot{}
	for _, task := range snapshot.Tasks {
		if !seen[task] {
			unphased = append(unphased, task)
		}
	}
	if len(unphased) > 0 {
		done, running, failed, cancelled, cached := workflowTaskStatusCounts(unphased)
		_ = running
		_ = failed
		_ = cancelled
		_ = cached
		lines = append(lines, fmt.Sprintf("  Unphased %d/%d done", done, len(unphased)))
		fields := []LocalResultField{{Label: "Tasks", Value: fmt.Sprintf("%d/%d done", done, len(unphased))}}
		for _, task := range unphased {
			label := fmt.Sprintf("#%d %s %s", task.Sequence, workflowDisplayTaskStatus(task), task.Label)
			value := string(task.ID)
			if metrics := workflowTaskMetricsValue(task); metrics != "" {
				value += " · " + metrics
			}
			if task.Message != "" {
				value += " · " + task.Message
			}
			lines = append(lines, "    "+label+" · "+value)
			fields = append(fields, LocalResultField{Label: label, Value: value, Tone: workflowTaskTone(task.Status)})
		}
		sections = append(sections, LocalResultSection{Title: "Unphased", Fields: fields})
	}
	return sections, lines
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
	for _, task := range tasks {
		if task.Phase == phase || (phase == "Tasks" && task.Phase == "") {
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
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
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
