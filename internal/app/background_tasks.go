package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/tools"
)

func (a *App) executePSCommand() CommandExecution {
	if a == nil || a.toolset == nil {
		text := "Background tasks\n\nNo background shell tasks."
		return CommandExecution{Handled: true, Text: text, LocalResult: buildBackgroundTasksLocalResult(nil, text)}
	}
	tasks := a.toolset.BackgroundShellTasks()
	text := formatBackgroundTasks(tasks)
	return CommandExecution{Handled: true, Text: text, LocalResult: buildBackgroundTasksLocalResult(tasks, text)}
}

func (a *App) executeStopCommand(line string) (CommandExecution, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 2 {
		return CommandExecution{Handled: true}, errors.New("usage: /stop <task_id>")
	}
	if a == nil || a.toolset == nil {
		return CommandExecution{Handled: true}, errors.New("task not found")
	}
	task, err := a.toolset.CancelBackgroundShellTask(a.ctx, fields[1])
	if err != nil {
		return CommandExecution{Handled: true}, err
	}
	text := fmt.Sprintf("Stopped background task\n\ntask:    %s\nstatus:  %s\ncommand: %s", task.ID, task.Status, task.Command)
	return CommandExecution{
		Handled:     true,
		Text:        text,
		LocalResult: buildBackgroundTasksLocalResult([]tools.BackgroundShellTask{task}, text),
		Mutated:     true,
	}, nil
}

func formatBackgroundTasks(tasks []tools.BackgroundShellTask) string {
	var b strings.Builder
	b.WriteString("Background tasks")
	if len(tasks) == 0 {
		b.WriteString("\n\nNo background shell tasks.")
		return b.String()
	}
	for _, task := range tasks {
		b.WriteString("\n\n")
		b.WriteString(task.ID)
		b.WriteString("\nstatus:  ")
		b.WriteString(task.Status)
		b.WriteString("\ncommand: ")
		b.WriteString(task.Command)
		if task.CWD != "" {
			b.WriteString("\ncwd:     ")
			b.WriteString(task.CWD)
		}
		b.WriteString("\nage:     ")
		b.WriteString(formatTaskAge(task.StartedAt))
		if task.LastOutput != nil {
			b.WriteString("\noutput:  ")
			b.WriteString(formatTaskAge(*task.LastOutput))
			b.WriteString(" ago")
		}
		if task.ExitCode != nil {
			b.WriteString(fmt.Sprintf("\nexit:    %d", *task.ExitCode))
		}
	}
	return b.String()
}

func buildBackgroundTasksLocalResult(tasks []tools.BackgroundShellTask, text string) *LocalResult {
	fields := make([]LocalResultField, 0, len(tasks))
	for _, task := range tasks {
		value := task.Status
		if task.CWD != "" {
			value += " · " + task.CWD
		}
		if task.Command != "" {
			value += " · " + task.Command
		}
		fields = append(fields, LocalResultField{Label: task.ID, Value: value})
	}
	if len(fields) == 0 {
		fields = append(fields, LocalResultField{Label: "tasks", Value: "none"})
	}
	return &LocalResult{
		Kind:      "background_tasks",
		Title:     "Background tasks",
		Fields:    fields,
		PlainText: text,
	}
}

func formatTaskAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t).Round(time.Second)
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return d.String()
	}
	if d < time.Hour {
		return d.Round(time.Minute).String()
	}
	return d.Round(time.Hour).String()
}
