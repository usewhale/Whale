package policy

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestShellExecutionRequestOnlyComesFromShellRun(t *testing.T) {
	req, ok := shellExecutionRequestFromToolCall(core.ToolCall{
		Name:  "shell_run",
		Input: `{"command":" go test ./... ","cwd":"internal/policy"}`,
	})
	if !ok {
		t.Fatal("expected shell_run to produce a shell execution request")
	}
	if req.Source != "shell_run" || req.Command != "go test ./..." || req.CWD != "internal/policy" {
		t.Fatalf("unexpected shell execution request: %+v", req)
	}

	if _, ok := shellExecutionRequestFromToolCall(core.ToolCall{
		Name:  "write_stdin",
		Input: `{"task_id":"task-1","chars":"rm -rf /tmp/x\n"}`,
	}); ok {
		t.Fatal("write_stdin must not produce a shell execution request")
	}
}

func TestExecBoundaryPolicyUsesProgramArgvAndBasenameFallback(t *testing.T) {
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "shell", Pattern: "rm -rf*", Action: PermissionDeny},
			{Permission: "shell", Pattern: "npm install*", Action: PermissionAsk},
		},
	}
	denied := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/bin/rm",
		Argv:    []string{"rm", "-rf", "/tmp/target"},
		CWD:     "/repo",
	})
	if denied.Allow || denied.MatchedRule != "shell:rm -rf*=deny" || denied.Pattern != "rm -rf /tmp/target" {
		t.Fatalf("path-qualified rm should match basename deny rule, got %+v", denied)
	}

	ask := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/usr/bin/npm",
		Argv:    []string{"npm", "install", "left-pad"},
		CWD:     "/repo",
	})
	if !ask.Allow || !ask.RequiresApproval || ask.MatchedRule != "shell:npm install*=ask" {
		t.Fatalf("path-qualified npm should match basename ask rule, got %+v", ask)
	}
}

func TestExecBoundaryPolicyPrefersExactProgramRule(t *testing.T) {
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "shell", Pattern: "tool *", Action: PermissionDeny},
			{Permission: "shell", Pattern: "/safe/bin/tool *", Action: PermissionAllow},
		},
	}
	got := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/safe/bin/tool",
		Argv:    []string{"tool", "status"},
		CWD:     "/repo",
	})
	if !got.Allow || got.RequiresApproval || got.MatchedRule != "shell:/safe/bin/tool *=allow" {
		t.Fatalf("exact program rule should win before basename fallback, got %+v", got)
	}
}

func TestExecBoundaryPolicyEnforcesExternalDirectory(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}

	got := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/bin/cat",
		Argv:    []string{"cat", "/etc/hosts"},
		CWD:     "/repo",
	})
	if got.Allow || got.Permission != "external_directory" || got.MatchedRule != "external_directory:*=deny" {
		t.Fatalf("external exec operand = %+v, want external_directory deny", got)
	}
}

func TestExecBoundaryPolicyUsesExecCWDForExternalDirectory(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}

	got := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/usr/bin/touch",
		Argv:    []string{"touch", "x"},
		CWD:     "/tmp",
	})
	if got.Allow || got.Permission != "external_directory" || got.Pattern != "/tmp" {
		t.Fatalf("external cwd relative write = %+v, want external_directory /tmp deny", got)
	}

	inside := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/usr/bin/touch",
		Argv:    []string{"touch", "x"},
		CWD:     "/repo/subdir",
	})
	if !inside.Allow || inside.RequiresApproval || inside.Permission == "external_directory" {
		t.Fatalf("workspace cwd relative write = %+v, want allow without external_directory", inside)
	}
}

func TestExecBoundaryPolicyEnforcesExternalDirectoryForImplicitCWD(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "*", Action: PermissionDeny})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}

	for _, req := range []ExecBoundaryRequest{
		{Program: "/bin/ls", Argv: []string{"ls"}, CWD: "/tmp"},
		{Program: "/usr/bin/du", Argv: []string{"du", "-sh"}, CWD: "/tmp"},
	} {
		got := p.DecideExecBoundary(req)
		if got.Allow || got.Permission != "external_directory" || got.Pattern != "/tmp" {
			t.Fatalf("implicit cwd exec %+v = %+v, want external_directory /tmp deny", req, got)
		}
	}

	inside := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/bin/ls",
		Argv:    []string{"ls", "-la"},
		CWD:     "/repo/subdir",
	})
	if !inside.Allow || inside.RequiresApproval || inside.Permission == "external_directory" {
		t.Fatalf("workspace implicit cwd exec = %+v, want allow without external_directory", inside)
	}

	explicit := p.DecideExecBoundary(ExecBoundaryRequest{
		Program: "/bin/ls",
		Argv:    []string{"ls", "/etc"},
		CWD:     "/repo",
	})
	if explicit.Allow || explicit.Permission != "external_directory" || explicit.Pattern != "/etc" {
		t.Fatalf("explicit external operand = %+v, want external_directory /etc deny", explicit)
	}
}
