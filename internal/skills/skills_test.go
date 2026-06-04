package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseContent(t *testing.T) {
	t.Parallel()

	skill, err := ParseContent([]byte("\n---\nname: test-skill\ndescription: Use this skill for tests.\nwhen: Use when tests need a reusable workflow.\nrequires:\n  commands: [git]\n  env:\n    - TEST_SKILL_TOKEN\n  mcp: [github]\n---\n\n# Test Skill\n\nInstructions here.\n"))
	if err != nil {
		t.Fatalf("ParseContent failed: %v", err)
	}
	if skill.Name != "test-skill" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if skill.Description != "Use this skill for tests." {
		t.Fatalf("unexpected description: %q", skill.Description)
	}
	if skill.When != "Use when tests need a reusable workflow." {
		t.Fatalf("unexpected when: %q", skill.When)
	}
	if strings.Join(skill.Requires.Commands, ",") != "git" {
		t.Fatalf("unexpected command requirements: %v", skill.Requires.Commands)
	}
	if strings.Join(skill.Requires.Env, ",") != "TEST_SKILL_TOKEN" {
		t.Fatalf("unexpected env requirements: %v", skill.Requires.Env)
	}
	if strings.Join(skill.Requires.MCP, ",") != "github" {
		t.Fatalf("unexpected mcp requirements: %v", skill.Requires.MCP)
	}
	if skill.Instructions != "# Test Skill\n\nInstructions here." {
		t.Fatalf("unexpected instructions: %q", skill.Instructions)
	}
}

func TestParseContentFoldedDescription(t *testing.T) {
	t.Parallel()

	skill, err := ParseContent([]byte("---\nname: code-review\ndescription: >\n  Structured code review for Go projects and general-purpose code. Use when the user\n  asks for code review, PR review, code quality assessment, code audit, CR, or similar\n  terms.\n---\n\n# Code Review\n"))
	if err != nil {
		t.Fatalf("ParseContent failed: %v", err)
	}
	want := "Structured code review for Go projects and general-purpose code. Use when the user asks for code review, PR review, code quality assessment, code audit, CR, or similar terms."
	if skill.Description != want {
		t.Fatalf("description:\nwant %q\n got %q", want, skill.Description)
	}
}

func TestParseContentRequiresFrontmatter(t *testing.T) {
	t.Parallel()

	if _, err := ParseContent([]byte("# Just Markdown")); err == nil {
		t.Fatal("expected missing frontmatter error")
	}
}

func TestSkillValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		skill   Skill
		wantErr string
	}{
		{
			name:  "valid",
			skill: Skill{Name: "test-skill", Description: "desc", Path: "/tmp/test-skill"},
		},
		{
			name:    "missing name",
			skill:   Skill{Description: "desc"},
			wantErr: "name is required",
		},
		{
			name:    "invalid name",
			skill:   Skill{Name: "-bad", Description: "desc"},
			wantErr: "alphanumeric with hyphens",
		},
		{
			name:    "missing description",
			skill:   Skill{Name: "test-skill", Path: "/tmp/test-skill"},
			wantErr: "description is required",
		},
		{
			name:    "directory mismatch",
			skill:   Skill{Name: "test-skill", Description: "desc", Path: "/tmp/other"},
			wantErr: "must match directory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.skill.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestDiscoverDeduplicatesWithEarlierRootWinning(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	global := t.TempDir()
	writeSkill(t, filepath.Join(workspace, "shared"), "shared", "Workspace skill.", "# Workspace")
	writeSkill(t, filepath.Join(global, "shared"), "shared", "Global skill.", "# Global")
	writeSkill(t, filepath.Join(global, "other"), "other", "Other skill.", "# Other")

	discovered, states := DiscoverWithStates([]string{workspace, global})
	if len(states) != 3 {
		t.Fatalf("expected 3 states, got %d", len(states))
	}
	if names := skillNames(discovered); strings.Join(names, ",") != "other,shared" {
		t.Fatalf("unexpected names: %v", names)
	}
	shared, _, ok := Find([]string{workspace, global}, "shared")
	if !ok {
		t.Fatal("expected shared skill")
	}
	if !strings.Contains(shared.Instructions, "Workspace") {
		t.Fatalf("expected workspace skill to win, got %q", shared.Instructions)
	}
	byPath, _, ok := FindByPath([]string{workspace, global}, filepath.Join(workspace, "shared", "SKILL.md"))
	if !ok {
		t.Fatal("expected shared skill by path")
	}
	if !strings.Contains(byPath.Instructions, "Workspace") {
		t.Fatalf("expected workspace skill by path, got %q", byPath.Instructions)
	}
	if _, _, ok := FindByPath([]string{workspace, global}, filepath.Join(global, "shared", "SKILL.md")); ok {
		t.Fatal("expected duplicate lower-priority path not to be selectable")
	}
}

func TestDiscoverSkipsMissingRootAndInvalidSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "valid"), "valid", "Valid skill.", "# Valid")
	writeSkill(t, filepath.Join(root, "invalid-dir"), "wrong-name", "Invalid skill.", "# Invalid")

	discovered, states := DiscoverWithStates([]string{filepath.Join(root, "missing"), root})
	if names := skillNames(discovered); strings.Join(names, ",") != "valid" {
		t.Fatalf("unexpected names: %v", names)
	}
	var errorStates int
	for _, st := range states {
		if st.State == StateError {
			errorStates++
		}
	}
	if errorStates != 1 {
		t.Fatalf("expected one invalid skill state, got %d", errorStates)
	}
}

func TestDefaultRootsIncludesWorkspaceBeforeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	roots := DefaultRoots(workspace)
	if len(roots) != 4 {
		t.Fatalf("expected 4 roots, got %v", roots)
	}
	wantPrefix := []string{
		filepath.Join(workspace, ".whale", "skills"),
		filepath.Join(workspace, ".agents", "skills"),
		filepath.Join(home, ".whale", "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	for i, want := range wantPrefix {
		if roots[i] != want {
			t.Fatalf("root[%d] = %q, want %q", i, roots[i], want)
		}
	}
}

func TestDiscoverFollowsSymlinkedAgentsSkillsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	claudeSkills := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(claudeSkills, "symlinked-skill"), "symlinked-skill", "Symlinked skill.", "# Symlinked")
	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}
	if err := os.Symlink(claudeSkills, filepath.Join(agentsDir, "skills")); err != nil {
		t.Fatalf("symlink .agents/skills: %v", err)
	}

	discovered := Discover(DefaultRoots(workspace))
	if names := skillNames(discovered); strings.Join(names, ",") != "symlinked-skill" {
		t.Fatalf("expected symlinked ~/.agents/skills root to be discovered, got %v", names)
	}
	if got, want := discovered[0].SkillFilePath, filepath.Join(home, ".agents", "skills", "symlinked-skill", SkillFileName); got != want {
		t.Fatalf("skill file path = %q, want %q", got, want)
	}
}

func TestRenderAvailableSkillsDoesNotIncludeInstructions(t *testing.T) {
	t.Parallel()

	rendered := RenderAvailableSkills([]*Skill{{
		Name:          "test-skill",
		Description:   "Use this for tests.",
		When:          "The user asks for a targeted test skill.",
		Instructions:  "secret instructions",
		SkillFilePath: "/skills/test-skill/SKILL.md",
	}})
	if !strings.Contains(rendered, "test-skill") || !strings.Contains(rendered, "load_skill") {
		t.Fatalf("unexpected rendered skills: %q", rendered)
	}
	for _, want := range []string{"metadata only", "Prefer direct tools or delegation", "Loaded skill results include the skill path"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered skill index missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "secret instructions") {
		t.Fatalf("rendered index should not include full instructions: %q", rendered)
	}
	if strings.Contains(rendered, "/skills/test-skill/SKILL.md") {
		t.Fatalf("rendered index should not include full skill paths: %q", rendered)
	}
}

func TestRenderAvailableSkillsStaysWithinCompactBudget(t *testing.T) {
	t.Parallel()

	var all []*Skill
	for i := 0; i < 80; i++ {
		all = append(all, &Skill{
			Name:        fmt.Sprintf("skill-%02d", i),
			Description: strings.Repeat("verbose description ", 20),
			When:        strings.Repeat("matching condition ", 20),
		})
	}
	rendered := RenderAvailableSkills(all)
	if len(rendered) > availableSkillsIndexMaxChars+512 {
		t.Fatalf("rendered skill index too large: %d chars\n%s", len(rendered), rendered)
	}
	if !strings.Contains(rendered, "more skills omitted") {
		t.Fatalf("expected omitted count in compact index:\n%s", rendered)
	}
}

func TestApproxTokenCount(t *testing.T) {
	t.Parallel()

	if ApproxTokenCount("") != 0 {
		t.Fatal("empty string should have zero tokens")
	}
	if ApproxTokenCount("abcde") != 2 {
		t.Fatalf("unexpected token estimate")
	}
}

func TestFilter(t *testing.T) {
	t.Parallel()

	all := []*Skill{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	filtered := Filter(all, []string{"b"})
	if names := skillNames(filtered); strings.Join(names, ",") != "a,c" {
		t.Fatalf("unexpected filtered names: %v", names)
	}
}

func TestBuildReportGroupsAvailability(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "ready"), "ready", "Ready skill.", "# Ready")
	writeSkillWithFrontmatter(t, filepath.Join(root, "needs-setup"), "---\nname: needs-setup\ndescription: Needs setup skill.\nrequires:\n  commands: [definitely-missing-whale-test-command]\n  env: [WHALE_TEST_MISSING_ENV]\n  mcp: [github]\n---\n\n# Needs setup")
	writeSkill(t, filepath.Join(root, "disabled"), "disabled", "Disabled skill.", "# Disabled")
	writeSkillWithFrontmatter(t, filepath.Join(root, "broken"), "---\nname: wrong-name\ndescription: Broken skill.\n---\n\n# Broken")

	report := BuildReport([]string{root}, ReportOptions{
		DisabledNames: []string{"disabled"},
		MCPConnected:  map[string]bool{"github": false},
		WorkspaceRoot: root,
	})
	if names := viewNames(report.Ready); strings.Join(names, ",") != "ready" {
		t.Fatalf("unexpected ready skills: %v", names)
	}
	if names := viewNames(report.NeedsSetup); strings.Join(names, ",") != "needs-setup" {
		t.Fatalf("unexpected needs setup skills: %v", names)
	}
	if reason := report.NeedsSetup[0].Reason; !strings.Contains(reason, "definitely-missing-whale-test-command") || !strings.Contains(reason, "WHALE_TEST_MISSING_ENV") || !strings.Contains(reason, "MCP server github") {
		t.Fatalf("unexpected needs setup reason: %q", reason)
	}
	if names := viewNames(report.Disabled); strings.Join(names, ",") != "disabled" {
		t.Fatalf("unexpected disabled skills: %v", names)
	}
	if names := viewNames(report.Problems); strings.Join(names, ",") != "wrong-name" {
		t.Fatalf("unexpected problem skills: %v", names)
	}
	if names := viewNames(report.Selectable()); strings.Join(names, ",") != "needs-setup,ready" {
		t.Fatalf("unexpected selectable skills: %v", names)
	}
}

func writeSkill(t *testing.T, dir, name, desc, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, SkillFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func writeSkillWithFrontmatter(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func skillNames(all []*Skill) []string {
	names := make([]string, 0, len(all))
	for _, skill := range all {
		names = append(names, skill.Name)
	}
	return names
}

func viewNames(all []SkillView) []string {
	names := make([]string, 0, len(all))
	for _, skill := range all {
		names = append(names, skill.Name)
	}
	return names
}
