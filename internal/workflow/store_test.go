package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileRunEventStoreAppendListAndLoadRun(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileRunEventStore(dir)
	if err != nil {
		t.Fatalf("NewFileRunEventStore: %v", err)
	}
	runID := RunID("run-test")
	start := time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	st.now = func() time.Time { return start }
	if err := st.Append(context.Background(), RunEvent{RunID: runID, Type: EventRunStarted, Message: "test run"}); err != nil {
		t.Fatalf("append start: %v", err)
	}
	st.now = func() time.Time { return start.Add(time.Second) }
	if err := st.Append(context.Background(), RunEvent{RunID: runID, TaskID: "task-a", Type: EventTaskStarted, Status: TaskStatusRunning}); err != nil {
		t.Fatalf("append task: %v", err)
	}
	st.now = func() time.Time { return start.Add(2 * time.Second) }
	if err := st.Append(context.Background(), RunEvent{RunID: runID, Type: EventRunCompleted, Message: "done"}); err != nil {
		t.Fatalf("append done: %v", err)
	}

	events, err := st.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}
	if events[0].Type != EventRunStarted || events[1].Type != EventTaskStarted || events[2].Type != EventRunCompleted {
		t.Fatalf("events out of order: %+v", events)
	}
	if events[0].Time != start {
		t.Fatalf("start time = %s, want %s", events[0].Time, start)
	}
	run, err := st.LoadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Status != RunStatusCompleted || run.Summary != "done" || !run.Ended.Equal(start.Add(2*time.Second)) {
		t.Fatalf("run = %+v", run)
	}
	if got := filepath.Base(filepath.Dir(filepath.Join(dir, "runs", "run-test", "events.jsonl"))); got != "run-test" {
		t.Fatalf("unexpected path base: %s", got)
	}
}

func TestFileRunEventStoreReadsLargeEventLines(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileRunEventStore(dir)
	if err != nil {
		t.Fatalf("NewFileRunEventStore: %v", err)
	}
	runID := RunID("run-large")
	large := strings.Repeat("x", 128*1024)
	if err := st.Append(context.Background(), RunEvent{
		RunID:   runID,
		Type:    EventRunCompleted,
		Message: "done",
		Data: map[string]any{
			"result": large,
		},
	}); err != nil {
		t.Fatalf("append large event: %v", err)
	}
	events, err := st.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if got, _ := events[0].Data["result"].(string); got != large {
		t.Fatalf("large result mismatch: len=%d want=%d", len(got), len(large))
	}
	run, err := st.LoadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Status != RunStatusCompleted || run.Summary != "done" {
		t.Fatalf("run = %+v", run)
	}
}

func TestFileRunEventStoreListWaitsForAppendLock(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileRunEventStore(dir)
	if err != nil {
		t.Fatalf("NewFileRunEventStore: %v", err)
	}
	runID := RunID("run-locked")
	if err := st.Append(context.Background(), RunEvent{RunID: runID, Type: EventRunStarted, Message: "start"}); err != nil {
		t.Fatalf("append start: %v", err)
	}

	st.mu.Lock()
	done := make(chan error, 1)
	go func() {
		events, err := st.List(context.Background(), runID)
		if err == nil && len(events) != 1 {
			err = errors.New("unexpected event count")
		}
		done <- err
	}()
	select {
	case err := <-done:
		st.mu.Unlock()
		t.Fatalf("List completed while append lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	st.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("List after unlock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("List did not complete after append lock released")
	}
}

func TestFileRunEventStoreListRunsRecentFirst(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileRunEventStore(dir)
	if err != nil {
		t.Fatalf("NewFileRunEventStore: %v", err)
	}
	base := time.Date(2026, 5, 29, 1, 0, 0, 0, time.UTC)
	for i, id := range []RunID{"run-old", "run-new"} {
		st.now = func() time.Time { return base.Add(time.Duration(i) * time.Minute) }
		if err := st.Append(context.Background(), RunEvent{RunID: id, Type: EventRunStarted, Message: string(id)}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	runs, err := st.ListRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-new" {
		t.Fatalf("runs = %+v, want only run-new", runs)
	}
}
