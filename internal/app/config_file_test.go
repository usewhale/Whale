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
	cfg := FileConfig{
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "max",
		ThinkingEnabled: &enabled,
		UI:              FileUIConfig{ViewMode: ViewModeFocus},
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
			MaxAttempts: intPtr(5),
			MaxDelay:    "45s",
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
	if cfg.RetryMaxAttempts != 5 || cfg.RetryMaxDelay != 45*time.Second {
		t.Fatalf("retry not applied: %+v", cfg)
	}
	if cfg.AutoCompact || cfg.AutoCompactThreshold != compactThreshold {
		t.Fatalf("context not applied: %+v", cfg)
	}
	if cfg.MemoryEnabled || cfg.MemoryMaxChars != projectDocMaxBytes || cfg.MemoryFileOrder != "AGENTS.md,TEAM.md" {
		t.Fatalf("project doc not applied: %+v", cfg)
	}
}

func TestApplyFileConfigRejectsInvalidRetryConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := ApplyFileConfig(&cfg, FileConfig{Retry: FileRetryConfig{MaxAttempts: intPtr(0)}}); err == nil {
		t.Fatal("expected invalid max_attempts error")
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

func TestSetProjectApprovalModeUpdatesProjectConfig(t *testing.T) {
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
	if path != ProjectConfigPath(workspace) {
		t.Fatalf("project config path: want %s, got %s", ProjectConfigPath(workspace), path)
	}
	if app.ApprovalMode() != policy.ApprovalModeNever || app.cfg.ApprovalMode != string(policy.ApprovalModeNever) {
		t.Fatalf("approval mode not updated in memory: app=%s cfg=%s", app.ApprovalMode(), app.cfg.ApprovalMode)
	}
	loaded, ok, err := LoadConfigFile(ProjectConfigPath(workspace))
	if err != nil || !ok {
		t.Fatalf("load project config loaded=%v err=%v", ok, err)
	}
	if loaded.Permissions.Mode != string(policy.ApprovalModeNever) {
		t.Fatalf("project permissions.mode: want never, got %q", loaded.Permissions.Mode)
	}
	if loaded.Model != "deepseek-v4-pro" || !containsString(loaded.Skills.Disabled, "project-skill") {
		t.Fatalf("expected unrelated project config to be preserved, got %+v", loaded)
	}
}

func TestClearProjectApprovalModePreservesOtherProjectConfig(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{
		Model: "deepseek-v4-pro",
		Permissions: FilePermissionsConfig{
			Mode:               string(policy.ApprovalModeNever),
			AllowShellPrefixes: []string{"git status"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
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
	if path != ProjectConfigPath(workspace) {
		t.Fatalf("project config path: want %s, got %s", ProjectConfigPath(workspace), path)
	}
	if mode != policy.ApprovalModeOnRequest || app.ApprovalMode() != policy.ApprovalModeOnRequest {
		t.Fatalf("approval mode after clear: returned=%s app=%s", mode, app.ApprovalMode())
	}
	loaded, ok, err := LoadConfigFile(ProjectConfigPath(workspace))
	if err != nil || !ok {
		t.Fatalf("load project config loaded=%v err=%v", ok, err)
	}
	if loaded.Permissions.Mode != "" {
		t.Fatalf("project permissions.mode should be cleared, got %q", loaded.Permissions.Mode)
	}
	if loaded.Model != "deepseek-v4-pro" || !containsString(loaded.Permissions.AllowShellPrefixes, "git status") {
		t.Fatalf("expected unrelated project config to be preserved, got %+v", loaded)
	}
	raw, err := os.ReadFile(ProjectConfigPath(workspace))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if strings.Contains(string(raw), "mode =") {
		t.Fatalf("project config should not contain permissions.mode after clear:\n%s", raw)
	}
}
