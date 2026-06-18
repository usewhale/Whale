package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHomePrefixedInWorkspaceReadAllowed guards against the regression where a
// file that lives INSIDE the workspace root was wrongly denied with
// "path escapes workspace" when referenced via a ~/-prefixed path, even though
// the exact same file reads fine via a relative path.
func TestHomePrefixedInWorkspaceReadAllowed(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skipf("no home dir: %v", err)
	}

	// Workspace root must live under $HOME so a ~/ path can point into it.
	root, err := os.MkdirTemp(home, "whale-repro-*")
	if err != nil {
		t.Fatalf("mkdir temp under home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("hello agents\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ts, err := NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	rel, err := filepath.Rel(home, root)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	homePath := "~/" + filepath.ToSlash(filepath.Join(rel, "AGENTS.md"))

	ctx := context.Background()

	// Control: relative path inside workspace must succeed.
	relRes, err := ts.readFile(ctx, tc("read_file", map[string]any{"file_path": "AGENTS.md"}))
	if err != nil || relRes.IsError() {
		t.Fatalf("relative read should succeed: err=%v res=%+v", err, relRes)
	}

	// The same in-workspace file via a ~/ path must also succeed.
	homeRes, err := ts.readFile(ctx, tc("read_file", map[string]any{"file_path": homePath}))
	if err != nil {
		t.Fatalf("home read returned go error: %v", err)
	}
	if homeRes.IsError() {
		t.Fatalf("in-workspace file denied via ~/ path %q: %s", homePath, homeRes.ModelText)
	}
	if !strings.Contains(homeRes.ModelText, "hello agents") {
		t.Fatalf("unexpected content via ~/ path: %s", homeRes.ModelText)
	}
}
