package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/plugins/memoryplugin"
)

func TestMemoryPluginToolsRegisteredByDefault(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	if app.toolRegistry.Get("remember") == nil || app.toolRegistry.Get("forget") == nil || app.toolRegistry.Get("recall_memory") == nil {
		t.Fatalf("memory tools not registered")
	}
	if app.baseToolRegistry.Get("remember") != nil {
		t.Fatal("memory tools should not be in base subagent registry")
	}
}

func TestMemoryPluginCanBeDisabledByConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	if err := SaveConfigFile(GlobalConfigPath(cfg.DataDir), FileConfig{
		Plugins: FilePluginsConfig{Disabled: []string{"memory"}},
	}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	if app.toolRegistry.Get("remember") != nil {
		t.Fatal("remember should not be registered when memory plugin is disabled")
	}
	handled, out, _, err := app.HandleLocalCommand("/memory")
	if err != nil || !handled {
		t.Fatalf("/memory disabled handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("expected disabled message, got %q", out)
	}
}

func TestMemoryPluginCanBeDisabledByExplicitConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.PluginsDisabled = []string{"memory"}
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	if app.toolRegistry.Get("remember") != nil {
		t.Fatal("remember should not be registered when explicit config disables memory plugin")
	}
}

func TestMemoryLocalCommandMatchesOnlyMemoryToken(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	handled, out, _, err := app.HandleLocalCommand("/memorybank")
	if err != nil {
		t.Fatalf("/memorybank err: %v", err)
	}
	if handled || strings.TrimSpace(out) != "" {
		t.Fatalf("/memorybank should not be handled as /memory: handled=%v out=%q", handled, out)
	}
}

func TestMCPRefreshPreservesMemoryTools(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	if err := app.refreshMCPTools(); err != nil {
		t.Fatalf("refreshMCPTools: %v", err)
	}
	if app.toolRegistry.Get("remember") == nil {
		t.Fatal("memory tool disappeared after MCP refresh")
	}
}

func TestPluginStatusesListOfficialPlugins(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	statuses := app.PluginStatuses()
	var got []string
	for _, st := range statuses {
		got = append(got, st.Manifest.ID)
	}
	out := strings.Join(got, "\n")
	if !strings.Contains(out, "memory") {
		t.Fatalf("plugin statuses missing memory:\n%s", out)
	}
	for _, hidden := range []string{"skills-improver", "local-indexer"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("plugin statuses should not list %s before it is ready:\n%s", hidden, out)
		}
	}
}

func TestSetPluginEnabledUpdatesProjectLocalConfigAndRuntime(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	if app.toolRegistry.Get("remember") == nil {
		t.Fatal("memory plugin should start enabled")
	}

	out, err := app.SetPluginEnabled("memory", false)
	if err != nil {
		t.Fatalf("SetPluginEnabled(false): %v", err)
	}
	if !strings.Contains(out, "disabled plugin: memory") {
		t.Fatalf("unexpected disable output: %q", out)
	}
	if app.toolRegistry.Get("remember") != nil {
		t.Fatal("memory tool should be removed after disabling plugin")
	}
	handled, out, _, err := app.HandleLocalCommand("/memory")
	if err != nil || !handled {
		t.Fatalf("/memory handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("expected memory disabled after toggle, got:\n%s", out)
	}

	cfgFile, loaded, err := LoadConfigFile(ProjectLocalConfigPath(app.workspaceRoot))
	if err != nil || !loaded {
		t.Fatalf("load project local config loaded=%v err=%v", loaded, err)
	}
	if len(cfgFile.Plugins.Disabled) != 1 || cfgFile.Plugins.Disabled[0] != "memory" {
		t.Fatalf("expected memory in disabled plugins config, got %+v", cfgFile.Plugins.Disabled)
	}
	if _, loaded, err := LoadConfigFile(ProjectConfigPath(app.workspaceRoot)); err != nil || loaded {
		t.Fatalf("shared project config should not be written by plugin toggle, loaded=%v err=%v", loaded, err)
	}

	out, err = app.SetPluginEnabled("memory", true)
	if err != nil {
		t.Fatalf("SetPluginEnabled(true): %v", err)
	}
	if !strings.Contains(out, "enabled plugin: memory") {
		t.Fatalf("unexpected enable output: %q", out)
	}
	if app.toolRegistry.Get("remember") == nil {
		t.Fatal("memory tool should be registered after enabling plugin")
	}
	cfgFile, _, err = LoadConfigFile(ProjectLocalConfigPath(app.workspaceRoot))
	if err != nil {
		t.Fatalf("load project local config: %v", err)
	}
	if len(cfgFile.Plugins.Disabled) != 0 {
		t.Fatalf("expected disabled plugin config cleared, got %+v", cfgFile.Plugins.Disabled)
	}
}

func TestSetPluginEnabledLocalEnableOverridesSharedDisabled(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{
		Plugins: FilePluginsConfig{Disabled: []string{"memory"}},
	}); err != nil {
		t.Fatalf("save shared project config: %v", err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	if app.toolRegistry.Get("remember") != nil {
		t.Fatal("memory tool should start disabled from shared project config")
	}

	out, err := app.SetPluginEnabled("memory", true)
	if err != nil {
		t.Fatalf("SetPluginEnabled(true): %v", err)
	}
	if !strings.Contains(out, "enabled plugin: memory") {
		t.Fatalf("unexpected enable output: %q", out)
	}
	if app.toolRegistry.Get("remember") == nil {
		t.Fatal("memory tool should be registered after local enable")
	}
	cfgFile, loaded, err := LoadConfigFile(ProjectLocalConfigPath(app.workspaceRoot))
	if err != nil || !loaded {
		t.Fatalf("load project local config loaded=%v err=%v", loaded, err)
	}
	if !containsString(cfgFile.Plugins.Enabled, "memory") {
		t.Fatalf("expected memory in enabled plugins config, got %+v", cfgFile.Plugins.Enabled)
	}
	reloaded, err := LoadAndApplyConfig(Config{DataDir: cfg.DataDir}, app.workspaceRoot)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if containsString(reloaded.PluginsDisabled, "memory") {
		t.Fatalf("expected local enable to override shared disabled on reload, got %+v", reloaded.PluginsDisabled)
	}
}

func TestOfficialPluginCommands(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	handled, out, _, err := app.HandleLocalCommand("/skills-improver proposals")
	if err != nil || handled || strings.TrimSpace(out) != "" {
		t.Fatalf("/skills-improver should not be exposed as a slash command: handled=%v out=%q err=%v", handled, out, err)
	}
	handled, out, _, err = app.HandleLocalCommand("/local-indexer rebuild")
	if err != nil {
		t.Fatalf("/local-indexer rebuild err=%v", err)
	}
	if handled || strings.TrimSpace(out) != "" {
		t.Fatalf("/local-indexer should not be exposed before it is ready: handled=%v out=%q", handled, out)
	}
}

func TestSkillsImproverIsHiddenFromSkillReportAndTools(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	if reportHasSkill(app.SkillReport(), "skills-improver") {
		t.Fatalf("skills-improver should not be visible in skill report: %+v", app.SkillReport().All())
	}
	if app.toolRegistry.Get("save_skill_proposal") != nil {
		t.Fatal("save_skill_proposal should not be registered while skills-improver is hidden")
	}
	blocked, output, updated := app.RunUserPromptSubmitHook("下次 $demo skill 要更具体")
	if blocked || output != "" || updated != "下次 $demo skill 要更具体" {
		t.Fatalf("skills-improver hooks should not run while hidden: blocked=%v output=%q updated=%q", blocked, output, updated)
	}
	if out := app.RunStopHook("final answer", 1); out != "" {
		t.Fatalf("skills-improver stop hook should not run while hidden: %q", out)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "plugins", "skills-improver", "data", "evidence.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skills-improver should not write evidence while hidden, stat err=%v", err)
	}
	if app.toolRegistry.Get("load_skill") == nil {
		t.Fatal("load_skill not registered")
	}
}

func TestMemoryLocalCommandListsAndShowsMemory(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	store := memoryplugin.NewStore(filepath.Join(app.cfg.DataDir, "plugins", "memory"), app.workspaceRoot)
	if _, err := store.Write(memoryplugin.WriteInput{Scope: "global", Type: "user", Name: "style", Description: "concise Chinese", Content: "Answer concisely in Chinese."}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	handled, out, _, err := app.HandleLocalCommand("/memory")
	if err != nil || !handled {
		t.Fatalf("/memory handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "[style](style.md)") {
		t.Fatalf("/memory output missing index:\n%s", out)
	}

	handled, out, _, err = app.HandleLocalCommand("/memory show global/style")
	if err != nil || !handled {
		t.Fatalf("/memory show handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "Answer concisely in Chinese.") {
		t.Fatalf("/memory show output missing body:\n%s", out)
	}
}

func TestMemoryForgetInvalidatesActiveAgent(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	store := memoryplugin.NewStore(filepath.Join(app.cfg.DataDir, "plugins", "memory"), app.workspaceRoot)
	if _, err := store.Write(memoryplugin.WriteInput{Scope: "project", Type: "project", Name: "roadmap", Description: "old fact", Content: "Use the old fact."}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := app.ensureAgent(); err != nil {
		t.Fatalf("ensureAgent: %v", err)
	}
	if app.a == nil {
		t.Fatal("expected active agent")
	}

	handled, out, _, err := app.HandleLocalCommand("/memory forget project/roadmap")
	if err != nil || !handled {
		t.Fatalf("/memory forget handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "forgot memory") {
		t.Fatalf("unexpected forget output: %s", out)
	}
	if app.a != nil {
		t.Fatal("memory deletion should invalidate active agent startup context")
	}
}
