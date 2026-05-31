package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerRestoreModifiedAndCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(workspace, "existing.txt")
	if err := os.WriteFile(existing, []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(sessionsDir, workspace)
	if err := manager.CreateSnapshot("s1", "m-1"); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := manager.TrackFile("s1", "m-1", existing); err != nil {
		t.Fatalf("TrackFile existing: %v", err)
	}
	created := filepath.Join(workspace, "created.txt")
	if err := manager.TrackFile("s1", "m-1", created); err != nil {
		t.Fatalf("TrackFile created: %v", err)
	}
	if err := os.WriteFile(existing, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := manager.Restore("s1", "m-1")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if report.FilesRestored != 1 || report.FilesDeleted != 1 {
		t.Fatalf("report = %+v, want one restore and one delete", report)
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("existing content = %q, want before", got)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file still exists or stat failed: %v", err)
	}
}

func TestManagerIgnoresPathsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(filepath.Join(dir, "sessions"), filepath.Join(dir, "workspace"))
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.TrackFile("s1", "m-1", outside); err != nil {
		t.Fatalf("TrackFile outside: %v", err)
	}
	if manager.CanRestore("s1", "m-1") {
		t.Fatal("outside workspace file should not create a restorable checkpoint")
	}
}

func TestManagerSnapshotsCarryTrackedFilesForward(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(workspace, "a.txt")
	b := filepath.Join(workspace, "b.txt")
	if err := os.WriteFile(a, []byte("a0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b0"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(sessionsDir, workspace)
	if err := manager.CreateSnapshot("s1", "m-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.TrackFile("s1", "m-1", a); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("a1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.CreateSnapshot("s1", "m-2"); err != nil {
		t.Fatal(err)
	}
	if err := manager.TrackFile("s1", "m-2", b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("a2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b1"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.Restore("s1", "m-2"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	gotA, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotA) != "a1" || string(gotB) != "b0" {
		t.Fatalf("restored content = %q/%q, want a1/b0", gotA, gotB)
	}
}
