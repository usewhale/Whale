package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/shell"
)

type shellTask struct {
	ID        string
	Command   string
	CWD       string
	StartedAt time.Time

	mu          sync.RWMutex
	status      string
	transport   shellTransportKind
	exitCode    *int
	finishedAt  *time.Time
	stdout      bytes.Buffer
	stderr      bytes.Buffer
	lastOutput  *time.Time
	diagnosis   shellDiagnosis
	timeoutCtx  shellTimeoutContext
	cancel      context.CancelFunc
	stdin       io.WriteCloser
	stdinReady  chan struct{}
	stdinOnce   sync.Once
	done        chan struct{}
	doneOnce    sync.Once
	execPolicy  policy.RulePolicy
	execApprove policy.ApprovalFunc
	sessionID   string
}

func (t *shellTask) snapshot() shellTaskSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return shellTaskSnapshot{
		ID:         t.ID,
		Command:    t.Command,
		CWD:        t.CWD,
		Status:     t.status,
		Transport:  t.transport,
		ExitCode:   t.exitCode,
		StartedAt:  t.StartedAt,
		FinishedAt: t.finishedAt,
		Stdout:     decodeShellOutput(t.stdout.Bytes()),
		Stderr:     decodeShellOutput(t.stderr.Bytes()),
		LastOutput: t.lastOutput,
		Diagnosis:  diagnoseShellTaskLocked(t),
		CanWrite:   t.stdin != nil && t.status == "running" && t.transport == shellTransportPTY,
	}
}

type shellTaskSnapshot struct {
	ID         string
	Command    string
	CWD        string
	Status     string
	Transport  shellTransportKind
	ExitCode   *int
	StartedAt  time.Time
	FinishedAt *time.Time
	Stdout     string
	Stderr     string
	LastOutput *time.Time
	Diagnosis  shellDiagnosis
	CanWrite   bool
}

const (
	shellTaskCompletedTTL = time.Duration(maxBackgroundShellTimeoutMS) * time.Millisecond
	shellTaskMaxRetained  = 128
)

type shellTransportKind string

const (
	shellTransportPipe shellTransportKind = "pipe"
	shellTransportPTY  shellTransportKind = "pty"
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

func (r *shellTaskRegistry) create(command, cwd string, transport shellTransportKind) *shellTask {
	if transport == "" {
		transport = shellTransportPipe
	}
	t := &shellTask{ID: r.nextID(), Command: command, CWD: cwd, StartedAt: time.Now(), status: "running", transport: transport, stdinReady: make(chan struct{}), done: make(chan struct{})}
	r.mu.Lock()
	r.tasks[t.ID] = t
	r.pruneCompletedLocked(time.Now(), t.ID)
	r.mu.Unlock()
	return t
}

func (t *shellTask) setExecBoundaryPolicy(p policy.RulePolicy) {
	p.Rules = append([]policy.PermissionRule(nil), p.Rules...)
	if p.Default == "" {
		p.Default = policy.PermissionAllow
	}
	t.mu.Lock()
	t.execPolicy = p
	t.mu.Unlock()
}

func (t *shellTask) execBoundaryPolicy() policy.RulePolicy {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p := t.execPolicy
	p.Rules = append([]policy.PermissionRule(nil), p.Rules...)
	if p.Default == "" {
		p.Default = policy.PermissionAllow
	}
	return p
}

func (t *shellTask) setExecBoundaryApproval(sessionID string, fn policy.ApprovalFunc) {
	t.mu.Lock()
	t.sessionID = strings.TrimSpace(sessionID)
	t.execApprove = fn
	t.mu.Unlock()
}

func (t *shellTask) execBoundaryApproval() (string, policy.ApprovalFunc) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sessionID, t.execApprove
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

func (t *shellTask) setCancel(cancel context.CancelFunc) {
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
}

func (t *shellTask) setStdin(stdin io.WriteCloser) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.stdin = stdin
	t.mu.Unlock()
	if stdin != nil {
		t.stdinOnce.Do(func() {
			close(t.stdinReady)
		})
	}
}

func (t *shellTask) writeStdin(ctx context.Context, input string) error {
	if err := t.waitForStdin(ctx, time.Duration(shellStdinReadyTimeoutMS)*time.Millisecond); err != nil {
		return err
	}
	t.mu.RLock()
	stdin := t.stdin
	status := t.status
	transport := t.transport
	t.mu.RUnlock()
	if transport != shellTransportPTY {
		return errShellStdinUnavailable
	}
	if stdin == nil || status != "running" {
		return errShellStdinClosed
	}
	if deadlineWriter, ok := stdin.(interface{ SetWriteDeadline(time.Time) error }); ok {
		deadline := time.Now().Add(time.Duration(shellStdinWriteTimeoutMS) * time.Millisecond)
		if ctx != nil {
			if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
				deadline = ctxDeadline
			}
		}
		if err := deadlineWriter.SetWriteDeadline(deadline); err == nil {
			defer func() {
				_ = deadlineWriter.SetWriteDeadline(time.Time{})
			}()
		}
	}
	_, err := io.Copy(stdin, strings.NewReader(input))
	if err != nil {
		return err
	}
	return nil
}

func (t *shellTask) waitForStdin(ctx context.Context, timeout time.Duration) error {
	if t == nil {
		return errShellStdinClosed
	}
	t.mu.RLock()
	stdin := t.stdin
	status := t.status
	transport := t.transport
	ready := t.stdinReady
	done := t.done
	t.mu.RUnlock()
	if transport != shellTransportPTY {
		return errShellStdinUnavailable
	}
	if stdin != nil && status == "running" {
		return nil
	}
	if status != "running" {
		return errShellStdinClosed
	}
	if timeout <= 0 {
		timeout = time.Duration(shellStdinReadyTimeoutMS) * time.Millisecond
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
	case <-done:
		return errShellStdinClosed
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.stdin != nil && t.status == "running" {
		return nil
	}
	if t.transport != shellTransportPTY {
		return errShellStdinUnavailable
	}
	return errShellStdinClosed
}

var (
	errShellStdinUnavailable = errors.New("stdin is only available for PTY shell tasks")
	errShellStdinClosed      = errors.New("stdin is closed")
)

func (t *shellTask) cancelRun() {
	t.mu.RLock()
	cancel := t.cancel
	t.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (t *shellTask) setDiagnosis(diagnosis shellDiagnosis) {
	if t == nil || diagnosis.Reason == "" {
		return
	}
	t.mu.Lock()
	t.diagnosis = diagnosis
	t.mu.Unlock()
}

func (t *shellTask) setTimeoutContext(timeoutCtx shellTimeoutContext) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.timeoutCtx = timeoutCtx
	t.mu.Unlock()
}

func (t *shellTask) waitDone() {
	if t == nil || t.done == nil {
		return
	}
	<-t.done
}

func (t *shellTask) closeDone() {
	if t == nil || t.done == nil {
		return
	}
	t.doneOnce.Do(func() {
		close(t.done)
	})
}

func (t *shellTask) outputWriter(stderr bool) io.Writer {
	return shellTaskOutputWriter{task: t, stderr: stderr}
}

type shellTaskOutputWriter struct {
	task   *shellTask
	stderr bool
}

func (w shellTaskOutputWriter) Write(p []byte) (int, error) {
	if w.task == nil {
		return len(p), nil
	}
	w.task.mu.Lock()
	defer w.task.mu.Unlock()
	if w.stderr {
		_, _ = w.task.stderr.Write(p)
	} else {
		_, _ = w.task.stdout.Write(p)
	}
	now := time.Now()
	w.task.lastOutput = &now
	return len(p), nil
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
	runShellBackgroundWithAfter(ctx, dir, command, task, nil)
}

func runShellBackgroundWithAfter(ctx context.Context, dir, command string, task *shellTask, after func()) {
	defer task.closeDone()
	if after != nil {
		defer after()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			markShellTaskFailed(task, fmt.Sprintf("panic: %v", recovered))
		}
	}()

	spec, err := shell.Resolve(command)
	if err != nil {
		markShellTaskFailed(task, err.Error())
		return
	}
	cmd := shell.Command(spec)
	cmd.Dir = dir
	if task.transport == shellTransportPTY {
		err = runShellExecBoundaryPTY(ctx, dir, command, task)
	} else {
		cmd.Stdout = task.outputWriter(false)
		cmd.Stderr = task.outputWriter(true)
		err = shell.RunCommand(ctx, cmd)
	}

	task.mu.Lock()
	defer task.mu.Unlock()
	task.stdin = nil
	now := time.Now()
	task.finishedAt = &now
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			task.status = "timeout"
			task.exitCode = nil
			task.diagnosis = task.timeoutDiagnosisLocked()
			return
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			task.status = "canceled"
			task.exitCode = nil
			if task.diagnosis.Reason == "" {
				task.diagnosis = shellDiagnosisForReason("cancelled")
			}
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

func markShellTaskFailed(task *shellTask, stderr string) {
	if task == nil {
		return
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	now := time.Now()
	task.finishedAt = &now
	task.stderr.Reset()
	_, _ = task.stderr.WriteString(stderr)
	task.status = "failed"
	task.exitCode = nil
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
