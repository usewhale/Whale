//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/execboundary"
	"github.com/usewhale/whale/internal/execenv"
	"github.com/usewhale/whale/internal/policy"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if os.Getenv(execenv.WrapperModeEnv) == "1" {
		os.Exit(execboundary.RunWrapper(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func enableTestPTYShell(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	fakeShell := filepath.Join(dir, "exec-boundary-shell")
	if err := os.WriteFile(fakeShell, []byte(`#!/bin/sh
if [ "$1" = "-lc" ]; then
  case "$2" in
    "command /bin/echo whale-exec-boundary-probe")
      "$EXEC_WRAPPER" /bin/echo echo whale-exec-boundary-probe
      exit $?
      ;;
    *)
      exec /bin/sh -lc "$2"
      ;;
  esac
fi
exec /bin/sh "$@"
`), 0o755); err != nil {
		t.Fatalf("write fake exec-boundary shell: %v", err)
	}
	t.Setenv(execenv.ShellEnv, fakeShell)
	t.Setenv(execenv.WrapperPathEnv, os.Args[0])
}

func TestShellExecBoundaryShellCandidatesIncludeBundledRuntimePaths(t *testing.T) {
	got := shellExecBoundaryShellCandidatesFor("/opt/whale/bin/whale", "/home/alice")
	want := []string{
		"/opt/whale/bin/runtime/zsh",
		"/opt/whale/bin/zsh",
		"/opt/whale/libexec/runtime/zsh",
		"/opt/whale/libexec/whale/zsh",
		"/opt/whale/libexec/whale/runtime/zsh",
		"/opt/whale/lib/whale/zsh",
		"/opt/whale/share/whale/runtime/zsh",
		"/home/alice/.whale/runtime/zsh",
		"/home/alice/.local/share/whale/runtime/zsh",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected boundary shell candidates:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestShellExecBoundaryShellPathPrefersExplicitEnv(t *testing.T) {
	t.Setenv(execenv.ShellEnv, "/tmp/explicit-zsh")
	got, source := shellExecBoundaryShellPath()
	if got != "/tmp/explicit-zsh" || source != execenv.ShellEnv {
		t.Fatalf("explicit env shell path not preferred, got path=%q source=%q", got, source)
	}
}

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

func TestShellRunPTYModeAcceptsWriteStdin(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf 'ready\\n'; read line; printf 'got:%s\\n' \"$line\"",
		"mode":       "pty",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running PTY task, err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	payload := data["payload"].(map[string]any)
	if payload["transport"] != "pty" || payload["can_write"] != true {
		t.Fatalf("expected writable PTY payload, got %#v", payload)
	}

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    payload["task_id"],
		"chars":      "hello",
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	writeData := shellRunData(t, writeRes)
	if writeData["status"] != "exited" {
		t.Fatalf("expected completed task, got %#v: %s", writeData["status"], writeRes.Content)
	}
	writePayload := writeData["payload"].(map[string]any)
	if !strings.Contains(writePayload["stdout"].(string), "got:hello") {
		t.Fatalf("expected write_stdin output, got %#v", writePayload)
	}
	if writePayload["can_write"] != false {
		t.Fatalf("completed task should not be writable, got %#v", writePayload)
	}
}

func TestWriteStdinEmptyInputPollsWithoutWriting(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf 'ready\\n'; read line; printf 'got:%s\\n' \"$line\"",
		"mode":       "pty",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running PTY task, err=%v res=%+v", err, startRes)
	}
	payload := shellRunData(t, startRes)["payload"].(map[string]any)

	pollRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    payload["task_id"],
		"chars":      "",
		"timeout_ms": 50,
	}))
	if err != nil || pollRes.IsError {
		t.Fatalf("empty write_stdin poll failed: err=%v res=%+v", err, pollRes)
	}
	pollData := shellRunData(t, pollRes)
	if pollData["status"] != "running" {
		t.Fatalf("poll should leave task running, got %#v: %s", pollData["status"], pollRes.Content)
	}
	metrics := pollData["metrics"].(map[string]any)
	if metrics["stdin_written"] != false || metrics["chars_written"] != float64(0) || metrics["keys_written"] != float64(0) {
		t.Fatalf("poll should not write stdin, metrics=%#v", metrics)
	}
	pollPayload := pollData["payload"].(map[string]any)
	if !strings.Contains(pollPayload["stdout"].(string), "ready") {
		t.Fatalf("poll should return current task output, got %#v", pollPayload)
	}

	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": payload["task_id"]}))
}

func TestShellRunRejectsManagedPTYMode(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "bash",
		"mode":       "managed_pty",
		"background": true,
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, `"code":"invalid_args"`) || !strings.Contains(res.Content, "mode must be auto, pipe, or pty") {
		t.Fatalf("managed_pty should be rejected as invalid args, got %+v", res)
	}
}

func TestShellRunPTYModeRequiresExecBoundaryShell(t *testing.T) {
	t.Setenv(execenv.ShellEnv, "")
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "read line",
		"mode":    "pty",
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, `"code":"unsupported_transport"`) || !strings.Contains(res.Content, "exec-boundary shell") {
		t.Fatalf("pty without exec-boundary shell should be rejected, got %+v", res)
	}
}

func TestShellRunPTYModeRejectsShellWithoutExecWrapperSupport(t *testing.T) {
	t.Setenv(execenv.ShellEnv, "/bin/sh")
	t.Setenv(execenv.WrapperPathEnv, os.Args[0])
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "read line",
		"mode":    "pty",
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, `"code":"unsupported_transport"`) {
		t.Fatalf("pty with ordinary shell should be rejected, got %+v", res)
	}
	if shellExecBoundaryEnabled() {
		t.Fatal("ordinary /bin/sh must not be treated as an exec-boundary shell")
	}
}

func TestShellRunExecBoundaryDeniesSplitPathQualifiedRecursiveRemove(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	fakeShell := filepath.Join(dir, "exec-boundary-shell")
	if err := os.WriteFile(fakeShell, []byte(`#!/bin/sh
if [ "$1" = "-lc" ] && [ "$2" = "command /bin/echo whale-exec-boundary-probe" ]; then
  "$EXEC_WRAPPER" /bin/echo echo whale-exec-boundary-probe
  exit $?
fi
printf 'ready\n'
line=''
while IFS= read -r chunk; do
  line="${line}${chunk}"
  case "$line" in
    /bin/rm\ -rf\ *)
      set -- $line
      "$EXEC_WRAPPER" /bin/rm rm -rf "$3"
      line=''
      ;;
    exit)
      exit 0
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	t.Setenv(execenv.ShellEnv, fakeShell)
	t.Setenv(execenv.WrapperPathEnv, os.Args[0])

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "bash",
		"mode":       "pty",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run exec-boundary PTY failed: err=%v res=%+v", err, startRes)
	}
	payload := shellRunData(t, startRes)["payload"].(map[string]any)
	taskID := payload["task_id"].(string)
	if payload["transport"] != "pty" || payload["can_write"] != true {
		t.Fatalf("expected writable PTY payload, got %#v", payload)
	}

	_, err = ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    taskID,
		"chars":      "/bin/rm -",
		"timeout_ms": 100,
	}))
	if err != nil {
		t.Fatalf("first split write_stdin returned dispatch error: %v", err)
	}
	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    taskID,
		"chars":      "rf " + target,
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("second split write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	stdout := shellRunData(t, writeRes)["payload"].(map[string]any)["stdout"].(string)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("exec-boundary shell allowed recursive remove, target stat: %v stdout=%q", err, stdout)
	}
	if !strings.Contains(stdout, "Whale policy denied shell command: rm -rf") {
		t.Fatalf("expected exec-boundary denial output, got %q", stdout)
	}

	_, _ = ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"chars":   "exit",
		"keys":    []string{"enter"},
	}))
	waitForTaskDone(t, ts, taskID)
}

func TestShellRunExecBoundaryUsesToolsetPolicy(t *testing.T) {
	dir := t.TempDir()
	fakeShell := filepath.Join(dir, "exec-boundary-shell")
	if err := os.WriteFile(fakeShell, []byte(`#!/bin/sh
if [ "$1" = "-lc" ] && [ "$2" = "command /bin/echo whale-exec-boundary-probe" ]; then
  "$EXEC_WRAPPER" /bin/echo echo whale-exec-boundary-probe
  exit $?
fi
printf 'ready\n'
while IFS= read -r line; do
  case "$line" in
    echo\ *)
      "$EXEC_WRAPPER" /bin/echo echo "${line#echo }"
      ;;
    exit)
      exit 0
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	t.Setenv(execenv.ShellEnv, fakeShell)
	t.Setenv(execenv.WrapperPathEnv, os.Args[0])

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetExecBoundaryPolicy(policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules: []policy.PermissionRule{
			{Permission: "shell", Pattern: "/bin/echo *", Action: policy.PermissionDeny},
		},
	})
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sh",
		"mode":       "pty",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run exec-boundary PTY failed: err=%v res=%+v", err, startRes)
	}
	payload := shellRunData(t, startRes)["payload"].(map[string]any)
	taskID := payload["task_id"].(string)

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    taskID,
		"chars":      "echo blocked",
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	stdout := shellRunData(t, writeRes)["payload"].(map[string]any)["stdout"].(string)
	if !strings.Contains(stdout, "Whale policy denied shell command: echo blocked") {
		t.Fatalf("expected custom exec-boundary policy denial, got %q", stdout)
	}

	_, _ = ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"chars":   "exit",
		"keys":    []string{"enter"},
	}))
	waitForTaskDone(t, ts, taskID)
}

func TestShellRunExecBoundaryApprovalAllowsPromptRule(t *testing.T) {
	dir := t.TempDir()
	fakeShell := filepath.Join(dir, "exec-boundary-shell")
	if err := os.WriteFile(fakeShell, []byte(`#!/bin/sh
if [ "$1" = "-lc" ] && [ "$2" = "command /bin/echo whale-exec-boundary-probe" ]; then
  "$EXEC_WRAPPER" /bin/echo echo whale-exec-boundary-probe
  exit $?
fi
printf 'ready\n'
while IFS= read -r line; do
  case "$line" in
    echo\ *)
      "$EXEC_WRAPPER" /bin/echo echo "${line#echo }"
      ;;
    exit)
      exit 0
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	t.Setenv(execenv.ShellEnv, fakeShell)
	t.Setenv(execenv.WrapperPathEnv, os.Args[0])

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetExecBoundaryPolicy(policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules: []policy.PermissionRule{
			{Permission: "shell", Pattern: "/bin/echo *", Action: policy.PermissionAsk},
		},
	})
	var approvalReq policy.ApprovalRequest
	ts.SetExecBoundaryApproval(func() string { return "session-pty" }, func(req policy.ApprovalRequest) policy.ApprovalDecision {
		approvalReq = req
		return policy.ApprovalAllow
	})
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sh",
		"mode":       "pty",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run exec-boundary PTY failed: err=%v res=%+v", err, startRes)
	}
	taskID := shellRunData(t, startRes)["payload"].(map[string]any)["task_id"].(string)

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    taskID,
		"chars":      "echo allowed",
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	stdout := shellRunData(t, writeRes)["payload"].(map[string]any)["stdout"].(string)
	if !strings.Contains(stdout, "allowed") {
		t.Fatalf("expected approved command output, got %q", stdout)
	}
	if approvalReq.SessionID != "session-pty" || approvalReq.ToolCall.Name != "shell_run" {
		t.Fatalf("unexpected exec-boundary approval request: %+v", approvalReq)
	}
	if !strings.Contains(policy.ApprovalSummary(approvalReq.ToolCall), "/bin/echo allowed") {
		t.Fatalf("approval summary should show intercepted command, got %q", policy.ApprovalSummary(approvalReq.ToolCall))
	}

	_, _ = ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"chars":   "exit",
		"keys":    []string{"enter"},
	}))
	waitForTaskDone(t, ts, taskID)
}

func TestShellRunExecBoundaryApprovalDeniesPromptRule(t *testing.T) {
	dir := t.TempDir()
	fakeShell := filepath.Join(dir, "exec-boundary-shell")
	if err := os.WriteFile(fakeShell, []byte(`#!/bin/sh
if [ "$1" = "-lc" ] && [ "$2" = "command /bin/echo whale-exec-boundary-probe" ]; then
  "$EXEC_WRAPPER" /bin/echo echo whale-exec-boundary-probe
  exit $?
fi
printf 'ready\n'
while IFS= read -r line; do
  case "$line" in
    echo\ *)
      "$EXEC_WRAPPER" /bin/echo echo "${line#echo }"
      ;;
    exit)
      exit 0
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	t.Setenv(execenv.ShellEnv, fakeShell)
	t.Setenv(execenv.WrapperPathEnv, os.Args[0])

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetExecBoundaryPolicy(policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules: []policy.PermissionRule{
			{Permission: "shell", Pattern: "/bin/echo *", Action: policy.PermissionAsk},
		},
	})
	ts.SetExecBoundaryApproval(func() string { return "session-pty" }, func(policy.ApprovalRequest) policy.ApprovalDecision {
		return policy.ApprovalDeny
	})
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sh",
		"mode":       "pty",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run exec-boundary PTY failed: err=%v res=%+v", err, startRes)
	}
	taskID := shellRunData(t, startRes)["payload"].(map[string]any)["task_id"].(string)

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    taskID,
		"chars":      "echo denied",
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	stdout := shellRunData(t, writeRes)["payload"].(map[string]any)["stdout"].(string)
	if strings.Contains(stdout, "\r\ndenied\r\n") || strings.Contains(stdout, "\ndenied\n") {
		t.Fatalf("denied command should not execute, got %q", stdout)
	}
	if !strings.Contains(stdout, "Whale policy denied shell command: echo denied") {
		t.Fatalf("expected exec-boundary denial output, got %q", stdout)
	}

	_, _ = ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"chars":   "exit",
		"keys":    []string{"enter"},
	}))
	waitForTaskDone(t, ts, taskID)
}

func TestShellRunBackgroundPTYReportsActualWritableState(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 5",
		"mode":       "pty",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background PTY failed: err=%v res=%+v", err, startRes)
	}
	payload := shellRunData(t, startRes)["payload"].(map[string]any)
	taskID := payload["task_id"].(string)
	task, ok := ts.tasks.get(taskID)
	if !ok {
		t.Fatalf("task %s not found", taskID)
	}
	snap := task.snapshot()
	if payload["can_write"] != snap.CanWrite {
		t.Fatalf("can_write should reflect actual task state, payload=%#v snapshot=%#v", payload["can_write"], snap.CanWrite)
	}
	if payload["can_write"] != true {
		t.Fatalf("background PTY should be writable when shell_run returns, got %#v", payload)
	}
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func TestWriteStdinImmediatelyAfterBackgroundPTY(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "read line; printf 'got:%s\\n' \"$line\"",
		"mode":       "pty",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background PTY failed: err=%v res=%+v", err, startRes)
	}
	payload := shellRunData(t, startRes)["payload"].(map[string]any)
	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    payload["task_id"],
		"chars":      "ready",
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("immediate write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	writePayload := shellRunData(t, writeRes)["payload"].(map[string]any)
	if !strings.Contains(writePayload["stdout"].(string), "got:ready") {
		t.Fatalf("expected immediate stdin output, got %#v", writePayload)
	}
}

func TestWriteStdinRejectsOversizedInput(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 5",
		"mode":       "pty",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running PTY task, err=%v res=%+v", err, startRes)
	}
	taskID := shellRunData(t, startRes)["payload"].(map[string]any)["task_id"].(string)
	waitForTaskWritable(t, ts, taskID)

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"chars":   strings.Repeat("x", maxShellStdinBytes+1),
	}))
	if err != nil {
		t.Fatalf("write_stdin dispatch failed: %v", err)
	}
	if !writeRes.IsError || !strings.Contains(writeRes.Content, `"code":"stdin_too_large"`) {
		t.Fatalf("expected stdin_too_large, got %+v", writeRes)
	}
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func waitForTaskWritable(t *testing.T, ts *Toolset, taskID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := ts.tasks.get(taskID)
		if ok && task.snapshot().CanWrite {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not become writable", taskID)
}

func waitForTaskDone(t *testing.T, ts *Toolset, taskID string) {
	t.Helper()
	task, ok := ts.tasks.get(taskID)
	if !ok {
		return
	}
	select {
	case <-task.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task %s did not finish", taskID)
	}
}

func TestWriteStdinRejectsUnsupportedKey(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "read line",
		"mode":       "pty",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should return running PTY task, err=%v res=%+v", err, startRes)
	}
	taskID := shellRunData(t, startRes)["payload"].(map[string]any)["task_id"].(string)

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"keys":    []string{"return"},
	}))
	if err != nil {
		t.Fatalf("write_stdin dispatch failed: %v", err)
	}
	if !writeRes.IsError || !strings.Contains(writeRes.Content, `unsupported key \"return\"`) {
		t.Fatalf("expected unsupported key error, got %+v", writeRes)
	}
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func TestShellRunPTYModeStartsNewSession(t *testing.T) {
	if os.Getenv("WHALE_PTY_SESSION_HELPER") == "1" {
		sid, err := unix.Getsid(0)
		if err != nil {
			t.Fatalf("getsid: %v", err)
		}
		fmt.Printf("sid=%d\n", sid)
		return
	}

	enableTestPTYShell(t)
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	helperPath := shellQuoteForTest(os.Args[0])
	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    fmt.Sprintf(`printf 'shell_pid=%%s ' "$$"; WHALE_PTY_SESSION_HELPER=1 %s -test.run=TestShellRunPTYModeStartsNewSession --`, helperPath),
		"mode":       "pty",
		"timeout_ms": 3000,
	}))
	if err != nil || res.IsError {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, res)
	}
	payload := shellRunData(t, res)["payload"].(map[string]any)
	stdout := payload["stdout"].(string)
	fields := strings.Fields(strings.ReplaceAll(stdout, "\r", ""))
	values := map[string]string{}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if ok {
			values[key] = value
		}
	}
	if values["shell_pid"] == "" || values["sid"] == "" {
		t.Fatalf("expected shell pid and session id in PTY output, got %q", stdout)
	}
	if values["shell_pid"] != values["sid"] {
		t.Fatalf("expected PTY shell to start a new session, got %q", stdout)
	}
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestShellRunAutoInteractiveAuthUsesPTYAndWriteStdin(t *testing.T) {
	enableTestPTYShell(t)
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeNPM := filepath.Join(binDir, "npm")
	if err := os.WriteFile(fakeNPM, []byte("#!/bin/sh\nif [ \"$1\" = login ]; then printf 'Press ENTER to open in the browser...'; read _; printf '\\nLogged in on https://registry.npmjs.org/.\\n'; else exit 2; fi\n"), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "npm login",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should background npm login as PTY, err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	payload := data["payload"].(map[string]any)
	if payload["transport"] != "pty" || payload["can_write"] != true {
		t.Fatalf("expected npm login to use writable PTY, got %#v", payload)
	}
	diagnosis := data["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "interactive_or_auth" || diagnosis["suggested_next_action"] != "write_stdin" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}
	taskID := payload["task_id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	sawPrompt := false
	for time.Now().Before(deadline) {
		task, ok := ts.tasks.get(taskID)
		if ok && strings.Contains(task.snapshot().Stdout, "Press ENTER") {
			sawPrompt = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawPrompt {
		t.Fatalf("npm login prompt did not appear before write_stdin: %s", startRes.Content)
	}

	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id":    taskID,
		"keys":       []string{"enter"},
		"timeout_ms": 3000,
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("write_stdin failed: err=%v res=%+v", err, writeRes)
	}
	writePayload := shellRunData(t, writeRes)["payload"].(map[string]any)
	if !strings.Contains(writePayload["stdout"].(string), "Logged in on https://registry.npmjs.org/.") {
		t.Fatalf("expected npm login completion, got %#v", writePayload)
	}
}

func TestShellRunAutoInteractiveAuthUsesPipeWithoutExecBoundaryShell(t *testing.T) {
	t.Setenv(execenv.ShellEnv, "")
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeNPM := filepath.Join(binDir, "npm")
	if err := os.WriteFile(fakeNPM, []byte("#!/bin/sh\nif [ \"$1\" = login ]; then printf 'Press ENTER to open in the browser...'; sleep 30; else exit 2; fi\n"), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "npm login",
		"timeout_ms": 50,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should background npm login as pipe without boundary shell, err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	payload := data["payload"].(map[string]any)
	if payload["transport"] != "pipe" || payload["can_write"] != false {
		t.Fatalf("expected pipe task without writable stdin, got %#v", payload)
	}
	diagnosis := data["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "interactive_or_auth" || diagnosis["suggested_next_action"] == "write_stdin" {
		t.Fatalf("pipe auth diagnosis must not suggest write_stdin: %#v", diagnosis)
	}
	taskID := payload["task_id"].(string)
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
}

func TestShellRunPipeInteractiveAuthDoesNotSuggestWriteStdin(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeNPM := filepath.Join(binDir, "npm")
	if err := os.WriteFile(fakeNPM, []byte("#!/bin/sh\nif [ \"$1\" = login ]; then printf 'Press ENTER to open in the browser...'; sleep 30; else exit 2; fi\n"), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "npm login",
		"timeout_ms": 50,
		"mode":       "pipe",
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run should background pipe npm login, err=%v res=%+v", err, startRes)
	}
	data := shellRunData(t, startRes)
	payload := data["payload"].(map[string]any)
	if payload["transport"] != "pipe" || payload["can_write"] != false {
		t.Fatalf("expected pipe task without writable stdin, got %#v", payload)
	}
	diagnosis := data["diagnosis"].(map[string]any)
	if diagnosis["reason"] != "interactive_or_auth" || diagnosis["suggested_next_action"] == "write_stdin" {
		t.Fatalf("pipe auth diagnosis must not suggest write_stdin: %#v", diagnosis)
	}
	taskID := payload["task_id"].(string)
	cancelRes, cancelErr := ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
	if cancelErr != nil || cancelRes.IsError {
		t.Fatalf("cleanup shell_cancel failed: err=%v res=%+v", cancelErr, cancelRes)
	}
}

func TestWriteStdinRejectsPipeTask(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "sleep 1",
		"background": true,
		"mode":       "pipe",
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	taskID := backgroundTaskID(t, startRes.Content)
	writeRes, err := ts.writeStdin(context.Background(), tc("write_stdin", map[string]any{
		"task_id": taskID,
		"chars":   "x",
	}))
	if err != nil {
		t.Fatalf("write_stdin dispatch failed: %v", err)
	}
	if !writeRes.IsError || !strings.Contains(writeRes.Content, `"code":"stdin_not_available"`) {
		t.Fatalf("expected stdin_not_available, got %+v", writeRes)
	}
	_, _ = ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID}))
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
	sawPrompt := false
	for time.Now().Before(deadline) {
		task, ok := ts.tasks.get(taskID)
		if ok && strings.Contains(task.snapshot().Stderr, "Password:") {
			sawPrompt = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawPrompt {
		t.Fatalf("promptcmd stderr did not appear before shell_wait: %s", startRes.Content)
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
	if diagnosis["reason"] != "interactive_prompt" || diagnosis["suggested_next_action"] != "rerun_non_interactive" {
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

func TestShellRunConfiguredForegroundMaxAllowsLongerRequest(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetForegroundShellWait(45000, 240000)

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "printf done; sleep 0.01",
		"timeout_ms": 180000,
	}))
	if err != nil {
		t.Fatalf("shell_run returned dispatch error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected shell_run success, got %+v", res)
	}
	metrics := shellRunData(t, res)["metrics"].(map[string]any)
	if metrics["requested_timeout_ms"].(float64) != 180000 || metrics["effective_timeout_ms"].(float64) != 180000 {
		t.Fatalf("unexpected timeout metrics: %#v", metrics)
	}
	if metrics["timeout_clamped"] != false {
		t.Fatalf("timeout_clamped = %#v, want false", metrics["timeout_clamped"])
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
		err := unix.Kill(pid, 0)
		if errors.Is(err, unix.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child process %d still exists after cancel", pid)
}
