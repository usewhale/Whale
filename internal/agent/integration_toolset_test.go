package agent_test

import (
	"context"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	whaletools "github.com/usewhale/whale/internal/tools"
)

type toolsetProvider struct {
	calls int
}

func (p *toolsetProvider) StreamResponse(_ context.Context, _ []agent.Message, _ []agent.Tool) <-chan agent.ProviderEvent {
	out := make(chan agent.ProviderEvent, 2)
	p.calls++
	if p.calls == 1 {
		out <- agent.ProviderEvent{
			Type: agent.EventComplete,
			Response: &agent.ProviderResponse{
				FinishReason: agent.FinishReasonToolUse,
				ToolCalls: []agent.ToolCall{
					{
						ID:    "tc-1",
						Name:  "write",
						Input: `{"file_path":"notes.txt","content":"alpha\nbeta\n"}`,
					},
					{
						ID:    "tc-2",
						Name:  "read_file",
						Input: `{"file_path":"notes.txt","offset":0,"limit":1}`,
					},
					{
						ID:    "tc-3",
						Name:  "shell_run",
						Input: `{"command":"echo ok"}`,
					},
				},
			},
		}
		close(out)
		return out
	}
	out <- agent.ProviderEvent{
		Type: agent.EventComplete,
		Response: &agent.ProviderResponse{
			FinishReason: agent.FinishReasonEndTurn,
			Content:      "done",
		},
	}
	close(out)
	return out
}

func TestAgentExecutesToolsetToolsInLoop(t *testing.T) {
	root := t.TempDir()
	toolset, err := whaletools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	registry := agent.NewToolRegistry(toolset.Tools())
	provider := &toolsetProvider{}
	store := agent.NewInMemoryStore()
	a := agent.NewAgentWithRegistry(
		provider,
		store,
		registry,
		agent.WithToolPolicy(agent.RulePolicy{Default: agent.PermissionAllow}),
	)

	msg, err := a.Run(context.Background(), "s-tools", "run tools")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.FinishReason != agent.FinishReasonEndTurn || msg.Text != "done" {
		t.Fatalf("unexpected final message: %+v", msg)
	}

	all, err := store.List(context.Background(), "s-tools")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 messages (user,assistant,tool,assistant), got %d", len(all))
	}
	toolMsg := all[2]
	if toolMsg.Role != agent.RoleTool {
		t.Fatalf("expected third message to be tool, got %s", toolMsg.Role)
	}
	if len(toolMsg.ToolResults) != 3 {
		t.Fatalf("expected 3 tool results, got %d", len(toolMsg.ToolResults))
	}
	for i, tr := range toolMsg.ToolResults {
		if tr.IsError {
			t.Fatalf("tool result %d is error: %+v", i, tr)
		}
		if tr.Content == "" {
			t.Fatalf("tool result %d empty content", i)
		}
	}
}
