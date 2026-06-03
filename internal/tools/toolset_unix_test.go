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
	if err != nil {
		t.Fatalf("shell_wait dispatch failed: %v", err)
	}
	if waitRes.IsError {
		t.Fatalf("shell_wait should return structured timeout result, got %+v", waitRes)
	}
	if !strings.Contains(waitRes.Content, `"status":"timeout"`) {
		t.Fatalf("expected timeout status, got: %s", waitRes.Content)
	}
	env, ok := core.ParseToolEnvelope(waitRes.Content)
	if !ok {
		t.Fatalf("parse timeout envelope: %s", waitRes.Content)
	}
	if !env.Success || env.Code != "ok" {
		t.Fatalf("expected successful wait envelope, got %+v", env)
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

func TestShellRunTimeoutDiagnosesInteractivePrompt(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf 'Password:' >&2; sleep 30",
		"timeout_ms": 50,
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error result, got %+v", res)
	}
	diagnosis := shellRunData(t, res)["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "interactive_prompt" || diagnosis["suggested_next_action"] != "rerun_non_interactive" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}
}

func TestShellRunNonZeroExitReturnsExecFailed(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "printf -- '---\n'; exit 1",
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected non-zero command exit to remain exec_failed, got %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse tool envelope: %s", res.Content)
	}
	if env.Success || env.Code != "exec_failed" {
		t.Fatalf("expected exec_failed tool envelope, got %+v", env)
	}
	if env.Data["status"] != "error" {
		t.Fatalf("status = %#v, want error", env.Data["status"])
	}
	payload := env.Data["payload"].(map[string]any)
	if payload["stdout"] != "---\n" {
		t.Fatalf("stdout = %#v, want marker output", payload["stdout"])
	}
	metrics := env.Data["metrics"].(map[string]any)
	if metrics["exit_code"].(float64) != 1 {
		t.Fatalf("exit_code = %#v, want 1", metrics["exit_code"])
	}
}

func TestShellRunExecFailedKeepsNetworkDiagnosis(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "printf 'curl: (6) Could not resolve host: example.invalid' >&2; exit 1",
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected failed result, got %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse tool envelope: %s", res.Content)
	}
	if env.Success || env.Code != "exec_failed" || env.Data["status"] != "error" {
		t.Fatalf("expected exec_failed envelope, got %+v", env)
	}
	diagnosis := env.Data["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "network_blocked" || diagnosis["suggested_next_action"] != "check_network" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}
}

func TestShellWaitNonZeroExitReturnsFailedResult(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf nope; exit 7",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	taskID := backgroundTaskID(t, startRes.Content)
	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    taskID,
		"timeout_ms": 3000,
	}))
	if err != nil {
		t.Fatalf("shell_wait dispatch failed: %v", err)
	}
	if waitRes.IsError {
		t.Fatalf("non-zero background exit should not be a tool error: %+v", waitRes)
	}
	env, ok := core.ParseToolEnvelope(waitRes.Content)
	if !ok {
		t.Fatalf("parse wait envelope: %s", waitRes.Content)
	}
	if !env.Success || env.Code != "ok" || env.Data["status"] != "failed" {
		t.Fatalf("expected failed wait envelope, got %+v", env)
	}
	payload := env.Data["payload"].(map[string]any)
	if payload["stdout"] != "nope" {
		t.Fatalf("stdout = %#v, want nope", payload["stdout"])
	}
	metrics := env.Data["metrics"].(map[string]any)
	if metrics["exit_code"].(float64) != 7 {
		t.Fatalf("exit_code = %#v, want 7", metrics["exit_code"])
	}
}

func TestShellRunTimeoutDiagnosesTooShortForegroundWait(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf running; sleep 30",
		"timeout_ms": 50,
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error result, got %+v", res)
	}
	diagnosis := shellRunData(t, res)["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "foreground_timeout_too_short" || diagnosis["suggested_next_action"] != "rerun_with_longer_timeout" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}
}

func TestShellRunBackgroundTimeoutDiagnosesRuntimeLimit(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf building\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    strconv.Quote(fakeGo) + " test ./internal/tui",
		"background": true,
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    backgroundTaskID(t, startRes.Content),
		"timeout_ms": 5000,
	}))
	if err != nil {
		t.Fatalf("shell_wait dispatch failed: %v", err)
	}
	if waitRes.IsError {
		t.Fatalf("shell_wait should return structured timeout result, got %+v", waitRes)
	}
	env, ok := core.ParseToolEnvelope(waitRes.Content)
	if !ok {
		t.Fatalf("parse timeout envelope: %s", waitRes.Content)
	}
	if !env.Success || env.Code != "ok" {
		t.Fatalf("expected successful wait envelope, got %+v", env)
	}
	data := env.Data
	diagnosis, ok := data["diagnosis"].(map[string]any)
	if !ok {
		t.Fatalf("missing diagnosis in wait data: %#v", data)
	}
	if diagnosis["reason"] != "background_runtime_timeout" || diagnosis["suggested_next_action"] != "rerun_background_with_longer_timeout" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}
}

func TestShellRunAutoBackgroundsLongCommandAfterForegroundWait(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf before\nsleep 0.3\nprintf after\n"), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	start := time.Now()
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    strconv.Quote(fakeGo) + " test ./internal/tui",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running task, err=%v res=%+v", err, startRes)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shell_run did not yield promptly: %s", elapsed)
	}
	data := shellRunData(t, startRes)
	if data["status"] != "running" {
		t.Fatalf("status = %#v, want running: %s", data["status"], startRes.Content)
	}
	metrics := data["metrics"].(map[string]any)
	if metrics["auto_backgrounded"] != true {
		t.Fatalf("expected auto_backgrounded metric, got %#v", metrics)
	}
	payload := data["payload"].(map[string]any)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %#v", payload)
	}

	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    taskID,
		"timeout_ms": 5000,
	}))
	if err != nil || waitRes.IsError {
		t.Fatalf("shell_wait failed: err=%v res=%+v", err, waitRes)
	}
	if !strings.Contains(waitRes.Content, "beforeafter") {
		t.Fatalf("expected completed fake go output, got: %s", waitRes.Content)
	}
}

func TestShellRunAutoBackgroundsUnknownNonInteractiveCommand(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeCmd := filepath.Join(binDir, "slowcmd")
	if err := os.WriteFile(fakeCmd, []byte("#!/bin/sh\nprintf before\nsleep 0.3\nprintf after\n"), 0o755); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "slowcmd",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running task, err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	diagnosis := data["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "unknown_long_running" {
		t.Fatalf("diagnosis = %#v, want unknown_long_running", diagnosis)
	}
	taskID := data["payload"].(map[string]any)["task_id"].(string)
	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    taskID,
		"timeout_ms": 5000,
	}))
	if err != nil || waitRes.IsError {
		t.Fatalf("shell_wait failed: err=%v res=%+v", err, waitRes)
	}
	if !strings.Contains(waitRes.Content, "beforeafter") {
		t.Fatalf("expected completed fake command output, got: %s", waitRes.Content)
	}
}

func TestShellWaitAutoBackgroundedFailureReturnsStructuredResult(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf before\nsleep 0.2\nprintf failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    strconv.Quote(fakeGo) + " test ./internal/tui",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running task, err=%v res=%+v", err, startRes)
	}
	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    backgroundTaskID(t, startRes.Content),
		"timeout_ms": 5000,
	}))
	if err != nil {
		t.Fatalf("shell_wait dispatch failed: %v", err)
	}
	if waitRes.IsError {
		t.Fatalf("shell_wait should return structured failed result, got %+v", waitRes)
	}
	env, ok := core.ParseToolEnvelope(waitRes.Content)
	if !ok {
		t.Fatalf("parse failure envelope: %s", waitRes.Content)
	}
	if !env.Success || env.Code != "ok" {
		t.Fatalf("expected successful wait envelope, got %+v", env)
	}
	data := env.Data
	if data["status"] != "failed" {
		t.Fatalf("status = %#v, want failed", data["status"])
	}
	metrics := data["metrics"].(map[string]any)
	if metrics["exit_code"].(float64) != 1 {
		t.Fatalf("exit_code = %#v, want 1", metrics["exit_code"])
	}
}

func TestShellRunExplicitBackgroundReportsEffectiveTimeout(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 5",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	metrics := data["metrics"].(map[string]any)
	if got := metricNumber(t, data, "requested_timeout_ms"); got != 0 {
		t.Fatalf("requested_timeout_ms = %d, want 0", got)
	}
	if got := metricNumber(t, data, "effective_timeout_ms"); got != defaultBackgroundShellTimeoutMS {
		t.Fatalf("effective_timeout_ms = %d, want %d", got, defaultBackgroundShellTimeoutMS)
	}
	if metrics["timeout_clamped"] != false {
		t.Fatalf("timeout_clamped = %#v, want false", metrics["timeout_clamped"])
	}
	taskID := data["payload"].(map[string]any)["task_id"].(string)
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func TestShellRunExplicitBackgroundClampsTimeout(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 5",
		"background": true,
		"timeout_ms": maxBackgroundShellTimeoutMS + 1,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	metrics := data["metrics"].(map[string]any)
	if got := metricNumber(t, data, "requested_timeout_ms"); got != maxBackgroundShellTimeoutMS+1 {
		t.Fatalf("requested_timeout_ms = %d, want %d", got, maxBackgroundShellTimeoutMS+1)
	}
	if got := metricNumber(t, data, "effective_timeout_ms"); got != maxBackgroundShellTimeoutMS {
		t.Fatalf("effective_timeout_ms = %d, want %d", got, maxBackgroundShellTimeoutMS)
	}
	if metrics["timeout_clamped"] != true {
		t.Fatalf("timeout_clamped = %#v, want true", metrics["timeout_clamped"])
	}
	taskID := data["payload"].(map[string]any)["task_id"].(string)
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func TestShellWaitRunningReturnsPartialOutputAndPromptDiagnosis(t *testing.T) {
	oldThreshold := shellStallThreshold
	shellStallThreshold = 10 * time.Millisecond
	t.Cleanup(func() { shellStallThreshold = oldThreshold })

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeCmd := filepath.Join(binDir, "promptcmd")
	if err := os.WriteFile(fakeCmd, []byte("#!/bin/sh\nprintf 'Password:' >&2\nsleep 2\n"), 0o755); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "promptcmd",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running task, err=%v res=%+v", err, startRes)
	}
	taskID := shellRunData(t, startRes)["payload"].(map[string]any)["task_id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := ts.tasks.get(taskID)
		if ok && strings.Contains(task.snapshot().Stderr, "Password:") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    taskID,
		"timeout_ms": 50,
	}))
	if err != nil || waitRes.IsError {
		t.Fatalf("shell_wait running failed: err=%v res=%+v", err, waitRes)
	}
	data := shellRunData(t, waitRes)
	if data["status"] != "running" {
		t.Fatalf("status = %#v, want running: %s", data["status"], waitRes.Content)
	}
	payload := data["payload"].(map[string]any)
	if payload["stderr"] != "Password:" {
		t.Fatalf("expected partial stderr, got %#v", payload)
	}
	diagnosis := data["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "interactive_prompt" || diagnosis["suggested_next_action"] != "shell_cancel" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func TestShellCancelStopsBackgroundTask(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 30 & echo $! > child.pid; wait",
		"background": true,
		"timeout_ms": 30000,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	taskID := backgroundTaskID(t, startRes.Content)
	pid := waitForPIDFile(t, filepath.Join(dir, "child.pid"))

	cancelRes, err := ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{
		"task_id": taskID,
	}))
	if err != nil || cancelRes.IsError {
		t.Fatalf("shell_cancel failed: err=%v res=%+v", err, cancelRes)
	}
	if !strings.Contains(cancelRes.Content, `"status":"cancelled"`) {
		t.Fatalf("expected cancelled status, got: %s", cancelRes.Content)
	}
	waitForProcessExit(t, pid)
}

func TestForegroundShellWaitCapsLongCommandRequests(t *testing.T) {
	if got := foregroundShellWaitMS(300000, true); got != 15000 {
		t.Fatalf("long-command foreground wait = %d, want 15000", got)
	}
	if got := foregroundShellWaitMS(300000, false); got != 120000 {
		t.Fatalf("regular foreground wait = %d, want 120000", got)
	}
	if got := foregroundShellWaitMS(50, true); got != 50 {
		t.Fatalf("short requested foreground wait = %d, want 50", got)
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
