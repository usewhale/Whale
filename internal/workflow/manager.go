package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const DefaultMaxConcurrency = 3

type RunManager struct {
	Store     RunEventStore
	Scheduler *TaskScheduler
	Now       func() time.Time
}

func NewRunManager(store RunEventStore, scheduler *TaskScheduler) *RunManager {
	return &RunManager{Store: store, Scheduler: scheduler, Now: time.Now}
}

func (m *RunManager) StartRun(ctx context.Context, parentSessionID, title string) (RunID, error) {
	if m == nil || m.Store == nil {
		return "", errors.New("run event store is required")
	}
	runID := RunID("run-" + uuid.NewString())
	if err := m.Store.Append(ctx, RunEvent{
		RunID:     runID,
		Type:      EventRunStarted,
		Time:      m.now().UTC(),
		Status:    RunStatusRunning,
		Message:   strings.TrimSpace(title),
		SessionID: strings.TrimSpace(parentSessionID),
	}); err != nil {
		return "", err
	}
	return runID, nil
}

func (m *RunManager) RunAgents(ctx context.Context, runID RunID, parentSessionID string, specs []AgentTaskSpec, maxConcurrency int) ([]AgentTaskResult, error) {
	if m == nil || m.Store == nil || m.Scheduler == nil {
		return nil, errors.New("run manager is not configured")
	}
	if runID == "" {
		return nil, errors.New("run_id is required")
	}
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	results := make([]AgentTaskResult, len(specs))
	if len(specs) == 0 {
		if err := m.completeRun(ctx, runID, RunStatusCompleted, EventRunCompleted, "run completed"); err != nil {
			return results, err
		}
		return results, nil
	}
	type job struct {
		index int
		spec  AgentTaskSpec
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	workerCount := min(maxConcurrency, len(specs))
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				actor := ActorContext{
					RunID:           runID,
					TaskID:          TaskID("task-" + uuid.NewString()),
					ParentSessionID: parentSessionID,
					ActorKind:       ActorKindSubagent,
					Role:            j.spec.Role,
					Phase:           j.spec.Phase,
					Label:           j.spec.Label,
				}
				res, err := m.Scheduler.SpawnAgent(ctx, actor, j.spec)
				results[j.index] = res
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
	for i, spec := range specs {
		select {
		case jobs <- job{index: i, spec: spec}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			_ = m.completeRun(context.Background(), runID, RunStatusCancelled, EventRunCancelled, ctx.Err().Error())
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		_ = m.completeRun(context.Background(), runID, RunStatusCancelled, EventRunCancelled, err.Error())
		return results, err
	}
	if len(errs) > 0 {
		msg := fmt.Sprintf("%d task(s) failed", len(errs))
		if err := m.completeRun(context.Background(), runID, RunStatusFailed, EventRunFailed, msg); err != nil {
			return results, err
		}
		return results, errors.Join(errs...)
	}
	if err := m.completeRun(ctx, runID, RunStatusCompleted, EventRunCompleted, "run completed"); err != nil {
		return results, err
	}
	return results, nil
}

func (m *RunManager) completeRun(ctx context.Context, runID RunID, status, eventType, message string) error {
	return m.Store.Append(ctx, RunEvent{
		RunID:   runID,
		Type:    eventType,
		Time:    m.now().UTC(),
		Status:  status,
		Message: strings.TrimSpace(message),
	})
}

func (m *RunManager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
