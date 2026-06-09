package tools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

type BackgroundShellTask struct {
	ID         string
	Command    string
	CWD        string
	Status     string
	Transport  string
	StartedAt  time.Time
	FinishedAt *time.Time
	LastOutput *time.Time
	CanWrite   bool
	ExitCode   *int
}

func (b *Toolset) BackgroundShellTasks() []BackgroundShellTask {
	return b.backgroundShellTasks(false)
}

func (b *Toolset) RunningBackgroundShellTasks() []BackgroundShellTask {
	return b.backgroundShellTasks(true)
}

func (b *Toolset) backgroundShellTasks(runningOnly bool) []BackgroundShellTask {
	if b == nil || b.tasks == nil {
		return nil
	}
	b.tasks.mu.Lock()
	b.tasks.pruneCompletedLocked(time.Now(), "")
	tasks := make([]*shellTask, 0, len(b.tasks.tasks))
	for _, task := range b.tasks.tasks {
		if !runningOnly || task.snapshot().Status == "running" {
			tasks = append(tasks, task)
		}
	}
	b.tasks.mu.Unlock()

	out := make([]BackgroundShellTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, backgroundShellTaskFromSnapshot(task.snapshot()))
	}
	sortBackgroundShellTasks(out)
	return out
}

func (b *Toolset) CancelBackgroundShellTask(ctx context.Context, taskID string) (BackgroundShellTask, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return BackgroundShellTask{}, errors.New("task_id is required")
	}
	if b == nil || b.tasks == nil {
		return BackgroundShellTask{}, errors.New("task not found")
	}
	task, ok := b.tasks.get(taskID)
	if !ok {
		return BackgroundShellTask{}, errors.New("task not found")
	}
	task.cancelRun()
	select {
	case <-ctx.Done():
		return BackgroundShellTask{}, ctx.Err()
	case <-task.done:
	case <-time.After(3 * time.Second):
	}
	snap := task.snapshot()
	return backgroundShellTaskFromSnapshot(snap), nil
}

func (b *Toolset) CancelAllBackgroundShellTasks(ctx context.Context) ([]BackgroundShellTask, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if b == nil || b.tasks == nil {
		return nil, nil
	}
	b.tasks.mu.Lock()
	b.tasks.pruneCompletedLocked(time.Now(), "")
	tasks := make([]*shellTask, 0, len(b.tasks.tasks))
	for _, task := range b.tasks.tasks {
		if task.snapshot().Status == "running" {
			tasks = append(tasks, task)
		}
	}
	b.tasks.mu.Unlock()
	if len(tasks) == 0 {
		return nil, nil
	}

	for _, task := range tasks {
		task.cancelRun()
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, task := range tasks {
		select {
		case <-waitCtx.Done():
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ctx.Err()
			}
		case <-task.done:
		}
	}

	out := make([]BackgroundShellTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, backgroundShellTaskFromSnapshot(task.snapshot()))
	}
	sortBackgroundShellTasks(out)
	return out, nil
}

func backgroundShellTaskFromSnapshot(snap shellTaskSnapshot) BackgroundShellTask {
	return BackgroundShellTask{
		ID:         snap.ID,
		Command:    snap.Command,
		CWD:        snap.CWD,
		Status:     snap.Status,
		Transport:  string(snap.Transport),
		StartedAt:  snap.StartedAt,
		FinishedAt: snap.FinishedAt,
		LastOutput: snap.LastOutput,
		CanWrite:   snap.CanWrite,
		ExitCode:   snap.ExitCode,
	}
}

func sortBackgroundShellTasks(tasks []BackgroundShellTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.Before(tasks[j].StartedAt)
	})
}
