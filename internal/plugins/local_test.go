package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	whalemcp "github.com/usewhale/whale/internal/mcp"
)

func TestInstallLocalPluginLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	pluginDir := writeLocalPlugin(t, "demo-plugin", "0.2.0")

	res, err := InstallLocal(dataDir, pluginDir)
	if err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}
	if res.Record.ID != "demo-plugin" || res.Record.Version != "0.2.0" {
		t.Fatalf("unexpected record: %+v", res.Record)
	}
	if _, err := os.Stat(filepath.Join(res.Record.InstallPath, ManifestFileName)); err != nil {
		t.Fatalf("manifest not copied to cache: %v", err)
	}

	records, err := LoadInstalled(dataDir)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(records) != 1 || records[0].ID != "demo-plugin" {
		t.Fatalf("unexpected installed records: %+v", records)
	}

	found, ok, err := FindInstalled(dataDir, "demo-plugin")
	if err != nil || !ok {
		t.Fatalf("FindInstalled ok=%v err=%v", ok, err)
	}
	if found.InstallPath != res.Record.InstallPath {
		t.Fatalf("install path mismatch: %q != %q", found.InstallPath, res.Record.InstallPath)
	}

	removed, err := UninstallLocal(dataDir, "demo-plugin")
	if err != nil {
		t.Fatalf("UninstallLocal: %v", err)
	}
	if removed.ID != "demo-plugin" {
		t.Fatalf("unexpected removed record: %+v", removed)
	}
	if _, err := os.Stat(res.Record.InstallPath); !os.IsNotExist(err) {
		t.Fatalf("expected cache removed, err=%v", err)
	}
}

func TestInstallLocalReplacesSamePluginID(t *testing.T) {
	dataDir := t.TempDir()
	first := writeLocalPlugin(t, "demo-plugin", "0.1.0")
	second := writeLocalPlugin(t, "demo-plugin", "0.2.0")

	firstRes, err := InstallLocal(dataDir, first)
	if err != nil {
		t.Fatalf("install first: %v", err)
	}
	secondRes, err := InstallLocal(dataDir, second)
	if err != nil {
		t.Fatalf("install second: %v", err)
	}
	records, err := LoadInstalled(dataDir)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(records) != 1 || records[0].Version != "0.2.0" {
		t.Fatalf("expected replacement with v0.2.0, got %+v", records)
	}
	if _, err := os.Stat(firstRes.Record.InstallPath); !os.IsNotExist(err) {
		t.Fatalf("expected old version cache removed, err=%v", err)
	}
	if _, err := os.Stat(secondRes.Record.InstallPath); err != nil {
		t.Fatalf("expected new version cache retained: %v", err)
	}
}

func TestInstallLocalPreservesExistingCacheWhenReplacementCopyFails(t *testing.T) {
	dataDir := t.TempDir()
	first := writeLocalPlugin(t, "demo-plugin", "0.1.0")
	writeFile(t, filepath.Join(first, "marker.txt"), []byte("first"))
	res, err := InstallLocal(dataDir, first)
	if err != nil {
		t.Fatalf("install first: %v", err)
	}
	second := writeLocalPlugin(t, "demo-plugin", "0.1.0")
	if err := os.Symlink(filepath.Join(second, "missing-target"), filepath.Join(second, "bad-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := InstallLocal(dataDir, second); err == nil || !strings.Contains(err.Error(), "unsupported symlink") {
		t.Fatalf("expected symlink copy failure, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(res.Record.InstallPath, "marker.txt"))
	if err != nil {
		t.Fatalf("existing plugin cache should remain readable: %v", err)
	}
	if string(got) != "first" {
		t.Fatalf("existing plugin cache was replaced: %q", got)
	}
	records, err := LoadInstalled(dataDir)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(records) != 1 || records[0].InstallPath != res.Record.InstallPath {
		t.Fatalf("installed record should still point at existing cache, got %+v", records)
	}
}

func TestUninstallLocalDoesNotRemoveCacheOutsideManagedRoot(t *testing.T) {
	dataDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-cache")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("create outside cache: %v", err)
	}
	writeFile(t, filepath.Join(outside, "marker.txt"), []byte("keep"))
	if err := saveInstalled(dataDir, []InstalledRecord{{
		ID:          "demo-plugin",
		Name:        "Demo Plugin",
		Version:     "0.1.0",
		InstallPath: outside,
	}}); err != nil {
		t.Fatalf("save installed: %v", err)
	}
	if _, err := UninstallLocal(dataDir, "demo-plugin"); err != nil {
		t.Fatalf("UninstallLocal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "marker.txt")); err != nil {
		t.Fatalf("outside path should not be removed: %v", err)
	}
	records, err := LoadInstalled(dataDir)
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected record removed from index, got %+v", records)
	}
}

func TestInstallLocalRejectsUnsafeInstallPathParts(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "unsafe version traversal",
			body: `id = "demo-plugin"
name = "Demo Plugin"
version = "../../outside"
`,
			want: "plugin version must be a safe slug",
		},
		{
			name: "unsafe version separator",
			body: `id = "demo-plugin"
name = "Demo Plugin"
version = "v1/escape"
`,
			want: "plugin version must be a safe slug",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pluginDir := t.TempDir()
			writeFile(t, filepath.Join(pluginDir, ManifestFileName), []byte(tc.body))
			_, err := InstallLocal(t.TempDir(), pluginDir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestLoadManifestReportsComponentDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), []byte(`id = "bad-paths"
name = "Bad Paths"
version = "0.1.0"

[components]
skills = "../outside"
commands = "./missing"
`))
	manifest, diags, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.ID != "bad-paths" {
		t.Fatalf("manifest id: %s", manifest.ID)
	}
	got := diagnosticsText(diags)
	if !strings.Contains(got, "components.skills") || !strings.Contains(got, "components.commands") {
		t.Fatalf("expected component diagnostics, got %+v", diags)
	}
}

func TestInstalledLocalPluginRuntimeComponents(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	pluginDir := writeLocalPlugin(t, "demo-plugin", "0.2.0")
	writeFile(t, filepath.Join(pluginDir, "mcp.json"), []byte(`{
  "mcpServers": {
    "Local_Server": {
      "command": "bin/server",
      "disabled_tools": ["read_secret"]
    }
  }
}`))
	writeFile(t, filepath.Join(pluginDir, "bin", "server"), []byte("#!/bin/sh\nexit 0\n"))
	if err := os.Chmod(filepath.Join(pluginDir, "bin", "server"), 0o700); err != nil {
		t.Fatalf("chmod mcp server: %v", err)
	}
	writeFile(t, filepath.Join(pluginDir, "hooks.toml"), []byte(`[[hooks.SessionStart]]
description = "Plugin startup marker"
command = "printf ok > marker.txt"
`))
	writeFile(t, filepath.Join(pluginDir, ManifestFileName), []byte(`id = "demo-plugin"
name = "Demo Plugin"
version = "0.2.0"
description = "A demo plugin."

[components]
skills = "./skills"
mcp = "./mcp.json"
hooks = "./hooks.toml"
`))
	if _, err := InstallLocal(dataDir, pluginDir); err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}

	enabled := true
	serverEnabled := false
	mgr := NewManager(Context{DataDir: dataDir, WorkspaceRoot: workspace}, ConfigMap{
		"demo-plugin": {
			Enabled: &enabled,
			MCPServers: map[string]MCPServerConfig{
				"local-server": {
					Enabled:       &serverEnabled,
					DisabledTools: []string{"write_file"},
				},
			},
		},
	})
	skills := mgr.Skills()
	if len(skills) != 1 || skills[0].Name != "demo" {
		t.Fatalf("unexpected plugin skills: %+v", skills)
	}
	servers := mgr.MCPServers()
	server, ok := servers["demo-plugin.local-server"]
	if !ok {
		t.Fatalf("missing namespaced plugin MCP server: %+v", servers)
	}
	if !server.Disabled || !contains(server.DisabledTools, "read_secret") || !contains(server.DisabledTools, "write_file") {
		t.Fatalf("unexpected MCP server policy merge: %+v", server)
	}
	if !strings.HasSuffix(server.Command, filepath.Join("bin", "server")) || !filepath.IsAbs(server.Command) {
		t.Fatalf("expected absolute plugin command, got %q", server.Command)
	}
	if server.Env["WHALE_PLUGIN_ROOT"] == "" || server.Env["WHALE_PLUGIN_DATA_DIR"] == "" || server.Env["WHALE_PLUGIN_PROJECT_DIR"] == "" {
		t.Fatalf("expected plugin MCP env defaults, got %+v", server.Env)
	}
	hooks := mgr.CommandHooks()
	if len(hooks) != 1 || hooks[0].Source != "plugin:demo-plugin" || !hooks[0].Managed {
		t.Fatalf("unexpected plugin hooks: %+v", hooks)
	}
	if hooks[0].CWD == "" || !filepath.IsAbs(hooks[0].CWD) {
		t.Fatalf("expected absolute plugin hook cwd, got %q", hooks[0].CWD)
	}
	outcome := mgr.Outcome()
	if len(outcome.Plugins) != 2 {
		t.Fatalf("expected memory plus local plugin in outcome, got %+v", outcome.Plugins)
	}
	if _, ok := outcome.MCPServers["demo-plugin.local-server"]; !ok {
		t.Fatalf("outcome missing plugin MCP server: %+v", outcome.MCPServers)
	}
	if len(outcome.CommandHooks) != 1 || outcome.CommandHooks[0].Source != "plugin:demo-plugin" {
		t.Fatalf("outcome missing plugin command hook: %+v", outcome.CommandHooks)
	}
	if len(outcome.Skills) != 1 || outcome.Skills[0].Name != "demo" {
		t.Fatalf("outcome missing plugin skill: %+v", outcome.Skills)
	}
	outcome.Skills[0].Name = "mutated"
	outcome.MCPServers["demo-plugin.local-server"] = whalemcpServerWithName("mutated")
	outcome.Statuses[0].Paths["root"] = "mutated"
	next := mgr.Outcome()
	if len(next.Skills) != 1 || next.Skills[0].Name != "demo" {
		t.Fatalf("outcome skills should be immutable snapshots, got %+v", next.Skills)
	}
	if next.MCPServers["demo-plugin.local-server"].Name != "demo-plugin.local-server" {
		t.Fatalf("outcome MCP servers should be immutable snapshots, got %+v", next.MCPServers)
	}
	if next.Statuses[0].Paths["root"] == "mutated" {
		t.Fatalf("outcome statuses should be immutable snapshots, got %+v", next.Statuses[0].Paths)
	}
}

func TestInstalledLocalPluginCommandsAgentsAndRules(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	pluginDir := writeLocalPlugin(t, "demo-plugin", "0.3.0")
	writeFile(t, filepath.Join(pluginDir, "commands", "ask.md"), []byte(`---
description: Ask with plugin context.
argument_hint: "<topic>"
read_only: true
---
Explain {{args}} using plugin guidance.
`))
	writeFile(t, filepath.Join(pluginDir, "commands", "commands.toml"), []byte(`[[commands]]
name = "fmt"
description = "Run formatter"
command = "gofmt -w internal/plugins"
timeout_ms = 45000
class = "mutating"
`))
	writeFile(t, filepath.Join(pluginDir, "agents", "reviewer.md"), []byte(`---
description: Plugin reviewer.
capabilities: workspace.read, web.search
max_tool_iters: 3
---
You are the plugin reviewer.
`))
	writeFile(t, filepath.Join(pluginDir, "rules", "style.md"), []byte(`---
name: style
---
Prefer direct wording.
`))
	writeFile(t, filepath.Join(pluginDir, ManifestFileName), []byte(`id = "demo-plugin"
name = "Demo Plugin"
version = "0.3.0"
description = "A demo plugin."

[components]
skills = "./skills"
commands = "./commands"
agents = "./agents"
rules = "./rules"
`))
	if _, err := InstallLocal(dataDir, pluginDir); err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}
	enabled := true
	mgr := NewManager(Context{DataDir: dataDir, WorkspaceRoot: workspace}, ConfigMap{
		"demo-plugin": {Enabled: &enabled},
	})
	names := mgr.SlashCommandNames()
	if !contains(names, "/demo-plugin:ask") || !contains(names, "/demo-plugin:fmt") {
		t.Fatalf("missing plugin commands: %+v", names)
	}
	res, handled, err := mgr.HandleCommand(nil, "/demo-plugin:ask hooks")
	if err != nil || !handled || res.Turn == nil {
		t.Fatalf("HandleCommand prompt handled=%v err=%v res=%+v", handled, err, res)
	}
	if !res.Turn.ReadOnly || !strings.Contains(res.Turn.Input, "Explain hooks using plugin guidance.") {
		t.Fatalf("unexpected prompt command turn: %+v", res.Turn)
	}
	res, handled, err = mgr.HandleCommand(nil, "/demo-plugin:fmt")
	if err != nil || !handled || res.Turn == nil {
		t.Fatalf("HandleCommand shell handled=%v err=%v res=%+v", handled, err, res)
	}
	if !res.Turn.Hidden || res.Turn.ReadOnly || !strings.Contains(res.Turn.Input, "shell_run") || !strings.Contains(res.Turn.Input, "gofmt -w internal/plugins") {
		t.Fatalf("unexpected shell command turn: %+v", res.Turn)
	}
	outcome := mgr.Outcome()
	if len(outcome.Agents) != 1 || outcome.Agents[0].Name != "demo-plugin:reviewer" || !contains(outcome.Agents[0].Capabilities, "web.search") {
		t.Fatalf("unexpected agents: %+v", outcome.Agents)
	}
	if len(outcome.Rules) != 1 || outcome.Rules[0].Name != "demo-plugin:style" || !strings.Contains(outcome.Rules[0].Content, "Prefer direct wording") {
		t.Fatalf("unexpected rules: %+v", outcome.Rules)
	}
	blocks := mgr.StartupBlocks(nil)
	if len(blocks) == 0 || !strings.Contains(strings.Join(blocks, "\n"), "Plugin rule demo-plugin:style") {
		t.Fatalf("expected plugin rule startup block, got %+v", blocks)
	}
	status, ok := mgr.Status("demo-plugin")
	if !ok {
		t.Fatal("status missing")
	}
	if !contains(status.Agents, "demo-plugin:reviewer") || !contains(status.Rules, "demo-plugin:style") {
		t.Fatalf("status missing agents/rules: %+v", status)
	}
}

func TestInstalledLocalPluginOutcomeReportsComponentLoadDiagnostics(t *testing.T) {
	dataDir := t.TempDir()
	pluginDir := writeLocalPlugin(t, "bad-plugin", "0.1.0")
	writeFile(t, filepath.Join(pluginDir, "mcp.json"), []byte(`{`))
	writeFile(t, filepath.Join(pluginDir, "hooks.toml"), []byte(`[[hooks.SessionStart]]
command = [
`))
	writeFile(t, filepath.Join(pluginDir, ManifestFileName), []byte(`id = "bad-plugin"
name = "Bad Plugin"
version = "0.1.0"
description = "A broken plugin."

[components]
skills = "./skills"
mcp = "./mcp.json"
hooks = "./hooks.toml"
`))
	if _, err := InstallLocal(dataDir, pluginDir); err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}
	enabled := true
	mgr := NewManager(Context{DataDir: dataDir, WorkspaceRoot: t.TempDir()}, ConfigMap{
		"bad-plugin": {Enabled: &enabled},
	})
	got := diagnosticsText(mgr.Outcome().Diagnostics)
	if !strings.Contains(got, "components.mcp") || !strings.Contains(got, "components.hooks") {
		t.Fatalf("expected bad component diagnostics in outcome, got:\n%s", got)
	}
	status, ok := mgr.Status("bad-plugin")
	if !ok {
		t.Fatal("bad plugin status missing")
	}
	got = diagnosticsText(status.Diagnostics)
	if !strings.Contains(got, "components.mcp") || !strings.Contains(got, "components.hooks") {
		t.Fatalf("expected bad component diagnostics in status, got:\n%s", got)
	}
	if len(mgr.MCPServers()) != 0 || len(mgr.CommandHooks()) != 0 {
		t.Fatalf("bad plugin should not expose invalid MCP/hooks, mcp=%+v hooks=%+v", mgr.MCPServers(), mgr.CommandHooks())
	}
}

func TestInstallLocalRejectsMissingManifestAndBuiltinID(t *testing.T) {
	if _, err := InstallLocal(t.TempDir(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "missing whale-plugin.toml") {
		t.Fatalf("expected missing manifest error, got %v", err)
	}
	dir := writeLocalPlugin(t, "memory", "0.1.0")
	if _, err := InstallLocal(t.TempDir(), dir); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected built-in id error, got %v", err)
	}
}

func writeLocalPlugin(t *testing.T, id, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "demo", "SKILL.md"), []byte(`---
name: demo
description: Use this demo skill for plugin tests.
---

# Demo
`))
	writeFile(t, filepath.Join(dir, ManifestFileName), []byte(`id = "`+id+`"
name = "Demo Plugin"
version = "`+version+`"
description = "A demo plugin."
authors = ["Whale"]
keywords = ["demo"]

[components]
skills = "./skills"

[display]
category = "development"
`))
	return dir
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func diagnosticsText(diags []Diagnostic) string {
	var parts []string
	for _, diag := range diags {
		parts = append(parts, diag.Label+" "+diag.Detail)
	}
	return strings.Join(parts, "\n")
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func whalemcpServerWithName(name string) whalemcp.ServerConfig {
	return whalemcp.ServerConfig{Name: name}
}
