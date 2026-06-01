package tasks

import (
	"sort"
	"strings"
)

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
}

type AgentRegistry struct {
	roles map[string]AgentDefinition
}

func NewAgentRegistry(extra []AgentDefinition) *AgentRegistry {
	r := &AgentRegistry{roles: map[string]AgentDefinition{}}
	for _, def := range builtinAgentDefinitions() {
		r.add(def)
	}
	for _, def := range extra {
		r.add(def)
	}
	return r
}

func (r *AgentRegistry) Resolve(name string) (AgentDefinition, bool) {
	if r == nil {
		r = NewAgentRegistry(nil)
	}
	def, ok := r.roles[strings.TrimSpace(name)]
	return def, ok
}

func (r *AgentRegistry) RoleNames() []string {
	if r == nil {
		r = NewAgentRegistry(nil)
	}
	out := make([]string, 0, len(r.roles))
	for name := range r.roles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *AgentRegistry) add(def AgentDefinition) {
	name := strings.TrimSpace(def.Name)
	if name == "" {
		return
	}
	def.Name = name
	def.Description = strings.TrimSpace(def.Description)
	def.SystemPrompt = strings.TrimSpace(def.SystemPrompt)
	def.Model = strings.TrimSpace(def.Model)
	def.Effort = strings.TrimSpace(def.Effort)
	def.Capabilities = cleanStringList(def.Capabilities)
	def.AllowedTools = cleanStringList(def.AllowedTools)
	def.DisallowedTools = cleanStringList(def.DisallowedTools)
	r.roles[name] = def
}

func builtinAgentDefinitions() []AgentDefinition {
	return []AgentDefinition{
		{
			Name:         "explore",
			Description:  "Read-only codebase or source exploration.",
			SystemPrompt: builtinExplorePrompt,
			Capabilities: []string{CapabilityWorkspaceRead},
			Source:       "builtin",
		},
		{
			Name:         "research",
			Description:  "Read-only source-backed research.",
			SystemPrompt: builtinResearchPrompt,
			Capabilities: []string{CapabilityWorkspaceRead, CapabilityWebSearch, CapabilityWebFetch},
			Source:       "builtin",
		},
		{
			Name:         "review",
			Description:  "Read-only correctness and regression review.",
			SystemPrompt: builtinReviewPrompt,
			Capabilities: []string{CapabilityWorkspaceRead},
			Source:       "builtin",
		},
	}
}

func cleanStringList(values []string) []string {
	var out []string
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
