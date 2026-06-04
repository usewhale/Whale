package tasks

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

func TestResolveAgentRuntimeConfigMergesDefinitionAndOverrides(t *testing.T) {
	cfg, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
		Role:         "review",
		Model:        "deepseek-v4-pro",
		MaxToolIters: 7,
		Agent: AgentDefinition{
			Tools:           []string{CapabilityWorkspaceRead, CapabilityWebFetch},
			DisallowedTools: []string{CapabilityWebFetch},
			Model:           "deepseek-v4-flash",
			Effort:          "high",
			PermissionMode:  AgentPermissionReadOnly,
			MaxTurns:        12,
			Skills:          []string{"review-skill"},
			MCPServers:      []string{"docs", "docs", "github"},
			Hooks: map[string]any{
				"PreToolUse": []any{map[string]any{"command": "echo pre", "match": "read_file"}},
			},
			InitialPrompt: "Load review context first.",
			Memory:        "project",
			Generation: AgentGenerationConfig{
				AssistantPrefix:  "Review:",
				PrefixCompletion: true,
			},
		},
	}, RunnerDefaults{
		Model:        "deepseek-v4-flash",
		MaxToolIters: 3,
	})
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfig: %v", err)
	}
	if cfg.Definition.Name != "review" {
		t.Fatalf("name = %q", cfg.Definition.Name)
	}
	if cfg.Model != "deepseek-v4-pro" {
		t.Fatalf("model = %q", cfg.Model)
	}
	if cfg.Effort != "high" {
		t.Fatalf("effort = %q", cfg.Effort)
	}
	if cfg.MaxToolIters != 7 {
		t.Fatalf("max tool iters = %d", cfg.MaxToolIters)
	}
	if cfg.MaxTurns != 12 {
		t.Fatalf("max turns = %d", cfg.MaxTurns)
	}
	if !reflect.DeepEqual(cfg.Skills, []string{"review-skill"}) || cfg.InitialPrompt != "Load review context first." || cfg.Memory != "project" {
		t.Fatalf("agent context fields = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.MCPServers, []string{"docs", "github"}) {
		t.Fatalf("mcp servers = %#v", cfg.MCPServers)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Event != "PreToolUse" || cfg.Hooks[0].Command != "echo pre" || cfg.Hooks[0].Match != "read_file" {
		t.Fatalf("hooks = %+v", cfg.Hooks)
	}
	if !reflect.DeepEqual(cfg.ToolSelectors, []string{CapabilityWorkspaceRead, CapabilityWebFetch, CapabilityMCPRead}) {
		t.Fatalf("tool selectors = %#v", cfg.ToolSelectors)
	}
	if !reflect.DeepEqual(cfg.DisallowedTools, []string{CapabilityWebFetch}) {
		t.Fatalf("disallowed tools = %#v", cfg.DisallowedTools)
	}
	if cfg.PermissionProfile != AgentPermissionReadOnly {
		t.Fatalf("permission profile = %q", cfg.PermissionProfile)
	}
	if cfg.Generation.AssistantPrefix != "Review:" || !cfg.Generation.PrefixCompletion {
		t.Fatalf("generation = %+v", cfg.Generation)
	}
}

func TestAgentDefinitionSystemBlockShowsDefaultWorkspaceReadTools(t *testing.T) {
	got := agentDefinitionSystemBlock(AgentDefinition{Name: "explore"}, []string{CapabilityWorkspaceRead}, []string{"read_file"}, "read_only")
	if !strings.Contains(got, "- requestedTools: workspace.read") || !strings.Contains(got, "- resolvedTools: read_file") {
		t.Fatalf("default tools not rendered:\n%s", got)
	}
	if strings.Contains(got, "- toolMode: model_only") {
		t.Fatalf("read-only tools should not render model-only:\n%s", got)
	}
	explicitEmpty := agentDefinitionSystemBlock(AgentDefinition{Name: "synthesis"}, []string{}, []string{}, "model_only")
	for _, want := range []string{
		"- requestedTools: []",
		"- resolvedTools: none",
		"- toolMode: model_only",
		"No tools are available",
		"Do not write pseudo tool calls",
	} {
		if !strings.Contains(explicitEmpty, want) {
			t.Fatalf("model-only block missing %q:\n%s", want, explicitEmpty)
		}
	}
}

func TestBuiltinResearchAgentIncludesWebTools(t *testing.T) {
	cfg, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
		Role: "research",
		Task: "research sources",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfig: %v", err)
	}
	want := []string{CapabilityWorkspaceRead, CapabilityWebSearch, CapabilityWebFetch}
	if !reflect.DeepEqual(cfg.ToolSelectors, want) {
		t.Fatalf("research tools = %#v, want %#v", cfg.ToolSelectors, want)
	}
}

func TestAgentDefinitionLibraryLoadsWhaleMarkdownAgent(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := `---
name: local-reviewer
description: "Review local changes"
whenToUse: "Use only when reviewing a local diff"
tools: workspace.read, shell.run
disallowedTools:
  - web.fetch
model: claude-sonnet-4-20250514
effort: xhigh
permissionMode: acceptEdits
maxTurns: 5
skills: [review-skill]
mcpServers:
  - github
initialPrompt: "Read the diff first."
memory: project
background: true
isolation: worktree
generation:
  assistantPrefix: "Review:"
  prefixCompletion: true
---

Focus on correctness risks and missing tests.
`
	if err := os.WriteFile(filepath.Join(agentDir, "local-reviewer.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "local-reviewer",
		Task: "review",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3}, library)
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfigWithLibrary: %v", err)
	}
	if cfg.Definition.Name != "local-reviewer" || cfg.Definition.Prompt != "Focus on correctness risks and missing tests." {
		t.Fatalf("definition = %+v", cfg.Definition)
	}
	if cfg.Definition.WhenToUse != "Use only when reviewing a local diff" {
		t.Fatalf("whenToUse = %q", cfg.Definition.WhenToUse)
	}
	if cfg.Model != "deepseek-v4-flash" || cfg.Effort != "max" || cfg.PermissionProfile != AgentPermissionAuto {
		t.Fatalf("runtime fields = %+v", cfg)
	}
	if cfg.MaxTurns != 5 || cfg.InitialPrompt != "Read the diff first." || cfg.Memory != "project" || cfg.Isolation != AgentIsolationWorktree {
		t.Fatalf("context fields = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.ToolSelectors, []string{CapabilityWorkspaceRead, CapabilityShellRun, CapabilityMCPRead}) {
		t.Fatalf("tool selectors = %#v", cfg.ToolSelectors)
	}
	if !reflect.DeepEqual(cfg.DisallowedTools, []string{CapabilityWebFetch}) || !reflect.DeepEqual(cfg.Skills, []string{"review-skill"}) || !reflect.DeepEqual(cfg.MCPServers, []string{"github"}) {
		t.Fatalf("lists = tools:%#v skills:%#v mcp:%#v", cfg.DisallowedTools, cfg.Skills, cfg.MCPServers)
	}
	if cfg.Generation.AssistantPrefix != "Review:" || !cfg.Generation.PrefixCompletion {
		t.Fatalf("generation = %+v", cfg.Generation)
	}
}

func TestAgentDefinitionGenerationPreservesAssistantPrefixWhitespace(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := `---
description: "Return JSON"
tools: []
generation:
  assistantPrefix: "{\n"
  prefixCompletion: true
---

Return compact JSON.
`
	if err := os.WriteFile(filepath.Join(agentDir, "json-prefix.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "json-prefix",
		Task: "return ok",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3}, library)
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfigWithLibrary: %v", err)
	}
	if cfg.Generation.AssistantPrefix != "{\n" {
		t.Fatalf("assistant prefix = %q, want %q", cfg.Generation.AssistantPrefix, "{\n")
	}
}

func TestAgentDefinitionLibraryUsesMemoryDefinitionsAsFallback(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	projectAgent := `---
name: plugin-reviewer
description: "Project reviewer"
tools: [workspace.read]
---

Project prompt.
`
	if err := os.WriteFile(filepath.Join(agentDir, "plugin-reviewer.md"), []byte(projectAgent), 0o644); err != nil {
		t.Fatalf("write project agent: %v", err)
	}
	library := NewAgentDefinitionLibraryWithDefinitions(root, []AgentDefinition{
		{
			Name:           "plugin-reviewer",
			Description:    "Plugin reviewer",
			Prompt:         "Plugin prompt.",
			Tools:          []string{CapabilityShellRun},
			PermissionMode: AgentPermissionAsk,
		},
		{
			Name:           "plugin-only",
			Description:    "Plugin only reviewer",
			Prompt:         "Plugin only prompt.",
			Tools:          []string{CapabilityWorkspaceRead},
			PermissionMode: AgentPermissionReadOnly,
		},
	})
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "plugin-reviewer",
		Task: "review",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3}, library)
	if err != nil {
		t.Fatalf("Resolve project override: %v", err)
	}
	if cfg.Definition.Prompt != "Project prompt." || !reflect.DeepEqual(cfg.ToolSelectors, []string{CapabilityWorkspaceRead}) {
		t.Fatalf("project definition should override plugin fallback: %+v", cfg)
	}
	cfg, err = ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "plugin-only",
		Task: "review",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3}, library)
	if err != nil {
		t.Fatalf("Resolve plugin-only: %v", err)
	}
	if cfg.Definition.Prompt != "Plugin only prompt." || cfg.PermissionProfile != AgentPermissionReadOnly {
		t.Fatalf("plugin definition not used: %+v", cfg)
	}
}

func TestAgentDefinitionLibraryLoadsJSONGeneration(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := `{
  "description": "Model-only prefix agent",
  "tools": [],
  "generation": {
    "assistantPrefix": "Review:",
    "prefixCompletion": true
  }
}`
	if err := os.WriteFile(filepath.Join(agentDir, "prefix-agent.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "prefix-agent",
		Task: "review",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3}, library)
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfigWithLibrary: %v", err)
	}
	if cfg.Definition.Name != "prefix-agent" {
		t.Fatalf("name = %q", cfg.Definition.Name)
	}
	if !reflect.DeepEqual(cfg.ToolSelectors, []string{}) {
		t.Fatalf("tool selectors = %#v", cfg.ToolSelectors)
	}
	if cfg.Generation.AssistantPrefix != "Review:" || !cfg.Generation.PrefixCompletion {
		t.Fatalf("generation = %+v", cfg.Generation)
	}
}

func TestAgentDefinitionLibraryLoadsMarkdownHooks(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := `---
name: hooked-reviewer
description: "Review with hooks"
hooks:
  PreToolUse:
    - type: prompt
      prompt: "Check this tool call"
      match: read_file
      model: haiku
    - type: http
      url: "https://example.com/hook"
      headers:
        X-Test: ok
      allowedEnvVars:
        - TOKEN
  Stop:
    - command: "echo stop"
---

Review with guardrails.
`
	if err := os.WriteFile(filepath.Join(agentDir, "hooked-reviewer.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "hooked-reviewer",
		Task: "review",
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3}, library)
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfigWithLibrary: %v", err)
	}
	if len(cfg.Hooks) != 3 {
		t.Fatalf("hooks = %+v", cfg.Hooks)
	}
	var promptHook, httpHook, stopHook *agent.ResolvedHook
	for i := range cfg.Hooks {
		switch {
		case cfg.Hooks[i].Type == "prompt":
			promptHook = &cfg.Hooks[i]
		case cfg.Hooks[i].Type == "http":
			httpHook = &cfg.Hooks[i]
		case cfg.Hooks[i].Command == "echo stop":
			stopHook = &cfg.Hooks[i]
		}
	}
	if promptHook == nil || promptHook.Prompt != "Check this tool call" || promptHook.Match != "read_file" || promptHook.Model != "haiku" {
		t.Fatalf("prompt hook = %+v", promptHook)
	}
	if httpHook == nil || httpHook.URL != "https://example.com/hook" || httpHook.Headers["X-Test"] != "ok" || !reflect.DeepEqual(httpHook.AllowedEnvVars, []string{"TOKEN"}) {
		t.Fatalf("http hook = %+v", httpHook)
	}
	if stopHook == nil || stopHook.Event != agent.HookEventSubagentStop {
		t.Fatalf("stop hook = %+v", stopHook)
	}
}

func TestAgentDefinitionLibraryIgnoresClaudeAgentsByDefault(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	content := `---
name: claude-reviewer
description: "Review with Claude-style tools"
tools: Read, Grep, Bash
---

Review local changes.
`
	if err := os.WriteFile(filepath.Join(agentDir, "claude-reviewer.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	defs, err := library.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected .claude agents to be ignored, got %+v", defs)
	}
}

func TestAgentDefinitionLibraryResolveIgnoresMalformedUnrelatedAgents(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "broken.json"), []byte(`{`), 0o644); err != nil {
		t.Fatalf("write broken agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "review",
		Task: "review local changes",
	}, RunnerDefaults{Model: "deepseek-v4-flash"}, library)
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfigWithLibrary builtin review: %v", err)
	}
	if cfg.Definition.Name != "review" || cfg.PermissionProfile != AgentPermissionReadOnly {
		t.Fatalf("builtin review config = %+v", cfg)
	}
}

func TestAgentDefinitionLibraryListSkipsMalformedAgents(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "broken.json"), []byte(`{`), 0o644); err != nil {
		t.Fatalf("write broken agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "reader.json"), []byte(`{
  "name": "reader",
  "description": "Reads files",
  "tools": ["workspace.read"],
  "permissionMode": "read_only"
}`), 0o644); err != nil {
		t.Fatalf("write reader agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	defs, err := library.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "reader" {
		t.Fatalf("definitions = %+v", defs)
	}
}

func TestAgentDefinitionLibraryResolveFailsMalformedRequestedAgent(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "review.json"), []byte(`{`), 0o644); err != nil {
		t.Fatalf("write broken agent: %v", err)
	}
	library := NewAgentDefinitionLibrary(root)
	_, err := ResolveAgentRuntimeConfigWithLibrary(SpawnSubagentRequest{
		Role: "review",
		Task: "review local changes",
	}, RunnerDefaults{Model: "deepseek-v4-flash"}, library)
	if err == nil || !strings.Contains(err.Error(), "parse agent definition") {
		t.Fatalf("expected requested agent parse error, got %v", err)
	}
}

func TestResolveAgentRuntimeConfigAcceptsCustomDefinition(t *testing.T) {
	cfg, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:           "local-reviewer",
			Description:    "review local changes",
			Tools:          []string{CapabilityWorkspaceRead},
			Model:          "deepseek-v4-flash",
			PermissionMode: AgentPermissionAsk,
		},
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfig: %v", err)
	}
	if cfg.Definition.Name != "local-reviewer" || cfg.Model != "deepseek-v4-flash" || cfg.PermissionProfile != AgentPermissionAsk {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestResolveAgentRuntimeConfigMapsClaudeModelAliases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "haiku", in: "haiku", want: "deepseek-v4-flash"},
		{name: "sonnet", in: "claude-sonnet-4-20250514", want: "deepseek-v4-flash"},
		{name: "opus", in: "opus", want: "deepseek-v4-pro"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
				Task:  "review",
				Agent: AgentDefinition{Name: "reviewer", Model: tt.in},
			}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
			if err != nil {
				t.Fatalf("ResolveAgentRuntimeConfig: %v", err)
			}
			if cfg.Model != tt.want {
				t.Fatalf("model = %q, want %q", cfg.Model, tt.want)
			}
		})
	}
}

func TestResolveAgentRuntimeConfigMapsEffortForDeepSeek(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "low", want: "high"},
		{in: "medium", want: "high"},
		{in: "xhigh", want: "max"},
		{in: "max", want: "max"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			cfg, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
				Task:  "review",
				Agent: AgentDefinition{Name: "reviewer", Effort: tt.in},
			}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
			if err != nil {
				t.Fatalf("ResolveAgentRuntimeConfig: %v", err)
			}
			if cfg.Effort != tt.want {
				t.Fatalf("effort = %q, want %q", cfg.Effort, tt.want)
			}
		})
	}
}

func TestResolveAgentRuntimeConfigNormalizesWorktreeIsolation(t *testing.T) {
	cfg, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:      "isolated-reviewer",
			Isolation: AgentIsolationWorktree,
		},
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
	if err != nil {
		t.Fatalf("ResolveAgentRuntimeConfig: %v", err)
	}
	if cfg.Isolation != AgentIsolationWorktree {
		t.Fatalf("isolation = %q", cfg.Isolation)
	}
}

func TestResolveAgentRuntimeConfigRejectsUnsupportedIsolation(t *testing.T) {
	_, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:      "remote-reviewer",
			Isolation: "remote",
		},
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
	if err == nil {
		t.Fatal("expected unsupported isolation to be rejected")
	}
}

func TestResolveAgentRuntimeConfigRejectsUnsupportedMemoryScope(t *testing.T) {
	_, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:   "memory-reviewer",
			Memory: "team",
		},
	}, RunnerDefaults{Model: "deepseek-v4-flash", MaxToolIters: 3})
	if err == nil {
		t.Fatal("expected unsupported memory scope to be rejected")
	}
}

func TestResolveAgentRuntimeConfigRejectsUnknownBareRole(t *testing.T) {
	_, err := ResolveAgentRuntimeConfig(SpawnSubagentRequest{Role: "writer"}, RunnerDefaults{Model: "deepseek-v4-flash"})
	if err == nil {
		t.Fatal("expected unknown bare role to be rejected")
	}
}

func TestResolveAgentHooksAcceptsClaudeMatcherCommandHooks(t *testing.T) {
	hooks, err := ResolveAgentHooks(AgentDefinition{
		Name: "reviewer",
		Hooks: map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "read_file",
				"hooks": []any{map[string]any{
					"type":          "command",
					"command":       "echo pre",
					"if":            "read_file",
					"shell":         "bash",
					"once":          true,
					"async":         true,
					"asyncRewake":   true,
					"timeout":       float64(2),
					"statusMessage": "checking read",
				}},
			}},
			"Stop": []any{map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": "echo stop"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ResolveAgentHooks: %v", err)
	}
	if len(hooks) != 2 {
		t.Fatalf("hooks = %+v", hooks)
	}
	var pre, stop *agent.ResolvedHook
	for i := range hooks {
		switch hooks[i].Event {
		case "PreToolUse":
			pre = &hooks[i]
		case "SubagentStop":
			stop = &hooks[i]
		}
	}
	if pre == nil || pre.Match != "read_file" || pre.TimeoutSec != 2 || pre.Description != "checking read" {
		t.Fatalf("pre hook = %+v", pre)
	}
	if pre.If != "read_file" || pre.Shell != "bash" || !pre.Once || !pre.Async || !pre.AsyncRewake {
		t.Fatalf("pre hook modifiers = %+v", pre)
	}
	if stop == nil || stop.Command != "echo stop" {
		t.Fatalf("stop hook = %+v", stop)
	}
}

func TestResolveAgentHooksAcceptsClaudePromptHTTPAndAgentHooks(t *testing.T) {
	hooks, err := ResolveAgentHooks(AgentDefinition{
		Name: "reviewer",
		Hooks: map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "read_file",
				"hooks": []any{
					map[string]any{"type": "http", "url": "https://example.com/hook", "headers": map[string]any{"X-Test": "ok"}, "allowedEnvVars": []any{"TOKEN"}},
					map[string]any{"type": "prompt", "prompt": "Decide if this read is allowed", "model": "haiku"},
					map[string]any{"type": "agent", "prompt": "Review this tool call", "model": "sonnet"},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ResolveAgentHooks: %v", err)
	}
	if len(hooks) != 3 {
		t.Fatalf("hooks = %+v", hooks)
	}
	if hooks[0].Type != "http" || hooks[0].URL != "https://example.com/hook" || hooks[0].Headers["X-Test"] != "ok" || len(hooks[0].AllowedEnvVars) != 1 {
		t.Fatalf("http hook = %+v", hooks[0])
	}
	if hooks[1].Type != "prompt" || hooks[1].Prompt == "" || hooks[1].Model != "haiku" {
		t.Fatalf("prompt hook = %+v", hooks[1])
	}
	if hooks[2].Type != "agent" || hooks[2].Prompt == "" || hooks[2].Model != "sonnet" {
		t.Fatalf("agent hook = %+v", hooks[2])
	}
}

func TestHookModelExecutorParsesOKContract(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		ProviderFactory: func(_ string, _ int) (llm.Provider, error) {
			return providerFunc(func(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
				out := make(chan llm.ProviderEvent, 1)
				go func() {
					defer close(out)
					if len(tools) != 0 {
						t.Errorf("hook model tools = %d", len(tools))
					}
					if len(history) == 0 || history[len(history)-1].Text == "" {
						t.Error("missing hook prompt")
					}
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						Content: `{"ok":false,"reason":"blocked by model"}`,
						Usage:   llm.Usage{TotalTokens: 3},
					}}
				}()
				return out
			}), nil
		},
		DefaultModel: "deepseek-v4-flash",
	})
	exec := runner.hookModelExecutor("deepseek-v4-flash", "", "prompt")
	res := exec(context.Background(), agent.HookConfig{Type: "prompt", Prompt: "Gate this."}, agent.HookPayload{Event: agent.HookEventPreToolUse, ToolName: "read_file"})
	if res.Decision != agent.HookDecisionBlock || res.Message != "blocked by model" {
		t.Fatalf("result = %+v", res)
	}
}

func TestCompleteHookModelDoesNotDuplicateCompleteContentAfterDeltas(t *testing.T) {
	provider := providerFunc(func(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
		out := make(chan llm.ProviderEvent, 3)
		go func() {
			defer close(out)
			out <- llm.ProviderEvent{Type: llm.EventContentDelta, Content: `{"ok":`}
			out <- llm.ProviderEvent{Type: llm.EventContentDelta, Content: `true}`}
			out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
				Content: `{"ok":true}`,
				Usage:   llm.Usage{TotalTokens: 5},
			}}
		}()
		return out
	})

	content, usage, err := completeHookModel(context.Background(), provider, "prompt")
	if err != nil {
		t.Fatalf("completeHookModel: %v", err)
	}
	if content != `{"ok":true}` {
		t.Fatalf("content = %q", content)
	}
	if usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v", usage)
	}
}
