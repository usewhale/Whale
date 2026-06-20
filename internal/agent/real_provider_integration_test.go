package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/llm/deepseek"
	"github.com/usewhale/whale/internal/store"
)

func requireRealSmoke(t *testing.T) string {
	t.Helper()
	if os.Getenv("RUN_REAL_SMOKE") != "1" {
		t.Skip("set RUN_REAL_SMOKE=1 to run real DeepSeek smoke tests")
	}
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY is not set")
	}
	return key
}

func TestRealDeepSeekStreamSmoke(t *testing.T) {
	key := requireRealSmoke(t)
	provider, err := deepseek.New(deepseek.WithAPIKey(key))
	if err != nil {
		t.Fatalf("init deepseek: %v", err)
	}
	a := NewAgent(provider, store.NewInMemoryStore(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	msg, err := a.RunSession(ctx, "real-stream-smoke", "只回复: ok")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.Text == "" {
		t.Fatal("expected non-empty assistant response")
	}
}

func TestRealDeepSeekCacheMetricsSmoke(t *testing.T) {
	key := requireRealSmoke(t)
	provider, err := deepseek.New(deepseek.WithAPIKey(key))
	if err != nil {
		t.Fatalf("init deepseek: %v", err)
	}
	tmp := t.TempDir()
	usagePath := filepath.Join(tmp, "usage")
	a := NewAgentWithRegistry(
		provider,
		store.NewInMemoryStore(),
		nil,
		WithUsageLogPath(usagePath),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := a.RunSession(ctx, "real-cache-smoke", "请用一句话介绍鲸鱼"); err != nil {
		t.Fatalf("turn1 failed: %v", err)
	}
	events, err := a.RunStream(ctx, "real-cache-smoke", "复述上句话，不要改写")
	if err != nil {
		t.Fatalf("turn2 stream failed: %v", err)
	}
	seen := false
	for ev := range events {
		if ev.Type == AgentEventTypePrefixCacheMetrics && ev.CacheMetrics != nil {
			seen = true
		}
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
	}
	if !seen {
		t.Fatal("expected prefix cache metrics event on turn2")
	}
	if _, err := os.Stat(usagePath); err != nil {
		t.Fatalf("usage log missing: %v", err)
	}
}
