package tools

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestShellTaskRegistryPrunesExpiredCompletedTasks(t *testing.T) {
	r := newShellTaskRegistry()
	now := time.Now()
	oldFinished := now.Add(-shellTaskCompletedTTL - time.Second)
	recentFinished := now.Add(-time.Second)

	r.tasks["old"] = &shellTask{ID: "old", StartedAt: oldFinished.Add(-time.Second), finishedAt: &oldFinished, status: "exited", stdout: "old"}
	r.tasks["recent"] = &shellTask{ID: "recent", StartedAt: recentFinished.Add(-time.Second), finishedAt: &recentFinished, status: "exited", stdout: "recent"}
	r.tasks["running"] = &shellTask{ID: "running", StartedAt: now, status: "running"}

	if _, ok := r.get("recent"); !ok {
		t.Fatal("expected recent completed task to remain available")
	}
	if _, ok := r.tasks["old"]; ok {
		t.Fatal("expected expired completed task to be pruned")
	}
	if _, ok := r.tasks["running"]; !ok {
		t.Fatal("expected running task to remain available")
	}
}

func TestRunShellBackgroundDoesNotPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("runShellBackground should recover internal panics, got %v", recovered)
		}
	}()

	runShellBackground(context.Background(), t.TempDir(), "echo unreachable", nil)
}

func TestShellTaskRegistryPrunesOldCompletedTasksOverLimit(t *testing.T) {
	r := newShellTaskRegistry()
	base := time.Now().Add(-time.Minute)
	for i := 0; i < shellTaskMaxRetained+5; i++ {
		finished := base.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("done-%03d", i)
		r.tasks[id] = &shellTask{ID: id, StartedAt: finished.Add(-time.Second), finishedAt: &finished, status: "exited"}
	}
	r.tasks["running"] = &shellTask{ID: "running", StartedAt: time.Now(), status: "running"}

	r.mu.Lock()
	r.pruneCompletedLocked(time.Now(), "")
	r.mu.Unlock()

	if got := completedShellTaskCount(r); got != shellTaskMaxRetained {
		t.Fatalf("expected completed task registry capped at %d tasks, got %d", shellTaskMaxRetained, got)
	}
	if _, ok := r.tasks["running"]; !ok {
		t.Fatal("expected running task to remain available")
	}
	if _, ok := r.tasks["done-000"]; ok {
		t.Fatal("expected oldest completed task to be pruned")
	}
	newestID := fmt.Sprintf("done-%03d", shellTaskMaxRetained+4)
	if _, ok := r.tasks[newestID]; !ok {
		t.Fatalf("expected newest completed task %s to remain available", newestID)
	}
}

func TestShellTaskRegistryCapacityDoesNotHideCompletedTaskAmongRunningTasks(t *testing.T) {
	r := newShellTaskRegistry()
	now := time.Now()
	for i := 0; i < shellTaskMaxRetained; i++ {
		id := fmt.Sprintf("running-%03d", i)
		r.tasks[id] = &shellTask{ID: id, StartedAt: now, status: "running"}
	}
	finished := now.Add(-time.Second)
	r.tasks["done"] = &shellTask{ID: "done", StartedAt: finished.Add(-time.Second), finishedAt: &finished, status: "exited"}

	if _, ok := r.get("done"); !ok {
		t.Fatal("expected completed task to remain available despite retained running tasks")
	}
}

func TestShellTaskRegistryGetProtectsRequestedCompletedTaskOverLimit(t *testing.T) {
	r := newShellTaskRegistry()
	base := time.Now().Add(-time.Minute)
	for i := 0; i < shellTaskMaxRetained+5; i++ {
		finished := base.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("done-%03d", i)
		r.tasks[id] = &shellTask{ID: id, StartedAt: finished.Add(-time.Second), finishedAt: &finished, status: "exited"}
	}

	if _, ok := r.get("done-000"); !ok {
		t.Fatal("expected requested completed task to remain available during capacity pruning")
	}
	if _, ok := r.tasks["done-000"]; !ok {
		t.Fatal("expected requested completed task to be protected from pruning")
	}
	if got := completedShellTaskCount(r); got != shellTaskMaxRetained {
		t.Fatalf("expected completed task registry capped at %d tasks, got %d", shellTaskMaxRetained, got)
	}
}

func TestShellTaskRegistryCompletionPrunesWithoutLaterAccess(t *testing.T) {
	r := newShellTaskRegistry()
	r.scheduleCleanup = func(time.Duration, func()) {}

	tasks := make([]*shellTask, 0, shellTaskMaxRetained+5)
	for i := 0; i < shellTaskMaxRetained+5; i++ {
		tasks = append(tasks, r.create(fmt.Sprintf("cmd-%03d", i), "."))
	}

	base := time.Now().Add(-10 * time.Minute)
	for i, task := range tasks {
		finished := base.Add(time.Duration(i) * time.Second)
		task.mu.Lock()
		task.finishedAt = &finished
		task.status = "exited"
		task.mu.Unlock()
		r.completed(task.ID)
	}

	if got := completedShellTaskCount(r); got != shellTaskMaxRetained {
		t.Fatalf("expected completed task registry capped at %d tasks after completion, got %d", shellTaskMaxRetained, got)
	}
	if _, ok := r.tasks[tasks[0].ID]; ok {
		t.Fatal("expected oldest completed task to be pruned without a later registry access")
	}
	if newest := tasks[len(tasks)-1]; newest == nil {
		t.Fatal("test setup produced nil newest task")
	} else if _, ok := r.tasks[newest.ID]; !ok {
		t.Fatalf("expected newest completed task %s to remain available", newest.ID)
	}
}

func TestShellTaskRegistryCompletionSchedulesExpiryCleanup(t *testing.T) {
	r := newShellTaskRegistry()
	var scheduledDelay time.Duration
	var scheduled func()
	r.scheduleCleanup = func(delay time.Duration, fn func()) {
		scheduledDelay = delay
		scheduled = fn
	}

	task := r.create("cmd", ".")
	finished := time.Now()
	task.mu.Lock()
	task.finishedAt = &finished
	task.status = "exited"
	task.stdout = "retained until ttl"
	task.mu.Unlock()

	r.completed(task.ID)

	if scheduled == nil {
		t.Fatal("expected completion to schedule TTL cleanup")
	}
	if scheduledDelay != shellTaskCompletedTTL {
		t.Fatalf("expected cleanup delay %s, got %s", shellTaskCompletedTTL, scheduledDelay)
	}
	if _, ok := r.tasks[task.ID]; !ok {
		t.Fatal("expected just-completed task to remain available before TTL cleanup")
	}

	expiredFinished := time.Now().Add(-shellTaskCompletedTTL - time.Second)
	task.mu.Lock()
	task.finishedAt = &expiredFinished
	task.mu.Unlock()

	scheduled()

	if _, ok := r.tasks[task.ID]; ok {
		t.Fatal("expected scheduled TTL cleanup to prune expired completed task")
	}
}

func completedShellTaskCount(r *shellTaskRegistry) int {
	count := 0
	for _, task := range r.tasks {
		if task.snapshot().Status != "running" {
			count++
		}
	}
	return count
}
