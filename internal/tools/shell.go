package tools

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shellsafe"
)

const maxForegroundShellWaitMS = 120000

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
		cctx, cancel := context.WithTimeout(context.Background(), timeout)
		task.setCancel(cancel)
		go func() {
			defer b.tasks.completed(task.ID)
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

	requestedTimeoutMS := in.TimeoutMS
	policy := shellPolicy(in.Command, requestedTimeoutMS)
	effectiveTimeoutMS := policy.ForegroundWaitMS
	task := b.tasks.create(in.Command, relCWD)
	cctx, cancel := context.WithTimeout(context.Background(), time.Duration(maxBackgroundShellTimeoutMS)*time.Millisecond)
	task.setCancel(cancel)
	go func() {
		defer b.tasks.completed(task.ID)
		defer cancel()
		runShellBackground(cctx, workdir, in.Command, task)
	}()

	timer := time.NewTimer(time.Duration(effectiveTimeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		task.cancelRun()
		task.waitDone()
		b.tasks.release(task.ID)
		return marshalToolError(call, "cancelled", "command canceled"), nil
	case <-task.done:
		snap := task.snapshot()
		b.tasks.release(task.ID)
		return shellRunForegroundFinalResult(call, snap, requestedTimeoutMS, effectiveTimeoutMS, warnings)
	case <-timer.C:
		if snap := task.snapshot(); snap.Status != "running" {
			b.tasks.release(task.ID)
			return shellRunForegroundFinalResult(call, snap, requestedTimeoutMS, effectiveTimeoutMS, warnings)
		}
		if policy.AutoBackground {
			return marshalToolResult(call, shellRunBackgroundedResult(task.snapshot(), requestedTimeoutMS, effectiveTimeoutMS, warnings, shellDiagnosis{
				Reason:              policy.Reason,
				Hint:                policy.Hint,
				SuggestedNextAction: policy.SuggestedNextAction,
			}))
		}
		task.cancelRun()
		task.waitDone()
		snap := task.snapshot()
		b.tasks.release(task.ID)
		return shellRunForegroundTimeoutResult(call, snap, requestedTimeoutMS, effectiveTimeoutMS, warnings)
	}
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
	Diagnosis          shellDiagnosis
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
	result := map[string]any{
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
	if diagnosis := in.Diagnosis.asMap(); diagnosis != nil {
		result["diagnosis"] = diagnosis
	}
	return result
}

func shellRunForegroundFinalResult(call core.ToolCall, snap shellTaskSnapshot, requestedTimeoutMS int, effectiveTimeoutMS int, warnings []string) (core.ToolResult, error) {
	stdout, stdoutTr := truncateTextSmart(snap.Stdout, maxToolTextChars)
	stderr, stderrTr := truncateTextSmart(snap.Stderr, maxToolTextChars)
	durationMS := shellTaskDurationMS(snap)
	status := "ok"
	summaryParts := []string{"command completed"}
	success := true
	code := "ok"
	message := ""
	timedOut := false
	switch snap.Status {
	case "failed":
		status = "error"
		summaryParts = []string{"command failed"}
		if hint := summarizeText(snap.Stderr, 220); hint != "" {
			summaryParts = append(summaryParts, hint)
		}
		success = false
		code = "exec_failed"
		message = "command failed"
	case "timeout":
		status = "timeout"
		summaryParts = []string{"command timed out"}
		success = false
		code = "timeout"
		message = "command timed out"
		timedOut = true
	case "canceled":
		return marshalToolError(call, "cancelled", "command canceled"), nil
	}
	if snap.Status == "exited" {
		if hint := summarizeText(snap.Stderr, 200); hint != "" {
			summaryParts = []string{"stderr:" + hint}
		}
	}
	result := shellRunResult(shellRunResultInput{
		Status:             status,
		SummaryParts:       summaryParts,
		Command:            snap.Command,
		CWD:                snap.CWD,
		Stdout:             stdout,
		Stderr:             stderr,
		StdoutRaw:          snap.Stdout,
		StderrRaw:          snap.Stderr,
		StdoutTruncation:   stdoutTr,
		StderrTruncation:   stderrTr,
		DurationMS:         durationMS,
		ExitCode:           snap.ExitCode,
		TimedOut:           timedOut,
		RequestedTimeoutMS: requestedTimeoutMS,
		EffectiveTimeoutMS: effectiveTimeoutMS,
		Diagnosis:          snap.Diagnosis,
		Warnings:           warnings,
	})
	if success {
		return marshalToolResult(call, result)
	}
	content, marshalErr := core.MarshalToolEnvelope(core.ToolEnvelope{Success: false, Data: result, Message: message, Code: code})
	if marshalErr != nil {
		return marshalToolError(call, code, message), nil
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
}

func shellRunForegroundTimeoutResult(call core.ToolCall, snap shellTaskSnapshot, requestedTimeoutMS int, effectiveTimeoutMS int, warnings []string) (core.ToolResult, error) {
	stdout, stdoutTr := truncateTextSmart(snap.Stdout, maxToolTextChars)
	stderr, stderrTr := truncateTextSmart(snap.Stderr, maxToolTextChars)
	result := shellRunResult(shellRunResultInput{
		Status:             "timeout",
		SummaryParts:       []string{"command timed out"},
		Command:            snap.Command,
		CWD:                snap.CWD,
		Stdout:             stdout,
		Stderr:             stderr,
		StdoutRaw:          snap.Stdout,
		StderrRaw:          snap.Stderr,
		StdoutTruncation:   stdoutTr,
		StderrTruncation:   stderrTr,
		DurationMS:         shellTaskDurationMS(snap),
		ExitCode:           nil,
		TimedOut:           true,
		RequestedTimeoutMS: requestedTimeoutMS,
		EffectiveTimeoutMS: effectiveTimeoutMS,
		Diagnosis:          snap.Diagnosis,
		Warnings:           warnings,
	})
	content, marshalErr := core.MarshalToolEnvelope(core.ToolEnvelope{Success: false, Data: result, Message: "command timed out", Code: "timeout"})
	if marshalErr != nil {
		return marshalToolError(call, "timeout", "command timed out"), nil
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
}

func shellRunBackgroundedResult(snap shellTaskSnapshot, requestedTimeoutMS int, effectiveTimeoutMS int, warnings []string, diagnosis shellDiagnosis) map[string]any {
	summaryParts := []string{"command still running in background"}
	if len(warnings) > 0 {
		summaryParts = append(summaryParts, "warning: "+strings.Join(warnings, "; "))
	}
	result := map[string]any{
		"status": "running",
		"metrics": map[string]any{
			"duration_ms":          shellTaskDurationMS(snap),
			"requested_timeout_ms": requestedTimeoutMS,
			"effective_timeout_ms": effectiveTimeoutMS,
			"timeout_clamped":      requestedTimeoutMS > 0 && requestedTimeoutMS != effectiveTimeoutMS,
			"auto_backgrounded":    true,
		},
		"payload": map[string]any{
			"task_id":    snap.ID,
			"command":    snap.Command,
			"cwd":        snap.CWD,
			"done":       false,
			"started_at": snap.StartedAt,
		},
		"warnings": warnings,
		"summary":  strings.Join(summaryParts, " | "),
	}
	if diagnosis.Reason == "" {
		diagnosis = snap.Diagnosis
	}
	if diag := diagnosis.asMap(); diag != nil {
		result["diagnosis"] = diag
	}
	return result
}

func shellTaskDurationMS(snap shellTaskSnapshot) int64 {
	end := time.Now()
	if snap.FinishedAt != nil {
		end = *snap.FinishedAt
	}
	return end.Sub(snap.StartedAt).Milliseconds()
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
			result := map[string]any{
				"status": "running",
				"payload": map[string]any{
					"task_id":        snap.ID,
					"command":        snap.Command,
					"cwd":            snap.CWD,
					"stdout":         summarizeTaskOutputForRunning(snap.Stdout),
					"stderr":         summarizeTaskOutputForRunning(snap.Stderr),
					"done":           false,
					"started_at":     snap.StartedAt,
					"last_output_at": snap.LastOutput,
				},
				"metrics": map[string]any{
					"duration_ms": shellTaskDurationMS(snap),
				},
				"summary": "task still running",
			}
			if diagnosis := snap.Diagnosis.asMap(); diagnosis != nil {
				result["diagnosis"] = diagnosis
			}
			return marshalToolResult(call, result)
		case <-poll.C:
		}
	}
}

func (b *Toolset) shellWaitFinalResult(call core.ToolCall, snap shellTaskSnapshot) (core.ToolResult, error) {
	stdout, stdoutTr := truncateTextSmart(snap.Stdout, maxToolTextChars)
	stderr, stderrTr := truncateTextSmart(snap.Stderr, maxToolTextChars)
	b.tasks.release(snap.ID)
	result := map[string]any{
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
			"duration_ms":       shellTaskDurationMS(snap),
		},
	}
	if diagnosis := snap.Diagnosis.asMap(); diagnosis != nil {
		result["diagnosis"] = diagnosis
	}
	return marshalToolResult(call, result)
}

func (b *Toolset) shellCancel(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return marshalToolError(call, "invalid_args", "task_id is required"), nil
	}
	task, ok := b.tasks.get(taskID)
	if !ok {
		return marshalToolError(call, "not_found", "task not found"), nil
	}
	task.cancelRun()
	select {
	case <-ctx.Done():
		return marshalToolError(call, "cancelled", ctx.Err().Error()), nil
	case <-task.done:
	case <-time.After(3 * time.Second):
	}
	snap := task.snapshot()
	return marshalToolResult(call, map[string]any{
		"status": "cancelled",
		"payload": map[string]any{
			"task_id":    snap.ID,
			"command":    snap.Command,
			"cwd":        snap.CWD,
			"done":       snap.Status != "running",
			"started_at": snap.StartedAt,
			"ended_at":   snap.FinishedAt,
		},
		"metrics": map[string]any{
			"duration_ms": shellTaskDurationMS(snap),
		},
		"diagnosis": shellDiagnosisForReason("cancelled").asMap(),
		"summary":   "task cancelled",
	})
}

func summarizeTaskOutputForRunning(text string) string {
	out, _ := truncateTextSmart(text, maxToolTextChars)
	return out
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
