package tools

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/usewhale/whale/internal/shell"
)

type shellTask struct {
	ID        string
	Command   string
	CWD       string
	StartedAt time.Time

	mu         sync.RWMutex
	status     string
	exitCode   *int
	finishedAt *time.Time
	stdout     string
	stderr     string
}

func (t *shellTask) snapshot() shellTaskSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return shellTaskSnapshot{
		ID:         t.ID,
		Command:    t.Command,
		CWD:        t.CWD,
		Status:     t.status,
		ExitCode:   t.exitCode,
		StartedAt:  t.StartedAt,
		FinishedAt: t.finishedAt,
		Stdout:     t.stdout,
		Stderr:     t.stderr,
	}
}

type shellTaskSnapshot struct {
	ID         string
	Command    string
	CWD        string
	Status     string
	ExitCode   *int
	StartedAt  time.Time
	FinishedAt *time.Time
	Stdout     string
	Stderr     string
}

const (
	shellTaskCompletedTTL = time.Duration(maxBackgroundShellTimeoutMS) * time.Millisecond
	shellTaskMaxRetained  = 128
)

type shellTaskRegistry struct {
	mu              sync.RWMutex
	tasks           map[string]*shellTask
	seq             uint64
	scheduleCleanup func(time.Duration, func())
}

func newShellTaskRegistry() *shellTaskRegistry {
	return &shellTaskRegistry{
		tasks: map[string]*shellTask{},
		scheduleCleanup: func(delay time.Duration, fn func()) {
			time.AfterFunc(delay, fn)
		},
	}
}

func (r *shellTaskRegistry) nextID() string {
	n := atomic.AddUint64(&r.seq, 1)
	return "task-" + time.Now().Format("20060102150405") + "-" + itoa(n)
}

func (r *shellTaskRegistry) create(command, cwd string) *shellTask {
	t := &shellTask{ID: r.nextID(), Command: command, CWD: cwd, StartedAt: time.Now(), status: "running"}
	r.mu.Lock()
	r.tasks[t.ID] = t
	r.pruneCompletedLocked(time.Now(), t.ID)
	r.mu.Unlock()
	return t
}

func (r *shellTaskRegistry) get(id string) (*shellTask, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[id]
	r.pruneCompletedLocked(time.Now(), id)
	return t, ok
}

func (r *shellTaskRegistry) completed(id string) {
	r.pruneCompleted(id)
	r.scheduleCompletedCleanup()
}

func (r *shellTaskRegistry) release(id string) {
	r.mu.Lock()
	delete(r.tasks, id)
	r.mu.Unlock()
}

func (r *shellTaskRegistry) pruneCompleted(keepID string) {
	r.mu.Lock()
	r.pruneCompletedLocked(time.Now(), keepID)
	r.mu.Unlock()
}

func (r *shellTaskRegistry) scheduleCompletedCleanup() {
	if r.scheduleCleanup == nil {
		return
	}
	r.scheduleCleanup(shellTaskCompletedTTL, func() {
		r.pruneCompleted("")
	})
}

func (r *shellTaskRegistry) pruneCompletedLocked(now time.Time, keepID string) {
	completed := 0
	for id, task := range r.tasks {
		snap := task.snapshot()
		if id != keepID && shellTaskExpired(snap, now) {
			delete(r.tasks, id)
			continue
		}
		if snap.Status != "running" {
			completed++
		}
	}
	for completed > shellTaskMaxRetained {
		oldestID := ""
		var oldestTime time.Time
		for id, task := range r.tasks {
			if id == keepID {
				continue
			}
			snap := task.snapshot()
			if snap.Status == "running" {
				continue
			}
			taskTime := snap.StartedAt
			if snap.FinishedAt != nil {
				taskTime = *snap.FinishedAt
			}
			if oldestID == "" || taskTime.Before(oldestTime) {
				oldestID = id
				oldestTime = taskTime
			}
		}
		if oldestID == "" {
			return
		}
		delete(r.tasks, oldestID)
		completed--
	}
}

func shellTaskExpired(task shellTaskSnapshot, now time.Time) bool {
	if task.Status == "running" || task.FinishedAt == nil {
		return false
	}
	return now.Sub(*task.FinishedAt) >= shellTaskCompletedTTL
}

func runShellBackground(ctx context.Context, dir, command string, task *shellTask) {
	spec, err := shell.Resolve(command)
	if err != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
		now := time.Now()
		task.finishedAt = &now
		task.stderr = err.Error()
		task.status = "failed"
		task.exitCode = nil
		return
	}
	cmd := exec.Command(spec.Bin, spec.Args...)
	cmd.Dir = dir
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err = shell.RunCommand(ctx, cmd)

	task.mu.Lock()
	defer task.mu.Unlock()
	now := time.Now()
	task.finishedAt = &now
	task.stdout = decodeShellOutput(stdoutBuf.Bytes())
	task.stderr = decodeShellOutput(stderrBuf.Bytes())
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			task.status = "timeout"
			task.exitCode = nil
			return
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			task.status = "canceled"
			task.exitCode = nil
			return
		}
		var ex *exec.ExitError
		if errors.As(err, &ex) {
			code := ex.ExitCode()
			task.exitCode = &code
			task.status = "failed"
			return
		}
		task.status = "failed"
		task.exitCode = nil
		return
	}
	code := 0
	task.exitCode = &code
	task.status = "exited"
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		d := n % 10
		buf = append([]byte{byte('0' + d)}, buf...)
		n /= 10
	}
	return string(buf)
}
