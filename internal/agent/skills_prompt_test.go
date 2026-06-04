package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
	"github.com/usewhale/whale/internal/skills"
)

func TestRuntimeSystemPromptIncludesSkillIndexOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, ".whale", "skills", "prompt-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: prompt-skill\ndescription: Prompt skill.\n---\n\n# Prompt Skill\n\nDo not inline this body.\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	a := &Agent{
		tools:                  core.NewToolRegistry(nil),
		workspaceRoot:          workspace,
		projectMemoryEnabled:   false,
		projectMemoryFileOrder: nil,
	}
	immutable := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")
	if strings.Contains(immutable, "Available skills") || strings.Contains(immutable, "prompt-skill") {
		t.Fatalf("skill index leaked into immutable system prompt:\n%s", immutable)
	}

	blocks := a.buildRuntimeSystemBlocks()
	joined := strings.Join(blocks, "\n\n")
	if !strings.Contains(joined, "Available skills") || !strings.Contains(joined, "prompt-skill") || !strings.Contains(joined, "load_skill") {
		t.Fatalf("missing skill index in runtime system prompt:\n%s", joined)
	}
	if strings.Contains(joined, "Do not inline this body") {
		t.Fatalf("runtime system prompt should not inline skill instructions:\n%s", joined)
	}
}

func TestRuntimeSystemPromptFiltersDisabledPluginSkills(t *testing.T) {
	workspace := t.TempDir()
	a := &Agent{
		tools:                core.NewToolRegistry(nil),
		workspaceRoot:        workspace,
		projectMemoryEnabled: false,
		disabledSkills:       []string{"plugin-skill"},
		extraSkills: []*skills.Skill{{
			Name:          "plugin-skill",
			Description:   "Plugin skill.",
			SkillFilePath: "plugin://test/SKILL.md",
		}},
	}
	joined := strings.Join(a.buildRuntimeSystemBlocks(), "\n\n")
	if strings.Contains(joined, "plugin-skill") {
		t.Fatalf("disabled plugin skill leaked into runtime system prompt:\n%s", joined)
	}
}

func TestImmutableSystemPromptIncludesDelegationPolicyBeforeToolPolicy(t *testing.T) {
	a := &Agent{
		tools:                core.NewToolRegistry(nil),
		projectMemoryEnabled: false,
	}
	blocks := a.buildImmutableSystemBlocks()
	joined := strings.Join(blocks, "\n\n")
	policyIx := strings.Index(joined, "Delegation policy.")
	toolIx := strings.Index(joined, "Tool use policy.")
	if policyIx < 0 {
		t.Fatalf("missing delegation policy:\n%s", joined)
	}
	if toolIx < 0 {
		t.Fatalf("missing tool policy block:\n%s", joined)
	}
	if policyIx > toolIx {
		t.Fatalf("delegation policy should appear before tool policy:\n%s", joined)
	}
	for _, want := range []string{"Use parallel_reason for 2-8 independent", "Use spawn_subagent for one bounded tool-scoped", "Use a single agent for direct questions", "Do not load a skill first unless the user explicitly names one"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("delegation policy missing %q:\n%s", want, joined)
		}
	}
}

func TestRuntimeSystemPromptIncludesRuntimeContext(t *testing.T) {
	a := &Agent{
		tools:                core.NewToolRegistry(nil),
		workspaceRoot:        "/repo",
		projectMemoryEnabled: false,
	}
	immutable := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")
	if strings.Contains(immutable, "Current Whale runtime:") || strings.Contains(immutable, "Current Whale workspace root: /repo") {
		t.Fatalf("runtime context leaked into immutable system prompt:\n%s", immutable)
	}

	blocks := a.buildRuntimeSystemBlocks()
	joined := strings.Join(blocks, "\n\n")
	for _, want := range []string{
		"Current Whale runtime:",
		"Current Whale workspace root: /repo",
		"- OS:",
		"- Shell:",
		"Shell commands run from the current Whale workspace by default.",
		"Do not assume a synthetic path such as /workspace",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("runtime system prompt missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderRuntimeBlockDescribesPowerShellSyntax(t *testing.T) {
	block := renderRuntimeBlock(`C:\repo`, runtimeWorktreeContext{}, shell.RuntimeDescription{
		GOOS: "windows",
		Spec: shell.Spec{Kind: shell.KindPowerShell, DisplayName: "PowerShell"},
	})
	for _, want := range []string{
		"Current Whale runtime:",
		`Current Whale workspace root: C:\repo`,
		"- OS: windows",
		"- Shell: PowerShell (PowerShell -NoLogo -NoProfile -NonInteractive -Command)",
		"Use PowerShell syntax",
		"Get-ChildItem",
		"Select-String",
		"$env:TEMP",
		"Avoid POSIX-only assumptions",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("runtime block missing %q:\n%s", want, block)
		}
	}
}
