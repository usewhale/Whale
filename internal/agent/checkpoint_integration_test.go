package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/checkpoint"
	whaletools "github.com/usewhale/whale/internal/tools"
)

type checkpointProvider struct {
	calls int
}

func (p *checkpointProvider) StreamResponse(_ context.Context, _ []agent.Message, _ []agent.Tool) <-chan agent.ProviderEvent {
	out := make(chan agent.ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonToolUse, ToolCalls: []agent.ToolCall{
			{ID: "tc-1", Name: "write", Input: `{"file_path":"notes.txt","content":"after"}`},
		}}}
	} else {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

type shellCheckpointProvider struct {
	calls int
}

func (p *shellCheckpointProvider) StreamResponse(_ context.Context, _ []agent.Message, _ []agent.Tool) <-chan agent.ProviderEvent {
	out := make(chan agent.ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonToolUse, ToolCalls: []agent.ToolCall{
			{ID: "tc-1", Name: "shell_run", Input: `{"command":"printf after > notes.txt && printf created > generated.txt"}`},
		}}}
	} else {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

func TestAgentRecordsCheckpointForToolMutations(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	toolset, err := whaletools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	manager := checkpoint.NewManager(sessionsDir, root)
	store := agent.NewInMemoryStore()
	a := agent.NewAgentWithRegistry(
		&checkpointProvider{},
		store,
		agent.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(agent.RulePolicy{Default: agent.PermissionAllow}),
		agent.WithSessionsDir(sessionsDir),
		agent.WithCheckpoints(manager),
	)

	if _, err := a.RunSession(context.Background(), "s-checkpoint", "edit notes"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	msgs, err := store.List(context.Background(), "s-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || msgs[0].ID == "" {
		t.Fatalf("missing user message: %+v", msgs)
	}
	if !manager.CanRestore("s-checkpoint", msgs[0].ID) {
		t.Fatalf("checkpoint was not recorded for %s", msgs[0].ID)
	}
	if _, err := manager.Restore("s-checkpoint", msgs[0].ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("notes.txt = %q, want before", got)
	}
}

func TestAgentRecordsCheckpointForShellMutations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell redirection syntax")
	}
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	toolset, err := whaletools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	manager := checkpoint.NewManager(sessionsDir, root)
	store := agent.NewInMemoryStore()
	a := agent.NewAgentWithRegistry(
		&shellCheckpointProvider{},
		store,
		agent.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(agent.RulePolicy{Default: agent.PermissionAllow}),
		agent.WithSessionsDir(sessionsDir),
		agent.WithCheckpoints(manager),
	)

	if _, err := a.RunSession(context.Background(), "s-shell-checkpoint", "edit notes through shell"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	msgs, err := store.List(context.Background(), "s-shell-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || msgs[0].ID == "" {
		t.Fatalf("missing user message: %+v", msgs)
	}
	if !manager.CanRestore("s-shell-checkpoint", msgs[0].ID) {
		t.Fatalf("checkpoint was not recorded for %s", msgs[0].ID)
	}
	if _, err := manager.Restore("s-shell-checkpoint", msgs[0].ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("notes.txt = %q, want before", got)
	}
	if _, err := os.Stat(filepath.Join(root, "generated.txt")); !os.IsNotExist(err) {
		t.Fatalf("generated.txt should be removed after restore, err=%v", err)
	}
}
