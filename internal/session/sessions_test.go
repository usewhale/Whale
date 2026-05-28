package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListSessionsIgnoresLegacyPlanSummary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.plan.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write legacy plan: %v", err)
	}
	got, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("unexpected sessions: %+v", got)
	}
}

func TestListSessionsIgnoresToolInputEventSidecars(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.tool_input_events.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.approval_events.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write approval sidecar: %v", err)
	}
	got, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("unexpected sessions: %+v", got)
	}
}
