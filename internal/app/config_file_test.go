package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/policy"
)

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestConfigFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := GlobalConfigPath(dir)
	enabled := true
	checkUpdates := false
	showReasoning := true
	cfg := FileConfig{
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "max",
		ThinkingEnabled: &enabled,
		UI:              FileUIConfig{ViewMode: ViewModeFocus, ShowReasoning: &showReasoning, CheckForUpdateOnStartup: &checkUpdates},
		API:             FileAPIConfig{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1/"},
		Permissions: FilePermissionsConfig{
			Default:    "ask",
			AutoAccept: &enabled,
			Shell:      map[string]string{"git push*": "ask"},
		},
	}
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(raw), `model = "deepseek-v4-pro"`) {
		t.Fatalf("unexpected config TOML:\n%s", raw)
	}
	if !strings.Contains(string(raw), "[permissions]") || strings.Contains(string(raw), "allow_shell_prefixes") || strings.Contains(string(raw), "\n  mode =") {
		t.Fatalf("expected grouped config TOML, got:\n%s", raw)
	}

	loaded, ok, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if !ok {
		t.Fatal("expected config file to be loaded")
	}
	if loaded.Model != "deepseek-v4-pro" || loaded.ReasoningEffort != "max" {
		t.Fatalf("loaded config: %+v", loaded)
	}
	if loaded.ThinkingEnabled == nil || !*loaded.ThinkingEnabled {
		t.Fatal("thinking_enabled: want true")
	}
	if loaded.API.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1/" {
		t.Fatalf("api base_url: %+v", loaded.API)
	}
	if loaded.UI.ViewMode != ViewModeFocus {
		t.Fatalf("ui.view_mode: want focus, got %+v", loaded.UI)
	}
	if loaded.UI.ShowReasoning == nil || !*loaded.UI.ShowReasoning {
		t.Fatalf("ui.show_reasoning: want true, got %+v", loaded.UI.ShowReasoning)
	}
	if loaded.UI.CheckForUpdateOnStartup == nil || *loaded.UI.CheckForUpdateOnStartup {
		t.Fatalf("ui.check_for_update_on_startup: want false, got %+v", loaded.UI.CheckForUpdateOnStartup)
	}
	if loaded.Permissions.Default != "ask" || loaded.Permissions.AutoAccept == nil || !*loaded.Permissions.AutoAccept || loaded.Permissions.Shell["git push*"] != "ask" {
		t.Fatalf("permissions config: %+v", loaded.Permissions)
	}
}

func TestApplyFileConfigSupportsMaxParallelSubagents(t *testing.T) {
	cfg := DefaultConfig()
	if err := ApplyFileConfig(&cfg, FileConfig{Tasks: FileTasksConfig{MaxParallelSubagents: intPtr(3)}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if cfg.MaxParallelSubagents != 3 {
		t.Fatalf("max parallel subagents: want 3, got %d", cfg.MaxParallelSubagents)
	}
}

func TestApplyFileConfigRejectsInvalidMaxParallelSubagents(t *testing.T) {
	for _, value := range []int{0, -1} {
		cfg := DefaultConfig()
		if err := ApplyFileConfig(&cfg, FileConfig{Tasks: FileTasksConfig{MaxParallelSubagents: intPtr(value)}}); err == nil {
			t.Fatalf("expected invalid tasks.max_parallel_subagents error for %d", value)
		}
	}
}

func TestApplyFileConfigSupportsViewMode(t *testing.T) {
	cfg := DefaultConfig()
	if err := ApplyFileConfig(&cfg, FileConfig{UI: FileUIConfig{ViewMode: ViewModeFocus}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if cfg.ViewMode != ViewModeFocus {
		t.Fatalf("view mode: want focus, got %q", cfg.ViewMode)
	}
	if err := ApplyFileConfig(&cfg, FileConfig{UI: FileUIConfig{ViewMode: "verbose"}}); err == nil {
		t.Fatal("expected invalid view mode error")
	}
}

func TestApplyFileConfigSupportsShowReasoning(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ShowReasoning {
		t.Fatal("default show_reasoning should be disabled")
	}
	enabled := true
	if err := ApplyFileConfig(&cfg, FileConfig{UI: FileUIConfig{ShowReasoning: &enabled}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if !cfg.ShowReasoning {
		t.Fatal("expected show_reasoning to be enabled")
	}
	disabled := false
	if err := ApplyFileConfig(&cfg, FileConfig{UI: FileUIConfig{ShowReasoning: &disabled}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if cfg.ShowReasoning {
		t.Fatal("expected show_reasoning to be disabled")
	}
}

func TestApplyFileConfigSupportsUpdateCheckSetting(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.CheckForUpdateOnStartup {
		t.Fatal("default update check should be enabled")
	}
	disabled := false
	if err := ApplyFileConfig(&cfg, FileConfig{UI: FileUIConfig{CheckForUpdateOnStartup: &disabled}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if cfg.CheckForUpdateOnStartup {
		t.Fatal("expected update check to be disabled")
	}
	enabled := true
	if err := ApplyFileConfig(&cfg, FileConfig{UI: FileUIConfig{CheckForUpdateOnStartup: &enabled}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if !cfg.CheckForUpdateOnStartup {
		t.Fatal("expected update check to be enabled")
	}
}

func TestConfigFileSupportsPlugins(t *testing.T) {
	dir := t.TempDir()
	path := GlobalConfigPath(dir)
	cfg := FileConfig{
		Plugins: FilePluginsConfig{Disabled: []string{"memory"}},
	}
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}
	loaded, ok, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if !ok {
		t.Fatal("expected config file")
	}
	if len(loaded.Plugins.Disabled) != 1 || loaded.Plugins.Disabled[0] != "memory" {
		t.Fatalf("plugins config not loaded: %+v", loaded.Plugins)
	}

	appCfg := DefaultConfig()
	ApplyFileConfig(&appCfg, loaded)
	if len(appCfg.PluginsDisabled) != 1 || appCfg.PluginsDisabled[0] != "memory" {
		t.Fatalf("plugins config not applied: %+v", appCfg.PluginsDisabled)
	}
}

func TestConfigFileSupportsTrustedWorkflows(t *testing.T) {
	dir := t.TempDir()
	path := GlobalConfigPath(dir)
	cfg := FileConfig{
		Workflows: FileWorkflowsConfig{Trusted: []string{"deep-research"}},
	}
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}
	loaded, ok, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if !ok {
		t.Fatal("expected config file")
	}
	if len(loaded.Workflows.Trusted) != 1 || loaded.Workflows.Trusted[0] != "deep-research" {
		t.Fatalf("trusted workflows not loaded: %+v", loaded.Workflows)
	}

	appCfg := DefaultConfig()
	if err := ApplyFileConfig(&appCfg, loaded); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if len(appCfg.TrustedWorkflows) != 1 || appCfg.TrustedWorkflows[0] != "deep-research" {
		t.Fatalf("trusted workflows not applied: %+v", appCfg.TrustedWorkflows)
	}
}

func TestConfigNewAppLoadsGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	enabled := false
	if err := SaveConfigFile(GlobalConfigPath(dir), FileConfig{
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "max",
		ThinkingEnabled: &enabled,
	}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dir

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app.Model() != "deepseek-v4-pro" {
		t.Fatalf("model: want deepseek-v4-pro from config, got %s", app.Model())
	}
	if app.ReasoningEffort() != "max" {
		t.Fatalf("effort: want max from config, got %s", app.ReasoningEffort())
	}
	if app.ThinkingEnabled() {
		t.Fatal("thinking: want false from config")
	}
}

func TestConfigProjectOverridesGlobal(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir .whale: %v", err)
	}
	if err := SaveConfigFile(GlobalConfigPath(dataDir), FileConfig{Model: "deepseek-v4-flash"}); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Model: "deepseek-v4-pro"}); err != nil {
		t.Fatalf("save project: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dataDir
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.Model != "deepseek-v4-pro" {
		t.Fatalf("model: want project override, got %s", loaded.Model)
	}
}

func TestConfigProjectLocalOverridesMaxParallelSubagents(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir .whale: %v", err)
	}
	if err := SaveConfigFile(GlobalConfigPath(dataDir), FileConfig{Tasks: FileTasksConfig{MaxParallelSubagents: intPtr(2)}}); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Tasks: FileTasksConfig{MaxParallelSubagents: intPtr(3)}}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := SaveConfigFile(ProjectLocalConfigPath(workspace), FileConfig{Tasks: FileTasksConfig{MaxParallelSubagents: intPtr(4)}}); err != nil {
		t.Fatalf("save project local: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dataDir
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.MaxParallelSubagents != 4 {
		t.Fatalf("max parallel subagents: want project-local override 4, got %d", loaded.MaxParallelSubagents)
	}
}

func TestConfigExplicitMaxParallelSubagentsOverridesFiles(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectLocalConfigPath(workspace), FileConfig{Tasks: FileTasksConfig{MaxParallelSubagents: intPtr(3)}}); err != nil {
		t.Fatalf("save project local: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dataDir
	cfg.MaxParallelSubagents = 5
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.MaxParallelSubagents != 5 {
		t.Fatalf("max parallel subagents: want explicit 5, got %d", loaded.MaxParallelSubagents)
	}
}

func TestConfigProjectLocalOverridesProject(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir .whale: %v", err)
	}
	if err := SaveConfigFile(GlobalConfigPath(dataDir), FileConfig{
		Model:           "deepseek-v4-flash",
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "max",
	}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := SaveConfigFile(ProjectLocalConfigPath(workspace), FileConfig{
		Model: "deepseek-v4-flash",
	}); err != nil {
		t.Fatalf("save project local: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dataDir
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.Model != "deepseek-v4-flash" {
		t.Fatalf("model: want project-local override, got %s", loaded.Model)
	}
	if loaded.ReasoningEffort != "max" {
		t.Fatalf("effort: want project value preserved, got %s", loaded.ReasoningEffort)
	}
}

func TestConfigExplicitModelOverridesProjectLocalConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectLocalConfigPath(workspace), FileConfig{Model: "deepseek-v4-pro"}); err != nil {
		t.Fatalf("save project local: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dataDir
	cfg.Model = "deepseek-v4-flash"
	cfg.ModelExplicit = true
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.Model != "deepseek-v4-flash" {
		t.Fatalf("model: want explicit override, got %s", loaded.Model)
	}
}

func TestConfigSourcesIncludeProjectLocalFirst(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(GlobalConfigPath(dataDir), FileConfig{Model: "deepseek-v4-flash"}); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{ReasoningEffort: "high"}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := SaveConfigFile(ProjectLocalConfigPath(workspace), FileConfig{ReasoningEffort: "max"}); err != nil {
		t.Fatalf("save project local: %v", err)
	}

	loaded, err := LoadConfigFiles(dataDir, workspace)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	got := ConfigSources(loaded)
	want := []string{
		ProjectLocalConfigPath(workspace),
		ProjectConfigPath(workspace),
		GlobalConfigPath(dataDir),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sources:\nwant %v\ngot  %v", want, got)
	}
}

func TestApplyFileConfigUsesGroupedConfig(t *testing.T) {
	autoCompact := false
	compactThreshold := 0.7
	projectDocEnabled := false
	projectDocMaxBytes := 12000
	budgetLimit := 1.25
	cfg := DefaultConfig()
	if err := ApplyFileConfig(&cfg, FileConfig{
		API: FileAPIConfig{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1/"},
		Retry: FileRetryConfig{
			MaxAttempts:       intPtr(5),
			StreamMaxAttempts: intPtr(7),
			StreamIdleTimeout: "30s",
			MaxDelay:          "45s",
		},
		Budget: FileBudgetConfig{SessionLimitUSD: &budgetLimit},
		MCP:    FileMCPConfig{ConfigPath: "~/custom-mcp.json"},
		Context: FileContextConfig{
			AutoCompact:      &autoCompact,
			CompactThreshold: &compactThreshold,
		},
		ProjectDoc: FileProjectDocConfig{
			Enabled:           &projectDocEnabled,
			MaxBytes:          &projectDocMaxBytes,
			FallbackFilenames: []string{"AGENTS.md", "TEAM.md"},
		},
	}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}

	if cfg.BudgetWarningUSD != budgetLimit {
		t.Fatalf("budget not applied: %+v", cfg)
	}
	if !strings.HasSuffix(cfg.MCPConfigPath, "custom-mcp.json") {
		t.Fatalf("mcp path not applied: %s", cfg.MCPConfigPath)
	}
	if cfg.APIBaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("api base url not applied: %s", cfg.APIBaseURL)
	}
	if cfg.RetryMaxAttempts != 5 || cfg.RetryStreamMaxAttempts != 7 || cfg.RetryStreamIdleTimeout != 30*time.Second || cfg.RetryMaxDelay != 45*time.Second {
		t.Fatalf("retry not applied: %+v", cfg)
	}
	if cfg.AutoCompact || cfg.AutoCompactThreshold != compactThreshold {
		t.Fatalf("context not applied: %+v", cfg)
	}
	if cfg.MemoryEnabled || cfg.MemoryMaxChars != projectDocMaxBytes || cfg.MemoryFileOrder != "AGENTS.md,TEAM.md" {
		t.Fatalf("project doc not applied: %+v", cfg)
	}
}

func TestApplyFileConfigLoadsPermissionRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PermissionRules = nil
	if err := ApplyFileConfig(&cfg, FileConfig{
		Permissions: FilePermissionsConfig{
			Default:    "ask",
			AutoAccept: boolPtr(true),
			Read:       map[string]string{"*": "allow", "*.env": "ask"},
			Shell:      map[string]string{"*": "allow", "git push*": "ask", "rm -rf*": "deny"},
			MCP:        map[string]string{"*": "ask"},
			MutatingTool: map[string]string{
				"delete_project": "deny",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if cfg.PermissionDefault != policy.PermissionAsk {
		t.Fatalf("permission default = %q, want ask", cfg.PermissionDefault)
	}
	if !cfg.AutoAcceptPermissions {
		t.Fatal("auto accept not applied")
	}
	if len(cfg.PermissionRules) != 7 {
		t.Fatalf("permission rules = %d, want 7: %+v", len(cfg.PermissionRules), cfg.PermissionRules)
	}
	if got := cfg.PermissionRules[0]; got.Permission != "read" || got.Pattern != "*" || got.Action != policy.PermissionAllow {
		t.Fatalf("first rule = %+v", got)
	}
	if got := cfg.PermissionRules[len(cfg.PermissionRules)-1]; got.Permission != "mutating_tool" || got.Pattern != "delete_project" || got.Action != policy.PermissionDeny {
		t.Fatalf("last rule = %+v", got)
	}
}

func TestLoadAndApplyConfigKeepsFilePermissionRulesWithDefaultCLIConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{
		Permissions: FilePermissionsConfig{
			Shell: map[string]string{"git push*": "deny"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	input := DefaultConfig()
	input.DataDir = dataDir
	cfg, err := LoadAndApplyConfig(input, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}

	for _, rule := range cfg.PermissionRules {
		if rule.Permission == "shell" && rule.Pattern == "git push*" && rule.Action == policy.PermissionDeny {
			return
		}
	}
	t.Fatalf("loaded permission rule was not preserved: %+v", cfg.PermissionRules)
}

func TestApplyLoadedConfigLocalEnabledOverridesSharedDisabled(t *testing.T) {
	cfg := Config{}
	err := ApplyLoadedConfig(&cfg, LoadedConfig{
		Project: FileConfig{
			Skills:  FileSkillsConfig{Disabled: []string{"review-skill", "keep-skill"}},
			Plugins: FilePluginsConfig{Disabled: []string{"memory", "other-plugin"}},
		},
		ProjectLocal: FileConfig{
			Skills:  FileSkillsConfig{Enabled: []string{"review-skill"}},
			Plugins: FilePluginsConfig{Enabled: []string{"memory"}},
		},
	})
	if err != nil {
		t.Fatalf("ApplyLoadedConfig: %v", err)
	}
	if containsString(cfg.SkillsDisabled, "review-skill") || !containsString(cfg.SkillsDisabled, "keep-skill") {
		t.Fatalf("skills disabled override mismatch: %+v", cfg.SkillsDisabled)
	}
	if containsString(cfg.PluginsDisabled, "memory") || !containsString(cfg.PluginsDisabled, "other-plugin") {
		t.Fatalf("plugins disabled override mismatch: %+v", cfg.PluginsDisabled)
	}
}

func TestApplyFileConfigRejectsInvalidRetryConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{MaxAttempts: intPtr(-1)}}); err == nil {
		t.Fatal("expected invalid max_attempts error")
	}
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{StreamMaxAttempts: intPtr(0)}}); err == nil {
		t.Fatal("expected invalid stream_max_attempts error")
	}
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{StreamIdleTimeout: "0s"}}); err == nil {
		t.Fatal("expected invalid stream_idle_timeout error")
	}
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{MaxDelay: "soon"}}); err == nil {
		t.Fatal("expected invalid max_delay error")
	}
}

func TestApplyFileConfigAllowsRetryMaxAttemptsZero(t *testing.T) {
	cfg := DefaultConfig()
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{MaxAttempts: intPtr(0)}}); err != nil {
		t.Fatalf("ApplyFileConfig: %v", err)
	}
	if cfg.RetryMaxAttempts != 0 {
		t.Fatalf("RetryMaxAttempts = %d, want explicit zero", cfg.RetryMaxAttempts)
	}
	if !cfg.RetryMaxAttemptsExplicit {
		t.Fatal("RetryMaxAttemptsExplicit was not set")
	}
}

func TestLoadConfigFileRejectsRemovedPermissionKeys(t *testing.T) {
	for _, body := range []string{
		"[permissions]\nmode = \"ask\"\n",
		"[permissions]\ndeny_shell_prefixes = [\"git push --force\"]\n",
		"allow_shell_prefixes = [\"ls\"]\n",
	} {
		dir := t.TempDir()
		path := GlobalConfigPath(dir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, ok, err := LoadConfigFile(path)
		if err == nil {
			t.Fatalf("expected removed-key error for config:\n%s", body)
		}
		if ok {
			t.Fatalf("config should not load when a removed key is present:\n%s", body)
		}
		if !strings.Contains(err.Error(), "removed permission key") {
			t.Fatalf("unexpected error %v for config:\n%s", err, body)
		}
	}
}

func TestConfigExplicitModelOverridesFileConfig(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfigFile(GlobalConfigPath(dir), FileConfig{Model: "deepseek-v4-pro"}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.Model = "deepseek-v4-flash"
	cfg.ModelExplicit = true

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app.Model() != "deepseek-v4-flash" {
		t.Fatalf("model: want explicit deepseek-v4-flash, got %s", app.Model())
	}
}

func TestConfigExplicitUpdateCheckDisableOverridesFileConfig(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	enabled := true
	if err := SaveConfigFile(GlobalConfigPath(dir), FileConfig{
		UI: FileUIConfig{CheckForUpdateOnStartup: &enabled},
	}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}

	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.CheckForUpdateOnStartup = false
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.CheckForUpdateOnStartup {
		t.Fatal("check_for_update_on_startup: want explicit programmatic false to be preserved")
	}
}

func TestConfigExplicitRetryZeroOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()

	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.RetryMaxAttempts = 0
	cfg.RetryMaxAttemptsExplicit = true
	loaded, err := LoadAndApplyConfig(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if loaded.RetryMaxAttempts != 0 {
		t.Fatalf("RetryMaxAttempts = %d, want explicit zero", loaded.RetryMaxAttempts)
	}
	if !loaded.RetryMaxAttemptsExplicit {
		t.Fatal("RetryMaxAttemptsExplicit was not preserved")
	}
}

func TestSetModelAndThinkingPersistToConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.DataDir = dir

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := app.SetModelAndEffort("deepseek-v4-pro", "max"); err != nil {
		t.Fatalf("SetModelAndEffort: %v", err)
	}
	app.SetThinkingEnabled(false)

	loaded, ok, err := LoadConfigFile(GlobalConfigPath(dir))
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if !ok {
		t.Fatal("expected config.toml to be written")
	}
	if loaded.Model != "deepseek-v4-pro" || loaded.ReasoningEffort != "max" {
		t.Fatalf("persisted config: %+v", loaded)
	}
	if loaded.ThinkingEnabled == nil || *loaded.ThinkingEnabled {
		t.Fatal("persisted thinking_enabled: want false")
	}
}
