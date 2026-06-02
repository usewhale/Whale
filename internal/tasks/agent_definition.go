package tasks

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/defaults"
)

const (
	AgentPermissionReadOnly = "read_only"
	AgentPermissionAsk      = "ask"
	AgentPermissionAuto     = "auto"
	AgentPermissionTrusted  = "trusted"

	AgentIsolationNone     = "none"
	AgentIsolationWorktree = "worktree"
)

// AgentDefinition is the durable contract for child-agent execution. Workflow
// agent calls and spawn_subagent requests should converge here before tools,
// model, and runtime limits are resolved.
type AgentDefinition struct {
	Name            string                `json:"name,omitempty"`
	Description     string                `json:"description,omitempty"`
	WhenToUse       string                `json:"whenToUse,omitempty"`
	Prompt          string                `json:"prompt,omitempty"`
	Tools           []string              `json:"tools,omitempty"`
	DisallowedTools []string              `json:"disallowedTools,omitempty"`
	Skills          []string              `json:"skills,omitempty"`
	MCPServers      []string              `json:"mcpServers,omitempty"`
	Hooks           any                   `json:"hooks,omitempty"`
	Model           string                `json:"model,omitempty"`
	Effort          string                `json:"effort,omitempty"`
	PermissionMode  string                `json:"permissionMode,omitempty"`
	MaxToolIters    int                   `json:"maxToolIters,omitempty"`
	MaxToolCalls    int                   `json:"maxToolCalls,omitempty"`
	MaxTurns        int                   `json:"maxTurns,omitempty"`
	InitialPrompt   string                `json:"initialPrompt,omitempty"`
	Memory          string                `json:"memory,omitempty"`
	Background      bool                  `json:"background,omitempty"`
	Isolation       string                `json:"isolation,omitempty"`
	Generation      AgentGenerationConfig `json:"generation,omitempty"`
}

type AgentGenerationConfig struct {
	AssistantPrefix  string `json:"assistantPrefix,omitempty"`
	PrefixCompletion bool   `json:"prefixCompletion,omitempty"`
}

type AgentRuntimeConfig struct {
	Definition        AgentDefinition
	Model             string
	Effort            string
	MaxToolIters      int
	MaxToolCalls      int
	MaxTurns          int
	Capabilities      []string
	DisallowedTools   []string
	MCPServers        []string
	Hooks             []agent.ResolvedHook
	PermissionProfile string
	Isolation         string
	Skills            []string
	InitialPrompt     string
	Memory            string
	Generation        AgentGenerationConfig
}

func ResolveAgentRuntimeConfig(req SpawnSubagentRequest, defaults RunnerDefaults) (AgentRuntimeConfig, error) {
	return ResolveAgentRuntimeConfigWithLibrary(req, defaults, nil)
}

func ResolveAgentRuntimeConfigWithLibrary(req SpawnSubagentRequest, defaults RunnerDefaults, library *AgentDefinitionLibrary) (AgentRuntimeConfig, error) {
	def, err := resolveAgentDefinition(req, library)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(def.Model)
	}
	if model == "" {
		model = defaults.Model
	}
	model, err = normalizeAgentModel(model)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	effort, err := normalizeAgentEffort(def.Effort)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	maxToolIters := req.MaxToolIters
	if maxToolIters <= 0 {
		maxToolIters = def.MaxToolIters
	}
	if maxToolIters <= 0 {
		maxToolIters = defaults.MaxToolIters
	}
	maxToolCalls := req.MaxToolCalls
	if maxToolCalls <= 0 {
		maxToolCalls = def.MaxToolCalls
	}
	if maxToolCalls <= 0 {
		maxToolCalls = defaults.MaxToolCalls
	}
	maxTurns := def.MaxTurns
	caps := req.Capabilities
	if caps == nil {
		caps = def.Tools
	}
	mcpServers := normalizeAgentMCPServers(def.MCPServers)
	hooks, err := ResolveAgentHooks(def)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	if len(mcpServers) > 0 && caps == nil {
		caps = []string{CapabilityWorkspaceRead}
	}
	if len(mcpServers) > 0 && !containsString(caps, CapabilityMCPRead) {
		caps = append(cloneStrings(caps), CapabilityMCPRead)
	}
	permission, err := normalizeAgentPermissionMode(def.PermissionMode)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	isolation, err := normalizeAgentIsolation(def.Isolation)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	memoryScope, err := normalizeAgentMemory(def.Memory)
	if err != nil {
		return AgentRuntimeConfig{}, err
	}
	return AgentRuntimeConfig{
		Definition:        def,
		Model:             model,
		Effort:            effort,
		MaxToolIters:      maxToolIters,
		MaxToolCalls:      maxToolCalls,
		MaxTurns:          maxTurns,
		Capabilities:      cloneStrings(caps),
		DisallowedTools:   cloneStrings(def.DisallowedTools),
		MCPServers:        cloneStrings(mcpServers),
		Hooks:             hooks,
		PermissionProfile: permission,
		Isolation:         isolation,
		Skills:            cloneStrings(def.Skills),
		InitialPrompt:     strings.TrimSpace(def.InitialPrompt),
		Memory:            memoryScope,
		Generation: AgentGenerationConfig{
			AssistantPrefix:  def.Generation.AssistantPrefix,
			PrefixCompletion: def.Generation.PrefixCompletion,
		},
	}, nil
}

func normalizeAgentModel(v string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(v))
	switch {
	case m == "":
		return defaults.DefaultModel, nil
	case defaults.IsSupportedModel(m):
		return m, nil
	case m == "haiku" || strings.Contains(m, "haiku"):
		return defaults.DefaultModel, nil
	case m == "sonnet" || strings.Contains(m, "sonnet"):
		return defaults.DefaultModel, nil
	case m == "opus" || strings.Contains(m, "opus"):
		return defaults.ProModel, nil
	case strings.HasPrefix(m, "claude-"):
		return defaults.DefaultModel, nil
	default:
		return "", fmt.Errorf("unsupported agent model %q", strings.TrimSpace(v))
	}
}

func normalizeAgentEffort(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", nil
	case "low", "medium", "high":
		return "high", nil
	case "xhigh", "max":
		return "max", nil
	default:
		return "", fmt.Errorf("unsupported agent effort %q", strings.TrimSpace(v))
	}
}

func normalizeAgentPermissionMode(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", AgentPermissionReadOnly, "readOnly", "plan":
		return AgentPermissionReadOnly, nil
	case AgentPermissionAsk, "default", "bubble":
		return AgentPermissionAsk, nil
	case AgentPermissionAuto, "acceptEdits":
		return AgentPermissionAuto, nil
	case AgentPermissionTrusted, "dontAsk", "bypassPermissions":
		return AgentPermissionTrusted, nil
	default:
		return "", fmt.Errorf("unsupported agent permissionMode %q", strings.TrimSpace(v))
	}
}

func normalizeAgentIsolation(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", AgentIsolationNone:
		return "", nil
	case AgentIsolationWorktree:
		return AgentIsolationWorktree, nil
	default:
		return "", fmt.Errorf("unsupported agent isolation %q", strings.TrimSpace(v))
	}
}

func normalizeAgentMemory(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "":
		return "", nil
	case "user", "project", "local":
		return strings.TrimSpace(v), nil
	default:
		return "", fmt.Errorf("unsupported agent memory scope %q", strings.TrimSpace(v))
	}
}

type RunnerDefaults struct {
	Model        string
	MaxToolIters int
	MaxToolCalls int
}

func resolveAgentDefinition(req SpawnSubagentRequest, library *AgentDefinitionLibrary) (AgentDefinition, error) {
	name := strings.TrimSpace(req.Agent.Name)
	if name == "" {
		name = strings.TrimSpace(req.Role)
	}
	if name == "" {
		name = "explore"
	}
	def, ok := AgentDefinition{}, false
	if library != nil {
		var err error
		def, ok, err = library.Resolve(name)
		if err != nil {
			return AgentDefinition{}, err
		}
	}
	if !ok {
		def, ok = builtinAgentDefinition(name)
	}
	if !ok && !agentRequestHasDefinition(req.Agent) {
		return AgentDefinition{}, fmt.Errorf("unsupported subagent role %q", name)
	}
	return mergeAgentDefinition(def, req.Agent, name), nil
}

func builtinAgentDefinition(name string) (AgentDefinition, bool) {
	switch strings.TrimSpace(name) {
	case "research":
		return AgentDefinition{
			Name:           "research",
			Description:    "Source-backed research child agent",
			WhenToUse:      "Use for bounded source-backed research using workspace, web, or model-only tools.",
			Tools:          []string{CapabilityWorkspaceRead, CapabilityWebSearch, CapabilityWebFetch},
			PermissionMode: AgentPermissionReadOnly,
		}, true
	case "review":
		return AgentDefinition{
			Name:           "review",
			Description:    "Read-only review child agent",
			WhenToUse:      "Use for bounded review of correctness risks, regressions, and missing verification.",
			PermissionMode: AgentPermissionReadOnly,
		}, true
	case "explore":
		return AgentDefinition{
			Name:           "explore",
			Description:    "Read-only exploration child agent",
			WhenToUse:      "Use for bounded codebase or source exploration.",
			PermissionMode: AgentPermissionReadOnly,
		}, true
	default:
		return AgentDefinition{}, false
	}
}

func mergeAgentDefinition(base, override AgentDefinition, fallbackName string) AgentDefinition {
	out := base
	if v := strings.TrimSpace(override.Name); v != "" {
		out.Name = v
	} else if out.Name == "" {
		out.Name = strings.TrimSpace(fallbackName)
	}
	if v := strings.TrimSpace(override.Description); v != "" {
		out.Description = v
	}
	if v := strings.TrimSpace(override.WhenToUse); v != "" {
		out.WhenToUse = v
	}
	if v := strings.TrimSpace(override.Prompt); v != "" {
		out.Prompt = v
	}
	if override.Tools != nil {
		out.Tools = cloneStrings(override.Tools)
	}
	if override.DisallowedTools != nil {
		out.DisallowedTools = cloneStrings(override.DisallowedTools)
	}
	if override.Skills != nil {
		out.Skills = cloneStrings(override.Skills)
	}
	if override.MCPServers != nil {
		out.MCPServers = cloneStrings(override.MCPServers)
	}
	if override.Hooks != nil {
		out.Hooks = override.Hooks
	}
	if v := strings.TrimSpace(override.Model); v != "" {
		out.Model = v
	}
	if v := strings.TrimSpace(override.Effort); v != "" {
		out.Effort = v
	}
	if v := strings.TrimSpace(override.PermissionMode); v != "" {
		out.PermissionMode = v
	}
	if override.MaxToolIters > 0 {
		out.MaxToolIters = override.MaxToolIters
	}
	if override.MaxToolCalls > 0 {
		out.MaxToolCalls = override.MaxToolCalls
	}
	if override.MaxTurns > 0 {
		out.MaxTurns = override.MaxTurns
	}
	if v := strings.TrimSpace(override.InitialPrompt); v != "" {
		out.InitialPrompt = v
	}
	if v := strings.TrimSpace(override.Memory); v != "" {
		out.Memory = v
	}
	if override.Background {
		out.Background = true
	}
	if v := strings.TrimSpace(override.Isolation); v != "" {
		out.Isolation = v
	}
	if agentGenerationConfigured(override.Generation) {
		out.Generation = AgentGenerationConfig{
			AssistantPrefix:  override.Generation.AssistantPrefix,
			PrefixCompletion: override.Generation.PrefixCompletion,
		}
	}
	return out
}

func agentRequestHasDefinition(def AgentDefinition) bool {
	return strings.TrimSpace(def.Name) != "" ||
		strings.TrimSpace(def.Description) != "" ||
		strings.TrimSpace(def.WhenToUse) != "" ||
		strings.TrimSpace(def.Prompt) != "" ||
		def.Tools != nil ||
		def.DisallowedTools != nil ||
		def.Skills != nil ||
		def.MCPServers != nil ||
		def.Hooks != nil ||
		strings.TrimSpace(def.Model) != "" ||
		strings.TrimSpace(def.Effort) != "" ||
		strings.TrimSpace(def.PermissionMode) != "" ||
		def.MaxTurns > 0 ||
		strings.TrimSpace(def.InitialPrompt) != "" ||
		strings.TrimSpace(def.Memory) != "" ||
		def.Background ||
		strings.TrimSpace(def.Isolation) != "" ||
		agentGenerationConfigured(def.Generation)
}

func agentGenerationConfigured(cfg AgentGenerationConfig) bool {
	return strings.TrimSpace(cfg.AssistantPrefix) != "" || cfg.PrefixCompletion
}

func normalizeAgentMCPServers(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(values))
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}
