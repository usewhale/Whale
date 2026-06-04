package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/memory"
)

type prefixDriftProvider struct {
	calls   int
	memFile string
}

func (p *prefixDriftProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		_ = os.WriteFile(p.memFile, []byte("v2"), 0o600)
		out <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonToolUse,
				ToolCalls:    []ToolCall{{ID: "tc-1", Name: "echo", Input: "hi"}},
			},
		}
		close(out)
		return out
	}
	out <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      "ok",
		},
	}
	close(out)
	return out
}

func TestBuildTurnProviderHistoryDoesNotAppendLegacyPlanRuntimeControl(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil))
	rt := memory.HydrateRuntime(memory.NewImmutablePrefix([]string{"sys"}), []core.Message{{Role: core.RoleUser, Text: "hi"}})

	out := a.buildTurnProviderHistory("s1", rt)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Role != core.RoleSystem || out[1].Role != core.RoleUser {
		t.Fatalf("unexpected history shape: %+v", out)
	}
}

func TestRunStreamDoesNotEmitPrefixDriftWhenRuntimeMemoryChanges(t *testing.T) {
	dir := t.TempDir()
	memFile := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(memFile, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	store := NewInMemoryStore()
	prov := &prefixDriftProvider{memFile: memFile}
	a := NewAgentWithRegistry(prov, store, core.NewToolRegistry([]core.Tool{echoTool{}}), WithProjectMemory(true, 8000, []string{"AGENTS.md"}, dir))

	events, err := a.RunStream(context.Background(), "s-prefix-drift", "hi")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	seen := false
	for ev := range events {
		if ev.Type == AgentEventTypePrefixDrift && ev.PrefixDrift != nil && ev.PrefixDrift.Expected != "" && ev.PrefixDrift.Actual != "" && ev.PrefixDrift.Expected != ev.PrefixDrift.Actual {
			t.Fatalf("unexpected prefix drift for runtime memory change: %+v", ev.PrefixDrift)
		}
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if seen {
		t.Fatal("unexpected prefix drift event")
	}
}

type cacheMetricsProvider struct{}

func (p *cacheMetricsProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      "ok",
			Model:        "deepseek-chat",
			Usage: Usage{
				PromptTokens:          120,
				PromptCacheHitTokens:  80,
				PromptCacheMissTokens: 20,
			},
		},
	}
	close(out)
	return out
}

type prefixCacheShapeProvider struct {
	prefixRequests int
}

func (p *prefixCacheShapeProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return p.complete()
}

func (p *prefixCacheShapeProvider) StreamResponseWithPrefix(_ context.Context, _ []Message, _ string, _ []string) <-chan ProviderEvent {
	return p.complete()
}

func (p *prefixCacheShapeProvider) complete() <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      "ok",
			Model:        "deepseek-chat",
			Usage: Usage{
				PromptTokens:             100,
				PromptCacheHitTokens:     50,
				PromptCacheMissTokens:    50,
				PrefixCompletionRequests: p.prefixRequests,
			},
		},
	}
	close(out)
	return out
}

func TestRunStreamEmitsPrefixCacheMetrics(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&cacheMetricsProvider{},
		store,
		core.NewToolRegistry(nil),
		WithUsageLogPath(filepath.Join(t.TempDir(), "usage.jsonl")),
	)

	events, err := a.RunStream(context.Background(), "s-cache-metrics", "hi")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	seen := false
	for ev := range events {
		if ev.Type == AgentEventTypePrefixCacheMetrics && ev.CacheMetrics != nil {
			if ev.CacheMetrics.PromptTokens != 120 || ev.CacheMetrics.CachedTokens != 80 || ev.CacheMetrics.CacheHitRatio != 0.8 {
				t.Fatalf("unexpected metrics: %+v", ev.CacheMetrics)
			}
			if ev.CacheMetrics.CacheShape == nil || ev.CacheMetrics.CacheShape.SystemHash == "" || ev.CacheMetrics.CacheShape.LogTailHash == "" || ev.CacheMetrics.CacheShape.RequestHash == "" {
				t.Fatalf("missing cache shape: %+v", ev.CacheMetrics.CacheShape)
			}
			seen = true
		}
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if !seen {
		t.Fatal("expected prefix cache metrics event")
	}
}

func TestRunStreamCacheShapeOmitsFallbackAssistantPrefix(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&prefixCacheShapeProvider{prefixRequests: 0},
		store,
		core.NewToolRegistry(nil),
		WithUsageLogPath(filepath.Join(t.TempDir(), "usage.jsonl")),
	)

	events, err := a.RunStreamWithTurnOptions(context.Background(), "s-prefix-fallback", "hi", RunOptions{
		PrefixCompletion: true,
		AssistantPrefix:  "{",
	})
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypePrefixCacheMetrics && ev.CacheMetrics != nil {
			if ev.CacheMetrics.CacheShape == nil {
				t.Fatal("missing cache shape")
			}
			if ev.CacheMetrics.CacheShape.AssistantPrefixHash != "" {
				t.Fatalf("assistant prefix hash = %q, want empty", ev.CacheMetrics.CacheShape.AssistantPrefixHash)
			}
			return
		}
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	t.Fatal("expected prefix cache metrics event")
}

func TestRunStreamCacheShapeIncludesUsedAssistantPrefix(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&prefixCacheShapeProvider{prefixRequests: 1},
		store,
		core.NewToolRegistry(nil),
		WithUsageLogPath(filepath.Join(t.TempDir(), "usage.jsonl")),
	)

	events, err := a.RunStreamWithTurnOptions(context.Background(), "s-prefix-used", "hi", RunOptions{
		PrefixCompletion: true,
		AssistantPrefix:  "{",
	})
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypePrefixCacheMetrics && ev.CacheMetrics != nil {
			if ev.CacheMetrics.CacheShape == nil {
				t.Fatal("missing cache shape")
			}
			if ev.CacheMetrics.CacheShape.AssistantPrefixHash == "" {
				t.Fatal("expected assistant prefix hash")
			}
			return
		}
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	t.Fatal("expected prefix cache metrics event")
}
