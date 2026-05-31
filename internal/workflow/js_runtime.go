package workflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fastschema/qjs"
)

const (
	defaultWorkflowJSMemoryLimit = 64 * 1024 * 1024
	defaultWorkflowJSMaxStack    = 2 * 1024 * 1024
	defaultWorkflowJSTimeout     = 10 * time.Second
)

type workflowJSRuntimeOptions struct {
	Context context.Context
	Timeout time.Duration
}

type workflowJSRuntime struct {
	runtime  *qjs.Runtime
	tempDir  string
	watchdog *workflowJSWatchdog
}

func newWorkflowJSRuntime(opts workflowJSRuntimeOptions) (*workflowJSRuntime, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeCtx := ctx
	var watchdog *workflowJSWatchdog
	if opts.Timeout > 0 {
		var cancel context.CancelCauseFunc
		runtimeCtx, cancel = context.WithCancelCause(ctx)
		watchdog = newWorkflowJSWatchdog(opts.Timeout, cancel)
		watchdog.start(runtimeCtx)
	}
	dir, err := os.MkdirTemp("", "whale-workflow-js-*")
	if err != nil {
		if watchdog != nil {
			watchdog.stop()
		}
		return nil, fmt.Errorf("create workflow JS sandbox: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		if watchdog != nil {
			watchdog.stop()
		}
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("secure workflow JS sandbox: %w", err)
	}
	rt, err := qjs.New(qjs.Option{
		Context:            runtimeCtx,
		CloseOnContextDone: true,
		CWD:                dir,
		MemoryLimit:        defaultWorkflowJSMemoryLimit,
		MaxStackSize:       defaultWorkflowJSMaxStack,
		Stdout:             io.Discard,
		Stderr:             io.Discard,
	})
	if err != nil {
		if watchdog != nil {
			watchdog.stop()
		}
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("create workflow JS runtime: %w", err)
	}
	return &workflowJSRuntime{runtime: rt, tempDir: dir, watchdog: watchdog}, nil
}

func (r *workflowJSRuntime) Close() {
	if r == nil {
		return
	}
	if r.watchdog != nil {
		r.watchdog.stop()
	}
	if r.runtime != nil {
		func() {
			defer func() {
				_ = recover()
			}()
			r.runtime.Close()
		}()
	}
	if r.tempDir != "" {
		_ = os.RemoveAll(r.tempDir)
	}
}

func (r *workflowJSRuntime) Context() *qjs.Context {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Context()
}

func (r *workflowJSRuntime) Eval(filename string, flags ...qjs.EvalOptionFunc) (val *qjs.Value, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if watchdogErr := r.watchdogErr(); watchdogErr != nil {
				val = nil
				err = watchdogErr
				return
			}
			val = nil
			err = fmt.Errorf("workflow JS runtime panic: %v", recovered)
		}
	}()
	if r != nil && r.watchdog != nil {
		r.watchdog.beat()
	}
	val, err = r.runtime.Context().Eval(filename, flags...)
	if watchdogErr := r.watchdogErr(); watchdogErr != nil {
		return val, watchdogErr
	}
	if r != nil && r.watchdog != nil {
		r.watchdog.beat()
	}
	return val, err
}

func (r *workflowJSRuntime) Compile(filename string, flags ...qjs.EvalOptionFunc) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if watchdogErr := r.watchdogErr(); watchdogErr != nil {
				err = watchdogErr
				return
			}
			err = fmt.Errorf("workflow JS runtime panic: %v", recovered)
		}
	}()
	_, err = r.runtime.Compile(filename, flags...)
	if watchdogErr := r.watchdogErr(); watchdogErr != nil {
		return watchdogErr
	}
	return err
}

func (r *workflowJSRuntime) watchdogErr() error {
	if r == nil || r.watchdog == nil {
		return nil
	}
	return r.watchdog.err()
}

type workflowJSWatchdog struct {
	timeout time.Duration
	cancel  context.CancelCauseFunc
	done    chan struct{}

	mu        sync.Mutex
	lastBeat  time.Time
	hostDepth int
	cause     error
}

func newWorkflowJSWatchdog(timeout time.Duration, cancel context.CancelCauseFunc) *workflowJSWatchdog {
	now := time.Now()
	return &workflowJSWatchdog{
		timeout:  timeout,
		cancel:   cancel,
		done:     make(chan struct{}),
		lastBeat: now,
	}
}

func (w *workflowJSWatchdog) start(ctx context.Context) {
	if w == nil || w.timeout <= 0 {
		return
	}
	interval := w.timeout / 4
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.check()
			case <-ctx.Done():
				return
			case <-w.done:
				return
			}
		}
	}()
}

func (w *workflowJSWatchdog) stop() {
	if w == nil {
		return
	}
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}

func (w *workflowJSWatchdog) beat() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cause != nil {
		return
	}
	w.lastBeat = time.Now()
}

func (w *workflowJSWatchdog) enterHostWait() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cause != nil {
		return
	}
	w.hostDepth++
	w.lastBeat = time.Now()
}

func (w *workflowJSWatchdog) leaveHostWait() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hostDepth > 0 {
		w.hostDepth--
	}
	w.lastBeat = time.Now()
}

func (w *workflowJSWatchdog) check() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cause != nil || w.hostDepth > 0 {
		return
	}
	if time.Since(w.lastBeat) <= w.timeout {
		return
	}
	w.cause = fmt.Errorf("workflow JS execution exceeded %s without yielding to workflow host APIs", w.timeout)
	w.cancel(w.cause)
}

func (w *workflowJSWatchdog) err() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cause
}

func workflowJSHostWait[T any](watchdog *workflowJSWatchdog, fn func() (T, error)) (T, error) {
	if watchdog == nil {
		return fn()
	}
	watchdog.enterHostWait()
	defer watchdog.leaveHostWait()
	return fn()
}

func freeWorkflowJSValue(value *qjs.Value) {
	if value == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	value.Free()
}
