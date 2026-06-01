package plugins

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/usewhale/whale/internal/agent"
	whalemcp "github.com/usewhale/whale/internal/mcp"
	"github.com/usewhale/whale/internal/skills"
)

const (
	ManifestFileName  = "whale-plugin.toml"
	InstalledFileName = "installed.toml"
)

type InstalledRecord struct {
	ID          string       `toml:"id"`
	Name        string       `toml:"name,omitempty"`
	Version     string       `toml:"version,omitempty"`
	Description string       `toml:"description,omitempty"`
	Authors     []string     `toml:"authors,omitempty"`
	License     string       `toml:"license,omitempty"`
	Homepage    string       `toml:"homepage,omitempty"`
	Repository  string       `toml:"repository,omitempty"`
	Keywords    []string     `toml:"keywords,omitempty"`
	SourcePath  string       `toml:"source_path,omitempty"`
	InstallPath string       `toml:"install_path,omitempty"`
	InstalledAt string       `toml:"installed_at,omitempty"`
	Components  Components   `toml:"components,omitempty"`
	Display     Display      `toml:"display,omitempty"`
	Diagnostics []Diagnostic `toml:"diagnostics,omitempty"`
}

type installedIndex struct {
	Plugins []InstalledRecord `toml:"plugins,omitempty"`
}

type InstallResult struct {
	Record InstalledRecord
}

type installedPlugin struct {
	record InstalledRecord
	config Config
}

type installedRuntime struct {
	skills       []*skills.Skill
	commands     []SlashCommand
	agents       []AgentDefinition
	rules        []RuleBlock
	mcpServers   map[string]whalemcp.ServerConfig
	commandHooks []agent.ResolvedHook
	diagnostics  []Diagnostic
}

func (p installedPlugin) Manifest() Manifest {
	return p.record.Manifest()
}

func (p installedPlugin) Paths(Context) map[string]string {
	return map[string]string{
		"source":  p.record.SourcePath,
		"install": p.record.InstallPath,
	}
}

func (p installedPlugin) loadRuntime(ctx Context) installedRuntime {
	runtime := installedRuntime{
		skills:     skills.Discover(pluginSkillRoots(p.record.InstallPath, p.record.Components)),
		mcpServers: map[string]whalemcp.ServerConfig{},
	}
	runtime.commands, runtime.diagnostics = p.loadCommands(runtime.diagnostics)
	runtime.agents, runtime.diagnostics = p.loadAgents(runtime.diagnostics)
	runtime.rules, runtime.diagnostics = p.loadRules(runtime.diagnostics)
	runtime.mcpServers, runtime.diagnostics = p.loadMCPServers(ctx, runtime.diagnostics)
	runtime.commandHooks, runtime.diagnostics = p.loadCommandHooks(runtime.diagnostics)
	if len(runtime.diagnostics) == 0 {
		runtime.diagnostics = append(runtime.diagnostics, Diagnostic{PluginID: p.record.ID, Level: DiagnosticOK, Label: "components", Detail: "loaded"})
	}
	return runtime
}

func (p installedPlugin) loadMCPServers(ctx Context, diags []Diagnostic) (map[string]whalemcp.ServerConfig, []Diagnostic) {
	out := map[string]whalemcp.ServerConfig{}
	path := componentPath(p.record.InstallPath, p.record.Components.MCP)
	if path == "" {
		if strings.TrimSpace(p.record.Components.MCP) != "" {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.mcp", Detail: "path not found: " + p.record.Components.MCP})
		}
		return out, diags
	}
	cfg, err := whalemcp.LoadConfig(path)
	if err != nil {
		diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.mcp", Detail: err.Error()})
		return out, diags
	}
	for name, srv := range cfg.Servers {
		name = cleanPluginComponentName(name)
		if name == "" {
			continue
		}
		srv.Name = name
		srv.Command = resolvePluginCommand(p.record.InstallPath, srv.Command)
		if srv.Env == nil {
			srv.Env = map[string]string{}
		}
		addDefaultEnv(srv.Env, "WHALE_PLUGIN_ROOT", p.record.InstallPath)
		addDefaultEnv(srv.Env, "WHALE_PLUGIN_DATA_DIR", ctx.PluginDataDir(p.record.ID))
		addDefaultEnv(srv.Env, "WHALE_PLUGIN_PROJECT_DIR", ctx.PluginProjectDir(p.record.ID))
		if policy, ok := p.config.MCPServers[name]; ok {
			if policy.Enabled != nil {
				srv.Disabled = !*policy.Enabled
			}
			srv.DisabledTools = mergeStringLists(srv.DisabledTools, policy.DisabledTools)
		}
		out[name] = srv
	}
	return out, diags
}

func (p installedPlugin) loadCommandHooks(diags []Diagnostic) ([]agent.ResolvedHook, []Diagnostic) {
	path := componentPath(p.record.InstallPath, p.record.Components.Hooks)
	if path == "" {
		if strings.TrimSpace(p.record.Components.Hooks) != "" {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.hooks", Detail: "path not found: " + p.record.Components.Hooks})
		}
		return nil, diags
	}
	hooks, err := loadPluginCommandHooks(path, p.record.InstallPath, p.record.ID)
	if err != nil {
		diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.hooks", Detail: err.Error()})
		return nil, diags
	}
	return hooks, diags
}

func (r InstalledRecord) Manifest() Manifest {
	return Manifest{
		ID:          r.ID,
		Name:        r.Name,
		Version:     r.Version,
		Description: r.Description,
		Authors:     append([]string(nil), r.Authors...),
		License:     r.License,
		Homepage:    r.Homepage,
		Repository:  r.Repository,
		Keywords:    append([]string(nil), r.Keywords...),
		Components:  r.Components,
		Display:     r.Display,
		Status:      "installed",
	}
}

func InstalledIndexPath(dataDir string) string {
	return filepath.Join(dataDir, "plugins", InstalledFileName)
}

func InstalledCacheRoot(dataDir string) string {
	return filepath.Join(dataDir, "plugins", "cache", "local")
}

func safeInstallPath(dataDir, id, version string) (string, error) {
	id = cleanPluginID(id)
	version = strings.TrimSpace(version)
	if !safePluginPathPart(id) {
		return "", fmt.Errorf("plugin id must be a safe slug: %s", id)
	}
	if !safePluginPathPart(version) {
		return "", fmt.Errorf("plugin version must be a safe slug: %s", version)
	}
	root, err := filepath.Abs(InstalledCacheRoot(dataDir))
	if err != nil {
		return "", fmt.Errorf("resolve plugin cache root: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(root, id, version))
	if err != nil {
		return "", fmt.Errorf("resolve plugin install path: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("resolve plugin install path: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("plugin install path escapes cache root")
	}
	return target, nil
}

func safePluginPathPart(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func LoadManifest(pluginDir string) (Manifest, []Diagnostic, error) {
	path := filepath.Join(pluginDir, ManifestFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, nil, fmt.Errorf("missing %s", ManifestFileName)
		}
		return Manifest{}, nil, fmt.Errorf("read %s: %w", ManifestFileName, err)
	}
	var manifest Manifest
	if err := toml.Unmarshal(b, &manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("parse %s: %w", ManifestFileName, err)
	}
	manifest = normalizeManifest(manifest)
	diags := validateManifest(pluginDir, manifest)
	if manifest.ID == "" {
		return manifest, diags, fmt.Errorf("plugin id must not be empty")
	}
	return manifest, diags, nil
}

func InstallLocal(dataDir, pluginDir string) (InstallResult, error) {
	abs, err := filepath.Abs(pluginDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("resolve plugin path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return InstallResult{}, fmt.Errorf("stat plugin path: %w", err)
	}
	if !info.IsDir() {
		return InstallResult{}, fmt.Errorf("plugin path must be a directory: %s", pluginDir)
	}
	manifest, diags, err := LoadManifest(abs)
	if err != nil {
		return InstallResult{}, err
	}
	if IsBuiltinID(manifest.ID) {
		return InstallResult{}, fmt.Errorf("plugin id is reserved by a built-in plugin: %s", manifest.ID)
	}
	installPath, err := safeInstallPath(dataDir, manifest.ID, manifest.Version)
	if err != nil {
		return InstallResult{}, err
	}
	rollback, cleanup, err := stageAndSwapPluginInstall(abs, installPath)
	if err != nil {
		return InstallResult{}, err
	}
	committed := false
	defer func() {
		if !committed && rollback != nil {
			_ = rollback()
		}
	}()
	record := InstalledRecord{
		ID:          manifest.ID,
		Name:        manifest.Name,
		Version:     manifest.Version,
		Description: manifest.Description,
		Authors:     append([]string(nil), manifest.Authors...),
		License:     manifest.License,
		Homepage:    manifest.Homepage,
		Repository:  manifest.Repository,
		Keywords:    append([]string(nil), manifest.Keywords...),
		SourcePath:  abs,
		InstallPath: installPath,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Components:  manifest.Components,
		Display:     manifest.Display,
		Diagnostics: diags,
	}
	replaced, err := upsertInstalled(dataDir, record)
	if err != nil {
		return InstallResult{}, err
	}
	committed = true
	if cleanup != nil {
		_ = cleanup()
	}
	if err := cleanupReplacedPluginCaches(dataDir, record.InstallPath, replaced); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Record: record}, nil
}

func UninstallLocal(dataDir, id string) (InstalledRecord, error) {
	id = cleanPluginID(id)
	if id == "" {
		return InstalledRecord{}, fmt.Errorf("plugin id must not be empty")
	}
	if IsBuiltinID(id) {
		return InstalledRecord{}, fmt.Errorf("built-in plugin cannot be uninstalled: %s", id)
	}
	records, err := LoadInstalled(dataDir)
	if err != nil {
		return InstalledRecord{}, err
	}
	next := make([]InstalledRecord, 0, len(records))
	var removed InstalledRecord
	for _, record := range records {
		if cleanPluginID(record.ID) == id {
			removed = record
			continue
		}
		next = append(next, record)
	}
	if removed.ID == "" {
		return InstalledRecord{}, fmt.Errorf("plugin not installed: %s", id)
	}
	if err := saveInstalled(dataDir, next); err != nil {
		return InstalledRecord{}, err
	}
	installPath := strings.TrimSpace(removed.InstallPath)
	if installPath != "" && installPathInsideCacheRoot(dataDir, installPath) {
		if err := os.RemoveAll(installPath); err != nil {
			return InstalledRecord{}, fmt.Errorf("remove plugin cache: %w", err)
		}
	}
	return removed, nil
}

func LoadInstalled(dataDir string) ([]InstalledRecord, error) {
	path := InstalledIndexPath(dataDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read installed plugins: %w", err)
	}
	var idx installedIndex
	if err := toml.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("parse installed plugins: %w", err)
	}
	records := make([]InstalledRecord, 0, len(idx.Plugins))
	for _, record := range idx.Plugins {
		record.ID = cleanPluginID(record.ID)
		if record.ID == "" {
			continue
		}
		if record.Name == "" {
			record.Name = record.ID
		}
		if record.Version == "" {
			record.Version = "0.1.0"
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

func FindInstalled(dataDir, id string) (InstalledRecord, bool, error) {
	id = cleanPluginID(id)
	records, err := LoadInstalled(dataDir)
	if err != nil {
		return InstalledRecord{}, false, err
	}
	for _, record := range records {
		if cleanPluginID(record.ID) == id {
			return record, true, nil
		}
	}
	return InstalledRecord{}, false, nil
}

func upsertInstalled(dataDir string, record InstalledRecord) ([]InstalledRecord, error) {
	records, err := LoadInstalled(dataDir)
	if err != nil {
		return nil, err
	}
	next := make([]InstalledRecord, 0, len(records)+1)
	var replaced []InstalledRecord
	for _, existing := range records {
		if cleanPluginID(existing.ID) != record.ID {
			next = append(next, existing)
			continue
		}
		replaced = append(replaced, existing)
	}
	next = append(next, record)
	if err := saveInstalled(dataDir, next); err != nil {
		return nil, err
	}
	return replaced, nil
}

func cleanupReplacedPluginCaches(dataDir, keepPath string, replaced []InstalledRecord) error {
	keepAbs, err := filepath.Abs(strings.TrimSpace(keepPath))
	if err != nil {
		return fmt.Errorf("resolve plugin cache path: %w", err)
	}
	for _, record := range replaced {
		oldPath := strings.TrimSpace(record.InstallPath)
		if oldPath == "" {
			continue
		}
		oldAbs, err := filepath.Abs(oldPath)
		if err != nil {
			return fmt.Errorf("resolve old plugin cache path: %w", err)
		}
		if oldAbs == keepAbs {
			continue
		}
		if !installPathInsideCacheRoot(dataDir, oldAbs) {
			continue
		}
		if err := os.RemoveAll(oldAbs); err != nil {
			return fmt.Errorf("remove old plugin cache: %w", err)
		}
	}
	return nil
}

func installPathInsideCacheRoot(dataDir, path string) bool {
	root, err := filepath.Abs(InstalledCacheRoot(dataDir))
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func saveInstalled(dataDir string, records []InstalledRecord) error {
	sort.Slice(records, func(i, j int) bool { return cleanPluginID(records[i].ID) < cleanPluginID(records[j].ID) })
	path := InstalledIndexPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create plugin index dir: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(installedIndex{Plugins: records}); err != nil {
		return fmt.Errorf("encode installed plugins: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write installed plugins: %w", err)
	}
	return nil
}

func validateManifest(pluginDir string, manifest Manifest) []Diagnostic {
	var diags []Diagnostic
	if manifest.ID != strings.TrimSpace(strings.ToLower(manifest.ID)) {
		diags = append(diags, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "manifest id", Detail: "id was normalized to " + manifest.ID})
	}
	for label, value := range componentPaths(manifest.Components, manifest.Display) {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if filepath.IsAbs(value) || strings.HasPrefix(filepath.Clean(value), "..") {
			diags = append(diags, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: label, Detail: "path must be relative and stay inside the plugin"})
			continue
		}
		target := filepath.Join(pluginDir, filepath.FromSlash(value))
		if _, err := os.Stat(target); err != nil {
			diags = append(diags, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: label, Detail: "path not found: " + value})
		}
	}
	return diags
}

func componentPaths(c Components, d Display) map[string]string {
	return map[string]string{
		"components.skills":   c.Skills,
		"components.commands": c.Commands,
		"components.hooks":    c.Hooks,
		"components.mcp":      c.MCP,
		"components.rules":    c.Rules,
		"components.agents":   c.Agents,
		"display.icon":        d.Icon,
	}
}

func pluginSkillRoots(pluginRoot string, components Components) []string {
	var roots []string
	if defaultRoot := filepath.Join(pluginRoot, "skills"); dirExists(defaultRoot) {
		roots = append(roots, defaultRoot)
	}
	if explicit := componentPath(pluginRoot, components.Skills); explicit != "" && dirExists(explicit) {
		roots = append(roots, explicit)
	}
	sort.Strings(roots)
	return dedupeStrings(roots)
}

func componentPath(pluginRoot, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "..") {
		return ""
	}
	return filepath.Join(pluginRoot, filepath.FromSlash(rel))
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolvePluginCommand(pluginRoot, command string) string {
	command = strings.TrimSpace(command)
	if command == "" || filepath.IsAbs(command) {
		return command
	}
	clean := filepath.Clean(filepath.FromSlash(command))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return command
	}
	if strings.HasPrefix(command, "./") || strings.HasPrefix(command, ".\\") || strings.ContainsAny(command, `/\`) {
		return filepath.Join(pluginRoot, clean)
	}
	return command
}

func addDefaultEnv(env map[string]string, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if _, ok := env[key]; !ok {
		env[key] = value
	}
}

func mergeStringLists(left, right []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func dedupeStrings(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func loadPluginCommandHooks(path, pluginRoot, pluginID string) ([]agent.ResolvedHook, error) {
	var file struct {
		Hooks map[agent.HookEvent][]agent.HookConfig `toml:"hooks"`
	}
	if _, err := toml.DecodeFile(path, &file); err != nil {
		return nil, err
	}
	settings := agent.HookSettings{Hooks: map[agent.HookEvent][]agent.HookConfig{}}
	for event, hooks := range file.Hooks {
		if agent.KnownHookEvent(event) {
			settings.Hooks[event] = append(settings.Hooks[event], hooks...)
		}
	}
	out := agent.ResolveHooks(settings, "plugin:"+pluginID)
	for i := range out {
		out[i].Managed = true
		out[i].CWD = resolvePluginHookCWD(pluginRoot, out[i].CWD)
	}
	return out, nil
}

func resolvePluginHookCWD(pluginRoot, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return pluginRoot
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	clean := filepath.Clean(filepath.FromSlash(cwd))
	if clean == "." {
		return pluginRoot
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return pluginRoot
	}
	return filepath.Join(pluginRoot, clean)
}

func copyPluginDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		if d.Name() == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin contains unsupported symlink: %s", rel)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func stageAndSwapPluginInstall(src, installPath string) (func() error, func() error, error) {
	parent := filepath.Dir(installPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, nil, fmt.Errorf("prepare plugin cache: %w", err)
	}
	stagingRoot := filepath.Join(parent, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, nil, fmt.Errorf("prepare plugin staging cache: %w", err)
	}
	staging, err := os.MkdirTemp(stagingRoot, filepath.Base(installPath)+"-")
	if err != nil {
		return nil, nil, fmt.Errorf("create plugin staging cache: %w", err)
	}
	if err := os.RemoveAll(staging); err != nil {
		return nil, nil, fmt.Errorf("prepare plugin staging cache: %w", err)
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := copyPluginDir(src, staging); err != nil {
		return nil, nil, err
	}
	backup := installPath + ".previous-" + time.Now().UTC().Format("20060102150405.000000000")
	hadExisting := false
	if _, err := os.Stat(installPath); err == nil {
		hadExisting = true
		if err := os.Rename(installPath, backup); err != nil {
			return nil, nil, fmt.Errorf("replace plugin cache: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("replace plugin cache: %w", err)
	}
	rollback := func() error {
		_ = os.RemoveAll(installPath)
		if hadExisting {
			return os.Rename(backup, installPath)
		}
		return nil
	}
	if err := os.Rename(staging, installPath); err != nil {
		_ = rollback()
		return nil, nil, fmt.Errorf("replace plugin cache: %w", err)
	}
	cleanupStaging = false
	rollbackAndCleanup := func() error {
		err := rollback()
		_ = os.RemoveAll(backup)
		return err
	}
	cleanupBackup := func() error {
		return os.RemoveAll(backup)
	}
	return rollbackAndCleanup, cleanupBackup, nil
}

func copyFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
