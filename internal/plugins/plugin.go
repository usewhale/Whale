package plugins

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/plugins/memoryplugin"
	"github.com/usewhale/whale/internal/skills"
)

type Capability string

const (
	CapabilityTools          Capability = "tools"
	CapabilitySlashCommands  Capability = "slash_commands"
	CapabilityStartupContext Capability = "startup_context"
	CapabilityStorage        Capability = "storage"
	CapabilitySkills         Capability = "skills"
	CapabilityHooks          Capability = "hooks"
	CapabilityBackgroundJobs Capability = "background_jobs"
	CapabilityLocalModel     Capability = "local_model"
)

type Permission string

const (
	PermissionReadPluginData  Permission = "read_plugin_data"
	PermissionWritePluginData Permission = "write_plugin_data"
	PermissionReadWorkspace   Permission = "read_workspace"
	PermissionWriteWorkspace  Permission = "write_workspace"
	PermissionBackgroundJobs  Permission = "background_jobs"
	PermissionLocalModel      Permission = "local_model"
)

type Manifest struct {
	ID           string
	Name         string
	Version      string
	Description  string
	Official     bool
	Capabilities []Capability
	Permissions  []Permission
	Status       string
}

type Context struct {
	DataDir       string
	WorkspaceRoot string
}

func (c Context) PluginRoot(pluginID string) string {
	return filepath.Join(c.DataDir, "plugins", cleanPluginID(pluginID))
}

func (c Context) PluginDataDir(pluginID string) string {
	return filepath.Join(c.PluginRoot(pluginID), "data")
}

func (c Context) PluginCacheDir(pluginID string) string {
	return filepath.Join(c.PluginRoot(pluginID), "cache")
}

func (c Context) PluginProjectDir(pluginID string) string {
	return filepath.Join(c.PluginRoot(pluginID), "projects", WorkspaceHash(c.WorkspaceRoot))
}

type Plugin interface {
	Manifest() Manifest
}

type ToolProvider interface {
	Tools(Context) []core.Tool
}

type StartupContextProvider interface {
	StartupContext(context.Context, Context) (string, error)
}

type SlashCommandProvider interface {
	SlashCommands(Context) []SlashCommand
}

type SkillProvider interface {
	Skills(Context) []*skills.Skill
}

type HookProvider interface {
	Hooks(Context) []agent.HookHandler
}

type ServiceProvider interface {
	Services(Context) []ServiceStatus
}

type DoctorProvider interface {
	Doctor(context.Context, Context) []Diagnostic
}

type CommandClass string

const (
	CommandReadOnly     CommandClass = "read_only"
	CommandMutating     CommandClass = "mutating"
	CommandUI           CommandClass = "ui"
	CommandTurnStarting CommandClass = "turn"
)

type SlashCommand struct {
	Name        string
	Usage       string
	Description string
	Class       CommandClass
	Classify    func(line string) CommandClass
	Run         func(context.Context, Context, string) (CommandResult, error)
}

type CommandResult struct {
	Text    string
	Mutated bool
	Turn    *CommandTurn
}

type CommandTurn struct {
	Input               string
	Hidden              bool
	ReadOnly            bool
	SkipUserPromptHooks bool
	SkipSkillInjection  bool
	ShellAllowPrefixes  []string
}

type ServiceStatus struct {
	Name   string
	Status string
	Detail string
}

type DiagnosticLevel string

const (
	DiagnosticOK   DiagnosticLevel = "ok"
	DiagnosticWarn DiagnosticLevel = "warn"
	DiagnosticFail DiagnosticLevel = "fail"
)

type Diagnostic struct {
	PluginID string
	Level    DiagnosticLevel
	Label    string
	Detail   string
}

type PluginStatus struct {
	Manifest    Manifest
	Enabled     bool
	Commands    []SlashCommand
	Tools       []string
	Skills      []string
	Hooks       []string
	Services    []ServiceStatus
	Diagnostics []Diagnostic
	Paths       map[string]string
}

type Manager struct {
	ctx      Context
	enabled  []Plugin
	disabled []Plugin
	byID     map[string]Plugin
	commands map[string]registeredCommand
	diag     []Diagnostic
}

type registeredCommand struct {
	pluginID string
	command  SlashCommand
}

func NewManager(ctx Context, disabled []string) *Manager {
	disabledSet := map[string]bool{}
	for _, id := range disabled {
		id = cleanPluginID(id)
		if id != "" {
			disabledSet[id] = true
		}
	}
	m := &Manager{
		ctx:      ctx,
		byID:     map[string]Plugin{},
		commands: map[string]registeredCommand{},
	}
	for _, p := range builtins() {
		manifest := normalizeManifest(p.Manifest())
		if manifest.ID == "" {
			continue
		}
		if disabledSet[manifest.ID] {
			m.disabled = append(m.disabled, p)
			continue
		}
		m.enabled = append(m.enabled, p)
		m.byID[manifest.ID] = p
	}
	sort.Slice(m.enabled, func(i, j int) bool {
		return normalizeManifest(m.enabled[i].Manifest()).ID < normalizeManifest(m.enabled[j].Manifest()).ID
	})
	sort.Slice(m.disabled, func(i, j int) bool {
		return normalizeManifest(m.disabled[i].Manifest()).ID < normalizeManifest(m.disabled[j].Manifest()).ID
	})
	m.collectCommands()
	return m
}

func (m *Manager) collectCommands() {
	if m == nil {
		return
	}
	for _, p := range m.enabled {
		manifest := normalizeManifest(p.Manifest())
		provider, ok := p.(SlashCommandProvider)
		if !ok {
			continue
		}
		for _, cmd := range provider.SlashCommands(m.ctx) {
			cmd.Name = strings.TrimSpace(cmd.Name)
			if cmd.Name == "" || !strings.HasPrefix(cmd.Name, "/") {
				m.diag = append(m.diag, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "command", Detail: "ignored invalid slash command"})
				continue
			}
			if _, exists := m.commands[cmd.Name]; exists {
				m.diag = append(m.diag, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "command conflict", Detail: cmd.Name + " already registered"})
				continue
			}
			m.commands[cmd.Name] = registeredCommand{pluginID: manifest.ID, command: cmd}
		}
	}
}

func (m *Manager) Tools() []core.Tool {
	if m == nil {
		return nil
	}
	var out []core.Tool
	seen := map[string]string{}
	for _, p := range m.enabled {
		manifest := normalizeManifest(p.Manifest())
		provider, ok := p.(ToolProvider)
		if !ok {
			continue
		}
		for _, tool := range provider.Tools(m.ctx) {
			if tool == nil || strings.TrimSpace(tool.Name()) == "" {
				continue
			}
			if owner := seen[tool.Name()]; owner != "" {
				m.diag = append(m.diag, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "tool conflict", Detail: fmt.Sprintf("%s already provided by %s", tool.Name(), owner)})
				continue
			}
			seen[tool.Name()] = manifest.ID
			out = append(out, tool)
		}
	}
	return out
}

func (m *Manager) StartupBlocks(ctx context.Context) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, p := range m.enabled {
		provider, ok := p.(StartupContextProvider)
		if !ok {
			continue
		}
		manifest := normalizeManifest(p.Manifest())
		block, err := provider.StartupContext(ctx, m.ctx)
		if err != nil {
			m.diag = append(m.diag, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "startup context", Detail: err.Error()})
			continue
		}
		if trimmed := strings.TrimSpace(block); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (m *Manager) Skills() []*skills.Skill {
	if m == nil {
		return nil
	}
	var out []*skills.Skill
	for _, p := range m.enabled {
		provider, ok := p.(SkillProvider)
		if !ok {
			continue
		}
		out = append(out, provider.Skills(m.ctx)...)
	}
	return skills.Sort(skills.Deduplicate(out))
}

func (m *Manager) Hooks() []agent.HookHandler {
	if m == nil {
		return nil
	}
	var out []agent.HookHandler
	for _, p := range m.enabled {
		provider, ok := p.(HookProvider)
		if !ok {
			continue
		}
		manifest := normalizeManifest(p.Manifest())
		for _, hook := range provider.Hooks(m.ctx) {
			if hook.Source == "" {
				hook.Source = "plugin:" + manifest.ID
			}
			out = append(out, hook)
		}
	}
	return out
}

func (m *Manager) SlashCommands() []SlashCommand {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.commands))
	for name := range m.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SlashCommand, 0, len(names))
	for _, name := range names {
		out = append(out, m.commands[name].command)
	}
	return out
}

func (m *Manager) SlashCommandNames() []string {
	commands := m.SlashCommands()
	out := make([]string, 0, len(commands))
	for _, cmd := range commands {
		out = append(out, cmd.Name)
	}
	return out
}

func (m *Manager) CommandClass(name string) (CommandClass, bool) {
	if m == nil {
		return "", false
	}
	cmd, ok := m.commands[strings.TrimSpace(name)]
	if !ok {
		return "", false
	}
	return cmd.command.Class, true
}

func (m *Manager) CommandClassForLine(line string) (CommandClass, bool) {
	if m == nil {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return "", false
	}
	reg, ok := m.commands[fields[0]]
	if !ok {
		return "", false
	}
	if reg.command.Classify != nil {
		return reg.command.Classify(line), true
	}
	return reg.command.Class, true
}

func (m *Manager) HandleCommand(ctx context.Context, line string) (CommandResult, bool, error) {
	if m == nil {
		return CommandResult{}, false, nil
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return CommandResult{}, false, nil
	}
	reg, ok := m.commands[fields[0]]
	if !ok {
		return CommandResult{}, false, nil
	}
	if reg.command.Run == nil {
		return CommandResult{}, true, fmt.Errorf("plugin command not implemented: %s", fields[0])
	}
	res, err := reg.command.Run(ctx, m.ctx, line)
	return res, true, err
}

func BuiltinSlashCommandClass(line string) (CommandClass, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return "", false
	}
	for _, p := range builtins() {
		provider, ok := p.(SlashCommandProvider)
		if !ok {
			continue
		}
		for _, cmd := range provider.SlashCommands(Context{}) {
			if cmd.Name != fields[0] {
				continue
			}
			if cmd.Classify != nil {
				return cmd.Classify(line), true
			}
			return cmd.Class, true
		}
	}
	return "", false
}

func (m *Manager) EnabledIDs() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.enabled))
	for _, p := range m.enabled {
		out = append(out, normalizeManifest(p.Manifest()).ID)
	}
	return out
}

func (m *Manager) Status(id string) (PluginStatus, bool) {
	if m == nil {
		return PluginStatus{}, false
	}
	id = cleanPluginID(id)
	if id == "" {
		return PluginStatus{}, false
	}
	if p := m.byID[id]; p != nil {
		return m.statusFor(p, true), true
	}
	for _, p := range m.disabled {
		manifest := normalizeManifest(p.Manifest())
		if manifest.ID == id {
			return m.statusFor(p, false), true
		}
	}
	return PluginStatus{}, false
}

func (m *Manager) Statuses() []PluginStatus {
	if m == nil {
		return nil
	}
	out := make([]PluginStatus, 0, len(m.enabled)+len(m.disabled))
	for _, p := range m.enabled {
		out = append(out, m.statusFor(p, true))
	}
	for _, p := range m.disabled {
		out = append(out, m.statusFor(p, false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

func (m *Manager) Diagnostics(ctx context.Context) []Diagnostic {
	if m == nil {
		return nil
	}
	out := append([]Diagnostic{}, m.diag...)
	for _, p := range m.enabled {
		if provider, ok := p.(DoctorProvider); ok {
			out = append(out, provider.Doctor(ctx, m.ctx)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PluginID == out[j].PluginID {
			return out[i].Label < out[j].Label
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

func (m *Manager) statusFor(p Plugin, enabled bool) PluginStatus {
	manifest := normalizeManifest(p.Manifest())
	status := PluginStatus{Manifest: manifest, Enabled: enabled, Paths: m.pathsFor(manifest.ID)}
	if provider, ok := p.(SlashCommandProvider); ok {
		status.Commands = provider.SlashCommands(m.ctx)
	}
	if provider, ok := p.(ToolProvider); ok {
		for _, tool := range provider.Tools(m.ctx) {
			if tool != nil {
				status.Tools = append(status.Tools, tool.Name())
			}
		}
		sort.Strings(status.Tools)
	}
	if provider, ok := p.(SkillProvider); ok {
		for _, skill := range provider.Skills(m.ctx) {
			if skill != nil {
				status.Skills = append(status.Skills, skill.Name)
			}
		}
		sort.Strings(status.Skills)
	}
	if provider, ok := p.(HookProvider); ok {
		for _, hook := range provider.Hooks(m.ctx) {
			if strings.TrimSpace(hook.Name) != "" {
				status.Hooks = append(status.Hooks, strings.TrimSpace(hook.Name))
			}
		}
		sort.Strings(status.Hooks)
	}
	if provider, ok := p.(ServiceProvider); ok {
		status.Services = provider.Services(m.ctx)
	}
	if provider, ok := p.(DoctorProvider); ok {
		status.Diagnostics = provider.Doctor(context.Background(), m.ctx)
	}
	return status
}

func (m *Manager) pathsFor(pluginID string) map[string]string {
	return map[string]string{
		"root":    m.ctx.PluginRoot(pluginID),
		"data":    m.ctx.PluginDataDir(pluginID),
		"cache":   m.ctx.PluginCacheDir(pluginID),
		"project": m.ctx.PluginProjectDir(pluginID),
	}
}

func builtins() []Plugin {
	return []Plugin{
		memoryPlugin{},
	}
}

func normalizeManifest(in Manifest) Manifest {
	in.ID = cleanPluginID(in.ID)
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = in.ID
	}
	in.Version = strings.TrimSpace(in.Version)
	if in.Version == "" {
		in.Version = "0.1.0"
	}
	in.Description = strings.TrimSpace(in.Description)
	in.Status = strings.TrimSpace(in.Status)
	if in.Status == "" {
		in.Status = "ready"
	}
	return in
}

func cleanPluginID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.ReplaceAll(id, "_", "-")
	return id
}

func WorkspaceHash(workspaceRoot string) string {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "no-workspace"
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	sum := sha1.Sum([]byte(root))
	return hex.EncodeToString(sum[:])[:16]
}

type memoryPlugin struct{}

func (memoryPlugin) Manifest() Manifest {
	return Manifest{
		ID:          memoryplugin.PluginID,
		Name:        "Memory",
		Version:     "0.1.0",
		Description: "Durable global and project memory across sessions.",
		Official:    true,
		Capabilities: []Capability{
			CapabilityTools,
			CapabilitySlashCommands,
			CapabilityStartupContext,
			CapabilityStorage,
		},
		Permissions: []Permission{PermissionReadPluginData, PermissionWritePluginData},
	}
}

func (memoryPlugin) store(ctx Context) *memoryplugin.Store {
	return memoryplugin.NewStore(ctx.PluginRoot(memoryplugin.PluginID), ctx.WorkspaceRoot)
}

func (p memoryPlugin) Tools(ctx Context) []core.Tool {
	return memoryplugin.Tools(p.store(ctx))
}

func (p memoryPlugin) StartupContext(c context.Context, ctx Context) (string, error) {
	return memoryplugin.StartupContext(c, p.store(ctx))
}

func (p memoryPlugin) SlashCommands(ctx Context) []SlashCommand {
	return []SlashCommand{
		{
			Name:        "/memory",
			Usage:       "/memory [list|path|show <global|project>/<name>|forget <global|project>/<name>]",
			Description: "List, inspect, or remove memories.",
			Class:       CommandReadOnly,
			Classify:    classifyMemoryCommand,
			Run: func(_ context.Context, c Context, line string) (CommandResult, error) {
				out, _, err := memoryplugin.HandleCommand(p.store(c), line)
				return CommandResult{Text: out, Mutated: memoryForgetDeleted(line, out, err)}, err
			},
		},
	}
}

func classifyMemoryCommand(line string) CommandClass {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 1 {
		return CommandReadOnly
	}
	if len(fields) == 2 && (fields[1] == "list" || fields[1] == "path") {
		return CommandReadOnly
	}
	if len(fields) == 3 && fields[1] == "show" {
		return CommandReadOnly
	}
	if len(fields) == 3 && fields[1] == "forget" {
		return CommandMutating
	}
	return ""
}

func memoryForgetDeleted(line, out string, err error) bool {
	if err != nil {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) == 3 && fields[0] == "/memory" && fields[1] == "forget" && strings.HasPrefix(strings.TrimSpace(out), "forgot memory:")
}

func (p memoryPlugin) Doctor(_ context.Context, ctx Context) []Diagnostic {
	store := p.store(ctx)
	if strings.TrimSpace(store.Root()) == "" {
		return []Diagnostic{{PluginID: memoryplugin.PluginID, Level: DiagnosticFail, Label: "storage", Detail: "memory data root is empty"}}
	}
	return []Diagnostic{{PluginID: memoryplugin.PluginID, Level: DiagnosticOK, Label: "storage", Detail: store.Root()}}
}

type localIndexerPlugin struct{}

func (localIndexerPlugin) Manifest() Manifest {
	return Manifest{
		ID:          "local-indexer",
		Name:        "Local Indexer",
		Version:     "0.1.0",
		Description: "Scaffold for local model and indexing services used by official plugins.",
		Official:    true,
		Capabilities: []Capability{
			CapabilitySlashCommands,
			CapabilityStorage,
			CapabilityHooks,
			CapabilityBackgroundJobs,
			CapabilityLocalModel,
		},
		Permissions: []Permission{PermissionReadPluginData, PermissionWritePluginData, PermissionBackgroundJobs, PermissionLocalModel},
		Status:      "scaffold",
	}
}

func (localIndexerPlugin) SlashCommands(ctx Context) []SlashCommand {
	return []SlashCommand{
		{
			Name:        "/local-indexer",
			Usage:       "/local-indexer [status|rebuild]",
			Description: "Inspect the local-indexer official plugin scaffold.",
			Class:       CommandReadOnly,
			Classify:    classifyLocalIndexerCommand,
			Run: func(_ context.Context, _ Context, line string) (CommandResult, error) {
				fields := strings.Fields(strings.TrimSpace(line))
				if len(fields) == 1 || (len(fields) == 2 && fields[1] == "status") {
					return CommandResult{Text: "local-indexer\n\nstatus: scaffold\nmodel: not installed\njobs: none"}, nil
				}
				if len(fields) == 2 && fields[1] == "rebuild" {
					return CommandResult{Text: "local-indexer rebuild\n\nnot implemented in this build"}, nil
				}
				return CommandResult{}, fmt.Errorf("usage: /local-indexer [status|rebuild]")
			},
		},
	}
}

func classifyLocalIndexerCommand(line string) CommandClass {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 1 {
		return CommandReadOnly
	}
	if len(fields) == 2 && (fields[1] == "status" || fields[1] == "rebuild") {
		return CommandReadOnly
	}
	return ""
}

func (localIndexerPlugin) Services(Context) []ServiceStatus {
	return []ServiceStatus{{Name: "indexer", Status: "stopped", Detail: "scaffold only"}}
}

func (localIndexerPlugin) Hooks(Context) []agent.HookHandler {
	return []agent.HookHandler{{
		Event:       agent.HookEventStop,
		Name:        "local-indexer.schedule-idle-index",
		Source:      "plugin:local-indexer",
		Description: "Placeholder for scheduling idle indexing after a completed turn.",
		Run: func(context.Context, agent.HookPayload) agent.HookResult {
			return agent.HookResult{Decision: agent.HookDecisionPass}
		},
	}}
}

func (localIndexerPlugin) Doctor(_ context.Context, _ Context) []Diagnostic {
	return []Diagnostic{{PluginID: "local-indexer", Level: DiagnosticWarn, Label: "implementation", Detail: "scaffold only; local inference and indexing are not implemented"}}
}
