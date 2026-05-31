package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDoctorReportsHealthyWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveCredentials(dataDir, Credentials{DeepSeekAPIKey: "sk-1234567890abcdef1234"}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	thinking := true
	if err := SaveConfigFile(GlobalConfigPath(dataDir), FileConfig{Model: "deepseek-v4-flash", ThinkingEnabled: &thinking}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir .whale: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".whale", "config.toml"), []byte("[[hooks.PreToolUse]]\ncommand = \"echo ok\"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	t.Setenv("DEEPSEEK_BASE_URL", newDoctorServer(t, http.StatusOK).URL)
	report, err := RunDoctor(context.Background(), Config{DataDir: dataDir, MemoryFileOrder: "AGENTS.md"}, workspace)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if report.HasFailures() {
		t.Fatalf("expected no failures: %+v", report.Checks)
	}
	if got := findDoctorCheck(report.Checks, "api key"); got.Level != DoctorOK {
		t.Fatalf("api key level: %+v", got)
	}
	if got := findDoctorCheck(report.Checks, "api reach"); got.Level != DoctorOK {
		t.Fatalf("api reach level: %+v", got)
	}
	if got := findDoctorCheck(report.Checks, "project doc"); got.Level != DoctorOK {
		t.Fatalf("project doc level: %+v", got)
	}
	if got := findDoctorCheck(report.Checks, "hooks"); got.Level != DoctorOK {
		t.Fatalf("hooks level: %+v", got)
	}
}

func TestRunDoctorOmitsHooksWhenNoneConfigured(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveCredentials(dataDir, Credentials{DeepSeekAPIKey: "sk-1234567890abcdef1234"}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	t.Setenv("DEEPSEEK_BASE_URL", newDoctorServer(t, http.StatusOK).URL)
	report, err := RunDoctor(context.Background(), Config{DataDir: dataDir, MemoryFileOrder: "AGENTS.md"}, workspace)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if got := findDoctorCheck(report.Checks, "hooks"); got.Level != "" {
		t.Fatalf("hooks check should be omitted when none configured: %+v", got)
	}
}

func TestRunDoctorFlagsBrokenFiles(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "credentials.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.toml"), []byte("[["), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	report, err := RunDoctor(context.Background(), Config{DataDir: dataDir, MemoryFileOrder: "AGENTS.md"}, workspace)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if got := findDoctorCheck(report.Checks, "credentials"); got.Level != DoctorFail {
		t.Fatalf("credentials level: %+v", got)
	}
	if got := findDoctorCheck(report.Checks, "config"); got.Level != DoctorFail {
		t.Fatalf("config level: %+v", got)
	}
	if got := findDoctorCheck(report.Checks, "project doc"); got.Level != DoctorWarn {
		t.Fatalf("project doc level: %+v", got)
	}
}

func TestRunDoctorUsesEnvKeyWhenPresent(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("DEEPSEEK_API_KEY", "sk-env1234567890abcdef")
	report, err := RunDoctor(context.Background(), Config{DataDir: dataDir}, workspace)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	got := findDoctorCheck(report.Checks, "api key")
	if got.Level != DoctorOK || !strings.Contains(got.Detail, "env DEEPSEEK_API_KEY") {
		t.Fatalf("api key check: %+v", got)
	}
}

func TestRunDoctorReportsWhaleHomeOverride(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("WHALE_HOME", dataDir)

	report, err := RunDoctor(context.Background(), Config{MemoryFileOrder: "AGENTS.md"}, workspace)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if report.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", report.DataDir, dataDir)
	}
	got := findDoctorCheck(report.Checks, "data dir override")
	if got.Level != DoctorOK || !strings.Contains(got.Detail, "WHALE_HOME") || !strings.Contains(got.Detail, dataDir) {
		t.Fatalf("data dir override check: %+v", got)
	}
}

func TestDoctorCheckDataDirOverrideHintsOnWindows(t *testing.T) {
	got := doctorCheckDataDirOverride("windows", func(string) string { return "" }, `C:\Users\dev\.whale`)
	if got.Level != DoctorOK || !strings.Contains(got.Detail, "WHALE_HOME") {
		t.Fatalf("windows data dir override hint: %+v", got)
	}
}

func TestDoctorCheckDataDirOverrideOmittedWhenUnsetOutsideWindows(t *testing.T) {
	got := doctorCheckDataDirOverride("linux", func(string) string { return "" }, "/home/dev/.whale")
	if got.Level != "" {
		t.Fatalf("data dir override check should be omitted: %+v", got)
	}
}

func TestDoctorCheckDataDirACLOmittedOutsideWindows(t *testing.T) {
	got := doctorCheckDataDirACL("linux", "/home/dev/.whale")
	if got.Level != "" {
		t.Fatalf("data dir acl check should be omitted: %+v", got)
	}
}

func TestCheckDeepSeekAPIReachabilityClassifiesResponses(t *testing.T) {
	t.Setenv("DEEPSEEK_BASE_URL", newDoctorServer(t, http.StatusUnauthorized).URL)
	msg, err := CheckDeepSeekAPIReachability(context.Background(), "sk-1234567890abcdef1234")
	if err == nil || !strings.Contains(msg, "unauthorized") {
		t.Fatalf("want unauthorized, got msg=%q err=%v", msg, err)
	}

	t.Setenv("DEEPSEEK_BASE_URL", newDoctorServer(t, http.StatusForbidden).URL)
	msg, err = CheckDeepSeekAPIReachability(context.Background(), "sk-1234567890abcdef1234")
	if err == nil || !strings.Contains(msg, "forbidden") {
		t.Fatalf("want forbidden, got msg=%q err=%v", msg, err)
	}
}

func TestClassifyDoctorHTTPError(t *testing.T) {
	msg := classifyDoctorHTTPError(&net.DNSError{Err: "no such host", Name: "api.deepseek.com"})
	if !strings.Contains(msg, "DNS resolution failed") {
		t.Fatalf("dns msg: %q", msg)
	}
	msg = classifyDoctorHTTPError(context.DeadlineExceeded)
	if !strings.Contains(msg, "timeout") {
		t.Fatalf("timeout msg: %q", msg)
	}
	msg = classifyDoctorHTTPError(errors.New("net/http: TLS handshake timeout"))
	if !strings.Contains(msg, "TLS handshake timed out") {
		t.Fatalf("tls msg: %q", msg)
	}
}

func newDoctorServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
}

func findDoctorCheck(checks []DoctorCheck, label string) DoctorCheck {
	for _, check := range checks {
		if check.Label == label {
			return check
		}
	}
	return DoctorCheck{}
}
