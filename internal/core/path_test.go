package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathInsideAllowsMissingChildUnderSymlinkedParent(t *testing.T) {
	tmp := t.TempDir()
	realWorkspace := filepath.Join(tmp, "real-workspace")
	if err := os.Mkdir(realWorkspace, 0o755); err != nil {
		t.Fatalf("mkdir real workspace: %v", err)
	}
	linkWorkspace := filepath.Join(tmp, "workspace-link")
	if err := os.Symlink(realWorkspace, linkWorkspace); err != nil {
		t.Fatalf("symlink workspace: %v", err)
	}

	ok, err := PathInside(filepath.Join(linkWorkspace, "new", "file.txt"), linkWorkspace)
	if err != nil {
		t.Fatalf("PathInside: %v", err)
	}
	if !ok {
		t.Fatal("missing child under symlinked parent should be inside")
	}
}

func TestPathInsideRejectsMissingChildThroughSymlinkEscape(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	outside := filepath.Join(tmp, "outside")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "outside-link")); err != nil {
		t.Fatalf("symlink outside: %v", err)
	}

	ok, err := PathInside(filepath.Join(workspace, "outside-link", "new.txt"), workspace)
	if err != nil {
		t.Fatalf("PathInside: %v", err)
	}
	if ok {
		t.Fatal("missing child through symlink escape should be outside")
	}
}
