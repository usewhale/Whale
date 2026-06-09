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
		text := "Background tasks\n\nNo background shell tasks running."
		return CommandExecution{Handled: true, Text: text, LocalResult: buildBackgroundTasksLocalResult(nil, text)}
	}
	tasks := a.toolset.RunningBackgroundShellTasks()
	text := formatBackgroundTasks(tasks)
	return CommandExecution{Handled: true, Text: text, LocalResult: buildBackgroundTasksLocalResult(tasks, text)}
}

func (a *App) executeStopCommand(line string) (CommandExecution, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 1 {
		return CommandExecution{Handled: true}, errors.New("usage: /stop")
	}
	if a == nil || a.toolset == nil {
		text := "No background shell tasks running."
		return CommandExecution{
			Handled:     true,
			Text:        text,
			LocalResult: buildStoppedBackgroundTasksLocalResult(nil, text),
			Mutated:     true,
		}, nil
	}
	tasks, err := a.toolset.CancelAllBackgroundShellTasks(a.ctx)
	if err != nil {
		return CommandExecution{Handled: true}, err
	}
	text := formatStoppedBackgroundTasks(tasks)
	return CommandExecution{
		Handled:     true,
		Text:        text,
		LocalResult: buildStoppedBackgroundTasksLocalResult(tasks, text),
		Mutated:     true,
	}, nil
}

func formatStoppedBackgroundTasks(tasks []tools.BackgroundShellTask) string {
	if len(tasks) == 0 {
		return "No background shell tasks running."
	}
	var b strings.Builder
	if len(tasks) == 1 {
		b.WriteString("Stopped 1 background shell task.")
	} else {
		b.WriteString(fmt.Sprintf("Stopped %d background shell tasks.", len(tasks)))
	}
	for _, task := range tasks {
		b.WriteString("\n\n")
		b.WriteString(taskDisplayName(task))
		b.WriteString("\nstatus:  ")
		b.WriteString(task.Status)
		if task.CWD != "" {
			b.WriteString("\ncwd:     ")
			b.WriteString(task.CWD)
		}
	}
	return b.String()
}

func formatBackgroundTasks(tasks []tools.BackgroundShellTask) string {
	var b strings.Builder
	b.WriteString("Background tasks")
	if len(tasks) == 0 {
		b.WriteString("\n\nNo background shell tasks running.")
		return b.String()
	}
	for _, task := range tasks {
		b.WriteString("\n\n")
		b.WriteString(taskDisplayName(task))
		b.WriteString("\nstatus:  ")
		b.WriteString(task.Status)
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
	return buildBackgroundTasksLocalResultWithTitle("Background tasks", tasks, text)
}

func buildStoppedBackgroundTasksLocalResult(tasks []tools.BackgroundShellTask, text string) *LocalResult {
	return buildBackgroundTasksLocalResultWithTitle("Stopped background tasks", tasks, text)
}

func buildBackgroundTasksLocalResultWithTitle(title string, tasks []tools.BackgroundShellTask, text string) *LocalResult {
	fields := make([]LocalResultField, 0, len(tasks))
	for _, task := range tasks {
		value := task.Status
		if task.CWD != "" {
			value += " · " + task.CWD
		}
		fields = append(fields, LocalResultField{Label: taskDisplayName(task), Value: value})
	}
	if len(fields) == 0 {
		fields = append(fields, LocalResultField{Label: "running", Value: "none"})
	}
	return &LocalResult{
		Kind:      "background_tasks",
		Title:     title,
		Fields:    fields,
		PlainText: text,
	}
}

func taskDisplayName(task tools.BackgroundShellTask) string {
	command := strings.TrimSpace(task.Command)
	if command == "" {
		return "background shell task"
	}
	return command
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
