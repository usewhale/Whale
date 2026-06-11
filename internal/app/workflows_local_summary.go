package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

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
