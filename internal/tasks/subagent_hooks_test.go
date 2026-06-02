package tasks

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/telemetry"
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

func TestCompleteHookModelUsesPrefixCompletionProvider(t *testing.T) {
	provider := &hookPrefixProvider{}
	content, usage, err := completeHookModel(context.Background(), provider, "return JSON")
	if err != nil {
		t.Fatalf("completeHookModel: %v", err)
	}
	if content != `{"decision":"pass"}` {
		t.Fatalf("content = %q", content)
	}
	if usage.PrefixCompletionRequests != 1 {
		t.Fatalf("prefix completion requests = %d, want 1", usage.PrefixCompletionRequests)
	}
	if provider.prefix != "{" {
		t.Fatalf("prefix = %q, want {", provider.prefix)
	}
}

func TestRecordHookModelUsageCountsSessionBudgetWithoutUsageLog(t *testing.T) {
	sessionsDir := t.TempDir()
	if err := session.SaveSessionMeta(sessionsDir, "child-session", session.SessionMeta{TotalCostUSD: 0.01}); err != nil {
		t.Fatalf("save session meta: %v", err)
	}
	usage := llm.Usage{PromptTokens: 1000, CompletionTokens: 500}
	r := NewRunner(RunnerConfig{SessionsDir: sessionsDir})
	r.recordHookModelUsage(agent.NewSubagentHookPayload(agent.HookEventSubagentStart, "child-session", ".", "prefill-smoke", "deepseek-v4-flash", ""), "deepseek-v4-flash", "prompt", usage)

	meta, err := session.LoadSessionMeta(sessionsDir, "child-session")
	if err != nil {
		t.Fatalf("load session meta: %v", err)
	}
	want := 0.01 + telemetry.EstimateTurnUSD("deepseek-v4-flash", usage)
	if math.Abs(meta.TotalCostUSD-want) > 0.0000001 {
		t.Fatalf("TotalCostUSD = %.9f, want %.9f", meta.TotalCostUSD, want)
	}
}

type hookPrefixProvider struct {
	prefix string
}

func (p *hookPrefixProvider) StreamResponse(context.Context, []core.Message, []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	go func() {
		defer close(out)
		out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{Content: "plain"}}
	}()
	return out
}

func (p *hookPrefixProvider) StreamResponseWithPrefix(_ context.Context, _ []core.Message, prefix string, _ []string) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	go func() {
		defer close(out)
		p.prefix = prefix
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				Content: `{"decision":"pass"}`,
				Usage:   llm.Usage{PrefixCompletionRequests: 1},
			},
		}
	}()
	return out
}
