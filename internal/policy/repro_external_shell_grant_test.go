package policy

import (
	"strconv"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// Regression for session 019edf3b: granting the external directory /ext/src
// "for session" must also cover later shell commands touching a subpath
// /ext/src/sub — regardless of command shape (bare reads, `||` lists, ...) —
// the same way it already covers built-in file tools. A command with its own
// shell ask rule (rm) must still require its command-scoped approval.
//
// Before the fix, shell_run approvals were always command-scoped, so the
// directory grant was never consulted and the subpath re-prompted.
func TestExternalDirectoryGrantCoversSafeReadShellSubpath(t *testing.T) {
	const workspace = "/repo"

	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: workspace}

	// Establish the grant by reading inside the dir with a built-in file tool.
	readCall := core.ToolCall{Name: "list_dir", Input: `{"path":"/ext/src"}`}
	readDecision := p.Decide(core.ToolSpec{Name: "list_dir"}, readCall)
	if !readDecision.RequiresApproval || readDecision.Permission != "external_directory" {
		t.Fatalf("list_dir decision = %+v, want external_directory approval", readDecision)
	}
	readKeys := ApprovalKeysForDecision(readCall, readDecision)

	cache := NewSessionApprovalCache()
	cache.GrantAll("s", readKeys) // "allow for session" on the external dir

	// Shell commands into the same subtree are now covered, including a `||`
	// list that shellrisk does not classify as safe-read (the case the user hit
	// in session 019edf3b).
	for _, command := range []string{
		"cat /ext/src/sub/worktree.go",
		`ls -la /ext/src/sub 2>/dev/null || echo "missing"`,
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		decision := p.Decide(core.ToolSpec{Name: "shell_run"}, call)
		if !decision.RequiresApproval || decision.Permission != "external_directory" {
			t.Fatalf("shell decision for %q = %+v, want external_directory approval", command, decision)
		}
		keys := ApprovalKeysForDecision(call, decision)
		if !cache.HasAll("s", keys) {
			t.Fatalf("shell subpath %q should be covered by the external dir grant; keys=%v", command, keys)
		}
	}

	// A command with its own shell ask rule (rm) still requires its
	// command-scoped approval, even inside an already-granted directory: the
	// directory grant covers the external_directory requirement but not the
	// independent rm approval.
	rmCall := core.ToolCall{Name: "shell_run", Input: `{"command":"rm /ext/src/sub/a.go"}`}
	rmDecision := p.Decide(core.ToolSpec{Name: "shell_run"}, rmCall)
	if !rmDecision.RequiresApproval {
		t.Fatalf("rm into external dir should require approval, got %+v", rmDecision)
	}
	rmKeys := ApprovalKeysForDecision(rmCall, rmDecision)
	if cache.HasAll("s", rmKeys) {
		t.Fatalf("external dir grant must not auto-approve rm (it has its own ask rule); keys=%v", rmKeys)
	}
}
