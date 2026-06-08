package tools

import (
	"testing"
	"time"
)

func TestShellPolicyClassifiesLongRunningCommands(t *testing.T) {
	tests := []struct {
		name           string
		command        string
		autoBackground bool
		reason         string
	}{
		{name: "go test", command: "go test ./internal/tui", autoBackground: true, reason: "build_test_long_running"},
		{name: "go test auth package path", command: "go test ./internal/auth", autoBackground: true, reason: "build_test_long_running"},
		{name: "go test authorizer package path", command: "go test ./pkg/authorizer", autoBackground: true, reason: "build_test_long_running"},
		{name: "go test pipeline", command: "go test -c -o /tmp/testbin ./internal/tui 2>&1 | head -20", autoBackground: true, reason: "build_test_long_running"},
		{name: "make build", command: "make build", autoBackground: true, reason: "build_test_long_running"},
		{name: "download", command: "curl https://example.com/file", autoBackground: true, reason: "download_long_running"},
		{name: "remote", command: "prlctl exec whale-test true", autoBackground: true, reason: "remote_command_long_running"},
		{name: "unknown simple", command: "pytest", autoBackground: true, reason: "unknown_long_running"},
		{name: "sudo", command: "sudo make test", autoBackground: true, reason: "interactive_or_auth"},
		{name: "auth arg", command: "gh auth login", autoBackground: true, reason: "interactive_or_auth"},
		{name: "plain sleep", command: "sleep 30", autoBackground: false, reason: "idle_sleep"},
		{name: "complex shell", command: "printf before; sleep 30", autoBackground: false, reason: "complex_shell"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellPolicy(tt.command, 300000)
			if got.AutoBackground != tt.autoBackground || got.Reason != tt.reason {
				t.Fatalf("policy = {auto:%v reason:%q}, want {auto:%v reason:%q}", got.AutoBackground, got.Reason, tt.autoBackground, tt.reason)
			}
		})
	}
}

func TestShellPolicyUsesConfiguredForegroundWait(t *testing.T) {
	waitCfg := foregroundShellWaitConfig{DefaultMS: 45000, MaxMS: 240000}
	auto := shellPolicyWithForegroundWait("go test ./...", 300000, waitCfg)
	if auto.ForegroundWaitMS != 45000 {
		t.Fatalf("auto-background foreground wait = %d, want 45000", auto.ForegroundWaitMS)
	}
	foreground := shellPolicyWithForegroundWait("printf before; sleep 30", 300000, waitCfg)
	if foreground.AutoBackground {
		t.Fatalf("complex shell should stay foreground: %#v", foreground)
	}
	if foreground.ForegroundWaitMS != 240000 {
		t.Fatalf("foreground wait = %d, want 240000", foreground.ForegroundWaitMS)
	}
	short := shellPolicyWithForegroundWait("go test ./...", 50, waitCfg)
	if short.ForegroundWaitMS != 50 {
		t.Fatalf("short requested foreground wait = %d, want 50", short.ForegroundWaitMS)
	}
}

func TestShellPolicyCapsDefaultForegroundWaits(t *testing.T) {
	auto := shellPolicy("go test ./...", 300000)
	if auto.ForegroundWaitMS != defaultForegroundShellWaitMS {
		t.Fatalf("auto-background foreground wait = %d, want %d", auto.ForegroundWaitMS, defaultForegroundShellWaitMS)
	}
	foreground := shellPolicy("printf before; sleep 30", 300000)
	if foreground.ForegroundWaitMS != maxForegroundShellWaitMS {
		t.Fatalf("regular foreground wait = %d, want %d", foreground.ForegroundWaitMS, maxForegroundShellWaitMS)
	}
}

func TestShellPolicyDoesNotAutoBackgroundInteractiveAuthWithoutPTY(t *testing.T) {
	orig := shellPTYSupportedForPolicy
	shellPTYSupportedForPolicy = func() bool { return false }
	t.Cleanup(func() {
		shellPTYSupportedForPolicy = orig
	})

	got := shellPolicy("npm login", 1000)
	if got.AutoBackground {
		t.Fatalf("interactive auth without PTY must stay foreground, got %#v", got)
	}
	if got.Reason != "interactive_or_auth" || got.SuggestedNextAction != "rerun_non_interactive" {
		t.Fatalf("unexpected no-PTY auth policy: %#v", got)
	}
}

func TestShellOutputDiagnosis(t *testing.T) {
	if !shellOutputLooksInteractivePrompt("Password:") {
		t.Fatal("expected password prompt to be detected")
	}
	if !shellOutputLooksNetworkBlocked("curl: (6) Could not resolve host: example.invalid") {
		t.Fatal("expected network failure to be detected")
	}
	if shellOutputLooksLikeProgress("fatal: locked") {
		t.Fatal("ordinary output should not be treated as progress")
	}
	if !shellOutputLooksLikeProgress("Downloading modules...") {
		t.Fatal("expected explicit progress output to be detected")
	}
	diagnosis := shellTimeoutDiagnosis(shellTaskSnapshot{Stderr: "fatal: locked"}, shellTimeoutContext{
		Policy:             shellContinuationPolicy{Reason: "complex_shell"},
		RequestedTimeoutMS: defaultForegroundShellWaitMS,
		EffectiveTimeoutMS: defaultForegroundShellWaitMS,
	})
	if diagnosis.Reason == "foreground_timeout_too_short" {
		t.Fatalf("ordinary output should not produce timeout-too-short diagnosis: %#v", diagnosis)
	}
	if diagnosis.Reason != "ordinary_timeout" {
		t.Fatalf("unexpected diagnosis: %#v", diagnosis)
	}

	authPipeDiagnosis := shellTimeoutDiagnosis(shellTaskSnapshot{Transport: shellTransportPipe}, shellTimeoutContext{
		Policy:             shellContinuationPolicy{Reason: "interactive_or_auth"},
		RequestedTimeoutMS: defaultForegroundShellWaitMS,
		EffectiveTimeoutMS: defaultForegroundShellWaitMS,
	})
	if authPipeDiagnosis.SuggestedNextAction == "write_stdin" {
		t.Fatalf("pipe auth diagnosis must not suggest write_stdin: %#v", authPipeDiagnosis)
	}
	if authPipeDiagnosis.SuggestedNextAction != "rerun_non_interactive" {
		t.Fatalf("unexpected pipe auth diagnosis: %#v", authPipeDiagnosis)
	}
}

func TestShellTimeoutDiagnosisMissingPolicyStaysGeneric(t *testing.T) {
	task := &shellTask{
		Command:   "go test ./...",
		StartedAt: time.Now(),
		status:    "timeout",
	}
	diagnosis := task.timeoutDiagnosisLocked()
	if diagnosis.Reason != "ordinary_timeout" {
		t.Fatalf("diagnosis = %#v, want generic ordinary timeout", diagnosis)
	}
}
