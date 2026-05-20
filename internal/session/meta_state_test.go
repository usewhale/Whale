package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdateSessionMetaConcurrentCostAndPatch(t *testing.T) {
	dir := t.TempDir()
	const iters = 200
	const delta = 0.01
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := UpdateSessionMeta(dir, "s1", func(m *SessionMeta) { m.TotalCostUSD += delta }); err != nil {
				t.Errorf("update meta: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := PatchSessionMeta(dir, "s1", SessionMeta{TurnCount: i + 1}); err != nil {
				t.Errorf("patch meta: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	got, err := LoadSessionMeta(dir, "s1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wantCost := delta * float64(iters)
	if diff := got.TotalCostUSD - wantCost; diff < -1e-6 || diff > 1e-6 {
		t.Fatalf("TotalCostUSD = %v, want %v (lost concurrent updates)", got.TotalCostUSD, wantCost)
	}
	if got.TurnCount != iters {
		t.Fatalf("TurnCount = %d, want %d (lost patch updates)", got.TurnCount, iters)
	}
}

func TestSessionMetaPatchAndLoad(t *testing.T) {
	dir := t.TempDir()
	_, err := PatchSessionMeta(dir, "s1", SessionMeta{
		Workspace:          "/tmp/work",
		Branch:             "main",
		Title:              "first request",
		TurnCount:          2,
		Summary:            "hello",
		WorktreeName:       "feature",
		WorktreePath:       "/tmp/worktrees/feature",
		WorktreeBranch:     "worktree-feature",
		OriginalWorkspace:  "/tmp/original",
		OriginalBranch:     "main",
		OriginalHeadCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("patch meta: %v", err)
	}
	got, err := LoadSessionMeta(dir, "s1")
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	if got.Workspace != "/tmp/work" || got.Branch != "main" || got.Title != "first request" || got.TurnCount != 2 || got.Summary != "hello" {
		t.Fatalf("unexpected meta: %+v", got)
	}
	if got.WorktreeName != "feature" || got.WorktreePath != "/tmp/worktrees/feature" || got.WorktreeBranch != "worktree-feature" || got.OriginalWorkspace != "/tmp/original" || got.OriginalBranch != "main" || got.OriginalHeadCommit != "abc123" {
		t.Fatalf("unexpected meta: %+v", got)
	}
}

func TestSessionMetaTitleIsSetOnce(t *testing.T) {
	dir := t.TempDir()
	if _, err := PatchSessionMeta(dir, "s1", SessionMeta{Title: "first request"}); err != nil {
		t.Fatalf("patch title: %v", err)
	}
	if _, err := PatchSessionMeta(dir, "s1", SessionMeta{Title: "second request"}); err != nil {
		t.Fatalf("patch second title: %v", err)
	}
	got, err := LoadSessionMeta(dir, "s1")
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	if got.Title != "first request" {
		t.Fatalf("expected first title to be preserved, got %q", got.Title)
	}
}

func TestListSessionsIncludesMeta(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSessionMeta(dir, "s1", SessionMeta{Branch: "dev", TurnCount: 3}); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	out, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 session, got %d", len(out))
	}
	if out[0].Meta.Branch != "dev" || out[0].Meta.TurnCount != 3 {
		t.Fatalf("unexpected meta: %+v", out[0].Meta)
	}
}

func TestListSessionsHidesSubagentSessions(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSessionMeta(dir, "parent", SessionMeta{Title: "parent"}); err != nil {
		t.Fatalf("save parent meta: %v", err)
	}
	if err := SaveSessionMeta(dir, "child", SessionMeta{Kind: "subagent", ParentSessionID: "parent", Status: "completed"}); err != nil {
		t.Fatalf("save child meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "parent.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write parent session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write child session: %v", err)
	}
	out, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(out) != 1 || out[0].ID != "parent" {
		t.Fatalf("expected only parent session, got %+v", out)
	}
}

func TestListSessionsConversationTitlePriorityAndFallback(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSessionMeta(dir, "titled", SessionMeta{Title: "Saved title"}); err != nil {
		t.Fatalf("save titled meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "titled.jsonl"), []byte("{\"Role\":\"user\",\"Text\":\"fallback\"}\n"), 0o600); err != nil {
		t.Fatalf("write titled session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fallback.jsonl"), []byte("{\"Role\":\"assistant\",\"Text\":\"skip\"}\n{\"Role\":\"user\",\"Text\":\"  hello\\nworld  \"}\n"), 0o600); err != nil {
		t.Fatalf("write fallback session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.jsonl"), []byte("{\"Role\":\"user\",\"Hidden\":true,\"Text\":\"hidden\"}\n"), 0o600); err != nil {
		t.Fatalf("write empty session: %v", err)
	}

	out, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	byID := map[string]SessionSummary{}
	for _, s := range out {
		byID[s.ID] = s
	}
	if byID["titled"].Conversation != "Saved title" {
		t.Fatalf("expected saved title, got %q", byID["titled"].Conversation)
	}
	if byID["fallback"].Conversation != "hello world" {
		t.Fatalf("expected first visible user fallback, got %q", byID["fallback"].Conversation)
	}
	if byID["empty"].Conversation != "(no message yet)" {
		t.Fatalf("expected empty placeholder, got %q", byID["empty"].Conversation)
	}
}
