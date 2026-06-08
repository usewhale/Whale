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
	if b == nil || b.tasks == nil {
		return nil
	}
	b.tasks.mu.Lock()
	b.tasks.pruneCompletedLocked(time.Now(), "")
	tasks := make([]*shellTask, 0, len(b.tasks.tasks))
	for _, task := range b.tasks.tasks {
		tasks = append(tasks, task)
	}
	b.tasks.mu.Unlock()

	out := make([]BackgroundShellTask, 0, len(tasks))
	for _, task := range tasks {
		snap := task.snapshot()
		out = append(out, BackgroundShellTask{
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
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
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
	}, nil
}
