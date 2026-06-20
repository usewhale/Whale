package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/policy/effects"
)

// TestExternalDirectoryGrantsCrossSessionsWithinProject verifies that an
// external_directory grant made in one session is honored by a different
// session in the same project (project-scoped), while a shell-command grant
// stays scoped to its own session.
func TestExternalDirectoryGrantsCrossSessionsWithinProject(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	project := filepath.Join(dir, "project")
	prov := &persistApprovalProvider{}
	reg := NewToolRegistry([]Tool{writeLikeTool{}})

	newAgent := func() *Agent {
		return NewAgentWithRegistry(prov, st, reg,
			WithProjectMemory(true, 0, nil, project),
		)
	}

	dirKey := effects.GrantKey(effects.ExternalDirectory, filepath.Join(dir, "external"))
	childKey := effects.GrantKey(effects.ExternalDirectory, filepath.Join(dir, "external", "sub"))
	cmdKey := "shell_run|cmd:git status"

	// Grant both an external_directory key and a shell-command key in session A.
	a1 := newAgent()
	a1.persistApprovals(context.Background(), "session-a", []string{dirKey, cmdKey})

	// A fresh agent / different session in the same project must inherit the
	// directory grant (including its subtree) but not the shell-command grant.
	a2 := newAgent()
	a2.ensureApprovalCacheLoaded(context.Background(), "session-b")

	if !a2.approvalCache.HasAll("session-b", []string{dirKey}) {
		t.Fatalf("external_directory grant did not cross sessions within project")
	}
	if !a2.approvalCache.HasAll("session-b", []string{childKey}) {
		t.Fatalf("external_directory grant did not widen to subtree across sessions")
	}
	if a2.approvalCache.HasAll("session-b", []string{cmdKey}) {
		t.Fatalf("shell-command grant leaked across sessions; should be session-scoped")
	}
}

// TestExternalDirectoryGrantsStaySessionScopedWithoutProject verifies that
// when no project root is known the directory grant is not promoted, so it
// stays session-scoped (the prior behavior).
func TestExternalDirectoryGrantsStaySessionScopedWithoutProject(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	prov := &persistApprovalProvider{}
	reg := NewToolRegistry([]Tool{writeLikeTool{}})

	dirKey := effects.GrantKey(effects.ExternalDirectory, filepath.Join(dir, "external"))

	a1 := NewAgentWithRegistry(prov, st, reg)
	if scope := a1.projectApprovalScope(); scope != "" {
		t.Fatalf("expected empty project scope without workspace root, got %q", scope)
	}
	a1.persistApprovals(context.Background(), "session-a", []string{dirKey})

	a2 := NewAgentWithRegistry(prov, st, reg)
	a2.ensureApprovalCacheLoaded(context.Background(), "session-b")
	if a2.approvalCache.HasAll("session-b", []string{dirKey}) {
		t.Fatalf("directory grant crossed sessions without a project scope")
	}
}
