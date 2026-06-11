package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
