package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/securefs"
)

type RunEventStore interface {
	Append(ctx context.Context, event RunEvent) error
	List(ctx context.Context, runID RunID) ([]RunEvent, error)
	LoadRun(ctx context.Context, runID RunID) (Run, error)
}

type RunListStore interface {
	ListRuns(ctx context.Context, limit int) ([]Run, error)
}

type FileRunEventStore struct {
	mu      sync.Mutex
	runsDir string
	now     func() time.Time
}

func NewFileRunEventStore(dataDir string) (*FileRunEventStore, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("dataDir is required")
	}
	runsDir := filepath.Join(dataDir, "runs")
	if err := securefs.MkdirPrivate(runsDir); err != nil {
		return nil, fmt.Errorf("create runs dir: %w", err)
	}
	return &FileRunEventStore{runsDir: runsDir, now: time.Now}, nil
}

func (s *FileRunEventStore) Append(ctx context.Context, event RunEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("run event store is nil")
	}
	runID := sanitizeID(string(event.RunID))
	if runID == "" {
		return fmt.Errorf("run_id is required")
	}
	event.RunID = RunID(runID)
	if event.Time.IsZero() {
		event.Time = s.now().UTC()
	}
	b, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal run event: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.runsDir, runID)
	if err := securefs.MkdirPrivate(dir); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open run events: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write run event: %w", err)
	}
	return nil
}

func (s *FileRunEventStore) List(ctx context.Context, runID RunID) ([]RunEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("run event store is nil")
	}
	id := sanitizeID(string(runID))
	if id == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.runsDir, id, "events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open run events: %w", err)
	}
	defer f.Close()
	out := []RunEvent{}
	reader := bufio.NewReader(f)
	for {
		raw, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read run events: %w", err)
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		var ev RunEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("decode run event: %w", err)
		}
		out = append(out, ev)
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return out, nil
}

func (s *FileRunEventStore) LoadRun(ctx context.Context, runID RunID) (Run, error) {
	events, err := s.List(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	run := Run{ID: RunID(sanitizeID(string(runID))), Status: RunStatusRunning, Events: events}
	for _, ev := range events {
		switch ev.Type {
		case EventRunStarted:
			run.Status = RunStatusRunning
			if run.Started.IsZero() {
				run.Started = ev.Time
			}
			if run.Summary == "" {
				run.Summary = ev.Message
			}
		case EventRunCompleted:
			run.Status = RunStatusCompleted
			run.Ended = ev.Time
			run.Summary = ev.Message
		case EventRunFailed:
			run.Status = RunStatusFailed
			run.Ended = ev.Time
			run.Error = ev.Message
		case EventRunCancelled:
			run.Status = RunStatusCancelled
			run.Ended = ev.Time
			run.Error = ev.Message
		}
	}
	return run, nil
}

func (s *FileRunEventStore) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("run event store is nil")
	}
	entries, err := os.ReadDir(s.runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read runs dir: %w", err)
	}
	runs := make([]Run, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := RunID(sanitizeID(entry.Name()))
		if runID == "" {
			continue
		}
		run, err := s.LoadRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(run.Events) == 0 {
			continue
		}
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return runSortTime(runs[i]).After(runSortTime(runs[j]))
	})
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func runSortTime(run Run) time.Time {
	if !run.Ended.IsZero() {
		return run.Ended
	}
	if !run.Started.IsZero() {
		return run.Started
	}
	for i := len(run.Events) - 1; i >= 0; i-- {
		if !run.Events[i].Time.IsZero() {
			return run.Events[i].Time
		}
	}
	return time.Time{}
}

func (s *FileRunEventStore) RunDir(runID RunID) (string, error) {
	if s == nil {
		return "", fmt.Errorf("run event store is nil")
	}
	id := sanitizeID(string(runID))
	if id == "" {
		return "", fmt.Errorf("run_id is required")
	}
	dir := filepath.Join(s.runsDir, id)
	if err := securefs.MkdirPrivate(dir); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	return dir, nil
}

func sanitizeID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}
