package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/policy"
)

func intPtr(v int) *int { return &v }

func TestConfigFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := GlobalConfigPath(dir)
	enabled := true
	checkUpdates := false
	cfg := FileConfig{
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "max",
		ThinkingEnabled: &enabled,
		UI:              FileUIConfig{ViewMode: ViewModeFocus, CheckForUpdateOnStartup: &checkUpdates},
		API:             FileAPIConfig{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1/"},
		Permissions: FilePermissionsConfig{
			Mode:               "never",
			AllowShellPrefixes: []string{"git status", "go test"},
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
	if !strings.Contains(string(raw), "[permissions]") || strings.Contains(string(raw), "allow_prefixes") {
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
	if loaded.UI.CheckForUpdateOnStartup == nil || *loaded.UI.CheckForUpdateOnStartup {
		t.Fatalf("ui.check_for_update_on_startup: want false, got %+v", loaded.UI.CheckForUpdateOnStartup)
	}
	if loaded.Permissions.Mode != "never" || len(loaded.Permissions.AllowShellPrefixes) != 2 {
		t.Fatalf("permissions config: %+v", loaded.Permissions)
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
			MaxDelay:          "45s",
		},
		Permissions: FilePermissionsConfig{
			Mode:               "never",
			AllowShellPrefixes: []string{"git status"},
			DenyShellPrefixes:  []string{"rm -rf"},
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

	if cfg.ApprovalMode != "never" || cfg.AllowPrefixes != "git status" || cfg.DenyPrefixes != "rm -rf" {
		t.Fatalf("permissions not applied: %+v", cfg)
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
	if cfg.RetryMaxAttempts != 5 || cfg.RetryStreamMaxAttempts != 7 || cfg.RetryMaxDelay != 45*time.Second {
		t.Fatalf("retry not applied: %+v", cfg)
	}
	if cfg.AutoCompact || cfg.AutoCompactThreshold != compactThreshold {
		t.Fatalf("context not applied: %+v", cfg)
	}
	if cfg.MemoryEnabled || cfg.MemoryMaxChars != projectDocMaxBytes || cfg.MemoryFileOrder != "AGENTS.md,TEAM.md" {
		t.Fatalf("project doc not applied: %+v", cfg)
	}
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
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{MaxAttempts: intPtr(0)}}); err == nil {
		t.Fatal("expected invalid max_attempts error")
	}
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{StreamMaxAttempts: intPtr(0)}}); err == nil {
		t.Fatal("expected invalid stream_max_attempts error")
	}
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{MaxDelay: "soon"}}); err == nil {
		t.Fatal("expected invalid max_delay error")
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
	if _, err := os.Stat(preferencesPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("preferences.json should not be created, err=%v", err)
	}
}

func TestSetProjectApprovalModeUpdatesProjectLocalConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{
		Model: "deepseek-v4-pro",
		Skills: FileSkillsConfig{
			Disabled: []string{"project-skill"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	app := &App{
		workspaceRoot: workspace,
		cfg:           Config{DataDir: dataDir, ApprovalMode: string(policy.ApprovalModeOnRequest)},
		approvalMode:  policy.ApprovalModeOnRequest,
	}

	path, err := app.SetProjectApprovalMode(policy.ApprovalModeNever)
	if err != nil {
		t.Fatalf("SetProjectApprovalMode: %v", err)
	}
	if path != ProjectLocalConfigPath(workspace) {
		t.Fatalf("project local config path: want %s, got %s", ProjectLocalConfigPath(workspace), path)
	}
	if app.ApprovalMode() != policy.ApprovalModeNever || app.cfg.ApprovalMode != string(policy.ApprovalModeNever) {
		t.Fatalf("approval mode not updated in memory: app=%s cfg=%s", app.ApprovalMode(), app.cfg.ApprovalMode)
	}
	local, ok, err := LoadConfigFile(ProjectLocalConfigPath(workspace))
	if err != nil || !ok {
		t.Fatalf("load project local config loaded=%v err=%v", ok, err)
	}
	if local.Permissions.Mode != string(policy.ApprovalModeNever) {
		t.Fatalf("project local permissions.mode: want never, got %q", local.Permissions.Mode)
	}
	shared, ok, err := LoadConfigFile(ProjectConfigPath(workspace))
	if err != nil || !ok {
		t.Fatalf("load shared project config loaded=%v err=%v", ok, err)
	}
	if shared.Permissions.Mode != "" {
		t.Fatalf("shared project permissions.mode should be untouched, got %q", shared.Permissions.Mode)
	}
	if shared.Model != "deepseek-v4-pro" || !containsString(shared.Skills.Disabled, "project-skill") {
		t.Fatalf("expected unrelated shared project config to be preserved, got %+v", shared)
	}
}

func TestClearProjectApprovalModeClearsLocalAndFallsBackToProject(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{
		Model: "deepseek-v4-pro",
		Permissions: FilePermissionsConfig{
			Mode:               string(policy.ApprovalModeOnRequest),
			AllowShellPrefixes: []string{"git status"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := SaveConfigFile(ProjectLocalConfigPath(workspace), FileConfig{
		Permissions: FilePermissionsConfig{
			Mode:               string(policy.ApprovalModeNever),
			AllowShellPrefixes: []string{"go test"},
		},
	}); err != nil {
		t.Fatalf("save project local config: %v", err)
	}
	app := &App{
		workspaceRoot: workspace,
		cfg:           Config{DataDir: dataDir, ApprovalMode: string(policy.ApprovalModeNever)},
		approvalMode:  policy.ApprovalModeNever,
	}

	mode, path, err := app.ClearProjectApprovalMode()
	if err != nil {
		t.Fatalf("ClearProjectApprovalMode: %v", err)
	}
	if path != ProjectLocalConfigPath(workspace) {
		t.Fatalf("project local config path: want %s, got %s", ProjectLocalConfigPath(workspace), path)
	}
	if mode != policy.ApprovalModeOnRequest || app.ApprovalMode() != policy.ApprovalModeOnRequest {
		t.Fatalf("approval mode after clear: returned=%s app=%s", mode, app.ApprovalMode())
	}
	local, ok, err := LoadConfigFile(ProjectLocalConfigPath(workspace))
	if err != nil || !ok {
		t.Fatalf("load project local config loaded=%v err=%v", ok, err)
	}
	if local.Permissions.Mode != "" {
		t.Fatalf("project local permissions.mode should be cleared, got %q", local.Permissions.Mode)
	}
	if !containsString(local.Permissions.AllowShellPrefixes, "go test") {
		t.Fatalf("expected unrelated project local config to be preserved, got %+v", local)
	}
	shared, ok, err := LoadConfigFile(ProjectConfigPath(workspace))
	if err != nil || !ok {
		t.Fatalf("load shared project config loaded=%v err=%v", ok, err)
	}
	if shared.Permissions.Mode != string(policy.ApprovalModeOnRequest) || !containsString(shared.Permissions.AllowShellPrefixes, "git status") {
		t.Fatalf("expected shared project config to be preserved, got %+v", shared)
	}
	raw, err := os.ReadFile(ProjectLocalConfigPath(workspace))
	if err != nil {
		t.Fatalf("read project local config: %v", err)
	}
	if strings.Contains(string(raw), "mode =") {
		t.Fatalf("project local config should not contain permissions.mode after clear:\n%s", raw)
	}
}
