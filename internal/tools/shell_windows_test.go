//go:build windows

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

func TestWindowsShellRunForegroundAndBackground(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	const marker = "whale_windows_shell_tool"
	foreground, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "echo " + marker,
	}))
	if err != nil || foreground.IsError() {
		t.Fatalf("shell_run foreground failed: err=%v res=%+v", err, foreground)
	}
	if !strings.Contains(foreground.ModelText, marker) {
		t.Fatalf("foreground result missing marker %q: %s", marker, foreground.ModelText)
	}

	start, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "echo " + marker,
		"background": true,
	}))
	if err != nil || start.IsError() {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, start)
	}

	var envelope struct {
		Data struct {
			Payload struct {
				TaskID string `json:"task_id"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(start.ModelText), &envelope); err != nil {
		t.Fatalf("unmarshal background result: %v", err)
	}
	if envelope.Data.Payload.TaskID == "" {
		t.Fatalf("expected task_id, got: %s", start.ModelText)
	}

	wait, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    envelope.Data.Payload.TaskID,
		"timeout_ms": 5000,
	}))
	if err != nil || wait.IsError() {
		t.Fatalf("shell_wait failed: err=%v res=%+v", err, wait)
	}
	if !strings.Contains(wait.ModelText, marker) {
		t.Fatalf("background result missing marker %q: %s", marker, wait.ModelText)
	}
}

func TestWindowsShellRunCancelKillsProcessTree(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	readyPath := filepath.Join(dir, "ready")
	markerPath := filepath.Join(dir, "marker")
	t.Setenv("WHALE_SHELL_TOOL_PROCESS_TREE_HELPER", "parent")
	t.Setenv("WHALE_SHELL_TOOL_PROCESS_TREE_READY", readyPath)
	t.Setenv("WHALE_SHELL_TOOL_PROCESS_TREE_MARKER", markerPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := currentTestBinaryCommand(t)
	type execResult struct {
		res string
		err error
	}
	done := make(chan execResult, 1)
	go func() {
		res, err := ts.shellRun(ctx, tc("shell_run", map[string]any{
			"command":    command,
			"timeout_ms": 120000,
		}))
		done <- execResult{res: res.ModelText, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("shell_run returned before helper was ready: %v", got.err)
		}
		t.Fatalf("shell_run returned before helper was ready: %s", got.res)
	case ok := <-waitForWindowsFile(readyPath, 5*time.Second):
		if !ok {
			t.Fatalf("timed out waiting for %s", readyPath)
		}
	}
	cancel()

	var taskID string
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("shell_run returned error: %v", got.err)
		}
		if !strings.Contains(got.res, `"status":"running"`) || !strings.Contains(got.res, `"reason":"yield_interrupted"`) {
			t.Fatalf("expected interrupted running task result, got: %s", got.res)
		}
		taskID = shellTaskID(t, got.res)
	case <-time.After(5 * time.Second):
		t.Fatal("shell_run did not return promptly after cancel")
	}

	if res, err := ts.shellCancel(context.Background(), tc("shell_cancel", map[string]any{"task_id": taskID})); err != nil || res.IsError() {
		t.Fatalf("shell_cancel failed: err=%v res=%+v", err, res)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(markerPath); err == nil {
		t.Fatalf("descendant process survived cancellation and wrote %s", markerPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

func TestWindowsShellRunKeepsLaunchedChildOnSuccess(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	markerPath := filepath.Join(dir, "marker")
	t.Setenv("WHALE_SHELL_TOOL_PROCESS_TREE_HELPER", "success-parent")
	t.Setenv("WHALE_SHELL_TOOL_PROCESS_TREE_MARKER", markerPath)

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    currentTestBinaryCommand(t),
		"timeout_ms": 120000,
	}))
	if err != nil || res.IsError() {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, res)
	}

	select {
	case ok := <-waitForWindowsFile(markerPath, 4*time.Second):
		if !ok {
			t.Fatalf("launched child did not survive long enough to write %s", markerPath)
		}
	}
}

func TestWindowsShellRunDecodesGBKStdout(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": windowsGBKOutputHelperCommand(t, "stdout", 0),
	}))
	if err != nil || res.IsError() {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, res)
	}
	stdout := shellPayloadString(t, res.ModelText, "stdout")
	if stdout != "拒绝访问 - \\" {
		t.Fatalf("stdout = %q, want decoded GBK text", stdout)
	}
	if strings.Contains(stdout, "\uFFFD") {
		t.Fatalf("stdout contains replacement rune: %q", stdout)
	}
}

func TestWindowsShellRunPreservesAmbiguousValidUTF8Stdout(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": windowsGBKOutputHelperCommandForSample(t, "stdout", 0, "ambiguous"),
	}))
	if err != nil || res.IsError() {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, res)
	}
	stdout := shellPayloadString(t, res.ModelText, "stdout")
	if stdout != "һ" {
		t.Fatalf("stdout = %q, want ambiguous valid UTF-8 bytes preserved", stdout)
	}
}

func TestWindowsDecodeShellOutputKeepsUTF8Chinese(t *testing.T) {
	for _, input := range []string{
		"一",
		"拒绝访问 - \\",
		"error: 文件不存在",
		"Привет",
		"Ω",
		"مرحبا",
		"һ",
	} {
		if got := decodeShellOutput([]byte(input)); got != input {
			t.Fatalf("decodeShellOutput(%q) = %q, want UTF-8 unchanged", input, got)
		}
	}
}

func TestWindowsShellRunDecodesGBKStderrOnFailure(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": windowsGBKOutputHelperCommand(t, "stderr", 1),
	}))
	if err != nil {
		t.Fatalf("shell_run dispatch error: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected failing command result, got: %s", res.ModelText)
	}
	stderr := shellPayloadString(t, res.ModelText, "stderr")
	if stderr != "拒绝访问 - \\" {
		t.Fatalf("stderr = %q, want decoded GBK text; full result: %s", stderr, res.ModelText)
	}
	if strings.Contains(stderr, "\uFFFD") {
		t.Fatalf("stderr contains replacement rune: %q", stderr)
	}
}

func TestWindowsShellWaitDecodesGBKOutput(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	start, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    windowsGBKOutputHelperCommand(t, "stdout", 0),
		"background": true,
	}))
	if err != nil || start.IsError() {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, start)
	}

	taskID := shellTaskID(t, start.ModelText)
	wait := shellWaitUntilDone(t, ts, taskID)
	stdout := shellPayloadString(t, wait.ModelText, "stdout")
	if stdout != "拒绝访问 - \\" {
		t.Fatalf("background stdout = %q, want decoded GBK text", stdout)
	}
	if strings.Contains(stdout, "\uFFFD") {
		t.Fatalf("background stdout contains replacement rune: %q", stdout)
	}
}

func TestWindowsShellRunProcessTreeHelper(t *testing.T) {
	switch os.Getenv("WHALE_SHELL_TOOL_PROCESS_TREE_HELPER") {
	case "parent":
		markerPath := os.Getenv("WHALE_SHELL_TOOL_PROCESS_TREE_MARKER")
		readyPath := os.Getenv("WHALE_SHELL_TOOL_PROCESS_TREE_READY")
		cmd := exec.Command(os.Args[0], "-test.run=TestWindowsShellRunProcessTreeHelper")
		cmd.Env = append(os.Environ(),
			"WHALE_SHELL_TOOL_PROCESS_TREE_HELPER=child",
			"WHALE_SHELL_TOOL_PROCESS_TREE_MARKER="+markerPath,
		)
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
			os.Exit(3)
		}
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "success-parent":
		markerPath := os.Getenv("WHALE_SHELL_TOOL_PROCESS_TREE_MARKER")
		time.Sleep(700 * time.Millisecond)
		cmd := exec.Command(os.Args[0], "-test.run=TestWindowsShellRunProcessTreeHelper")
		cmd.Env = append(os.Environ(),
			"WHALE_SHELL_TOOL_PROCESS_TREE_HELPER=success-child",
			"WHALE_SHELL_TOOL_PROCESS_TREE_MARKER="+markerPath,
		)
		if err := cmd.Start(); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	case "success-child":
		time.Sleep(700 * time.Millisecond)
		if err := os.WriteFile(os.Getenv("WHALE_SHELL_TOOL_PROCESS_TREE_MARKER"), []byte("survived"), 0o644); err != nil {
			os.Exit(6)
		}
		os.Exit(0)
	case "child":
		time.Sleep(time.Second)
		if err := os.WriteFile(os.Getenv("WHALE_SHELL_TOOL_PROCESS_TREE_MARKER"), []byte("alive"), 0o644); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
}

func TestWindowsShellRunGBKOutputHelper(t *testing.T) {
	if os.Getenv("WHALE_GBK_OUTPUT_HELPER") != "1" {
		return
	}
	output := os.Getenv("WHALE_GBK_OUTPUT_STREAM")
	if output == "" {
		output = "stdout"
	}
	code := 0
	if os.Getenv("WHALE_GBK_OUTPUT_EXIT") == "1" {
		code = 1
	}
	gbk := []byte{0xbe, 0xdc, 0xbe, 0xf8, 0xb7, 0xc3, 0xce, 0xca, 0x20, 0x2d, 0x20, 0x5c}
	if os.Getenv("WHALE_GBK_OUTPUT_SAMPLE") == "ambiguous" {
		// "一" encoded as GBK/CP936; these bytes are also syntactically valid UTF-8.
		gbk = []byte{0xd2, 0xbb}
	}
	if output == "stderr" {
		_, _ = os.Stderr.Write(gbk)
	} else {
		_, _ = os.Stdout.Write(gbk)
	}
	os.Exit(code)
}

func currentTestBinaryCommand(t *testing.T) string {
	t.Helper()
	spec, err := shell.Resolve("")
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	testBinary := os.Args[0]
	switch spec.Kind {
	case shell.KindPowerShell:
		return fmt.Sprintf("& '%s' '-test.run=TestWindowsShellRunProcessTreeHelper'", strings.ReplaceAll(testBinary, "'", "''"))
	case shell.KindCmd:
		if strings.ContainsAny(testBinary, " \t") {
			t.Fatalf("cmd.exe helper path cannot contain spaces: %q", testBinary)
		}
		return testBinary + " -test.run=TestWindowsShellRunProcessTreeHelper"
	default:
		t.Fatalf("unexpected Windows shell kind: %q", spec.Kind)
		return ""
	}
}

func windowsGBKOutputHelperCommandForSample(t *testing.T, stream string, exitCode int, sample string) string {
	t.Helper()
	return windowsGBKOutputHelperCommandWithEnv(t, stream, exitCode, map[string]string{
		"WHALE_GBK_OUTPUT_SAMPLE": sample,
	})
}

func windowsGBKOutputHelperCommand(t *testing.T, stream string, exitCode int) string {
	t.Helper()
	return windowsGBKOutputHelperCommandWithEnv(t, stream, exitCode, nil)
}

func windowsGBKOutputHelperCommandWithEnv(t *testing.T, stream string, exitCode int, extraEnv map[string]string) string {
	t.Helper()
	spec, err := shell.Resolve("")
	if err != nil {
		t.Fatalf("resolve shell: %v", err)
	}
	testBinary := os.Args[0]
	exitValue := "0"
	if exitCode != 0 {
		exitValue = "1"
	}
	switch spec.Kind {
	case shell.KindPowerShell:
		escapedBin := strings.ReplaceAll(testBinary, "'", "''")
		var env strings.Builder
		env.WriteString(fmt.Sprintf("$env:WHALE_GBK_OUTPUT_HELPER='1'; $env:WHALE_GBK_OUTPUT_STREAM='%s'; $env:WHALE_GBK_OUTPUT_EXIT='%s'; ", stream, exitValue))
		for key, value := range extraEnv {
			env.WriteString(fmt.Sprintf("$env:%s='%s'; ", key, strings.ReplaceAll(value, "'", "''")))
		}
		return fmt.Sprintf("%s& '%s' '-test.run=TestWindowsShellRunGBKOutputHelper'", env.String(), escapedBin)
	case shell.KindCmd:
		if strings.ContainsAny(testBinary, " \t") {
			t.Fatalf("cmd.exe helper path cannot contain spaces: %q", testBinary)
		}
		var env strings.Builder
		env.WriteString(fmt.Sprintf("set WHALE_GBK_OUTPUT_HELPER=1&& set WHALE_GBK_OUTPUT_STREAM=%s&& set WHALE_GBK_OUTPUT_EXIT=%s&& ", stream, exitValue))
		for key, value := range extraEnv {
			env.WriteString(fmt.Sprintf("set %s=%s&& ", key, value))
		}
		return fmt.Sprintf("%s%s -test.run TestWindowsShellRunGBKOutputHelper", env.String(), testBinary)
	default:
		t.Fatalf("unexpected Windows shell kind: %q", spec.Kind)
		return ""
	}
}

func shellTaskID(t *testing.T, content string) string {
	t.Helper()
	var env struct {
		Data struct {
			Payload struct {
				TaskID string `json:"task_id"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		t.Fatalf("unmarshal shell result: %v\n%s", err, content)
	}
	if env.Data.Payload.TaskID == "" {
		t.Fatalf("missing task id in shell result: %s", content)
	}
	return env.Data.Payload.TaskID
}

func shellWaitUntilDone(t *testing.T, ts *Toolset, taskID string) core.ToolResult {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		wait, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
			"task_id":    taskID,
			"timeout_ms": 5000,
		}))
		if err != nil || wait.IsError() {
			t.Fatalf("shell_wait failed: err=%v res=%+v", err, wait)
		}
		if shellPayloadBool(t, wait.ModelText, "done") {
			return wait
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for shell task %s", taskID)
	return core.ToolResult{}
}

func shellPayloadString(t *testing.T, content, key string) string {
	t.Helper()
	var env struct {
		Data struct {
			Payload map[string]any `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		t.Fatalf("unmarshal shell result: %v\n%s", err, content)
	}
	value, _ := env.Data.Payload[key].(string)
	return value
}

func shellPayloadBool(t *testing.T, content, key string) bool {
	t.Helper()
	var env struct {
		Data struct {
			Payload map[string]any `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		t.Fatalf("unmarshal shell result: %v\n%s", err, content)
	}
	value, _ := env.Data.Payload[key].(bool)
	return value
}

func waitForWindowsFile(path string, timeout time.Duration) <-chan bool {
	done := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				done <- true
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		done <- false
	}()
	return done
}
