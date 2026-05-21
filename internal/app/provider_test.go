package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

func TestNewDeepSeekProviderAppliesBaseURL(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_BASE_URL", "https://env.example")
	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	provider, err := newDeepSeekProvider(providerOptions{
		BaseURL:         srv.URL,
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "high",
		ThinkingEnabled: true,
	})
	if err != nil {
		t.Fatalf("newDeepSeekProvider: %v", err)
	}
	for ev := range provider.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		if ev.Type == llm.EventError {
			t.Fatalf("provider error: %v", ev.Err)
		}
	}
	if !sawRequest {
		t.Fatal("expected request to configured base URL")
	}
}

func TestTaskProviderUsesConfiguredRetryPolicy(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	var parentRequests atomic.Int32
	var childRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, hasTools := payload["tools"]; !hasTools {
			childRequests.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if parentRequests.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_parallel\",\"function\":{\"name\":\"parallel_reason\",\"arguments\":\"{\\\"prompts\\\":[\\\"x\\\"]}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.APIBaseURL = srv.URL
	cfg.AutoAcceptPermissions = true
	cfg.RetryMaxAttempts = 1
	cfg.RetryMaxDelay = time.Second
	a, err := New(context.Background(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := a.RunTurn(ctx, "use parallel reasoning", false)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	for ev := range events {
		if ev.Type == "error" && ev.Err != nil {
			t.Fatalf("agent error: %v", ev.Err)
		}
	}
	if got := childRequests.Load(); got != 1 {
		t.Fatalf("child provider requests: want configured max_attempts=1, got %d", got)
	}
}
