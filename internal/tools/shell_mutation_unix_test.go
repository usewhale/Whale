//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/checkpoint"
)

func TestShellRunRecordsCheckpointForWorkspaceMutations(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, err := NewToolset(root)
	if err != nil {
		t.Fatalf("NewToolset: %v", err)
	}
	manager := checkpoint.NewManager(sessionsDir, root)
	ctx := checkpoint.WithRecorder(context.Background(), manager.Recorder("s1", "m1"))

	res, err := ts.shellRun(ctx, tc("shell_run", map[string]any{
		"command": "printf after > existing.txt && printf created > created.txt",
	}))
	if err != nil || res.IsError() {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, res)
	}
	if !manager.CanRestore("s1", "m1") {
		t.Fatal("expected shell mutation checkpoint")
	}
	if _, err := manager.Restore("s1", "m1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("existing.txt = %q, want before", got)
	}
	if _, err := os.Stat(filepath.Join(root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt should be removed after restore, err=%v", err)
	}
}
