package tools

import (
	"strings"
	"time"

	"github.com/usewhale/whale/internal/shellsafe"
)

const (
	defaultForegroundShellWaitMS = int((15 * time.Second) / time.Millisecond)
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
	for _, arg := range argv {
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "login") || strings.Contains(lower, "auth") || strings.Contains(lower, "onboard") {
			return shellDiagnosisForReason("interactive_or_auth")
		}
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
	return shellDiagnosis{}
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
	if task.status == "running" && shellOutputLooksInteractivePrompt(text) {
		last := task.lastOutput
		if last == nil || time.Since(*last) >= shellStallThreshold {
			return shellDiagnosisForReason("interactive_prompt")
		}
	}
	if task.status == "timeout" {
		return shellDiagnosisForReason("ordinary_timeout")
	}
	if task.status == "canceled" {
		return shellDiagnosisForReason("cancelled")
	}
	return shellDiagnosis{}
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
		return shellDiagnosis{Reason: reason, Hint: "Output looks like a network or sandbox restriction. Check network access before retrying.", SuggestedNextAction: "ask_user"}
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
