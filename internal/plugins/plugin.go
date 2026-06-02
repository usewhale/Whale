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
	whalemcp "github.com/usewhale/whale/internal/mcp"
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
	Authors      []string
	License      string
	Homepage     string
	Repository   string
	Keywords     []string
	Components   Components
	Display      Display
	Capabilities []Capability
	Permissions  []Permission
	Status       string
}

type Components struct {
	Skills   string `toml:"skills,omitempty"`
	Commands string `toml:"commands,omitempty"`
	Hooks    string `toml:"hooks,omitempty"`
	MCP      string `toml:"mcp,omitempty"`
	Rules    string `toml:"rules,omitempty"`
	Agents   string `toml:"agents,omitempty"`
}

type Display struct {
	Category string `toml:"category,omitempty"`
	Icon     string `toml:"icon,omitempty"`
}

type Context struct {
	DataDir       string
	WorkspaceRoot string
}

type Config struct {
	Enabled    *bool
	MCPServers map[string]MCPServerConfig
}

type ConfigMap map[string]Config

type MCPServerConfig struct {
	Enabled       *bool    `toml:"enabled,omitempty"`
	DisabledTools []string `toml:"disabled_tools,omitempty"`
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

type PathProvider interface {
	Paths(Context) map[string]string
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

type CommandHookProvider interface {
	CommandHooks(Context) []agent.ResolvedHook
}

type MCPProvider interface {
	MCPServers(Context) map[string]whalemcp.ServerConfig
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
	StartsTurn  bool
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
	GoalContinuation    bool
	SkipUserPromptHooks bool
	SkipSkillInjection  bool
	ShellAllowPrefixes  []string
}

type AgentDefinition struct {
	Name            string
	Description     string
	SystemPrompt    string
	Model           string
	Effort          string
	MaxToolIters    int
	MaxToolCalls    int
	Capabilities    []string
	AllowedTools    []string
	DisallowedTools []string
	Source          string
	FilePath        string
}

type RuleBlock struct {
	Name     string
	Content  string
	Source   string
	FilePath string
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
	Agents      []string
	Rules       []string
	Hooks       []string
	Services    []ServiceStatus
	Diagnostics []Diagnostic
	Paths       map[string]string
}

type LoadedPlugin struct {
	Manifest     Manifest
	Enabled      bool
	Paths        map[string]string
	Commands     []SlashCommand
	Tools        []core.Tool
	ToolNames    []string
	Skills       []*skills.Skill
	Agents       []AgentDefinition
	Rules        []RuleBlock
	MCPServers   map[string]whalemcp.ServerConfig
	CommandHooks []agent.ResolvedHook
	HookHandlers []agent.HookHandler
	Services     []ServiceStatus
	Diagnostics  []Diagnostic
}

type LoadOutcome struct {
	Plugins      []LoadedPlugin
	Statuses     []PluginStatus
	Commands     []SlashCommand
	Tools        []core.Tool
	Skills       []*skills.Skill
	Agents       []AgentDefinition
	Rules        []RuleBlock
	MCPServers   map[string]whalemcp.ServerConfig
	CommandHooks []agent.ResolvedHook
	HookHandlers []agent.HookHandler
	Diagnostics  []Diagnostic
}

type Manager struct {
	ctx       Context
	enabled   []Plugin
	disabled  []Plugin
	installed []Plugin
	byID      map[string]Plugin
	commands  map[string]registeredCommand
	diag      []Diagnostic
	outcome   LoadOutcome
}

type registeredCommand struct {
	pluginID string
	command  SlashCommand
}

func NewManager(ctx Context, config ConfigMap) *Manager {
	config = normalizeConfigMap(config)
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
		m.byID[manifest.ID] = p
		enabled := true
		if cfg, ok := config[manifest.ID]; ok && cfg.Enabled != nil {
			enabled = *cfg.Enabled
		}
		if !enabled {
			m.disabled = append(m.disabled, p)
			continue
		}
		m.enabled = append(m.enabled, p)
	}
	records, err := LoadInstalled(ctx.DataDir)
	if err != nil {
		m.diag = append(m.diag, Diagnostic{Level: DiagnosticWarn, Label: "installed plugins", Detail: err.Error()})
	} else {
		builtinIDs := builtinIDSet()
		for _, record := range records {
			manifest := normalizeManifest(record.Manifest())
			if manifest.ID == "" || builtinIDs[manifest.ID] {
				continue
			}
			p := installedPlugin{record: record, config: config[manifest.ID]}
			m.byID[manifest.ID] = p
			enabled := false
			if cfg, ok := config[manifest.ID]; ok && cfg.Enabled != nil {
				enabled = *cfg.Enabled
			}
			if enabled {
				m.enabled = append(m.enabled, p)
			} else {
				m.installed = append(m.installed, p)
			}
		}
	}
	sort.Slice(m.enabled, func(i, j int) bool {
		return normalizeManifest(m.enabled[i].Manifest()).ID < normalizeManifest(m.enabled[j].Manifest()).ID
	})
	sort.Slice(m.disabled, func(i, j int) bool {
		return normalizeManifest(m.disabled[i].Manifest()).ID < normalizeManifest(m.disabled[j].Manifest()).ID
	})
	sort.Slice(m.installed, func(i, j int) bool {
		return normalizeManifest(m.installed[i].Manifest()).ID < normalizeManifest(m.installed[j].Manifest()).ID
	})
	m.buildOutcome()
	return m
}

func (m *Manager) buildOutcome() {
	if m == nil {
		return
	}
	m.commands = map[string]registeredCommand{}
	outcome := LoadOutcome{
		MCPServers:  map[string]whalemcp.ServerConfig{},
		Diagnostics: append([]Diagnostic(nil), m.diag...),
	}
	toolOwners := map[string]string{}
	for _, p := range m.enabled {
		loaded := m.loadPlugin(p, true)
		manifest := loaded.Manifest
		for _, cmd := range loaded.Commands {
			cmd.Name = strings.TrimSpace(cmd.Name)
			if cmd.Name == "" || !strings.HasPrefix(cmd.Name, "/") {
				outcome.Diagnostics = append(outcome.Diagnostics, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "command", Detail: "ignored invalid slash command"})
				continue
			}
			if _, exists := m.commands[cmd.Name]; exists {
				outcome.Diagnostics = append(outcome.Diagnostics, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "command conflict", Detail: cmd.Name + " already registered"})
				continue
			}
			m.commands[cmd.Name] = registeredCommand{pluginID: manifest.ID, command: cmd}
			outcome.Commands = append(outcome.Commands, cmd)
		}
		for _, tool := range loaded.Tools {
			if tool == nil || strings.TrimSpace(tool.Name()) == "" {
				continue
			}
			if owner := toolOwners[tool.Name()]; owner != "" {
				outcome.Diagnostics = append(outcome.Diagnostics, Diagnostic{PluginID: manifest.ID, Level: DiagnosticWarn, Label: "tool conflict", Detail: fmt.Sprintf("%s already provided by %s", tool.Name(), owner)})
				continue
			}
			toolOwners[tool.Name()] = manifest.ID
			outcome.Tools = append(outcome.Tools, tool)
		}
		outcome.Skills = append(outcome.Skills, loaded.Skills...)
		outcome.Agents = append(outcome.Agents, loaded.Agents...)
		outcome.Rules = append(outcome.Rules, loaded.Rules...)
		for name, srv := range loaded.MCPServers {
			outcome.MCPServers[name] = srv
		}
		outcome.CommandHooks = append(outcome.CommandHooks, loaded.CommandHooks...)
		outcome.HookHandlers = append(outcome.HookHandlers, loaded.HookHandlers...)
		outcome.Diagnostics = append(outcome.Diagnostics, loaded.Diagnostics...)
		outcome.Plugins = append(outcome.Plugins, cloneLoadedPlugin(loaded))
	}
	for _, p := range m.disabled {
		outcome.Plugins = append(outcome.Plugins, cloneLoadedPlugin(m.loadPlugin(p, false)))
	}
	for _, p := range m.installed {
		outcome.Plugins = append(outcome.Plugins, cloneLoadedPlugin(m.loadPlugin(p, false)))
	}
	outcome.Skills = skills.Sort(skills.Deduplicate(outcome.Skills))
	outcome.Statuses = make([]PluginStatus, 0, len(outcome.Plugins))
	for _, loaded := range outcome.Plugins {
		outcome.Statuses = append(outcome.Statuses, loadedPluginStatus(loaded))
	}
	sort.Slice(outcome.Statuses, func(i, j int) bool { return outcome.Statuses[i].Manifest.ID < outcome.Statuses[j].Manifest.ID })
	sort.Slice(outcome.Diagnostics, func(i, j int) bool {
		if outcome.Diagnostics[i].PluginID == outcome.Diagnostics[j].PluginID {
			return outcome.Diagnostics[i].Label < outcome.Diagnostics[j].Label
		}
		return outcome.Diagnostics[i].PluginID < outcome.Diagnostics[j].PluginID
	})
	m.diag = append([]Diagnostic(nil), outcome.Diagnostics...)
	m.outcome = cloneLoadOutcome(outcome)
}

func (m *Manager) loadPlugin(p Plugin, enabled bool) LoadedPlugin {
	manifest := normalizeManifest(p.Manifest())
	loaded := LoadedPlugin{
		Manifest:   manifest,
		Enabled:    enabled,
		Paths:      m.pathsFor(manifest.ID),
		MCPServers: map[string]whalemcp.ServerConfig{},
	}
	if provider, ok := p.(PathProvider); ok {
		loaded.Paths = provider.Paths(m.ctx)
	}
	if installed, ok := p.(installedPlugin); ok {
		runtime := installed.loadRuntime(m.ctx)
		loaded.Commands = append(loaded.Commands, runtime.commands...)
		loaded.Skills = runtime.skills
		loaded.Agents = append(loaded.Agents, runtime.agents...)
		loaded.Rules = append(loaded.Rules, runtime.rules...)
		for name, server := range runtime.mcpServers {
			name = cleanPluginComponentName(name)
			if name == "" {
				continue
			}
			namespaced := manifest.ID + "." + name
			server.Name = namespaced
			loaded.MCPServers[namespaced] = server
			loaded.Services = append(loaded.Services, ServiceStatus{Name: "mcp:" + name, Status: "configured"})
		}
		for _, hook := range runtime.commandHooks {
			if hook.Event == "" || strings.TrimSpace(hook.Command) == "" {
				continue
			}
			if hook.Source == "" {
				hook.Source = "plugin:" + manifest.ID
			}
			hook.Managed = true
			loaded.CommandHooks = append(loaded.CommandHooks, hook)
		}
		loaded.Diagnostics = append(loaded.Diagnostics, installed.record.Diagnostics...)
		loaded.Diagnostics = append(loaded.Diagnostics, runtime.diagnostics...)
		sort.Slice(loaded.Services, func(i, j int) bool { return loaded.Services[i].Name < loaded.Services[j].Name })
		return loaded
	}
	if provider, ok := p.(SlashCommandProvider); ok {
		loaded.Commands = provider.SlashCommands(m.ctx)
	}
	if provider, ok := p.(ToolProvider); ok {
		loaded.Tools = append([]core.Tool(nil), provider.Tools(m.ctx)...)
		for _, tool := range loaded.Tools {
			if tool != nil && strings.TrimSpace(tool.Name()) != "" {
				loaded.ToolNames = append(loaded.ToolNames, tool.Name())
			}
		}
		sort.Strings(loaded.ToolNames)
	}
	if provider, ok := p.(SkillProvider); ok {
		loaded.Skills = skills.Sort(skills.Deduplicate(provider.Skills(m.ctx)))
	}
	if provider, ok := p.(MCPProvider); ok {
		for name, server := range provider.MCPServers(m.ctx) {
			name = cleanPluginComponentName(name)
			if name == "" {
				continue
			}
			namespaced := manifest.ID + "." + name
			server.Name = namespaced
			loaded.MCPServers[namespaced] = server
			loaded.Services = append(loaded.Services, ServiceStatus{Name: "mcp:" + name, Status: "configured"})
		}
	}
	if provider, ok := p.(CommandHookProvider); ok {
		for _, hook := range provider.CommandHooks(m.ctx) {
			if hook.Event == "" || strings.TrimSpace(hook.Command) == "" {
				continue
			}
			if hook.Source == "" {
				hook.Source = "plugin:" + manifest.ID
			}
			hook.Managed = true
			loaded.CommandHooks = append(loaded.CommandHooks, hook)
		}
	}
	if provider, ok := p.(HookProvider); ok {
		for _, hook := range provider.Hooks(m.ctx) {
			if hook.Source == "" {
				hook.Source = "plugin:" + manifest.ID
			}
			loaded.HookHandlers = append(loaded.HookHandlers, hook)
		}
	}
	if provider, ok := p.(ServiceProvider); ok {
		loaded.Services = provider.Services(m.ctx)
	}
	if provider, ok := p.(DoctorProvider); ok {
		loaded.Diagnostics = append(loaded.Diagnostics, provider.Doctor(context.Background(), m.ctx)...)
	}
	sort.Slice(loaded.Services, func(i, j int) bool { return loaded.Services[i].Name < loaded.Services[j].Name })
	return loaded
}

func (m *Manager) Tools() []core.Tool {
	if m == nil {
		return nil
	}
	return append([]core.Tool(nil), m.outcome.Tools...)
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
	for _, rule := range m.outcome.Rules {
		if trimmed := strings.TrimSpace(rule.Content); trimmed != "" {
			name := strings.TrimSpace(rule.Name)
			if name == "" {
				name = "plugin rule"
			}
			out = append(out, fmt.Sprintf("Plugin rule %s:\n%s", name, trimmed))
		}
	}
	return out
}

func (m *Manager) Skills() []*skills.Skill {
	if m == nil {
		return nil
	}
	return cloneSkills(m.outcome.Skills)
}

func (m *Manager) MCPServers() map[string]whalemcp.ServerConfig {
	if m == nil {
		return nil
	}
	return cloneMCPServers(m.outcome.MCPServers)
}

func (m *Manager) CommandHooks() []agent.ResolvedHook {
	if m == nil {
		return nil
	}
	return append([]agent.ResolvedHook(nil), m.outcome.CommandHooks...)
}

func (m *Manager) Hooks() []agent.HookHandler {
	if m == nil {
		return nil
	}
	return append([]agent.HookHandler(nil), m.outcome.HookHandlers...)
}

func (m *Manager) SlashCommands() []SlashCommand {
	if m == nil {
		return nil
	}
	return append([]SlashCommand(nil), m.outcome.Commands...)
}

func (m *Manager) Agents() []AgentDefinition {
	if m == nil {
		return nil
	}
	return cloneAgentDefinitions(m.outcome.Agents)
}

func (m *Manager) Rules() []RuleBlock {
	if m == nil {
		return nil
	}
	return cloneRuleBlocks(m.outcome.Rules)
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
	for _, status := range m.outcome.Statuses {
		if status.Manifest.ID == id {
			return clonePluginStatus(status), true
		}
	}
	return PluginStatus{}, false
}

func (m *Manager) Statuses() []PluginStatus {
	if m == nil {
		return nil
	}
	return clonePluginStatuses(m.outcome.Statuses)
}

func (m *Manager) Diagnostics(ctx context.Context) []Diagnostic {
	if m == nil {
		return nil
	}
	_ = ctx
	return append([]Diagnostic(nil), m.outcome.Diagnostics...)
}

func (m *Manager) Outcome() LoadOutcome {
	if m == nil {
		return LoadOutcome{}
	}
	return cloneLoadOutcome(m.outcome)
}

func (m *Manager) pathsFor(pluginID string) map[string]string {
	return map[string]string{
		"root":    m.ctx.PluginRoot(pluginID),
		"data":    m.ctx.PluginDataDir(pluginID),
		"cache":   m.ctx.PluginCacheDir(pluginID),
		"project": m.ctx.PluginProjectDir(pluginID),
	}
}

func loadedPluginStatus(loaded LoadedPlugin) PluginStatus {
	status := PluginStatus{
		Manifest:    cloneManifest(loaded.Manifest),
		Enabled:     loaded.Enabled,
		Commands:    append([]SlashCommand(nil), loaded.Commands...),
		Tools:       append([]string(nil), loaded.ToolNames...),
		Services:    append([]ServiceStatus(nil), loaded.Services...),
		Diagnostics: append([]Diagnostic(nil), loaded.Diagnostics...),
		Paths:       cloneStringMap(loaded.Paths),
	}
	for _, skill := range loaded.Skills {
		if skill != nil {
			status.Skills = append(status.Skills, skill.Name)
		}
	}
	for _, agent := range loaded.Agents {
		if strings.TrimSpace(agent.Name) != "" {
			status.Agents = append(status.Agents, strings.TrimSpace(agent.Name))
		}
	}
	for _, rule := range loaded.Rules {
		if strings.TrimSpace(rule.Name) != "" {
			status.Rules = append(status.Rules, strings.TrimSpace(rule.Name))
		}
	}
	for _, hook := range loaded.CommandHooks {
		if strings.TrimSpace(hook.Description) != "" {
			status.Hooks = append(status.Hooks, strings.TrimSpace(hook.Description))
		} else if strings.TrimSpace(hook.Command) != "" {
			status.Hooks = append(status.Hooks, strings.TrimSpace(hook.Command))
		}
	}
	for _, hook := range loaded.HookHandlers {
		if strings.TrimSpace(hook.Name) != "" {
			status.Hooks = append(status.Hooks, strings.TrimSpace(hook.Name))
		}
	}
	sort.Strings(status.Skills)
	sort.Strings(status.Agents)
	sort.Strings(status.Rules)
	sort.Strings(status.Hooks)
	sort.Slice(status.Services, func(i, j int) bool { return status.Services[i].Name < status.Services[j].Name })
	return status
}

func cloneLoadOutcome(in LoadOutcome) LoadOutcome {
	out := LoadOutcome{
		Plugins:      make([]LoadedPlugin, 0, len(in.Plugins)),
		Statuses:     clonePluginStatuses(in.Statuses),
		Commands:     append([]SlashCommand(nil), in.Commands...),
		Tools:        append([]core.Tool(nil), in.Tools...),
		Skills:       cloneSkills(in.Skills),
		Agents:       cloneAgentDefinitions(in.Agents),
		Rules:        cloneRuleBlocks(in.Rules),
		MCPServers:   cloneMCPServers(in.MCPServers),
		CommandHooks: append([]agent.ResolvedHook(nil), in.CommandHooks...),
		HookHandlers: append([]agent.HookHandler(nil), in.HookHandlers...),
		Diagnostics:  append([]Diagnostic(nil), in.Diagnostics...),
	}
	for _, plugin := range in.Plugins {
		out.Plugins = append(out.Plugins, cloneLoadedPlugin(plugin))
	}
	return out
}

func cloneLoadedPlugin(in LoadedPlugin) LoadedPlugin {
	return LoadedPlugin{
		Manifest:     cloneManifest(in.Manifest),
		Enabled:      in.Enabled,
		Paths:        cloneStringMap(in.Paths),
		Commands:     append([]SlashCommand(nil), in.Commands...),
		Tools:        append([]core.Tool(nil), in.Tools...),
		ToolNames:    append([]string(nil), in.ToolNames...),
		Skills:       cloneSkills(in.Skills),
		Agents:       cloneAgentDefinitions(in.Agents),
		Rules:        cloneRuleBlocks(in.Rules),
		MCPServers:   cloneMCPServers(in.MCPServers),
		CommandHooks: append([]agent.ResolvedHook(nil), in.CommandHooks...),
		HookHandlers: append([]agent.HookHandler(nil), in.HookHandlers...),
		Services:     append([]ServiceStatus(nil), in.Services...),
		Diagnostics:  append([]Diagnostic(nil), in.Diagnostics...),
	}
}

func cloneManifest(in Manifest) Manifest {
	out := in
	out.Authors = append([]string(nil), in.Authors...)
	out.Keywords = append([]string(nil), in.Keywords...)
	out.Capabilities = append([]Capability(nil), in.Capabilities...)
	out.Permissions = append([]Permission(nil), in.Permissions...)
	return out
}

func clonePluginStatuses(in []PluginStatus) []PluginStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]PluginStatus, 0, len(in))
	for _, status := range in {
		out = append(out, clonePluginStatus(status))
	}
	return out
}

func clonePluginStatus(in PluginStatus) PluginStatus {
	return PluginStatus{
		Manifest:    cloneManifest(in.Manifest),
		Enabled:     in.Enabled,
		Commands:    append([]SlashCommand(nil), in.Commands...),
		Tools:       append([]string(nil), in.Tools...),
		Skills:      append([]string(nil), in.Skills...),
		Agents:      append([]string(nil), in.Agents...),
		Rules:       append([]string(nil), in.Rules...),
		Hooks:       append([]string(nil), in.Hooks...),
		Services:    append([]ServiceStatus(nil), in.Services...),
		Diagnostics: append([]Diagnostic(nil), in.Diagnostics...),
		Paths:       cloneStringMap(in.Paths),
	}
}

func cloneAgentDefinitions(in []AgentDefinition) []AgentDefinition {
	if len(in) == 0 {
		return nil
	}
	out := make([]AgentDefinition, 0, len(in))
	for _, agent := range in {
		cp := agent
		cp.Capabilities = append([]string(nil), agent.Capabilities...)
		cp.AllowedTools = append([]string(nil), agent.AllowedTools...)
		cp.DisallowedTools = append([]string(nil), agent.DisallowedTools...)
		out = append(out, cp)
	}
	return out
}

func cloneRuleBlocks(in []RuleBlock) []RuleBlock {
	if len(in) == 0 {
		return nil
	}
	return append([]RuleBlock(nil), in...)
}

func cloneSkills(in []*skills.Skill) []*skills.Skill {
	if len(in) == 0 {
		return nil
	}
	out := make([]*skills.Skill, 0, len(in))
	for _, skill := range in {
		if skill == nil {
			continue
		}
		cp := *skill
		cp.Requires.Commands = append([]string(nil), skill.Requires.Commands...)
		cp.Requires.Env = append([]string(nil), skill.Requires.Env...)
		cp.Requires.MCP = append([]string(nil), skill.Requires.MCP...)
		out = append(out, &cp)
	}
	return out
}

func cloneMCPServers(in map[string]whalemcp.ServerConfig) map[string]whalemcp.ServerConfig {
	if len(in) == 0 {
		return nil
	}
	out := map[string]whalemcp.ServerConfig{}
	for name, server := range in {
		cp := server
		cp.Args = append([]string(nil), server.Args...)
		cp.DisabledTools = append([]string(nil), server.DisabledTools...)
		if server.Env != nil {
			cp.Env = cloneStringMap(server.Env)
		}
		if server.Headers != nil {
			cp.Headers = cloneStringMap(server.Headers)
		}
		out[name] = cp
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func builtins() []Plugin {
	return []Plugin{
		memoryPlugin{},
	}
}

func builtinIDSet() map[string]bool {
	out := map[string]bool{}
	for _, p := range builtins() {
		id := normalizeManifest(p.Manifest()).ID
		if id != "" {
			out[id] = true
		}
	}
	return out
}

func IsBuiltinID(id string) bool {
	return builtinIDSet()[cleanPluginID(id)]
}

func normalizeConfigMap(in ConfigMap) ConfigMap {
	if len(in) == 0 {
		return nil
	}
	out := ConfigMap{}
	for id, cfg := range in {
		id = cleanPluginID(id)
		if id != "" {
			cp := cfg
			if cfg.Enabled != nil {
				enabled := *cfg.Enabled
				cp.Enabled = &enabled
			}
			if len(cfg.MCPServers) > 0 {
				cp.MCPServers = map[string]MCPServerConfig{}
				for name, serverCfg := range cfg.MCPServers {
					name = cleanPluginComponentName(name)
					if name == "" {
						continue
					}
					serverCfg.DisabledTools = append([]string(nil), serverCfg.DisabledTools...)
					cp.MCPServers[name] = serverCfg
				}
			}
			out[id] = cp
		}
	}
	return out
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

func NormalizePluginID(id string) string {
	return cleanPluginID(id)
}

func cleanPluginID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.ReplaceAll(id, "_", "-")
	return id
}

func cleanPluginComponentName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	return name
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
