package policy

import (
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

func TestRulePolicyDefaultEditRequiresApproval(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}
	spec := core.ToolSpec{Name: "edit"}
	call := core.ToolCall{Name: "edit", Input: `{"file_path":"a.txt","search":"a","replace":"b"}`}

	got := p.Decide(spec, call)
	if !got.Allow || !got.RequiresApproval {
		t.Fatalf("edit should require approval under the default posture: %+v", got)
	}
}

func TestRulePolicyExternalDirectory(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"cat /etc/hosts"}`})
	if !got.Allow || !got.RequiresApproval || got.MatchedRule != "external_directory:*=ask" {
		t.Fatalf("external dir decision = %+v, want external_directory approval", got)
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

func TestRulePolicyShellWildcardAllowStillUsesRiskClassifier(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules()}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"echo ok; rm -rf ./dir",
		"rm -fr ./dir",
		"make test",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !got.Allow || !got.RequiresApproval {
			t.Fatalf("shell wildcard allow should require approval for %q: %+v", command, got)
		}
	}

	safe := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git status --short"}`})
	if !safe.Allow || safe.RequiresApproval {
		t.Fatalf("shell wildcard allow should still allow safe read-only command: %+v", safe)
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
	// Migrated allow_shell_prefixes become an exact rule plus a "<prefix> *"
	// glob. Neither must auto-allow a second command smuggled onto a later
	// line, and a deny rule must still catch that later line.
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

func TestRulePolicyExternalDirectoryDenyOverridesShellApproval(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}

	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":"echo x > /etc/whale-test"}`})
	if got.Allow || got.Code != "permission_denied" {
		t.Fatalf("external_directory deny should override shell approval: %+v", got)
	}
}

func TestRulePolicyExternalDirectoryCatchesSpacelessRedirections(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"echo x >/etc/whale-test",
		"echo x >>/etc/whale-test",
		"cat </etc/passwd",
		"echo x 2>/etc/whale-test",
		"cat foo>/etc/whale-test",
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

func TestRulePolicyExternalDirectoryCatchesFlagValuePaths(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	spec := core.ToolSpec{Name: "shell_run"}

	for _, command := range []string{
		"go test -coverprofile=/etc/out ./...",
		"git diff --output=/etc/out",
	} {
		got := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if got.Allow || got.Code != "permission_denied" {
			t.Fatalf("flag value path %q = %+v, want external_directory deny", command, got)
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
