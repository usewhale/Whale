package tools

import (
	"strings"
	"time"

	"github.com/usewhale/whale/internal/shellsafe"
)

var shellStallThreshold = 45 * time.Second

type shellContinuationPolicy struct {
	AutoBackground      bool
	ForegroundWaitMS    int
	Reason              string
	Hint                string
	SuggestedNextAction string
}

type shellDiagnosis struct {
	Reason              string
	Hint                string
	SuggestedNextAction string
}

type shellTimeoutContext struct {
	Policy             shellContinuationPolicy
	RequestedTimeoutMS int
	EffectiveTimeoutMS int
	BackgroundRuntime  bool
}

func (d shellDiagnosis) asMap() map[string]any {
	if d.Reason == "" && d.Hint == "" && d.SuggestedNextAction == "" {
		return nil
	}
	out := map[string]any{}
	if d.Reason != "" {
		out["reason"] = d.Reason
	}
	if d.Hint != "" {
		out["hint"] = d.Hint
	}
	if d.SuggestedNextAction != "" {
		out["suggested_next_action"] = d.SuggestedNextAction
	}
	return out
}

func shellPolicy(command string, requestedTimeoutMS int) shellContinuationPolicy {
	policy := shellContinuationPolicy{
		AutoBackground:      true,
		ForegroundWaitMS:    requestedForegroundWaitMS(requestedTimeoutMS, defaultForegroundShellWaitMS),
		Reason:              "unknown_long_running",
		Hint:                "Command is still running. Use shell_wait with the task_id instead of rerunning the command.",
		SuggestedNextAction: "shell_wait",
	}
	parts := shellPolicyParts(command)
	if len(parts) == 0 {
		policy.AutoBackground = false
		policy.Reason = "ordinary_timeout"
		policy.Hint = "Command timed out."
		policy.SuggestedNextAction = "rerun_with_longer_timeout"
		policy.ForegroundWaitMS = requestedForegroundWaitMS(requestedTimeoutMS, maxForegroundShellWaitMS)
		return policy
	}

	recognizedReason := ""
	for _, part := range parts {
		partPolicy := shellCommandPartPolicy(part)
		if partPolicy.Reason == "interactive_or_auth" || partPolicy.Reason == "idle_sleep" || partPolicy.Reason == "complex_shell" {
			policy.AutoBackground = false
			policy.Reason = partPolicy.Reason
			policy.Hint = partPolicy.Hint
			policy.SuggestedNextAction = partPolicy.SuggestedNextAction
			policy.ForegroundWaitMS = requestedForegroundWaitMS(requestedTimeoutMS, maxForegroundShellWaitMS)
			return policy
		}
		if recognizedReason == "" && partPolicy.Reason != "" {
			recognizedReason = partPolicy.Reason
		}
	}
	if recognizedReason != "" {
		policy.Reason = recognizedReason
		policy.Hint = shellDiagnosisForReason(recognizedReason).Hint
	}
	return policy
}

func requestedForegroundWaitMS(requested int, limit int) int {
	if requested <= 0 {
		return defaultForegroundShellWaitMS
	}
	if requested > limit {
		return limit
	}
	return requested
}

func foregroundShellWaitMS(requested int, autoBackground bool) int {
	limit := maxForegroundShellWaitMS
	if autoBackground {
		limit = defaultForegroundShellWaitMS
	}
	return requestedForegroundWaitMS(requested, limit)
}

func shellCommandPartPolicy(command string) shellDiagnosis {
	argv, ok := parseShellPolicyCommand(command)
	if !ok || len(argv) == 0 {
		return shellDiagnosisForReason("complex_shell")
	}
	base := strings.ToLower(argv[0])
	if strings.Contains(base, "/") {
		base = base[strings.LastIndex(base, "/")+1:]
	}
	switch base {
	case "sudo", "su", "ssh", "login", "passwd", "vi", "vim", "nano", "emacs", "less", "more":
		return shellDiagnosisForReason("interactive_or_auth")
	case "sleep":
		return shellDiagnosisForReason("idle_sleep")
	}
	switch base {
	case "make":
		for _, arg := range argv[1:] {
			switch strings.ToLower(arg) {
			case "test", "test-tui", "test-evals", "build":
				return shellDiagnosisForReason("build_test_long_running")
			}
		}
	case "go":
		if len(argv) > 1 && argv[1] == "test" {
			return shellDiagnosisForReason("build_test_long_running")
		}
	case "brew":
		if len(argv) > 1 && (argv[1] == "install" || argv[1] == "upgrade") {
			return shellDiagnosisForReason("package_manager_long_running")
		}
	case "curl", "wget":
		return shellDiagnosisForReason("download_long_running")
	case "gh":
		if len(argv) > 2 && argv[1] == "run" && argv[2] == "watch" {
			return shellDiagnosisForReason("watch_long_running")
		}
	case "prlctl":
		if len(argv) > 1 && argv[1] == "exec" {
			return shellDiagnosisForReason("remote_command_long_running")
		}
	}
	for _, arg := range argv[1:] {
		if shellArgLooksInteractiveAuth(arg) {
			return shellDiagnosisForReason("interactive_or_auth")
		}
	}
	return shellDiagnosis{}
}

func shellArgLooksInteractiveAuth(arg string) bool {
	lower := strings.Trim(strings.ToLower(arg), "-")
	if lower == "" || strings.ContainsAny(lower, `/\`) {
		return false
	}
	switch lower {
	case "login", "auth", "authenticate", "onboard":
		return true
	default:
		return false
	}
}

func shellPolicyParts(command string) []string {
	andParts := shellCommandParts(command)
	out := make([]string, 0, len(andParts))
	for _, part := range andParts {
		if pipeline, ok := shellsafe.SplitPipeline(part); ok {
			for _, pipePart := range pipeline {
				if trimmed := strings.TrimSpace(pipePart); trimmed != "" {
					out = append(out, trimmed)
				}
			}
			continue
		}
		out = append(out, part)
	}
	return out
}

func parseShellPolicyCommand(command string) ([]string, bool) {
	if argv, ok := parsePOSIXReadOnlyShellCommand(command); ok {
		return argv, true
	}
	if strings.ContainsAny(command, ";`") || strings.Contains(command, "$(") || strings.Contains(command, "&&") || strings.Contains(command, "||") {
		return nil, false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, false
	}
	argv := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "&" || field == "|" {
			return nil, false
		}
		if shellPolicyRedirectionToken(field) {
			continue
		}
		if strings.ContainsAny(field, "<>") {
			return nil, false
		}
		argv = append(argv, field)
	}
	return argv, len(argv) > 0
}

func shellPolicyRedirectionToken(field string) bool {
	switch field {
	case ">", ">>", "1>", "1>>", "2>", "2>>", "&>", "&>>", "2>&1", "1>&2":
		return true
	default:
		return false
	}
}

func diagnoseShellTaskLocked(task *shellTask) shellDiagnosis {
	if task == nil {
		return shellDiagnosis{}
	}
	if task.diagnosis.Reason != "" {
		return task.diagnosis
	}
	stdout := decodeShellOutput(task.stdout.Bytes())
	stderr := decodeShellOutput(task.stderr.Bytes())
	text := strings.TrimSpace(stderr + "\n" + stdout)
	if shellOutputLooksNetworkBlocked(text) {
		return shellDiagnosisForReason("network_blocked")
	}
	if shellOutputLooksInteractivePrompt(text) {
		if task.status != "running" {
			return shellDiagnosisForReason("interactive_prompt")
		}
		if last := task.lastOutput; last == nil || time.Since(*last) >= shellStallThreshold {
			return shellDiagnosisForReason("interactive_prompt")
		}
	}
	if task.status == "timeout" {
		return task.timeoutDiagnosisLocked()
	}
	if task.status == "canceled" {
		return shellDiagnosisForReason("cancelled")
	}
	return shellDiagnosis{}
}

func (t *shellTask) timeoutDiagnosisLocked() shellDiagnosis {
	timeoutCtx := t.timeoutCtx
	if timeoutCtx.Policy.Reason == "" {
		timeoutCtx.Policy = shellPolicy(t.Command, 0)
	}
	return shellTimeoutDiagnosis(t.snapshotWithoutDiagnosisLocked(), timeoutCtx)
}

func (t *shellTask) snapshotWithoutDiagnosisLocked() shellTaskSnapshot {
	return shellTaskSnapshot{
		ID:         t.ID,
		Command:    t.Command,
		CWD:        t.CWD,
		Status:     t.status,
		ExitCode:   t.exitCode,
		StartedAt:  t.StartedAt,
		FinishedAt: t.finishedAt,
		Stdout:     decodeShellOutput(t.stdout.Bytes()),
		Stderr:     decodeShellOutput(t.stderr.Bytes()),
		LastOutput: t.lastOutput,
	}
}

func shellTimeoutDiagnosis(snap shellTaskSnapshot, timeoutCtx shellTimeoutContext) shellDiagnosis {
	text := strings.TrimSpace(snap.Stderr + "\n" + snap.Stdout)
	if shellOutputLooksNetworkBlocked(text) {
		return shellDiagnosisForReason("network_blocked")
	}
	if shellOutputLooksInteractivePrompt(text) {
		diagnosis := shellDiagnosisForReason("interactive_prompt")
		diagnosis.SuggestedNextAction = "rerun_non_interactive"
		return diagnosis
	}
	if timeoutCtx.BackgroundRuntime {
		return shellDiagnosisForReason("background_runtime_timeout")
	}
	switch timeoutCtx.Policy.Reason {
	case "interactive_or_auth":
		return shellDiagnosisForReason("interactive_or_auth")
	case "build_test_long_running":
		return shellDiagnosisForReason("build_or_test_timeout")
	}
	if shellOutputLooksLikeProgress(text) || timeoutCtx.RequestedTimeoutMS > 0 && timeoutCtx.RequestedTimeoutMS <= 1000 {
		return shellDiagnosisForReason("foreground_timeout_too_short")
	}
	if timeoutCtx.EffectiveTimeoutMS > 0 && timeoutCtx.EffectiveTimeoutMS < defaultForegroundShellWaitMS {
		return shellDiagnosisForReason("foreground_timeout_too_short")
	}
	return shellDiagnosisForReason("ordinary_timeout")
}

func shellDiagnosisForReason(reason string) shellDiagnosis {
	switch reason {
	case "build_test_long_running":
		return shellDiagnosis{Reason: reason, Hint: "Build or test command is still running. Wait for the existing task instead of rerunning it.", SuggestedNextAction: "shell_wait"}
	case "package_manager_long_running":
		return shellDiagnosis{Reason: reason, Hint: "Package manager command is still running. Wait for the existing task because reruns can duplicate work.", SuggestedNextAction: "shell_wait"}
	case "download_long_running":
		return shellDiagnosis{Reason: reason, Hint: "Download command is still running. Wait for the existing task before retrying.", SuggestedNextAction: "shell_wait"}
	case "watch_long_running":
		return shellDiagnosis{Reason: reason, Hint: "Watch command is still running. Wait for more output or cancel the task when enough evidence is collected.", SuggestedNextAction: "shell_wait"}
	case "remote_command_long_running":
		return shellDiagnosis{Reason: reason, Hint: "Remote command is still running. Wait for the task or inspect the remote target before rerunning.", SuggestedNextAction: "shell_wait"}
	case "unknown_long_running":
		return shellDiagnosis{Reason: reason, Hint: "Command is still running. Use shell_wait with the task_id instead of rerunning the command.", SuggestedNextAction: "shell_wait"}
	case "interactive_or_auth":
		return shellDiagnosis{Reason: reason, Hint: "Command may require interactive input or authentication, so Whale will not auto-background it.", SuggestedNextAction: "ask_user"}
	case "interactive_prompt":
		return shellDiagnosis{Reason: reason, Hint: "Command appears blocked on an interactive prompt. Cancel it and rerun with non-interactive flags or piped input.", SuggestedNextAction: "shell_cancel"}
	case "network_blocked":
		return shellDiagnosis{Reason: reason, Hint: "Output looks like a network or sandbox restriction. Check network access before retrying.", SuggestedNextAction: "check_network"}
	case "foreground_timeout_too_short":
		return shellDiagnosis{Reason: reason, Hint: "The foreground timeout was too short for this command. Rerun with a longer timeout instead of changing the command.", SuggestedNextAction: "rerun_with_longer_timeout"}
	case "build_or_test_timeout":
		return shellDiagnosis{Reason: reason, Hint: "Build or test command was stopped by timeout. Rerun with a longer timeout or let it continue in the background.", SuggestedNextAction: "rerun_with_longer_timeout"}
	case "background_runtime_timeout":
		return shellDiagnosis{Reason: reason, Hint: "Background task reached its runtime limit. Rerun with a larger timeout_ms or split the command into a shorter step.", SuggestedNextAction: "rerun_background_with_longer_timeout"}
	case "idle_sleep":
		return shellDiagnosis{Reason: reason, Hint: "Plain sleep commands are not useful to keep in the background.", SuggestedNextAction: "rerun_with_shorter_timeout"}
	case "complex_shell":
		return shellDiagnosis{Reason: reason, Hint: "Complex shell snippets are kept in the foreground so timeout output remains visible.", SuggestedNextAction: "rerun_with_longer_timeout"}
	case "ordinary_timeout":
		return shellDiagnosis{Reason: reason, Hint: "Command exceeded the foreground timeout and was stopped.", SuggestedNextAction: "rerun_with_longer_timeout"}
	case "cancelled":
		return shellDiagnosis{Reason: reason, Hint: "Command was cancelled.", SuggestedNextAction: "none"}
	default:
		return shellDiagnosis{}
	}
}

func shellOutputLooksInteractivePrompt(text string) bool {
	lower := strings.ToLower(text)
	patterns := []string{
		"password:",
		"passphrase",
		"(y/n)",
		"[y/n]",
		"yes/no",
		"press enter",
		"are you sure",
		"continue?",
		"do you want to continue",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func shellOutputLooksNetworkBlocked(text string) bool {
	lower := strings.ToLower(text)
	patterns := []string{
		"could not resolve host",
		"temporary failure in name resolution",
		"network is unreachable",
		"operation not permitted",
		"connection timed out",
		"connection refused",
		"proxyconnect tcp",
		"tls handshake timeout",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func shellOutputLooksLikeProgress(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	patterns := []string{
		"downloading",
		"building",
		"compiling",
		"running",
		"fetching",
		"installing",
		"extracting",
		"resolving",
		"progress",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return strings.Contains(trimmed, "%") || strings.Contains(trimmed, "...") || strings.Contains(trimmed, "=>")
}
