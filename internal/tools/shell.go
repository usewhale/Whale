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
	"github.com/usewhale/whale/internal/shellsafe"
)

func (b *Toolset) shellRun(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
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
	warnings := b.shellRunWarnings(in.Command)
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
			defer b.tasks.completed(task.ID)
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
			"warnings": warnings,
			"summary":  "background shell task started",
		})
	}

	timeout := 15 * time.Second
	requestedTimeoutMS := in.TimeoutMS
	effectiveTimeoutMS := int(timeout / time.Millisecond)
	if in.TimeoutMS > 0 {
		if in.TimeoutMS > 120000 {
			in.TimeoutMS = 120000
		}
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
		effectiveTimeoutMS = in.TimeoutMS
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	spec, err := shell.Resolve(in.Command)
	if err != nil {
		return marshalToolError(call, "exec_failed", err.Error()), nil
	}
	cmd := shell.Command(spec)
	cmd.Dir = workdir
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	start := time.Now()
	err = shell.RunCommand(cctx, cmd)
	durationMS := time.Since(start).Milliseconds()

	stdoutRaw := decodeShellOutput(stdoutBuf.Bytes())
	stderrRaw := decodeShellOutput(stderrBuf.Bytes())
	stdout, stdoutTr := truncateTextSmart(stdoutRaw, maxToolTextChars)
	stderr, stderrTr := truncateTextSmart(stderrRaw, maxToolTextChars)

	exitCode := 0
	summaryParts := make([]string, 0, 4)
	if cctx.Err() == context.Canceled {
		return marshalToolError(call, "cancelled", "command canceled"), nil
	}
	if cctx.Err() == context.DeadlineExceeded {
		result := shellRunResult(shellRunResultInput{
			Status:             "timeout",
			SummaryParts:       append(summaryParts, "command timed out"),
			Command:            in.Command,
			CWD:                relCWD,
			Stdout:             stdout,
			Stderr:             stderr,
			StdoutRaw:          stdoutRaw,
			StderrRaw:          stderrRaw,
			StdoutTruncation:   stdoutTr,
			StderrTruncation:   stderrTr,
			DurationMS:         durationMS,
			ExitCode:           nil,
			TimedOut:           true,
			RequestedTimeoutMS: requestedTimeoutMS,
			EffectiveTimeoutMS: effectiveTimeoutMS,
			Warnings:           warnings,
		})
		content, marshalErr := core.MarshalToolEnvelope(core.ToolEnvelope{Success: false, Data: result, Message: "command timed out", Code: "timeout"})
		if marshalErr != nil {
			return marshalToolError(call, "timeout", "command timed out"), nil
		}
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
	}
	if err != nil {
		var ex *exec.ExitError
		if errors.As(err, &ex) {
			exitCode = ex.ExitCode()
			summaryParts = append(summaryParts, "command failed")
			failHint := summarizeText(stderrRaw, 220)
			if failHint != "" {
				summaryParts = append(summaryParts, failHint)
			}
			result := shellRunResult(shellRunResultInput{
				Status:             "error",
				SummaryParts:       summaryParts,
				Command:            in.Command,
				CWD:                relCWD,
				Stdout:             stdout,
				Stderr:             stderr,
				StdoutRaw:          stdoutRaw,
				StderrRaw:          stderrRaw,
				StdoutTruncation:   stdoutTr,
				StderrTruncation:   stderrTr,
				DurationMS:         durationMS,
				ExitCode:           &exitCode,
				TimedOut:           false,
				RequestedTimeoutMS: requestedTimeoutMS,
				EffectiveTimeoutMS: effectiveTimeoutMS,
				Warnings:           warnings,
			})
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
	result := shellRunResult(shellRunResultInput{
		Status:             "ok",
		SummaryParts:       summaryParts,
		Command:            in.Command,
		CWD:                relCWD,
		Stdout:             stdout,
		Stderr:             stderr,
		StdoutRaw:          stdoutRaw,
		StderrRaw:          stderrRaw,
		StdoutTruncation:   stdoutTr,
		StderrTruncation:   stderrTr,
		DurationMS:         durationMS,
		ExitCode:           &exitCode,
		TimedOut:           false,
		RequestedTimeoutMS: requestedTimeoutMS,
		EffectiveTimeoutMS: effectiveTimeoutMS,
		Warnings:           warnings,
	})
	return marshalToolResult(call, result)
}

type shellRunResultInput struct {
	Status             string
	SummaryParts       []string
	Command            string
	CWD                string
	Stdout             string
	Stderr             string
	StdoutRaw          string
	StderrRaw          string
	StdoutTruncation   truncationMeta
	StderrTruncation   truncationMeta
	DurationMS         int64
	ExitCode           *int
	TimedOut           bool
	RequestedTimeoutMS int
	EffectiveTimeoutMS int
	Warnings           []string
}

func shellRunResult(in shellRunResultInput) map[string]any {
	summaryParts := append([]string(nil), in.SummaryParts...)
	if len(in.Warnings) > 0 {
		summaryParts = append(summaryParts, "warning: "+strings.Join(in.Warnings, "; "))
	}
	if len(summaryParts) == 0 {
		summaryParts = append(summaryParts, "command completed")
	}
	metrics := map[string]any{
		"exit_code":            in.ExitCode,
		"duration_ms":          in.DurationMS,
		"stdout_chars":         len([]rune(in.StdoutRaw)),
		"stderr_chars":         len([]rune(in.StderrRaw)),
		"stdout_truncation":    in.StdoutTruncation,
		"stderr_truncation":    in.StderrTruncation,
		"timed_out":            in.TimedOut,
		"command_was_present":  strings.TrimSpace(in.Command) != "",
		"requested_timeout_ms": in.RequestedTimeoutMS,
		"effective_timeout_ms": in.EffectiveTimeoutMS,
		"timeout_clamped":      in.RequestedTimeoutMS > 0 && in.EffectiveTimeoutMS > 0 && in.RequestedTimeoutMS != in.EffectiveTimeoutMS,
	}
	return map[string]any{
		"status":  in.Status,
		"metrics": metrics,
		"payload": map[string]any{
			"command": in.Command,
			"cwd":     in.CWD,
			"stdout":  in.Stdout,
			"stderr":  in.Stderr,
		},
		"warnings": in.Warnings,
		"summary":  strings.Join(summaryParts, " | "),
	}
}

func (b *Toolset) shellRunWarnings(command string) []string {
	original := strings.TrimSpace(b.originalWorkspace)
	worktree := strings.TrimSpace(b.worktreeRoot)
	if original == "" || worktree == "" || original == worktree {
		return nil
	}
	var warnings []string
	for _, part := range shellCommandParts(command) {
		argv, ok := parsePOSIXReadOnlyShellCommand(part)
		if !ok || len(argv) == 0 {
			if shellCommandMentionsOriginalWorkspace(part, original) {
				warnings = append(warnings, "command references the original workspace; this session is running in a worktree")
			}
			continue
		}
		if argv[0] == "cd" && len(argv) >= 2 && samePath(argv[1], original) {
			warnings = append(warnings, "command cd's to the original workspace; keep work in the current worktree unless the user explicitly asks otherwise")
			continue
		}
		if argv[0] == "git" {
			for i := 1; i+1 < len(argv); i++ {
				if argv[i] == "-C" && samePath(argv[i+1], original) {
					warnings = append(warnings, "command uses git -C with the original workspace; keep git commands in the current worktree unless the user explicitly asks otherwise")
					break
				}
			}
		}
	}
	return dedupeStrings(warnings)
}

func shellCommandParts(command string) []string {
	parts := []string{strings.TrimSpace(command)}
	if split, ok := shellsafe.SplitAndList(command); ok {
		parts = split
	}
	out := parts[:0]
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func samePath(a, b string) bool {
	a = cleanOptionalAbsPath(a)
	b = cleanOptionalAbsPath(b)
	return a != "" && b != "" && a == b
}

func shellCommandMentionsOriginalWorkspace(command, original string) bool {
	original = strings.TrimSpace(original)
	if original == "" {
		return false
	}
	return strings.Contains(command, "cd "+original) || strings.Contains(command, "git -C "+original)
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (b *Toolset) shellWait(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
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
			return b.shellWaitFinalResult(call, snap)
		}
		select {
		case <-ctx.Done():
			return marshalToolError(call, "cancelled", ctx.Err().Error()), nil
		case <-timer.C:
			snap = task.snapshot()
			if snap.Status != "running" {
				return b.shellWaitFinalResult(call, snap)
			}
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

func (b *Toolset) shellWaitFinalResult(call core.ToolCall, snap shellTaskSnapshot) (core.ToolResult, error) {
	stdout, stdoutTr := truncateTextSmart(snap.Stdout, maxToolTextChars)
	stderr, stderrTr := truncateTextSmart(snap.Stderr, maxToolTextChars)
	b.tasks.release(snap.ID)
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
