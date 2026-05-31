package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/workflow"
)

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
