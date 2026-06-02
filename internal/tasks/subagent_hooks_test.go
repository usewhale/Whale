package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
)

func TestRunSubagentStartHooksRejectsReadOnlyCommandHooks(t *testing.T) {
	hooks := []agent.ResolvedHook{{
		HookConfig: agent.HookConfig{
			Command: "echo should-not-run",
		},
		Event: agent.HookEventSubagentStart,
	}}

	_, err := runSubagentStartHooks(context.Background(), hooks, "session", t.TempDir(), "review", "deepseek-v4-flash", AgentPermissionReadOnly, "review this", nil, nil)
	if err == nil {
		t.Fatal("expected read-only command hook to be rejected")
	}
	if !strings.Contains(err.Error(), "read-only subagent cannot run command/shell hook") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSubagentStartHooksPassesModelToPromptHook(t *testing.T) {
	hooks := []agent.ResolvedHook{{
		HookConfig: agent.HookConfig{
			Type:   "prompt",
			Prompt: "inspect payload",
		},
		Event: agent.HookEventSubagentStart,
	}}
	var gotModel string
	promptExecutor := func(ctx context.Context, cfg agent.HookConfig, payload agent.HookPayload) agent.HookResult {
		gotModel = payload.SubagentModel
		return agent.HookResult{Decision: agent.HookDecisionPass}
	}

	_, err := runSubagentStartHooks(context.Background(), hooks, "session", t.TempDir(), "review", "deepseek-v4-pro", AgentPermissionReadOnly, "review this", promptExecutor, nil)
	if err != nil {
		t.Fatalf("prompt hook failed: %v", err)
	}
	if gotModel != "deepseek-v4-pro" {
		t.Fatalf("SubagentModel = %q, want deepseek-v4-pro", gotModel)
	}
}

func TestRunSubagentStopHooksPassesModelToPromptHook(t *testing.T) {
	hooks := []agent.ResolvedHook{{
		HookConfig: agent.HookConfig{
			Type:   "prompt",
			Prompt: "inspect payload",
		},
		Event: agent.HookEventSubagentStop,
	}}
	var gotModel string
	promptExecutor := func(ctx context.Context, cfg agent.HookConfig, payload agent.HookPayload) agent.HookResult {
		gotModel = payload.SubagentModel
		return agent.HookResult{Decision: agent.HookDecisionPass}
	}

	if err := runSubagentStopHooks(context.Background(), hooks, "session", t.TempDir(), "review", "deepseek-v4-flash", AgentPermissionReadOnly, "done", promptExecutor, nil); err != nil {
		t.Fatalf("prompt hook failed: %v", err)
	}
	if gotModel != "deepseek-v4-flash" {
		t.Fatalf("SubagentModel = %q, want deepseek-v4-flash", gotModel)
	}
}
