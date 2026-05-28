package tools

import "testing"

func TestShellPolicyClassifiesLongRunningCommands(t *testing.T) {
	tests := []struct {
		name           string
		command        string
		autoBackground bool
		reason         string
	}{
		{name: "go test", command: "go test ./internal/tui", autoBackground: true, reason: "build_test_long_running"},
		{name: "go test pipeline", command: "go test -c -o /tmp/testbin ./internal/tui 2>&1 | head -20", autoBackground: true, reason: "build_test_long_running"},
		{name: "make build", command: "make build", autoBackground: true, reason: "build_test_long_running"},
		{name: "download", command: "curl https://example.com/file", autoBackground: true, reason: "download_long_running"},
		{name: "remote", command: "prlctl exec whale-test true", autoBackground: true, reason: "remote_command_long_running"},
		{name: "unknown simple", command: "pytest", autoBackground: true, reason: "unknown_long_running"},
		{name: "sudo", command: "sudo make test", autoBackground: false, reason: "interactive_or_auth"},
		{name: "auth arg", command: "gh auth login", autoBackground: false, reason: "interactive_or_auth"},
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

func TestShellOutputDiagnosis(t *testing.T) {
	if !shellOutputLooksInteractivePrompt("Password:") {
		t.Fatal("expected password prompt to be detected")
	}
	if !shellOutputLooksNetworkBlocked("curl: (6) Could not resolve host: example.invalid") {
		t.Fatal("expected network failure to be detected")
	}
}
