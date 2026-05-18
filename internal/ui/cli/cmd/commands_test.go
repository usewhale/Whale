package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/policy"
)

func TestRunSetupSavesCredentials(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	in := strings.NewReader("sk-1234567890abcdef1234\n")

	if err := runSetup(&out, in, dir); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	creds, err := app.LoadCredentials(dir)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.DeepSeekAPIKey != "sk-1234567890abcdef1234" {
		t.Fatalf("deepseek_api_key: got %q", creds.DeepSeekAPIKey)
	}
	if !strings.Contains(out.String(), filepath.Join(dir, "credentials.json")) {
		t.Fatalf("expected output to mention credentials path, got %q", out.String())
	}
}

func TestRunSetupRejectsInvalidKey(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	in := strings.NewReader("invalid\n")

	if err := runSetup(&out, in, dir); err == nil {
		t.Fatal("expected invalid key error")
	}
}

func TestRunDoctorReturnsExitErrorOnFailures(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("DEEPSEEK_BASE_URL", "http://127.0.0.1:1")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	var out bytes.Buffer
	err = runDoctor(&out, app.Config{DataDir: filepath.Join(dir, ".whale")})
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected ExitError{1}, got %v", err)
	}
	if !strings.Contains(out.String(), "0 fail") && !strings.Contains(out.String(), "1 fail") {
		t.Fatalf("expected summary output, got %q", out.String())
	}
}

func TestDoctorBadge(t *testing.T) {
	if got := doctorBadge(app.DoctorOK); got != "ok" {
		t.Fatalf("doctorBadge ok = %q", got)
	}
	if got := doctorBadge(app.DoctorWarn); got != "warn" {
		t.Fatalf("doctorBadge warn = %q", got)
	}
	if got := doctorBadge(app.DoctorFail); got != "fail" {
		t.Fatalf("doctorBadge fail = %q", got)
	}
}

func TestMigrateConfigHelpExplainsVersionBoundary(t *testing.T) {
	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"migrate-config", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("migrate-config help: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "v0.1.8 or earlier") || !strings.Contains(help, "v0.1.9") || !strings.Contains(help, "newer") {
		t.Fatalf("expected version boundary in help, got:\n%s", help)
	}
}

func TestMigrateConfigOutputExplainsVersionBoundary(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"migrate-config"})
	if err := root.Execute(); err != nil {
		t.Fatalf("migrate-config: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "v0.1.8 or earlier") || !strings.Contains(got, "no legacy config to migrate") {
		t.Fatalf("expected version boundary in output, got:\n%s", got)
	}
}

func TestPrepareCLIConfigLoadsConfigAndAppliesFlagOverride(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir .whale: %v", err)
	}
	if err := app.SaveConfigFile(app.GlobalConfigPath(dataDir), app.FileConfig{Model: "deepseek-v4-pro"}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("model", "deepseek-v4-flash"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if opts.cfg.Model != "deepseek-v4-flash" {
		t.Fatalf("model: want CLI override, got %s", opts.cfg.Model)
	}
	if !opts.cfg.ModelExplicit {
		t.Fatal("model should be explicit")
	}
}

func TestPrepareCLIConfigPreservesConfiguredThinking(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	thinking := true
	if err := app.SaveConfigFile(app.GlobalConfigPath(dataDir), app.FileConfig{ThinkingEnabled: &thinking}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if !opts.cfg.ThinkingEnabled {
		t.Fatal("thinking_enabled should stay true from config")
	}
}

func TestPrepareCLIConfigExplicitThinkingFalseOverridesConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	thinking := true
	if err := app.SaveConfigFile(app.GlobalConfigPath(dataDir), app.FileConfig{ThinkingEnabled: &thinking}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("thinking", "false"); err != nil {
		t.Fatalf("set thinking: %v", err)
	}
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if opts.cfg.ThinkingEnabled {
		t.Fatal("thinking_enabled should be overridden to false")
	}

	loaded, _, err := app.LoadConfigFile(app.GlobalConfigPath(dataDir))
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if loaded.ThinkingEnabled == nil || !*loaded.ThinkingEnabled {
		t.Fatal("global config should remain unchanged")
	}
}

func TestPrepareCLIConfigExplicitThinkingTrueOverridesConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	thinking := false
	if err := app.SaveConfigFile(app.GlobalConfigPath(dataDir), app.FileConfig{ThinkingEnabled: &thinking}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("thinking", "true"); err != nil {
		t.Fatalf("set thinking: %v", err)
	}
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if !opts.cfg.ThinkingEnabled {
		t.Fatal("thinking_enabled should be overridden to true")
	}
}

func TestPrepareCLIConfigExplicitEffortOverridesConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := app.SaveConfigFile(app.GlobalConfigPath(dataDir), app.FileConfig{ReasoningEffort: "high"}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("effort", "max"); err != nil {
		t.Fatalf("set effort: %v", err)
	}
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if opts.cfg.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort: want max, got %s", opts.cfg.ReasoningEffort)
	}
}

func TestPrepareCLIConfigDangerouslySkipPermissionsOverridesConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir .whale: %v", err)
	}
	if err := app.SaveConfigFile(filepath.Join(workspace, ".whale", app.ConfigFileName), app.FileConfig{
		Permissions: app.FilePermissionsConfig{Mode: string(policy.ApprovalModeOnRequest)},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("dangerously-skip-permissions", "true"); err != nil {
		t.Fatalf("set dangerously-skip-permissions: %v", err)
	}
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if opts.cfg.ApprovalMode != string(policy.ApprovalModeNever) {
		t.Fatalf("approval mode: want never, got %s", opts.cfg.ApprovalMode)
	}

	loaded, _, err := app.LoadConfigFile(filepath.Join(workspace, ".whale", app.ConfigFileName))
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if loaded.Permissions.Mode != string(policy.ApprovalModeOnRequest) {
		t.Fatalf("project config should remain on-request, got %q", loaded.Permissions.Mode)
	}
}

func TestPrepareCLIConfigDangerouslySkipPermissionsAppliesToSubcommands(t *testing.T) {
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("dangerously-skip-permissions", "true"); err != nil {
		t.Fatalf("set dangerously-skip-permissions: %v", err)
	}
	execCmd, _, err := root.Find([]string{"exec"})
	if err != nil {
		t.Fatalf("find exec: %v", err)
	}
	if err := prepareCLIConfig(execCmd, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if opts.cfg.ApprovalMode != string(policy.ApprovalModeNever) {
		t.Fatalf("approval mode: want never, got %s", opts.cfg.ApprovalMode)
	}
}

func TestPrepareCLIConfigRejectsUnsupportedEffortAlias(t *testing.T) {
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	if err := root.PersistentFlags().Set("effort", "xhigh"); err != nil {
		t.Fatalf("set effort: %v", err)
	}
	err = prepareCLIConfig(root, opts)
	if err == nil {
		t.Fatal("expected unsupported effort error")
	}
	if !strings.Contains(err.Error(), "unsupported effort: xhigh") || !strings.Contains(err.Error(), "high, max") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareCLIConfigKeepsUnspecifiedThinkingAndEffortFromConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	thinking := true
	if err := app.SaveConfigFile(app.GlobalConfigPath(dataDir), app.FileConfig{
		ReasoningEffort: "max",
		ThinkingEnabled: &thinking,
	}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dataDir
	root := newRootCmd(opts)
	if err := prepareCLIConfig(root, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if !opts.cfg.ThinkingEnabled {
		t.Fatal("thinking_enabled should stay true from config")
	}
	if opts.cfg.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort: want max from config, got %s", opts.cfg.ReasoningEffort)
	}
}

func TestReadExecPromptPrefersArg(t *testing.T) {
	got, err := readExecPrompt(strings.NewReader("stdin prompt"), []string{"arg prompt"})
	if err != nil {
		t.Fatalf("readExecPrompt: %v", err)
	}
	if got != "arg prompt" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestReadExecPromptFallsBackToStdin(t *testing.T) {
	got, err := readExecPrompt(strings.NewReader("stdin prompt\n"), nil)
	if err != nil {
		t.Fatalf("readExecPrompt: %v", err)
	}
	if got != "stdin prompt" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestRunExecTextOutput(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-1234567890abcdef1234")
	srv := newExecTestServer(t, "hello from exec")
	defer srv.Close()
	t.Setenv("DEEPSEEK_BASE_URL", srv.URL)

	dir := t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	var out bytes.Buffer
	var errOut bytes.Buffer
	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dir
	if err := runExec(&out, &errOut, strings.NewReader(""), opts, []string{"hi"}, false, 0); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	if got := out.String(); got != "hello from exec\n" {
		t.Fatalf("stdout = %q", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRunExecJSONOutput(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-1234567890abcdef1234")
	srv := newExecTestServer(t, "hello json")
	defer srv.Close()
	t.Setenv("DEEPSEEK_BASE_URL", srv.URL)

	dir := t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	var out bytes.Buffer
	var errOut bytes.Buffer
	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dir
	if err := runExec(&out, &errOut, strings.NewReader("stdin prompt"), opts, nil, true, 0); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	var res app.ExecResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json: %v\n%s", err, out.String())
	}
	if res.Status != "completed" || res.Output != "hello json" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.SessionID == "" {
		t.Fatalf("expected session id: %+v", res)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRootExecAppliesThinkingAndEffortOverridesWithoutChangingTextOutput(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-1234567890abcdef1234")
	requests := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n", "hello from exec")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	t.Setenv("DEEPSEEK_BASE_URL", srv.URL)

	dir := t.TempDir()
	workspace := t.TempDir()
	thinking := false
	if err := app.SaveConfigFile(app.GlobalConfigPath(dir), app.FileConfig{
		ReasoningEffort: "high",
		ThinkingEnabled: &thinking,
	}); err != nil {
		t.Fatalf("save global config: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	opts.cfg.DataDir = dir
	root := newRootCmd(opts)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"exec", "--thinking=true", "--effort=max", "hi"})
	if err := root.Execute(); err != nil {
		t.Fatalf("root exec: %v", err)
	}
	if got := out.String(); got != "hello from exec\n" {
		t.Fatalf("stdout = %q", got)
	}

	var payload map[string]any
	select {
	case payload = <-requests:
	default:
		t.Fatal("expected exec request payload")
	}
	thinkingPayload, ok := payload["thinking"].(map[string]any)
	if !ok || thinkingPayload["type"] != "enabled" {
		t.Fatalf("thinking payload = %#v, want enabled", payload["thinking"])
	}
	if got := payload["reasoning_effort"]; got != "max" {
		t.Fatalf("reasoning_effort = %#v, want max", got)
	}
}

func TestPrepareCLIConfigMarksExplicitDefaultModel(t *testing.T) {
	opts := &cliOptions{cfg: app.DefaultConfig()}
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("model", opts.cfg.Model, "")
	if err := cmd.Flags().Set("model", "deepseek-v4-flash"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := prepareCLIConfig(cmd, opts); err != nil {
		t.Fatalf("prepareCLIConfig: %v", err)
	}
	if !opts.cfg.ModelExplicit {
		t.Fatal("expected ModelExplicit=true")
	}
}

func TestRootHelpOnlyShowsPublicRootFlags(t *testing.T) {
	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	help := out.String()
	for _, removed := range []string{
		"--data-dir",
		"--approval-mode",
		"--allow-prefixes",
		"--deny-prefixes",
		"--auto-compact",
		"--auto-compact-threshold",
		"--model-context-window",
		"--memory-enabled",
		"--memory-max-chars",
		"--memory-file-order",
		"--mcp-config",
		"--budget-warning-usd",
		"--config",
		"--session",
		"--mode",
	} {
		if helpHasFlag(help, removed) {
			t.Fatalf("removed flag should not appear in help: %s\n%s", removed, help)
		}
	}
	for _, expected := range []string{"--model", "--thinking", "--effort", "--dangerously-skip-permissions", "--version"} {
		if !helpHasFlag(help, expected) {
			t.Fatalf("expected public flag %s in help, got:\n%s", expected, help)
		}
	}
	if !strings.Contains(help, "Override thinking for this run only") || !strings.Contains(help, "Override reasoning effort for this run only") {
		t.Fatalf("expected public flags in help, got:\n%s", help)
	}
}

func TestExecRejectsUnsupportedEffortBeforeRun(t *testing.T) {
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"exec", "--effort=low", "hi"})
	err = root.Execute()
	if err == nil {
		t.Fatal("expected unsupported effort error")
	}
	if !strings.Contains(err.Error(), "unsupported effort: low") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "unsupported effort: low") {
		t.Fatalf("expected CLI error output, got %q", out.String())
	}
	if strings.Contains(out.String(), "hello from exec") {
		t.Fatalf("expected exec to stop before normal output, got %q", out.String())
	}
}

func TestThinkingAndEffortFlagsArePersistent(t *testing.T) {
	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)

	if root.PersistentFlags().Lookup("thinking") == nil {
		t.Fatal("expected thinking to be a root persistent flag")
	}
	if root.PersistentFlags().Lookup("effort") == nil {
		t.Fatal("expected effort to be a root persistent flag")
	}

	for _, args := range [][]string{
		{"exec", "--help"},
		{"resume", "--help"},
	} {
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%s help: %v", strings.Join(args[:1], " "), err)
		}
		help := out.String()
		if !helpHasFlag(help, "--thinking") || !helpHasFlag(help, "--effort") {
			t.Fatalf("expected inherited flags in %v help, got:\n%s", args, help)
		}
	}
}

func helpHasFlag(help, flag string) bool {
	for _, field := range strings.Fields(help) {
		field = strings.TrimRight(field, ",")
		if field == flag || strings.HasPrefix(field, flag+"=") {
			return true
		}
	}
	return false
}

func TestResumeStartOptions(t *testing.T) {
	got, err := resumeStartOptions(nil, false)
	if err != nil {
		t.Fatalf("resumeStartOptions picker: %v", err)
	}
	if !got.ResumeMenu || got.SessionID != "" || got.NewSession {
		t.Fatalf("picker start options = %+v", got)
	}

	got, err = resumeStartOptions(nil, true)
	if err != nil {
		t.Fatalf("resumeStartOptions last: %v", err)
	}
	if got.ResumeMenu || got.SessionID != "" || got.NewSession {
		t.Fatalf("last start options = %+v", got)
	}

	got, err = resumeStartOptions([]string{"sess-1"}, false)
	if err != nil {
		t.Fatalf("resumeStartOptions id: %v", err)
	}
	if got.ResumeMenu || got.SessionID != "sess-1" || got.NewSession {
		t.Fatalf("id start options = %+v", got)
	}

	if _, err = resumeStartOptions([]string{"sess-1"}, true); err == nil {
		t.Fatal("expected --last with id to fail")
	}
}

func newExecTestServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n", content)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}
