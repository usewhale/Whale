package agent

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
)

type usageLogProvider struct{}

func (p *usageLogProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			Content:      "ok",
			Usage:        llm.Usage{PromptTokens: 100, CompletionTokens: 50, PromptCacheHitTokens: 20, PromptCacheMissTokens: 80},
			Model:        "deepseek-v4-flash",
			FinishReason: core.FinishReasonEndTurn,
		},
	}
	close(out)
	return out
}

func TestRecordTurnCostWritesUsageLogWithoutSessionRuntime(t *testing.T) {
	tmp := t.TempDir()
	usagePath := filepath.Join(tmp, "usage.jsonl")
	provider := &usageLogProvider{}

	a := NewAgentWithRegistry(provider, store.NewInMemoryStore(), nil, WithUsageLogPath(usagePath))
	if _, err := a.Run(context.Background(), "usage-log-no-runtime", "hi"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, err := os.Stat(usagePath); err != nil {
		t.Fatalf("usage log missing: %v", err)
	}
}

func TestRecordTurnCostSerializesConcurrentSessionMetaUpdates(t *testing.T) {
	sessionsDir := t.TempDir()
	usagePath := filepath.Join(t.TempDir(), "usage.jsonl")
	a := NewAgentWithRegistry(&usageLogProvider{}, store.NewInMemoryStore(), nil,
		WithSessionsDir(sessionsDir),
		WithUsageLogPath(usagePath),
	)
	if err := session.SaveSessionMeta(sessionsDir, "s-cost", session.SessionMeta{}); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	usage := llm.Usage{PromptTokens: 1_000_000}
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			a.recordTurnCost("s-cost", usage, "deepseek-v4-flash", "fp")
		}()
	}
	wg.Wait()
	meta, err := session.LoadSessionMeta(sessionsDir, "s-cost")
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	want := float64(n) * telemetry.EstimateTurnUSD("deepseek-v4-flash", usage)
	if math.Abs(meta.TotalCostUSD-want) > 0.0000001 {
		t.Fatalf("total cost = %.9f, want %.9f", meta.TotalCostUSD, want)
	}
}
