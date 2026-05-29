package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/session"
	whaletools "github.com/usewhale/whale/internal/tools"
)

type e2eNewToolsProvider struct {
	turn      int
	waitInput string
}

func (p *e2eNewToolsProvider) StreamResponse(_ context.Context, _ []agent.Message, _ []agent.Tool) <-chan agent.ProviderEvent {
	out := make(chan agent.ProviderEvent, 1)
	p.turn++
	if p.turn == 1 {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonToolUse, ToolCalls: []agent.ToolCall{
			{ID: "c1", Name: "search_files", Input: `{"path":".","pattern":"target.txt","limit":20}`},
			{ID: "c2", Name: "shell_run", Input: `{"command":"echo e2e-pass","background":true}`},
			{ID: "c3", Name: "todo_add", Input: `{"text":"verify e2e tools","priority":2}`},
			{ID: "c4", Name: "todo_list", Input: `{}`},
		}}}
	} else if p.turn == 2 {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonToolUse, ToolCalls: []agent.ToolCall{
			{ID: "c5", Name: "shell_wait", Input: p.waitInput},
		}}}
	} else {
		out <- agent.ProviderEvent{Type: agent.EventComplete, Response: &agent.ProviderResponse{FinishReason: agent.FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

func (p *e2eNewToolsProvider) setWaitInputFromToolMessage(msgs []agent.Message) {
	for _, m := range msgs {
		if m.Role != agent.RoleTool {
			continue
		}
		for _, tr := range m.ToolResults {
			if tr.Name != "shell_run" {
				continue
			}
			s := tr.Content
			i := strings.Index(s, `"task_id":"`)
			if i < 0 {
				continue
			}
			rest := s[i+len(`"task_id":"`):]
			j := strings.Index(rest, `"`)
			if j < 0 {
				continue
			}
			taskID := rest[:j]
			p.waitInput = `{"task_id":"` + taskID + `","timeout_ms":5000}`
			return
		}
	}
}

func TestAgentE2ENewToolsFlow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	sessionsDir := filepath.Join(root, ".sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	toolset, err := whaletools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	provider := &e2eNewToolsProvider{}
	store := agent.NewInMemoryStore()
	a := agent.NewAgentWithRegistry(
		provider,
		store,
		agent.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(agent.RulePolicy{Default: agent.PermissionAllow}),
		agent.WithSessionsDir(sessionsDir),
	)

	if _, err := a.RunSession(context.Background(), "s-e2e-tools", "run"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	msgs, err := store.List(context.Background(), "s-e2e-tools")
	if err != nil {
		t.Fatalf("list after run1 failed: %v", err)
	}
	provider.setWaitInputFromToolMessage(msgs)
	if provider.waitInput == "" {
		t.Fatalf("failed to capture task_id from shell_run result")
	}

	if _, err := a.RunSession(context.Background(), "s-e2e-tools", "wait"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if _, err := a.RunSession(context.Background(), "s-e2e-tools", "finish"); err != nil {
		t.Fatalf("run3 failed: %v", err)
	}

	all, err := store.List(context.Background(), "s-e2e-tools")
	if err != nil {
		t.Fatalf("final list failed: %v", err)
	}
	joined := ""
	for _, m := range all {
		for _, tr := range m.ToolResults {
			joined += tr.Content + "\n"
		}
	}
	if !strings.Contains(joined, "target.txt") {
		t.Fatalf("search_files result missing target.txt")
	}
	if !strings.Contains(joined, "e2e-pass") {
		t.Fatalf("shell_wait result missing command output")
	}
	if !strings.Contains(joined, "verify e2e tools") {
		t.Fatalf("todo result missing item text")
	}

	st, err := session.LoadTodoState(sessionsDir, "s-e2e-tools")
	if err != nil {
		t.Fatalf("load todo state failed: %v", err)
	}
	if len(st.Items) == 0 {
		t.Fatalf("expected persisted todo items")
	}
}
