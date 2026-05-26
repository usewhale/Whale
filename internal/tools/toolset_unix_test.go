//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
)

func TestShellRunCancelKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type execResult struct {
		res string
		err error
	}
	done := make(chan execResult, 1)
	go func() {
		res, err := ts.shellRun(ctx, tc("shell_run", map[string]any{
			"command":    "sleep 30 & echo $! > child.pid; wait",
			"timeout_ms": 120000,
		}))
		done <- execResult{res: res.Content, err: err}
	}()

	pid := waitForPIDFile(t, filepath.Join(dir, "child.pid"))
	cancel()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("exec shell returned error: %v", got.err)
		}
		if !strings.Contains(got.res, `"code":"cancelled"`) {
			t.Fatalf("expected cancelled result, got: %s", got.res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec shell did not return promptly after cancel")
	}

	waitForProcessExit(t, pid)
}

func TestShellRunBackgroundTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 30 & echo $! > child.pid; wait",
		"background": true,
		"timeout_ms": 100,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	taskID := backgroundTaskID(t, startRes.Content)
	pid := waitForPIDFile(t, filepath.Join(dir, "child.pid"))

	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    taskID,
		"timeout_ms": 3000,
	}))
	if err != nil || waitRes.IsError {
		t.Fatalf("shell_wait failed: err=%v res=%+v", err, waitRes)
	}
	if !strings.Contains(waitRes.Content, `"status":"timeout"`) {
		t.Fatalf("expected timeout status, got: %s", waitRes.Content)
	}
	waitForProcessExit(t, pid)
}

func TestShellRunTimeoutReturnsStructuredResult(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf before; printf err-before >&2; sleep 30",
		"timeout_ms": 100,
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error result, got %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse tool envelope: %s", res.Content)
	}
	if env.Success || env.Code != "timeout" {
		t.Fatalf("expected timeout envelope, got %+v", env)
	}
	data := env.Data
	if data["status"] != "timeout" {
		t.Fatalf("status = %#v, want timeout", data["status"])
	}
	payload := data["payload"].(map[string]any)
	if payload["command"] != "printf before; printf err-before >&2; sleep 30" || payload["cwd"] != "." {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload["stdout"] != "before" || payload["stderr"] != "err-before" {
		t.Fatalf("timeout did not keep partial output: %#v", payload)
	}
	metrics := data["metrics"].(map[string]any)
	if metrics["timed_out"] != true {
		t.Fatalf("timed_out = %#v, want true", metrics["timed_out"])
	}
	if metrics["exit_code"] != nil {
		t.Fatalf("exit_code = %#v, want nil on timeout", metrics["exit_code"])
	}
	if metrics["requested_timeout_ms"].(float64) != 100 || metrics["effective_timeout_ms"].(float64) != 100 {
		t.Fatalf("unexpected timeout metrics: %#v", metrics)
	}
	if _, ok := metrics["stdout_truncation"].(map[string]any); !ok {
		t.Fatalf("missing stdout truncation metrics: %#v", metrics)
	}
}

func TestShellRunWarnsWhenCommandTargetsOriginalWorkspace(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "original")
	worktree := filepath.Join(parent, "worktree")
	if err := os.MkdirAll(original, 0o755); err != nil {
		t.Fatalf("mkdir original: %v", err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	ts, err := NewToolset(worktree)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetWorktreeContext(worktree, original)

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "cd " + original + " && pwd",
	}))
	if err != nil || res.IsError {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, res)
	}
	data := shellRunData(t, res)
	warnings := data["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "original workspace") {
		t.Fatalf("expected original workspace warning, got %#v", warnings)
	}
	payload := data["payload"].(map[string]any)
	if strings.TrimSpace(payload["stdout"].(string)) != original {
		t.Fatalf("warning should not block command, payload=%#v", payload)
	}

	plain, err := NewToolset(worktree)
	if err != nil {
		t.Fatalf("new plain toolset: %v", err)
	}
	plainRes, err := plain.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "cd " + original + " && pwd",
	}))
	if err != nil || plainRes.IsError {
		t.Fatalf("plain shell_run failed: err=%v res=%+v", err, plainRes)
	}
	plainData := shellRunData(t, plainRes)
	if warnings, ok := plainData["warnings"].([]any); ok && len(warnings) > 0 {
		t.Fatalf("non-worktree shell_run should not warn, got %#v", warnings)
	}
}

func TestShellRunWarnsForGitDashCOriginalWorkspace(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "original")
	worktree := filepath.Join(parent, "worktree")
	if err := os.MkdirAll(original, 0o755); err != nil {
		t.Fatalf("mkdir original: %v", err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	ts, err := NewToolset(worktree)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetWorktreeContext(worktree, original)

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "git -C " + original + " status --short",
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	data := shellRunData(t, res)
	warnings := data["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "git -C") {
		t.Fatalf("expected git -C original warning, got %#v", warnings)
	}
}

func shellRunData(t *testing.T, res core.ToolResult) map[string]any {
	t.Helper()
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse tool envelope: %s", res.Content)
	}
	return env.Data
}

func backgroundTaskID(t *testing.T, content string) string {
	t.Helper()
	var envelope struct {
		Data struct {
			Payload struct {
				TaskID string `json:"task_id"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		t.Fatalf("unmarshal background result: %v", err)
	}
	if envelope.Data.Payload.TaskID == "" {
		t.Fatalf("expected task_id, got: %s", content)
	}
	return envelope.Data.Payload.TaskID
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
			if convErr != nil {
				t.Fatalf("parse pid %q: %v", strings.TrimSpace(string(b)), convErr)
			}
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid file was not written: %s", path)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child process %d still exists after cancel", pid)
}
