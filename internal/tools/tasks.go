package tools

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
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

type shellTaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]*shellTask
	seq   uint64
}

func newShellTaskRegistry() *shellTaskRegistry {
	return &shellTaskRegistry{tasks: map[string]*shellTask{}}
}

func (r *shellTaskRegistry) nextID() string {
	n := atomic.AddUint64(&r.seq, 1)
	return "task-" + time.Now().Format("20060102150405") + "-" + itoa(n)
}

func (r *shellTaskRegistry) create(command, cwd string) *shellTask {
	t := &shellTask{ID: r.nextID(), Command: command, CWD: cwd, StartedAt: time.Now(), status: "running"}
	r.mu.Lock()
	r.tasks[t.ID] = t
	r.mu.Unlock()
	return t
}

func (r *shellTaskRegistry) get(id string) (*shellTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	return t, ok
}

func runShellBackground(ctx context.Context, dir, command string, task *shellTask) {
	spec, err := resolveShell(command)
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
	cmd := exec.CommandContext(ctx, spec.Bin, spec.Args...)
	configureShellCommand(cmd)
	cmd.Dir = dir
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err = cmd.Run()

	task.mu.Lock()
	defer task.mu.Unlock()
	now := time.Now()
	task.finishedAt = &now
	task.stdout = stdoutBuf.String()
	task.stderr = stderrBuf.String()
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
