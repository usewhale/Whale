package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/tasks"
)

type memoryRunEventStore struct {
	mu     sync.Mutex
	events []RunEvent
}

func (s *memoryRunEventStore) Append(_ context.Context, event RunEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *memoryRunEventStore) List(_ context.Context, runID RunID) ([]RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []RunEvent{}
	for _, ev := range s.events {
		if ev.RunID == runID {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (s *memoryRunEventStore) LoadRun(ctx context.Context, runID RunID) (Run, error) {
	events, err := s.List(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	run := Run{ID: runID, Status: RunStatusRunning, Events: events}
	for _, ev := range events {
		switch ev.Type {
		case EventRunStarted:
			run.Summary = ev.Message
		case EventRunCompleted:
			run.Status = RunStatusCompleted
			run.Summary = ev.Message
		case EventRunFailed:
			run.Status = RunStatusFailed
			run.Error = ev.Message
		case EventRunCancelled:
			run.Status = RunStatusCancelled
			run.Error = ev.Message
		}
	}
	return run, nil
}

type fakeAgentSpawner struct {
	delay       time.Duration
	delays      map[string]time.Duration
	failPrompt  string
	failMessage string
	block       bool
	summaries   map[string]string
	structured  map[string]any
	usages      map[string]llm.Usage
	respond     func(tasks.SpawnSubagentRequest) (tasks.SpawnSubagentResponse, bool)
	mu          sync.Mutex
	requests    []tasks.SpawnSubagentRequest
	calls       atomic.Int64
	active      atomic.Int64
	maxActive   atomic.Int64
}

func (s *fakeAgentSpawner) AllowedSubagentTools(req tasks.SpawnSubagentRequest) ([]string, error) {
	if len(req.Capabilities) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(req.Capabilities))
	for _, cap := range req.Capabilities {
		out = append(out, "allowed:"+cap)
	}
	return out, nil
}

func (s *fakeAgentSpawner) SpawnSubagentWithProgress(ctx context.Context, req tasks.SpawnSubagentRequest, progress func(core.ToolProgress)) (tasks.SpawnSubagentResponse, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	cur := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		max := s.maxActive.Load()
		if cur <= max || s.maxActive.CompareAndSwap(max, cur) {
			break
		}
	}
	call := s.calls.Add(1)
	if progress != nil {
		progress(core.ToolProgress{Count: int(call), Status: TaskStatusRunning, Summary: "progress " + req.Task})
	}
	if s.block {
		<-ctx.Done()
		return tasks.SpawnSubagentResponse{}, ctx.Err()
	}
	delay := s.delay
	if s.delays != nil {
		if configured, ok := s.delays[req.Task]; ok {
			delay = configured
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return tasks.SpawnSubagentResponse{}, ctx.Err()
		}
	}
	if s.failPrompt != "" && strings.Contains(req.Task, s.failPrompt) {
		msg := s.failMessage
		if msg == "" {
			msg = "boom"
		}
		return tasks.SpawnSubagentResponse{}, &tasks.SpawnSubagentError{
			SessionID: "child-failed",
			Code:      "failed",
			Message:   msg,
			Err:       errors.New(msg),
		}
	}
	if s.respond != nil {
		if res, ok := s.respond(req); ok {
			if res.SessionID == "" {
				res.SessionID = "child-" + req.WorkflowTaskLabel
			}
			if res.Status == "" {
				res.Status = TaskStatusCompleted
			}
			if res.CompletedAt == "" {
				res.CompletedAt = time.Now().UTC().Format(time.RFC3339)
			}
			return res, nil
		}
	}
	summary := "summary " + req.Task
	if s.summaries != nil {
		if configured, ok := s.summaries[req.Task]; ok {
			summary = configured
		}
	}
	usage := llm.Usage{}
	if s.usages != nil {
		usage = s.usages[req.Task]
	}
	var structured any
	if s.structured != nil {
		structured = s.structured[req.Task]
	}
	return tasks.SpawnSubagentResponse{
		SessionID:        "child-" + req.Task,
		Role:             req.Role,
		Model:            req.Model,
		Status:           TaskStatusCompleted,
		Summary:          summary,
		StructuredResult: structured,
		ToolCalls:        []string{"read_file"},
		Usage:            usage,
		DurationMS:       int64(delay / time.Millisecond),
		CompletedAt:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func TestRunManagerRunAgentsRunsConcurrentlyAndPreservesResultOrder(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{delay: 120 * time.Millisecond}
	scheduler := NewTaskScheduler(store, spawner)
	manager := NewRunManager(store, scheduler)
	runID, err := manager.StartRun(context.Background(), "parent", "parallel")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	start := time.Now()
	results, err := manager.RunAgents(context.Background(), runID, "parent", []AgentTaskSpec{
		{Prompt: "a", Role: "explore", Phase: "Review", Label: "a"},
		{Prompt: "b", Role: "review", Phase: "Review", Label: "b"},
		{Prompt: "c", Role: "research", Phase: "Verify", Label: "c"},
	}, 3)
	if err != nil {
		t.Fatalf("RunAgents: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 280*time.Millisecond {
		t.Fatalf("expected concurrent execution, elapsed %s", elapsed)
	}
	if spawner.maxActive.Load() < 2 {
		t.Fatalf("expected overlapping tasks, maxActive=%d", spawner.maxActive.Load())
	}
	for i, want := range []string{"child-a", "child-b", "child-c"} {
		if results[i].ChildSessionID != want {
			t.Fatalf("results[%d].ChildSessionID = %q, want %q", i, results[i].ChildSessionID, want)
		}
	}
	events, err := store.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventTaskStarted) != 3 || countEvents(events, EventTaskCompleted) != 3 || countEvents(events, EventRunCompleted) != 1 {
		t.Fatalf("unexpected event counts: %+v", events)
	}
}

func TestRunManagerRunAgentsRecordsFailure(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{failPrompt: "fail"}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runID, err := manager.StartRun(context.Background(), "parent", "failure")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	results, err := manager.RunAgents(context.Background(), runID, "parent", []AgentTaskSpec{
		{Prompt: "ok"},
		{Prompt: "fail"},
	}, 2)
	if err == nil {
		t.Fatalf("expected RunAgents error")
	}
	if results[1].Status != TaskStatusFailed || results[1].ChildSessionID != "child-failed" {
		t.Fatalf("failed result = %+v", results[1])
	}
	events, err := store.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventTaskFailed) != 1 || countEvents(events, EventRunFailed) != 1 {
		t.Fatalf("unexpected failure events: %+v", events)
	}
}

func TestRunManagerRunAgentsRecordsCancellation(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{block: true}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runID, err := manager.StartRun(context.Background(), "parent", "cancel")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.RunAgents(ctx, runID, "parent", []AgentTaskSpec{{Prompt: "wait"}}, 1)
		done <- err
	}()
	for spawner.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatalf("expected cancellation error")
	}
	events, err := store.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventTaskCancelled) != 1 || countEvents(events, EventRunCancelled) != 1 {
		t.Fatalf("unexpected cancellation events: %+v", events)
	}
}

func countEvents(events []RunEvent, typ string) int {
	n := 0
	for _, ev := range events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}
