package policy

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestDefaultToolPolicyRequiresApprovalForMCPTools(t *testing.T) {
	decision := DefaultToolPolicy{}.Decide(
		core.ToolSpec{Name: "mcp__github__create_issue"},
		core.ToolCall{Name: "mcp__github__create_issue", Input: `{}`},
	)
	if !decision.Allow || !decision.RequiresApproval {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestDefaultToolPolicyRequiresApprovalForReadOnlyMCPTools(t *testing.T) {
	decision := DefaultToolPolicy{}.Decide(
		core.ToolSpec{Name: "mcp__fs__read", ReadOnly: true},
		core.ToolCall{Name: "mcp__fs__read", Input: `{}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.Code != "permission_required" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestRulePolicyMatchesMCPByToolName(t *testing.T) {
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "mcp", Pattern: "*", Action: PermissionAllow},
			{Permission: "mcp", Pattern: "mcp__github__create_issue", Action: PermissionAsk},
		},
	}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__github__create_issue"},
		core.ToolCall{Name: "mcp__github__create_issue", Input: `{"name":"unrelated","path":"/tmp/x"}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.MatchedRule != "mcp:mcp__github__create_issue=ask" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestRulePolicyMCPArgumentsDoNotChangePermissionTarget(t *testing.T) {
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "mcp", Pattern: "*", Action: PermissionAllow},
			{Permission: "mcp", Pattern: "/etc/passwd", Action: PermissionDeny},
		},
	}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__fs__read"},
		core.ToolCall{Name: "mcp__fs__read", Input: `{"path":"/etc/passwd"}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code == "permission_denied" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestRulePolicyMCPPathOutsideWorkspaceRequiresExternalDirectoryApproval(t *testing.T) {
	root, err := os.MkdirTemp(".", "whale-mcp-ext-root-*")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	workspace := filepath.Join(root, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside", "file.txt")
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: workspace}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__fs__read_file", Capabilities: []string{"mcp_filesystem"}},
		core.ToolCall{Name: "mcp__fs__read_file", Input: `{"path":` + strconv.Quote(outside) + `}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.MatchedRule != "external_directory:*=ask" {
		t.Fatalf("decision: %+v, want external_directory approval", decision)
	}
	if decision.Permission != "external_directory" || decision.Pattern != filepath.Dir(outside) {
		t.Fatalf("decision permission metadata = %q %q, want external_directory %q", decision.Permission, decision.Pattern, filepath.Dir(outside))
	}
}

func TestRulePolicyNonFilesystemMCPPathDoesNotRequireExternalDirectoryApproval(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "mcp", Pattern: "*", Action: PermissionAllow},
			{Permission: "external_directory", Pattern: "*", Action: PermissionDeny},
		},
		WorkspaceRoot: workspace,
	}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__github__get_file"},
		core.ToolCall{Name: "mcp__github__get_file", Input: `{"path":"/org/repo/issues/123","owner":"usewhale"}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code == "permission_denied" {
		t.Fatalf("decision: %+v, want non-filesystem MCP path field to ignore external_directory rules", decision)
	}
}

func TestRulePolicyMCPPathOutsideServerAllowedDirsDeniesBeforeApproval(t *testing.T) {
	root, err := os.MkdirTemp(".", "whale-mcp-policy-root-*")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	workspace := filepath.Join(root, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: workspace}

	decision := p.Decide(
		core.ToolSpec{
			Name:         "mcp__fs__read_file",
			Capabilities: []string{"mcp_filesystem", "mcp_filesystem_allowed_dir:/tmp"},
		},
		core.ToolCall{Name: "mcp__fs__read_file", Input: `{"path":` + strconv.Quote(filepath.Join(workspace, "AGENTS.md")) + `}`},
	)
	if decision.Allow || decision.RequiresApproval || decision.Code != "mcp_allowed_dirs_denied" {
		t.Fatalf("decision: %+v, want allowed-dirs denial before approval", decision)
	}
}

func TestRulePolicyMCPAllowedDirsCanonicalizeSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	realAllowed := filepath.Join(root, "real-allowed")
	if err := os.MkdirAll(realAllowed, 0o755); err != nil {
		t.Fatal(err)
	}
	allowedLink := filepath.Join(root, "allowed-link")
	if err := os.Symlink(realAllowed, allowedLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	workspace := filepath.Join(root, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: workspace}

	decision := p.Decide(
		core.ToolSpec{
			Name:         "mcp__fs__read_file",
			Capabilities: []string{"mcp_filesystem", "mcp_filesystem_allowed_dir:" + allowedLink},
		},
		core.ToolCall{Name: "mcp__fs__read_file", Input: `{"path":` + strconv.Quote(filepath.Join(realAllowed, "missing.txt")) + `}`},
	)
	if decision.Code == "mcp_allowed_dirs_denied" {
		t.Fatalf("decision: %+v, want symlink-equivalent path to pass allowed-dir preflight", decision)
	}
}

func TestRulePolicyMCPPathInsideWorkspaceDoesNotRequireExternalDirectoryApproval(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "mcp", Pattern: "*", Action: PermissionAllow},
			{Permission: "external_directory", Pattern: "*", Action: PermissionDeny},
		},
		WorkspaceRoot: workspace,
	}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__fs__read_file"},
		core.ToolCall{Name: "mcp__fs__read_file", Input: `{"path":` + strconv.Quote(filepath.Join(workspace, "README.md")) + `}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code == "permission_denied" {
		t.Fatalf("decision: %+v, want MCP allow without external_directory", decision)
	}
}
