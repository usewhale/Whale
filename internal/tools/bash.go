package tools

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

var resolveShell = shell.Resolve

func shellCommand(command string) (string, []string) {
	spec, err := resolveShell(command)
	if err != nil {
		return "", nil
	}
	return spec.Bin, spec.Args
}

func (b *Toolset) execShell(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Command    string `json:"command"`
		TimeoutMS  int    `json:"timeout_ms"`
		Background bool   `json:"background"`
		CWD        string `json:"cwd"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	if strings.TrimSpace(in.Command) == "" {
		return marshalToolError(call, "invalid_args", "command is required"), nil
	}
	workdir, relCWD, err := b.resolveShellCWD(in.CWD)
	if err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	if in.Background {
		timeout := 5 * time.Minute
		if in.TimeoutMS > 0 {
			if in.TimeoutMS > maxBackgroundShellTimeoutMS {
				in.TimeoutMS = maxBackgroundShellTimeoutMS
			}
			timeout = time.Duration(in.TimeoutMS) * time.Millisecond
		}
		task := b.tasks.create(in.Command, relCWD)
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			runShellBackground(cctx, workdir, in.Command, task)
		}()
		return marshalToolResult(call, map[string]any{
			"status": "running",
			"payload": map[string]any{
				"task_id": task.ID,
				"command": in.Command,
				"cwd":     relCWD,
			},
			"summary": "background shell task started",
		})
	}

	timeout := 15 * time.Second
	if in.TimeoutMS > 0 {
		if in.TimeoutMS > 120000 {
			in.TimeoutMS = 120000
		}
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	spec, err := resolveShell(in.Command)
	if err != nil {
		return marshalToolError(call, "shell_unavailable", err.Error()), nil
	}
	cmd := exec.CommandContext(cctx, spec.Bin, spec.Args...)
	configureShellCommand(cmd)
	cmd.Dir = workdir
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	start := time.Now()
	err = cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	stdoutRaw := stdoutBuf.String()
	stderrRaw := stderrBuf.String()
	stdout, stdoutTr := truncateTextSmart(stdoutRaw, maxToolTextChars)
	stderr, stderrTr := truncateTextSmart(stderrRaw, maxToolTextChars)

	exitCode := 0
	status := "ok"
	summaryParts := make([]string, 0, 4)
	if cctx.Err() == context.Canceled {
		return marshalToolError(call, "cancelled", "command canceled"), nil
	}
	if cctx.Err() == context.DeadlineExceeded {
		return marshalToolError(call, "timeout", "command timed out"), nil
	}
	if err != nil {
		var ex *exec.ExitError
		if errors.As(err, &ex) {
			exitCode = ex.ExitCode()
			status = "error"
			summaryParts = append(summaryParts, "command failed")
			failHint := summarizeText(stderrRaw, 220)
			if failHint != "" {
				summaryParts = append(summaryParts, failHint)
			}
			result := map[string]any{
				"status": status,
				"metrics": map[string]any{
					"exit_code":           exitCode,
					"duration_ms":         durationMS,
					"stdout_chars":        len([]rune(stdoutRaw)),
					"stderr_chars":        len([]rune(stderrRaw)),
					"stdout_truncation":   stdoutTr,
					"stderr_truncation":   stderrTr,
					"timed_out":           false,
					"command_was_present": strings.TrimSpace(in.Command) != "",
				},
				"payload": map[string]any{
					"command": in.Command,
					"cwd":     relCWD,
					"stdout":  stdout,
					"stderr":  stderr,
				},
				"summary": strings.Join(summaryParts, " | "),
			}
			content, marshalErr := core.MarshalToolEnvelope(core.ToolEnvelope{Success: false, Data: result, Message: "command failed", Code: "exec_failed"})
			if marshalErr != nil {
				return marshalToolError(call, "exec_failed", "command failed"), nil
			}
			return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
		}
		return marshalToolError(call, "exec_failed", err.Error()), nil
	}

	if hint := summarizeText(stderrRaw, 200); hint != "" {
		summaryParts = append(summaryParts, "stderr:"+hint)
	}
	if len(summaryParts) == 0 {
		summaryParts = append(summaryParts, "command completed")
	}
	result := map[string]any{
		"status": status,
		"metrics": map[string]any{
			"exit_code":           exitCode,
			"duration_ms":         durationMS,
			"stdout_chars":        len([]rune(stdoutRaw)),
			"stderr_chars":        len([]rune(stderrRaw)),
			"stdout_truncation":   stdoutTr,
			"stderr_truncation":   stderrTr,
			"timed_out":           false,
			"command_was_present": strings.TrimSpace(in.Command) != "",
		},
		"payload": map[string]any{
			"command": in.Command,
			"cwd":     relCWD,
			"stdout":  stdout,
			"stderr":  stderr,
		},
		"summary": strings.Join(summaryParts, " | "),
	}
	return marshalToolResult(call, result)
}

func (b *Toolset) execShellWait(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		TaskID    string `json:"task_id"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return marshalToolError(call, "invalid_args", "task_id is required"), nil
	}
	task, ok := b.tasks.get(strings.TrimSpace(in.TaskID))
	if !ok {
		return marshalToolError(call, "not_found", "task not found"), nil
	}
	deadline := 20 * time.Second
	if in.TimeoutMS > 0 {
		if in.TimeoutMS > 120000 {
			in.TimeoutMS = 120000
		}
		deadline = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	timer := time.NewTimer(deadline)
	defer timer.Stop()

	for {
		snap := task.snapshot()
		if snap.Status != "running" {
			stdout, stdoutTr := truncateTextSmart(snap.Stdout, maxToolTextChars)
			stderr, stderrTr := truncateTextSmart(snap.Stderr, maxToolTextChars)
			return marshalToolResult(call, map[string]any{
				"status": snap.Status,
				"payload": map[string]any{
					"task_id":    snap.ID,
					"command":    snap.Command,
					"cwd":        snap.CWD,
					"stdout":     stdout,
					"stderr":     stderr,
					"done":       true,
					"started_at": snap.StartedAt,
					"ended_at":   snap.FinishedAt,
				},
				"metrics": map[string]any{
					"exit_code":         snap.ExitCode,
					"stdout_truncation": stdoutTr,
					"stderr_truncation": stderrTr,
				},
			})
		}
		select {
		case <-ctx.Done():
			return marshalToolError(call, "cancelled", ctx.Err().Error()), nil
		case <-timer.C:
			snap = task.snapshot()
			return marshalToolResult(call, map[string]any{
				"status": "running",
				"payload": map[string]any{
					"task_id":    snap.ID,
					"command":    snap.Command,
					"cwd":        snap.CWD,
					"done":       false,
					"started_at": snap.StartedAt,
				},
				"summary": "task still running",
			})
		case <-poll.C:
		}
	}
}

func (b *Toolset) resolveShellCWD(raw string) (abs string, rel string, err error) {
	abs, err = b.safePath(raw)
	if err != nil {
		return "", "", err
	}
	rel, err = filepath.Rel(b.root, abs)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		return abs, ".", nil
	}
	return abs, filepath.ToSlash(rel), nil
}
