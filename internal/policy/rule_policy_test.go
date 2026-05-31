package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestRulePolicyDefaultPosture(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}

	read := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":"README.md"}`})
	if !read.Allow || read.RequiresApproval {
		t.Fatalf("read decision = %+v, want allow without approval", read)
	}

	env := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":".env"}`})
	if !env.Allow || !env.RequiresApproval {
		t.Fatalf(".env decision = %+v, want approval", env)
	}

	example := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":".env.example"}`})
	if !example.Allow || example.RequiresApproval {
		t.Fatalf(".env.example decision = %+v, want allow without approval", example)
	}

	push := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"git push origin main"}`})
	if !push.Allow || !push.RequiresApproval {
		t.Fatalf("git push decision = %+v, want approval", push)
	}

	deny := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rf /tmp/x"}`})
	if deny.Allow || deny.Code != "permission_denied" {
		t.Fatalf("rm -rf decision = %+v, want deny", deny)
	}
}

func TestRulePolicyDefaultControlsUnspecifiedTools(t *testing.T) {
	// web_fetch has no entry in DefaultRules, so its decision is governed
	// entirely by the policy default.
	spec := core.ToolSpec{Name: "web_fetch"}
	call := core.ToolCall{Name: "web_fetch", Input: `{"url":"https://example.com"}`}

	ask := RulePolicy{Default: PermissionAsk, Rules: DefaultRules()}.Decide(spec, call)
	if !ask.Allow || !ask.RequiresApproval || ask.Code != "permission_required" {
		t.Fatalf("default ask should require approval for unspecified tool: %+v", ask)
	}

	deny := RulePolicy{Default: PermissionDeny, Rules: DefaultRules()}.Decide(spec, call)
	if deny.Allow || deny.Code != "permission_denied" {
		t.Fatalf("default deny should deny unspecified tool: %+v", deny)
	}
}

func TestRulePolicyDefaultEditAllowsWorkspaceMutation(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}
	spec := core.ToolSpec{Name: "edit"}
	call := core.ToolCall{Name: "edit", Input: `{"file_path":"a.txt","search":"a","replace":"b"}`}

	got := p.Decide(spec, call)
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("edit should be allowed under the default workspace posture: %+v", got)
	}
}

func TestRulePolicyUserEditRuleOverridesDefaultAllow(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "edit", Pattern: "*", Action: PermissionAsk})
	p := RulePolicy{Default: PermissionAllow, Rules: rules}
	spec := core.ToolSpec{Name: "edit"}
	call := core.ToolCall{Name: "edit", Input: `{"file_path":"a.txt","search":"a","replace":"b"}`}

	got := p.Decide(spec, call)
	if !got.Allow || !got.RequiresApproval || got.MatchedRule != "edit:*=ask" {
		t.Fatalf("user edit ask rule should override default allow: %+v", got)
	}
}

func TestRulePolicyExternalDirectory(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	for _, command := range []string{
		"cat /etc/hosts",
		"stat /etc/hosts",
		"du -sh /etc",
		"/bin/cat /etc/hosts",
	} {
		got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || !got.RequiresApproval || got.MatchedRule != "external_directory:*=ask" {
			t.Fatalf("external dir decision for %q = %+v, want external_directory approval", command, got)
		}
	}
}

func TestRulePolicyExternalDirectoryForReadOnlyFileTools(t *testing.T) {
	home, err := os.MkdirTemp(".", "whale-ext-read-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	root := filepath.Join(home, "repo")
	external := filepath.Join(home, "external")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}

	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: root}
	cases := []core.ToolCall{
		{Name: "read_file", Input: `{"file_path":"../external/README.md"}`},
		{Name: "list_dir", Input: `{"path":"../external"}`},
		{Name: "grep", Input: `{"path":"../external","pattern":"needle"}`},
		{Name: "search_files", Input: `{"path":"../external","pattern":"README"}`},
	}
	for _, call := range cases {
		got := p.Decide(core.ToolSpec{Name: call.Name}, call)
		if !got.Allow || !got.RequiresApproval || got.Permission != "external_directory" || got.MatchedRule != "external_directory:*=ask" {
			t.Fatalf("%s external read decision = %+v, want external_directory approval", call.Name, got)
		}
	}

	inside := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":"README.md"}`})
	if !inside.Allow || inside.RequiresApproval {
		t.Fatalf("workspace read should not require external approval: %+v", inside)
	}
}

func TestRulePolicyExternalReadAllowsDiscoveredGlobalSkillReferences(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillDir := filepath.Join(home, ".whale", "skills", "global-skill")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatalf("mkdir skill reference dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: global-skill\ndescription: Global test skill.\n---\n\n# Global Skill\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	refPath := filepath.Join(refDir, "guide.md")
	if err := os.WriteFile(refPath, []byte("reference marker\n"), 0o644); err != nil {
		t.Fatalf("write reference: %v", err)
	}

	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: workspace}
	cases := []core.ToolCall{
		{Name: "read_file", Input: fmt.Sprintf(`{"file_path":%q}`, refPath)},
		{Name: "list_dir", Input: fmt.Sprintf(`{"path":%q}`, refDir)},
		{Name: "grep", Input: fmt.Sprintf(`{"path":%q,"pattern":"reference marker"}`, skillDir)},
		{Name: "search_files", Input: fmt.Sprintf(`{"path":%q,"pattern":"guide"}`, skillDir)},
	}
	for _, call := range cases {
		got := p.Decide(core.ToolSpec{Name: call.Name}, call)
		if !got.Allow || got.RequiresApproval || got.Permission == "external_directory" {
			t.Fatalf("%s skill reference decision = %+v, want direct allow without external_directory approval", call.Name, got)
		}
	}

	outside := filepath.Join(home, "outside", "guide.md")
	got := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: fmt.Sprintf(`{"file_path":%q}`, outside)})
	if !got.Allow || !got.RequiresApproval || got.Permission != "external_directory" {
		t.Fatalf("ordinary external read decision = %+v, want external_directory approval", got)
	}
}

func TestRulePolicyExternalReadWithoutWorkspaceRootDoesNotGrantExternalDirectory(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow}
	got := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":"../outside.txt"}`})
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("custom allow policy decision = %+v, want plain allow without approval", got)
	}
	if got.Permission == "external_directory" || got.Pattern != "" {
		t.Fatalf("custom allow policy should not synthesize external read approval metadata: %+v", got)
	}
}

func TestRulePolicyExternalDirectoryAllowCarriesReadScope(t *testing.T) {
	home, err := os.MkdirTemp(".", "whale-ext-read-allow-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	root := filepath.Join(home, "repo")
	external := filepath.Join(home, "external")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}

	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: external, Action: PermissionAllow})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: root}
	got := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":"../external/README.md"}`})
	if !got.Allow || got.RequiresApproval || got.Permission != "external_directory" || got.Pattern != external {
		t.Fatalf("external_directory allow decision = %+v, want allowed external_directory metadata", got)
	}
}

func TestRulePolicyExternalDirectoryForTempReadOnlyFileTools(t *testing.T) {
	home, err := os.MkdirTemp(".", "whale-ext-read-tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	root := filepath.Join(home, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: root}
	for _, call := range []core.ToolCall{
		{Name: "read_file", Input: `{"file_path":"/tmp/whale-external-read.txt"}`},
		{Name: "read_file", Input: `{"file_path":"/private/tmp/whale-external-read.txt"}`},
	} {
		got := p.Decide(core.ToolSpec{Name: call.Name}, call)
		if !got.Allow || !got.RequiresApproval || got.Permission != "external_directory" || got.MatchedRule != "external_directory:*=ask" {
			t.Fatalf("%s temp external read decision = %+v, want external_directory approval", call.Input, got)
		}
	}
}

func TestRulePolicyExternalReadTreatsWorktreeAsProjectBoundary(t *testing.T) {
	home, err := os.MkdirTemp(".", "whale-ext-worktree-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	worktree := filepath.Join(home, "worktree")
	workspace := filepath.Join(worktree, "subdir")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: workspace, WorktreeRoot: worktree}
	got := p.Decide(core.ToolSpec{Name: "read_file"}, core.ToolCall{Name: "read_file", Input: `{"file_path":"../README.md"}`})
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("worktree read should not require external approval: %+v", got)
	}
}

func TestRulePolicyExternalDirectoryResolvesHomeAndParentRelativePaths(t *testing.T) {
	// Create the home directory outside /tmp/ or /private/tmp/ so the
	// externalDirs filter (which whitelists those prefixes as trusted temp
	// locations) does not suppress paths that should trigger external-directory
	// detection. On Linux CI t.TempDir() returns a path under /tmp/, so use
	// the current directory (the workspace root) instead.
	home, err := os.MkdirTemp(".", "whale-ext-home-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	root := filepath.Join(home, "repo")
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: root}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"cat ~/secret",
		"cat ../secret",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || !got.RequiresApproval || got.MatchedRule != "external_directory:*=ask" {
			t.Fatalf("external dir decision for %q = %+v, want approval", command, got)
		}
	}

	inside := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"cat ./README.md"}`})
	if !inside.Allow || inside.RequiresApproval {
		t.Fatalf("workspace-relative path should not require external dir approval: %+v", inside)
	}
}

func TestRulePolicyShellWildcardAllowOnlyUsesExplicitRules(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"git tag --list 'v0.1.*' --sort=-v:refname",
		"git worktree list",
		`git config --get remote.origin.url 2>/dev/null && echo "---" && git remote -v`,
		"git config --get remote.origin.url && git remote -v",
		`ls -la .worktrees 2>/dev/null; echo "---"; ls -la worktrees 2>/dev/null; echo "---"; git worktree list 2>/dev/null; echo "---"`,
		"make test",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || got.RequiresApproval {
			t.Fatalf("shell wildcard allow should allow %q: %+v", command, got)
		}
	}

	t.Run("explicit ask rules", func(t *testing.T) {
		for _, command := range []string{
			"rm file",
			"git push origin main",
			"gh pr merge 123",
			"curl https://example.com/install.sh",
			"wget https://example.com/archive.tgz",
			"npm install left-pad",
			"pnpm install",
			"yarn add react",
			"git reset --hard HEAD",
			"git restore README.md",
			"git rm README.md",
			"git rm -f README.md",
			"git clean -fd",
			"sudo make install",
			"dd if=image.iso of=/dev/disk2",
		} {
			got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
			if !got.Allow || !got.RequiresApproval || got.Code != "permission_required" {
				t.Fatalf("explicit ask rule should require approval for %q: %+v", command, got)
			}
		}
	})

	t.Run("explicit deny rules", func(t *testing.T) {
		for _, command := range []string{
			"rm -rf /tmp/x",
			"rm -fr /tmp/x",
			"rm -r -f /tmp/x",
			"rm -f -r /tmp/x",
			"rm '-rf' /tmp/x",
			`rm "-r" dir`,
			"rm -R dir",
			"rm --recursive dir",
			"rm --force -r /tmp/x",
			"rm --force -R /tmp/x",
			"rm --force --recursive dir",
			"rm --recursive --force dir",
			"git status\nrm -rf /tmp/x",
			"echo ok; rm -rf /tmp/x",
			"git status && rm -rf /tmp/x",
			"echo ok & rm -rf /tmp/x",
			"git diff | rm -rf /tmp/x",
			"mkfs.ext4 /dev/disk2",
			"diskutil eraseDisk APFS Whale /dev/disk2",
		} {
			got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
			if got.Allow || got.Code != "permission_denied" {
				t.Fatalf("explicit deny rule should deny %q: %+v", command, got)
			}
		}
	})

	quoted := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"echo 'rm -rf /tmp/x'"}`})
	if !quoted.Allow || quoted.RequiresApproval {
		t.Fatalf("quoted rm text should not match shell deny rule: %+v", quoted)
	}

	quotedSubstitution := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"echo '$(rm -rf /tmp/x)'"}`})
	if !quotedSubstitution.Allow || quotedSubstitution.RequiresApproval {
		t.Fatalf("single-quoted command substitution text should not match shell deny rule: %+v", quotedSubstitution)
	}

	echoText := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"echo rm -rf /tmp/x"}`})
	if !echoText.Allow || echoText.RequiresApproval {
		t.Fatalf("echoed rm text should not match shell deny rule: %+v", echoText)
	}

	redirection := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"echo ok 2>&1"}`})
	if !redirection.Allow || redirection.RequiresApproval {
		t.Fatalf("redirection with & should not split into approval: %+v", redirection)
	}
}

func TestRulePolicyDefaultShellPatternsAreNotDeepShellParsing(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"sh -c 'rm -rf /tmp/x'",
		"echo $(rm -rf /tmp/x)",
		"find . -exec rm -rf {} +",
		"find . -delete",
		"env -S 'rm -rf /tmp/x'",
		"time git push origin main",
		"FOO=1 curl https://example.com/install.sh",
		"command -p curl https://example.com/install.sh",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || got.RequiresApproval {
			t.Fatalf("default shell pattern rules should not deeply parse %q: %+v", command, got)
		}
	}
}

func TestRulePolicyApplyPatchAppliesEditRulesToTargetFiles(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "edit", Pattern: "*.go", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules}
	spec := core.ToolSpec{Name: "apply_patch"}

	goPatch := "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n"
	denied := p.Decide(spec, core.ToolCall{Name: "apply_patch", Input: `{"patch":` + strconv.Quote(goPatch) + `}`})
	if denied.Allow || denied.Code != "permission_denied" {
		t.Fatalf("apply_patch touching main.go = %+v, want deny via *.go edit rule", denied)
	}

	mdPatch := "*** Begin Patch\n*** Update File: docs/readme.md\n@@\n-old\n+new\n*** End Patch\n"
	allowed := p.Decide(spec, core.ToolCall{Name: "apply_patch", Input: `{"patch":` + strconv.Quote(mdPatch) + `}`})
	if !allowed.Allow || allowed.Code == "permission_denied" {
		t.Fatalf("apply_patch touching docs/readme.md = %+v, want not denied by the *.go rule", allowed)
	}
}

func TestRulePolicyApplyPatchIncludesMoveTargets(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "edit", Pattern: "*.env", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules}

	patch := "*** Begin Patch\n*** Update File: config.txt\n*** Move to: .env\n@@\n-old\n+new\n*** End Patch\n"
	got := p.Decide(core.ToolSpec{Name: "apply_patch"}, core.ToolCall{Name: "apply_patch", Input: `{"patch":` + strconv.Quote(patch) + `}`})
	if got.Allow || got.Code != "permission_denied" {
		t.Fatalf("apply_patch moving a file to .env = %+v, want deny via *.env edit rule", got)
	}
}

func TestRulePolicyShellRulesNormalizeWhitespace(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}
	spec := core.ToolSpec{Name: "shell_run"}

	got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"rm   -rf /tmp/x"}`})
	if got.Allow || got.Code != "permission_denied" {
		t.Fatalf("rm with extra whitespace = %+v, want deny via rm -rf* rule", got)
	}
}

func TestRulePolicyShellRulesPreserveNewlineBoundaries(t *testing.T) {
	// An explicit allow rule must not auto-allow a second command smuggled
	// onto a later line, and a deny rule must still catch that later line.
	rules := append(DefaultRules(),
		PermissionRule{Permission: "shell", Pattern: "git status", Action: PermissionAllow},
		PermissionRule{Permission: "shell", Pattern: "git status *", Action: PermissionAllow},
	)
	p := RulePolicy{Default: PermissionAllow, Rules: rules}
	spec := core.ToolSpec{Name: "shell_run"}

	smuggled := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git status\nrm -rf /tmp/x"}`})
	if smuggled.Allow || smuggled.Code != "permission_denied" {
		t.Fatalf("multi-line command past an allow prefix = %+v, want deny via rm -rf rule", smuggled)
	}

	// The allow prefix still applies to a genuine single-line command,
	// including one written with irregular spacing.
	single := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git   status --short"}`})
	if !single.Allow || single.RequiresApproval {
		t.Fatalf("single-line allowed command = %+v, want allow without approval", single)
	}
}

func TestRulePolicyShellDenyPrecedenceAcrossSegments(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"git push origin main; rm -rf /tmp/x",
		"sudo make install && rm -rf /tmp/x",
		"git clean -fd | rm -rf /tmp/x",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if got.Allow || got.Code != "permission_denied" {
			t.Fatalf("deny segment should dominate compound command %q: %+v", command, got)
		}
	}
}

func TestRulePolicyShellAllowBeatsRestrictiveFallback(t *testing.T) {
	spec := core.ToolSpec{Name: "shell_run"}

	defaultAllow := RulePolicy{Default: PermissionDeny, Rules: DefaultRules()}
	got := defaultAllow.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git status --short"}`})
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("default shell wildcard allow should beat deny fallback: %+v", got)
	}

	whitelist := RulePolicy{
		Default: PermissionDeny,
		Rules: []PermissionRule{
			{Permission: "shell", Pattern: "git status*", Action: PermissionAllow},
		},
	}
	allowed := whitelist.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git status --short"}`})
	if !allowed.Allow || allowed.RequiresApproval {
		t.Fatalf("explicit shell allow should beat deny fallback: %+v", allowed)
	}

	denied := whitelist.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git log --oneline"}`})
	if denied.Allow || denied.Code != "permission_denied" {
		t.Fatalf("unmatched shell command should still use deny fallback: %+v", denied)
	}
}

func TestRulePolicyUserShellRuleOverridesDefaultAllow(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "shell", Pattern: "git worktree *", Action: PermissionAsk})
	p := RulePolicy{Default: PermissionAllow, Rules: rules}
	got := p.Decide(
		core.ToolSpec{Name: "shell_run"},
		core.ToolCall{Name: "shell_run", Input: `{"command":"git worktree list"}`},
	)
	if !got.Allow || !got.RequiresApproval || got.MatchedRule != "shell:git worktree *=ask" {
		t.Fatalf("user rule should override default allow: %+v", got)
	}
}

func TestRulePolicyExternalDirectoryDenyOverridesShellApproval(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}

	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"cat /etc/hosts"}`})
	if got.Allow || got.Code != "permission_denied" {
		t.Fatalf("external_directory deny should override shell approval: %+v", got)
	}
}

func TestRulePolicyExternalDirectoryScansRedirections(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"echo x >/etc/whale-test",
		"echo x >>/etc/whale-test",
		"cat </etc/passwd",
		"echo x 2>/etc/whale-test",
		"cat foo>/etc/whale-test",
		"echo x >| /etc/whale-test",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if got.Allow || got.Code != "permission_denied" {
			t.Fatalf("redirection %q = %+v, want external_directory deny", command, got)
		}
	}

	inside := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"echo x >./out.txt"}`})
	if !inside.Allow || inside.Code == "permission_denied" {
		t.Fatalf("workspace-relative redirection should not be denied: %+v", inside)
	}
}

func TestRulePolicyExternalDirectoryIgnoresQuotedRedirectionCharacters(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		`printf '%s\n' 'see > /etc/passwd'`,
		`printf "%s\n" "see < /etc/passwd"`,
		`echo \> /etc/passwd`,
		`printf foo \< /etc/passwd`,
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || got.Code == "permission_denied" {
			t.Fatalf("quoted redirection text %q = %+v, want no external_directory deny", command, got)
		}
	}
}

func TestRulePolicyExternalDirectoryRedirectionRequiresApprovalByDefault(t *testing.T) {
	home, err := os.MkdirTemp(".", "whale-ext-redir-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })

	root := filepath.Join(home, "repo")
	external := filepath.Join(home, "external")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}

	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: root}
	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"echo hello > ` + external + `/whale-test.txt"}`})
	if !got.Allow || !got.RequiresApproval || got.Permission != "external_directory" {
		t.Fatalf("external redirection = %+v, want external_directory approval", got)
	}
}

func TestRulePolicyTempRedirectionRequiresApprovalByDefault(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"echo hello > /tmp/whale-test.txt"}`})
	if !got.Allow || !got.RequiresApproval || got.Permission != "external_directory" || got.Pattern != "/tmp" {
		t.Fatalf("temp redirection = %+v, want external_directory /tmp approval", got)
	}
}

func TestRulePolicyDynamicRedirectionRequiresApproval(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		`echo hello > "$(dirname "$(pwd)")/opencode-dev/whale-test.txt"`,
		`printf hello > "$OUT_FILE"`,
		`printf hello > ~/whale-test.txt`,
		"printf hello > `pwd`/out.txt",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || !got.RequiresApproval || got.Permission != "external_directory" || got.Pattern != dynamicShellRedirectionTarget {
			t.Fatalf("dynamic redirection %q = %+v, want external_directory approval for dynamic target", command, got)
		}
	}
}

func TestRulePolicyExternalDirectoryKeepsOperandsBeforeAttachedRedirections(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"cat /etc/hosts>/tmp/out",
		"cp local /etc/out>/tmp/log",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if got.Allow || got.Code != "permission_denied" {
			t.Fatalf("external operand before attached redirection %q = %+v, want external_directory deny", command, got)
		}
	}
}

func TestRulePolicyExternalDirectorySkipsFlagValuePaths(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"go test -coverprofile=/etc/out ./...",
		"git diff --output=/etc/out",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || got.Code == "permission_denied" {
			t.Fatalf("flag value path %q = %+v, want no external_directory deny", command, got)
		}
	}

	inside := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"go test -coverprofile=./cover.out ./..."}`})
	if inside.Code == "permission_denied" {
		t.Fatalf("workspace-relative flag value should not be denied: %+v", inside)
	}
}

func TestRulePolicyExternalDirectoryMatchesDirectoryOperandItself(t *testing.T) {
	home, err := os.MkdirTemp(".", "whale-ext-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	root := filepath.Join(home, "repo")
	extDir := filepath.Join(home, "external-dir")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rule := PermissionRule{Permission: "external_directory", Pattern: extDir, Action: PermissionDeny}
	p := RulePolicy{Default: PermissionAllow, Rules: append(DefaultRules(), rule), WorkspaceRoot: root}

	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"ls ` + extDir + `"}`})
	if got.Allow || got.MatchedRule != ruleLabel(rule) {
		t.Fatalf("ls of external directory = %+v, want deny matching the directory's own rule", got)
	}
}

func TestRulePolicyMutatingCapabilityToolRequiresApproval(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}

	mutating := core.ToolSpec{Name: "delete_project", Capabilities: []string{"mutates_state"}}
	got := p.Decide(mutating, core.ToolCall{Name: "delete_project", Input: `{}`})
	if !got.Allow || !got.RequiresApproval || got.Code != "permission_required" {
		t.Fatalf("mutating plugin tool should require approval: %+v", got)
	}

	// A custom tool with no mutating capability stays on the default allow.
	readOnly := core.ToolSpec{Name: "list_projects"}
	if got := p.Decide(readOnly, core.ToolCall{Name: "list_projects", Input: `{}`}); !got.Allow || got.RequiresApproval {
		t.Fatalf("non-mutating custom tool should not require approval: %+v", got)
	}
}

func TestRulePolicyMutatingToolAllowBeatsRestrictiveFallback(t *testing.T) {
	mutating := core.ToolSpec{Name: "delete_project", Capabilities: []string{"mutates_state"}}
	call := core.ToolCall{Name: "delete_project", Input: `{}`}
	p := RulePolicy{
		Default: PermissionDeny,
		Rules: []PermissionRule{
			{Permission: "mutating_tool", Pattern: "delete_project", Action: PermissionAllow},
		},
	}

	got := p.Decide(mutating, call)
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("mutating_tool allow should beat raw tool fallback: %+v", got)
	}
}

func TestRulePolicyPathSpecificRecursiveRemoveAllowOverridesDeny(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "shell", Pattern: "rm -rf ./dist*", Action: PermissionAllow})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	allowed := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rf ./dist/cache"}`})
	if !allowed.Allow || allowed.RequiresApproval {
		t.Fatalf("path-specific rm -rf allow should permit the command: %+v", allowed)
	}

	// A recursive remove outside the allowed path still hits the default deny.
	denied := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rf /etc"}`})
	if denied.Allow || denied.Code != "permission_denied" {
		t.Fatalf("rm -rf outside the allowed path should still be denied: %+v", denied)
	}
}

func TestRulePolicyUserShellWildcardAllowOverridesDefaultShellPatterns(t *testing.T) {
	rules := append(DefaultRules(),
		PermissionRule{Permission: "external_directory", Pattern: "/tmp", Action: PermissionAllow},
		PermissionRule{Permission: "shell", Pattern: "*", Action: PermissionAllow},
	)
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}

	got := p.Decide(
		core.ToolSpec{Name: "shell_run"},
		core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rf /tmp/x"}`},
	)
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("user shell wildcard allow should override default shell pattern deny: %+v", got)
	}
}
