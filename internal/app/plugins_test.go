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
	handled, out, err := app.HandleLocalCommand("/memory")
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

	handled, out, err := app.HandleLocalCommand("/memorybank")
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

func TestPluginsCommandListsOfficialPlugins(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	handled, out, err := app.HandleLocalCommand("/plugins")
	if err != nil || !handled {
		t.Fatalf("/plugins handled=%v err=%v", handled, err)
	}
	for _, want := range []string{"memory", "skills-improver", "local-indexer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/plugins output missing %s:\n%s", want, out)
		}
	}
}

func TestPluginsReloadRefreshesDisabledConfigFromDisk(t *testing.T) {
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
	if err := SaveConfigFile(ProjectConfigPath(app.workspaceRoot), FileConfig{
		Plugins: FilePluginsConfig{Disabled: []string{"memory"}},
	}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}

	handled, out, err := app.HandleLocalCommand("/plugins reload")
	if err != nil || !handled {
		t.Fatalf("/plugins reload handled=%v err=%v out=%q", handled, err, out)
	}
	if app.toolRegistry.Get("remember") != nil {
		t.Fatal("memory tool should be removed after reloading disabled plugin config")
	}
	handled, out, err = app.HandleLocalCommand("/memory")
	if err != nil || !handled {
		t.Fatalf("/memory handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("expected memory disabled after reload, got:\n%s", out)
	}
}

func TestOfficialPluginScaffoldCommands(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	handled, out, err := app.HandleLocalCommand("/skills-improver proposals")
	if err != nil || !handled {
		t.Fatalf("/skills-improver proposals handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "none") {
		t.Fatalf("expected empty proposals output, got:\n%s", out)
	}
	handled, out, err = app.HandleLocalCommand("/local-indexer rebuild")
	if err != nil || !handled {
		t.Fatalf("/local-indexer rebuild handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "not implemented") {
		t.Fatalf("expected scaffold rebuild output, got:\n%s", out)
	}
}

func TestPluginStatusShowsHookContributions(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	handled, out, err := app.HandleLocalCommand("/plugins status skills-improver")
	if err != nil || !handled {
		t.Fatalf("/plugins status skills-improver handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "hooks: skills-improver.collect-evidence") {
		t.Fatalf("expected hook contribution in plugin status, got:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(cfg.DataDir, "plugins", "skills-improver")) && !strings.Contains(out, "`"+filepath.Join(cfg.DataDir, "plugins", "skills-improver")+"`") {
		t.Fatalf("expected plugin paths to be markdown inline code, got:\n%s", out)
	}
}

func TestPluginSkillIsAvailableToSkillReportAndLoadTool(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	if !reportHasSkill(app.SkillReport(), "skills-improver") {
		t.Fatalf("expected plugin skill in report: %+v", app.SkillReport().All())
	}
	if app.toolRegistry.Get("load_skill") == nil {
		t.Fatal("load_skill not registered")
	}
}

func TestDisabledPluginSkillIsNotSelectable(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.SkillsDisabled = []string{"skills-improver"}
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	report := app.SkillReport()
	for _, view := range report.Ready {
		if view.Name == "skills-improver" {
			t.Fatalf("disabled plugin skill should not be ready: %+v", report)
		}
	}
	for _, view := range report.Selectable() {
		if view.Name == "skills-improver" {
			t.Fatalf("disabled plugin skill should not be selectable: %+v", report.Selectable())
		}
	}
	foundDisabled := false
	for _, view := range report.Disabled {
		if view.Name == "skills-improver" && view.Source == "plugin" {
			foundDisabled = true
		}
	}
	if !foundDisabled {
		t.Fatalf("expected disabled plugin skill in disabled group: %+v", report.Disabled)
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

	handled, out, err := app.HandleLocalCommand("/memory")
	if err != nil || !handled {
		t.Fatalf("/memory handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "[style](style.md)") {
		t.Fatalf("/memory output missing index:\n%s", out)
	}

	handled, out, err = app.HandleLocalCommand("/memory show global/style")
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

	handled, out, err := app.HandleLocalCommand("/memory forget project/roadmap")
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
