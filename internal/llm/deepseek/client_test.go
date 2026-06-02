package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
	whaletools "github.com/usewhale/whale/internal/tools"
)

type fakeTool struct{ n string }

func (f fakeTool) Name() string { return f.n }
func (f fakeTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}

type nestedTool struct{}

func (nestedTool) Name() string { return "nested" }
func (nestedTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}
func (nestedTool) Description() string { return "nested test tool" }
func (nestedTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payload": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
						},
						"required": []string{"path"},
					},
				},
				"required": []string{"file"},
			},
		},
		"required": []string{"payload"},
	}
}

func TestStreamResponseParsesToolCallAndContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		streamOptions, ok := payload["stream_options"].(map[string]any)
		if !ok || streamOptions["include_usage"] != true {
			t.Fatalf("expected stream_options.include_usage=true, got %#v", payload["stream_options"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\"}}]}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"x\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	_ = os.Setenv("DEEPSEEK_API_KEY", "test-key")
	c, err := New(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	events := c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, []core.Tool{fakeTool{"echo"}})

	var gotComplete bool
	var gotToolArgsReady bool
	for ev := range events {
		if ev.Type == llm.EventError {
			t.Fatalf("provider error: %v", ev.Err)
		}
		if ev.Type == llm.EventToolArgsDelta && ev.ToolArgsDelta != nil && ev.ToolArgsDelta.ReadyCount >= 1 {
			gotToolArgsReady = true
		}
		if ev.Type == llm.EventComplete {
			gotComplete = true
			if ev.Response == nil {
				t.Fatal("expected response")
			}
			if ev.Response.FinishReason != core.FinishReasonToolUse {
				t.Fatalf("finish reason: %s", ev.Response.FinishReason)
			}
			if len(ev.Response.ToolCalls) != 1 {
				t.Fatalf("tool calls: %d", len(ev.Response.ToolCalls))
			}
			if ev.Response.ToolCalls[0].Name != "echo" {
				t.Fatalf("tool name: %s", ev.Response.ToolCalls[0].Name)
			}
		}
	}
	if !gotComplete {
		t.Fatal("missing complete event")
	}
	if !gotToolArgsReady {
		t.Fatal("missing tool args ready progress event")
	}
}

func TestStreamResponseErrorsAfterPartialToolCallWithoutComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"preparing\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"shell_run\",\"arguments\":\"{\\\"command\\\":\"}}]}}]}\n\n")
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	events := c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, []core.Tool{fakeTool{"shell_run"}})
	var sawToolProgress bool
	var sawComplete bool
	var sawError bool
	for ev := range events {
		switch ev.Type {
		case llm.EventToolArgsDelta:
			sawToolProgress = true
		case llm.EventComplete:
			sawComplete = true
		case llm.EventError:
			sawError = true
			var progressErr *streamProgressError
			if !errors.As(ev.Err, &progressErr) {
				t.Fatalf("expected stream progress error, got %T: %v", ev.Err, ev.Err)
			}
		}
	}
	if !sawToolProgress {
		t.Fatal("missing partial tool progress")
	}
	if sawComplete {
		t.Fatal("partial tool call stream must not emit complete")
	}
	if !sawError {
		t.Fatal("missing stream error")
	}
}

func TestStreamResponseParsesReasoningDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking...\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	_ = os.Setenv("DEEPSEEK_API_KEY", "test-key")
	c, err := New(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	events := c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil)
	var sawReasoning bool
	var sawDone bool
	for ev := range events {
		if ev.Type == llm.EventError {
			t.Fatalf("provider error: %v", ev.Err)
		}
		if ev.Type == llm.EventReasoningDelta && ev.ReasoningDelta == "thinking..." {
			sawReasoning = true
		}
		if ev.Type == llm.EventComplete && ev.Response != nil && ev.Response.Reasoning == "thinking..." {
			sawDone = true
		}
	}
	if !sawReasoning {
		t.Fatal("expected reasoning delta event")
	}
	if !sawDone {
		t.Fatal("expected complete response with reasoning")
	}
}

func TestStreamResponseRetriesRateLimitBeforeSSE(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var delays []time.Duration
	policy := llmretry.DefaultPolicy()
	policy.MaxAttempts = 2
	policy.Jitter = 0
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithRetryPolicy(policy),
		withRetrySleeper(func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	events := c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil)
	var sawRetry bool
	var sawComplete bool
	for ev := range events {
		switch ev.Type {
		case llm.EventError:
			t.Fatalf("provider error: %v", ev.Err)
		case llm.EventRetryScheduled:
			sawRetry = true
			if ev.Retry == nil || ev.Retry.Attempt != 1 || ev.Retry.StatusCode != http.StatusTooManyRequests || ev.Retry.Stage != "request" || ev.Retry.StreamReset {
				t.Fatalf("retry info: %+v", ev.Retry)
			}
		case llm.EventComplete:
			sawComplete = true
		}
	}
	if requests != 2 {
		t.Fatalf("requests: want 2, got %d", requests)
	}
	if !sawRetry {
		t.Fatal("missing retry event")
	}
	if !sawComplete {
		t.Fatal("missing complete event after retry")
	}
	if len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("delays: %+v", delays)
	}
}

func TestStreamResponseDoesNotRetryDisconnectedSSEAfterProgress(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"old\"}}]}\n\n")
	}))
	defer srv.Close()

	policy := llmretry.DefaultPolicy()
	policy.Jitter = 0
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithRetryPolicy(policy),
		WithStreamMaxAttempts(2),
		withRetrySleeper(func(_ context.Context, d time.Duration) error {
			if d != time.Second {
				t.Fatalf("delay: want 1s, got %s", d)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var sawOld bool
	var retryEvents int
	var gotErr error
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventError:
			gotErr = ev.Err
		case llm.EventContentDelta:
			if ev.Content == "old" {
				sawOld = true
			}
		case llm.EventRetryScheduled:
			retryEvents++
		}
	}
	if requests != 1 {
		t.Fatalf("requests: want 1, got %d", requests)
	}
	if !sawOld {
		t.Fatal("missing streamed content before disconnect")
	}
	if retryEvents != 0 {
		t.Fatalf("retry events: want 0, got %d", retryEvents)
	}
	if !errors.Is(gotErr, errIncompleteStream) {
		t.Fatalf("error: want incomplete stream, got %v", gotErr)
	}
}

func TestStreamResponseExhaustsDisconnectedSSEBeforeProgress(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()

	policy := llmretry.DefaultPolicy()
	policy.Jitter = 0
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithRetryPolicy(policy),
		WithStreamMaxAttempts(2),
		withRetrySleeper(func(_ context.Context, _ time.Duration) error { return nil }),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var retryEvents int
	var gotErr error
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventRetryScheduled:
			retryEvents++
		case llm.EventError:
			gotErr = ev.Err
		}
	}
	if requests != 2 {
		t.Fatalf("requests: want 2, got %d", requests)
	}
	if retryEvents != 1 {
		t.Fatalf("retry events: want 1, got %d", retryEvents)
	}
	if !errors.Is(gotErr, errIncompleteStream) {
		t.Fatalf("error: want incomplete stream, got %v", gotErr)
	}
}

func TestStreamResponseRetriesIdleSSEBeforeProgress(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	policy := llmretry.DefaultPolicy()
	policy.Jitter = 0
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithRetryPolicy(policy),
		WithStreamMaxAttempts(2),
		WithStreamIdleTimeout(20*time.Millisecond),
		withRetrySleeper(func(_ context.Context, _ time.Duration) error { return nil }),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var sawRetry bool
	var complete string
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventError:
			t.Fatalf("provider error: %v", ev.Err)
		case llm.EventRetryScheduled:
			sawRetry = true
			if ev.Retry == nil || ev.Retry.Stage != "stream" || !ev.Retry.StreamReset {
				t.Fatalf("retry info: %+v", ev.Retry)
			}
		case llm.EventComplete:
			if ev.Response != nil {
				complete = ev.Response.Content
			}
		}
	}
	if requests != 2 {
		t.Fatalf("requests: want 2, got %d", requests)
	}
	if !sawRetry {
		t.Fatal("missing retry event")
	}
	if complete != "ok" {
		t.Fatalf("complete content: %q", complete)
	}
}

func TestStreamResponseKeepalivesDoNotResetIdleTimeout(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_, _ = fmt.Fprint(w, ": keepalive\n\n")
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				case <-r.Context().Done():
					return
				}
			}
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	policy := llmretry.DefaultPolicy()
	policy.Jitter = 0
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithRetryPolicy(policy),
		WithStreamMaxAttempts(2),
		WithStreamIdleTimeout(20*time.Millisecond),
		withRetrySleeper(func(_ context.Context, _ time.Duration) error { return nil }),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var sawRetry bool
	var complete string
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventError:
			t.Fatalf("provider error: %v", ev.Err)
		case llm.EventRetryScheduled:
			sawRetry = true
		case llm.EventComplete:
			if ev.Response != nil {
				complete = ev.Response.Content
			}
		}
	}
	if requests != 2 {
		t.Fatalf("requests: want 2, got %d", requests)
	}
	if !sawRetry {
		t.Fatal("missing retry event")
	}
	if complete != "ok" {
		t.Fatalf("complete content: %q", complete)
	}
}

func TestStreamResponseDoesNotRetryIdleSSEAfterReasoning(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Let\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithStreamMaxAttempts(2),
		WithStreamIdleTimeout(20*time.Millisecond),
		withRetrySleeper(func(_ context.Context, _ time.Duration) error {
			t.Fatal("unexpected retry sleep")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var sawReasoning bool
	var retryEvents int
	var gotErr error
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventReasoningDelta:
			if ev.ReasoningDelta == "Let" {
				sawReasoning = true
			}
		case llm.EventRetryScheduled:
			retryEvents++
		case llm.EventError:
			gotErr = ev.Err
		}
	}
	if requests != 1 {
		t.Fatalf("requests: want 1, got %d", requests)
	}
	if !sawReasoning {
		t.Fatal("missing reasoning delta")
	}
	if retryEvents != 0 {
		t.Fatalf("retry events: want 0, got %d", retryEvents)
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "stalled") {
		t.Fatalf("error: want stall, got %v", gotErr)
	}
}

func TestStreamResponseRejectsReasoningOnlyComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Let\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var sawComplete bool
	var gotErr error
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventComplete:
			sawComplete = true
		case llm.EventError:
			gotErr = ev.Err
		}
	}
	if sawComplete {
		t.Fatal("unexpected complete event")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "reasoning") {
		t.Fatalf("error: want reasoning-only response, got %v", gotErr)
	}
}

func TestStreamResponseDoesNotRetryMalformedSSEFrame(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {not-json}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	policy := llmretry.DefaultPolicy()
	policy.MaxAttempts = 3
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithRetryPolicy(policy),
		withRetrySleeper(func(_ context.Context, _ time.Duration) error {
			t.Fatal("unexpected retry sleep")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		if ev.Type == llm.EventRetryScheduled {
			t.Fatalf("unexpected retry event: %+v", ev.Retry)
		}
	}
	if requests != 1 {
		t.Fatalf("requests: want 1, got %d", requests)
	}
}

func TestStreamResponseDoesNotRetryInvalidRequestURL(t *testing.T) {
	policy := llmretry.DefaultPolicy()
	policy.MaxAttempts = 3
	c, err := New(
		WithAPIKey("test-key"),
		WithBaseURL("://bad-url"),
		WithRetryPolicy(policy),
		withRetrySleeper(func(_ context.Context, _ time.Duration) error {
			t.Fatal("unexpected retry sleep")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var gotErr error
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventRetryScheduled:
			t.Fatalf("unexpected retry event: %+v", ev.Retry)
		case llm.EventError:
			gotErr = ev.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected invalid request URL error")
	}
	if !strings.Contains(gotErr.Error(), "new request") {
		t.Fatalf("error should identify request construction failure, got %v", gotErr)
	}
}

func TestWithBaseURLOverridesEnvironment(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_BASE_URL", "https://env.example")
	c, err := New(WithBaseURL("https://config.example/"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if c.baseURL != "https://config.example" {
		t.Fatalf("baseURL: want config override, got %q", c.baseURL)
	}
}

func TestStreamResponseWithPrefixUsesBetaPayload(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/beta/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := payload["tools"]; ok {
			t.Fatalf("prefix completion request should not include tools: %+v", payload["tools"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\\\"decision\\\":\\\"pass\\\"}\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	c, err := New(WithBaseURL(srv.URL+"/beta"), WithHTTPClient(srv.Client()), WithPrefixCompletion(true))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var final string
	var usage llm.Usage
	for ev := range c.StreamResponseWithPrefix(context.Background(), []core.Message{{Role: core.RoleUser, Text: "return json"}}, "{", nil) {
		if ev.Type == llm.EventError {
			t.Fatalf("provider error: %v", ev.Err)
		}
		if ev.Type == llm.EventComplete {
			final = ev.Response.Content
			usage = ev.Response.Usage
		}
	}
	if final != `{"decision":"pass"}` {
		t.Fatalf("final content = %q", final)
	}
	if usage.PrefixCompletionRequests != 1 {
		t.Fatalf("prefix completion requests = %d, want 1", usage.PrefixCompletionRequests)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %+v", payload["messages"])
	}
	last, ok := messages[len(messages)-1].(map[string]any)
	if !ok || last["role"] != "assistant" || last["content"] != "{" || last["prefix"] != true {
		t.Fatalf("last message should be assistant prefix, got %+v", last)
	}
}

func TestStreamResponseWithPrefixMapsDocumentedV1BaseToBeta(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	c, err := New(WithBaseURL(defaultBaseURL+"/v1"), WithPrefixCompletion(true))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	got, ok := c.prefixCompletionBaseURL()
	if !ok {
		t.Fatal("documented /v1 base should support prefix completion")
	}
	if got != defaultBaseURL+"/beta" {
		t.Fatalf("prefix base = %q, want %q", got, defaultBaseURL+"/beta")
	}
}

func TestStreamResponseWithPrefixFallsBackForCustomNonBetaBaseURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		messages, _ := payload["messages"].([]any)
		last, _ := messages[len(messages)-1].(map[string]any)
		if last["prefix"] == true {
			t.Fatalf("custom non-beta base should not send prefix message: %+v", last)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	c, err := New(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithPrefixCompletion(true))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var final string
	var usage llm.Usage
	for ev := range c.StreamResponseWithPrefix(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, "{", nil) {
		if ev.Type == llm.EventError {
			t.Fatalf("provider error: %v", ev.Err)
		}
		if ev.Type == llm.EventComplete {
			final = ev.Response.Content
			usage = ev.Response.Usage
		}
	}
	if path != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", path)
	}
	if final != "ok" {
		t.Fatalf("final = %q", final)
	}
	if usage.PrefixCompletionRequests != 0 {
		t.Fatalf("prefix completion requests = %d, want 0", usage.PrefixCompletionRequests)
	}
}

func TestToDeepSeekMessagesRecoversMissingToolResults(t *testing.T) {
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "echo", Input: `{"x":1}`},
			},
		},
		{Role: core.RoleUser, Text: "next"},
	}
	out := toDeepSeekMessages(history)
	if len(out) < 3 {
		t.Fatalf("expected recovered tool message inserted, got %d", len(out))
	}
	if out[1]["role"] != "tool" || out[1]["tool_call_id"] != "call_1" {
		t.Fatalf("expected recovered tool response for call_1, got %+v", out[1])
	}
}

func TestToDeepSeekMessagesDropsStrayToolResults(t *testing.T) {
	history := []core.Message{
		{Role: core.RoleUser, Text: "hi"},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{ToolCallID: "orphan", Name: "bash", Content: "x"},
			},
		},
	}
	out := toDeepSeekMessages(history)
	if len(out) != 1 {
		t.Fatalf("expected stray tool message dropped, got %d", len(out))
	}
	if out[0]["role"] != "user" {
		t.Fatalf("unexpected first role: %+v", out[0])
	}
}

func TestToDeepSeekMessagesDoesNotSendToolResultMetadata(t *testing.T) {
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "edit", Input: `{"file_path":"a.txt"}`},
			},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{
					ToolCallID: "call_1",
					Name:       "edit",
					Content:    `{"success":true}`,
					Metadata: map[string]any{
						"kind":  "file_diff",
						"files": []any{map[string]any{"unified_diff": "-old\n+new"}},
					},
				},
			},
		},
	}
	out := toDeepSeekMessages(history)
	if len(out) != 2 {
		t.Fatalf("expected assistant and tool messages, got %d", len(out))
	}
	toolMsg := out[1]
	if _, ok := toolMsg["metadata"]; ok {
		t.Fatalf("metadata must not be sent to provider: %+v", toolMsg)
	}
	if content, _ := toolMsg["content"].(string); strings.Contains(content, "unified_diff") || strings.Contains(content, "-old") {
		t.Fatalf("tool content must not include metadata diff: %q", content)
	}
}

func TestToDeepSeekMessagesCompactsOversizedToolResultForReplay(t *testing.T) {
	large := "HEAD-" + strings.Repeat("a", 45000) + "-TAIL"
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "read_file", Input: `{"file_path":"big.txt"}`},
			},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{ToolCallID: "call_1", Name: "read_file", Content: large},
			},
		},
	}

	out := toDeepSeekMessages(history)
	if len(out) != 2 {
		t.Fatalf("expected assistant and tool messages, got %d", len(out))
	}
	toolMsg := out[1]
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Fatalf("tool message pairing changed: %+v", toolMsg)
	}
	content, _ := toolMsg["content"].(string)
	if !strings.Contains(content, "[tool result compacted for model replay]") {
		t.Fatalf("expected compaction marker, got %q", content[:min(len(content), 200)])
	}
	if !strings.Contains(content, "HEAD-") || !strings.Contains(content, "-TAIL") {
		t.Fatalf("expected compacted content to preserve head and tail, got %q", content)
	}
	if len(content) >= len(large) {
		t.Fatalf("expected compacted content to shrink: before=%d after=%d", len(large), len(content))
	}
}

func TestToDeepSeekMessagesCompactsMediumToolResultForReplay(t *testing.T) {
	medium := "HEAD-" + strings.Repeat("a", 10000) + "-TAIL"
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "grep", Input: `{"pattern":"needle"}`},
			},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{ToolCallID: "call_1", Name: "grep", Content: medium},
			},
		},
	}

	out := toDeepSeekMessages(history)
	content, _ := out[1]["content"].(string)
	if !strings.Contains(content, "[tool result compacted for model replay]") {
		t.Fatalf("expected medium tool result to compact, got %q", content[:min(len(content), 200)])
	}
	if !strings.Contains(content, "HEAD-") || !strings.Contains(content, "-TAIL") {
		t.Fatalf("expected compacted content to preserve head and tail, got %q", content)
	}
	if len(content) >= len(medium) {
		t.Fatalf("expected medium content to shrink: before=%d after=%d", len(medium), len(content))
	}
}

func TestToDeepSeekMessagesLeavesSmallToolResultUnchanged(t *testing.T) {
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "echo", Input: `{"x":1}`},
			},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{ToolCallID: "call_1", Name: "echo", Content: "small result"},
			},
		},
	}

	out := toDeepSeekMessages(history)
	content, _ := out[1]["content"].(string)
	if content != "small result" {
		t.Fatalf("expected small content unchanged, got %q", content)
	}
}

func TestToDeepSeekMessagesCompactsOversizedWhitespaceToolResult(t *testing.T) {
	large := strings.Repeat(" \n\t", 15000)
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "shell_run", Input: `{"command":"printf spaces"}`},
			},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{ToolCallID: "call_1", Name: "shell_run", Content: large},
			},
		},
	}

	out := toDeepSeekMessages(history)
	content, _ := out[1]["content"].(string)
	if !strings.Contains(content, "[tool result compacted for model replay]") {
		t.Fatalf("expected whitespace-only oversized result to compact, got len=%d", len(content))
	}
	if len(content) >= len(large) {
		t.Fatalf("expected compacted whitespace to shrink: before=%d after=%d", len(large), len(content))
	}
}

func TestToDeepSeekMessagesCompactsToolResultOnRuneBoundaries(t *testing.T) {
	large := "开头-" + strings.Repeat("你", 9000) + "-结尾"
	history := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "read_file", Input: `{"file_path":"big.txt"}`},
			},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{
				{ToolCallID: "call_1", Name: "read_file", Content: large},
			},
		},
	}

	out := toDeepSeekMessages(history)
	content, _ := out[1]["content"].(string)
	if !utf8.ValidString(content) {
		t.Fatalf("compacted content is not valid UTF-8")
	}
	if !strings.Contains(content, "开头-") || !strings.Contains(content, "-结尾") {
		t.Fatalf("expected unicode head and tail to survive, got %q", content)
	}
	if !strings.Contains(content, "[tool result compacted for model replay]") {
		t.Fatalf("expected compaction marker, got %q", content)
	}
}

func TestSanitizeDeepSeekMessagesStripsPlainAssistantReasoning(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "plain", "reasoning_content": strings.Repeat("r", 400)},
	}

	out, diag := sanitizeDeepSeekMessagesForRequest(msgs, true)
	if diag.strippedReasoning != 1 {
		t.Fatalf("expected stripped reasoning count=1, got %+v", diag)
	}
	if _, ok := out[1]["reasoning_content"]; ok {
		t.Fatalf("plain assistant reasoning must not be replayed: %+v", out[1])
	}
	if got := estimateReasoningReplayTokens(out); got != 0 {
		t.Fatalf("expected replay tokens to drop to 0, got %d", got)
	}
	if _, ok := msgs[1]["reasoning_content"]; !ok {
		t.Fatal("sanitizer mutated input message")
	}
}

func TestSanitizeDeepSeekMessagesPreservesToolCallReasoning(t *testing.T) {
	msgs := []map[string]any{
		{
			"role":              "assistant",
			"content":           "",
			"reasoning_content": "need a tool",
			"tool_calls": []map[string]any{
				{"id": "call_1", "type": "function", "function": map[string]any{"name": "echo", "arguments": "{}"}},
			},
		},
		{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
	}

	out, diag := sanitizeDeepSeekMessagesForRequest(msgs, true)
	if diag.preservedToolReasoning != 1 || diag.strippedReasoning != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diag)
	}
	if out[0]["reasoning_content"] != "need a tool" {
		t.Fatalf("tool-call reasoning should be preserved: %+v", out[0])
	}
	if out[1]["role"] != "tool" || out[1]["tool_call_id"] != "call_1" {
		t.Fatalf("tool pairing changed: %+v", out)
	}
}

func TestSanitizeDeepSeekMessagesKeepsEmptyReasoningForThinkingToolCall(t *testing.T) {
	msgs := []map[string]any{
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{"id": "call_1", "type": "function", "function": map[string]any{"name": "echo", "arguments": "{}"}},
			},
		},
		{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
	}

	out, _ := sanitizeDeepSeekMessagesForRequest(msgs, true)
	value, ok := out[0]["reasoning_content"].(string)
	if !ok || value != "" {
		t.Fatalf("thinking tool-call assistant should carry empty reasoning_content, got %+v", out[0])
	}
}

func TestSanitizeDeepSeekMessagesRepairsMissingToolCallID(t *testing.T) {
	msgs := []map[string]any{
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{"id": "", "type": "function", "function": map[string]any{"name": "echo", "arguments": "{}"}},
			},
		},
		{"role": "tool", "tool_call_id": "", "content": "ok"},
	}

	out, diag := sanitizeDeepSeekMessagesForRequest(msgs, true)
	if diag.repairedMissingToolCallID != 1 {
		t.Fatalf("expected one repaired missing id, got %+v", diag)
	}
	calls, ok := out[0]["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected tool calls, got %+v", out[0]["tool_calls"])
	}
	id, _ := calls[0]["id"].(string)
	if id == "" {
		t.Fatalf("expected synthetic id: %+v", calls[0])
	}
	if out[1]["tool_call_id"] != id {
		t.Fatalf("tool result id should match repaired call id: call=%q tool=%q", id, out[1]["tool_call_id"])
	}
}

func TestSanitizeDeepSeekMessagesRepairsMissingToolResultAndDropsStrays(t *testing.T) {
	msgs := []map[string]any{
		{"role": "tool", "tool_call_id": "orphan", "content": "secret orphan content"},
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{"id": "call_1", "type": "function", "function": map[string]any{"name": "echo", "arguments": "{}"}},
			},
		},
		{"role": "user", "content": "next"},
	}

	out, diag := sanitizeDeepSeekMessagesForRequest(msgs, true)
	if diag.syntheticToolResults != 1 || diag.droppedStrayTools != 1 {
		t.Fatalf("unexpected diagnostics: %+v", diag)
	}
	if out[0]["role"] != "assistant" || out[1]["role"] != "tool" || out[1]["tool_call_id"] != "call_1" {
		t.Fatalf("expected assistant plus synthetic tool result first, got %+v", out)
	}
	content, _ := out[1]["content"].(string)
	if !strings.Contains(content, "missing_tool_result_recovered") {
		t.Fatalf("expected synthetic missing tool result, got %q", content)
	}
	for _, msg := range out {
		if strings.Contains(fmt.Sprint(msg["content"]), "secret orphan content") {
			t.Fatalf("stray tool content leaked into sanitized messages: %+v", out)
		}
	}
}

func TestSanitizeDeepSeekMessagesLeavesCompactedToolResultPaired(t *testing.T) {
	large := "HEAD-" + strings.Repeat("x", 45000) + "-TAIL"
	msgs := []map[string]any{
		{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{
				{"id": "call_1", "type": "function", "function": map[string]any{"name": "read_file", "arguments": "{}"}},
			},
		},
		{"role": "tool", "tool_call_id": "call_1", "content": compactToolResultForReplay(large)},
	}

	out, diag := sanitizeDeepSeekMessagesForRequest(msgs, true)
	if diag.syntheticToolResults != 0 || diag.droppedStrayTools != 0 {
		t.Fatalf("valid compacted pair should not need repair: %+v", diag)
	}
	content, _ := out[1]["content"].(string)
	if !strings.Contains(content, "[tool result compacted for model replay]") || !strings.Contains(content, "HEAD-") || !strings.Contains(content, "-TAIL") {
		t.Fatalf("compacted content was not preserved: %q", content)
	}
}

func TestDeepSeek400IncludesBoundedMessageDiagnostics(t *testing.T) {
	const secretReasoning = "SECRET_REASONING_SHOULD_NOT_LEAK"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid message shape", http.StatusBadRequest)
	}))
	defer srv.Close()

	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	c, err := New(WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithRetryPolicy(llmretry.Policy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	history := []core.Message{
		{Role: core.RoleAssistant, Text: "plain", Reasoning: secretReasoning},
	}
	var gotErr error
	for ev := range c.StreamResponse(context.Background(), history, nil) {
		if ev.Type == llm.EventError {
			gotErr = ev.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected provider error")
	}
	msg := gotErr.Error()
	if !strings.Contains(msg, "message diagnostics:") || !strings.Contains(msg, "stripped_reasoning=1") {
		t.Fatalf("expected bounded diagnostics in error, got %q", msg)
	}
	if strings.Contains(msg, secretReasoning) {
		t.Fatalf("diagnostic leaked message content: %q", msg)
	}
}

func TestToDeepSeekTools_FlattensDeepSchema(t *testing.T) {
	out := toDeepSeekTools([]core.Tool{nestedTool{}})
	if len(out) != 1 {
		t.Fatalf("expected one tool, got %d", len(out))
	}
	fn, _ := out[0]["function"].(map[string]any)
	params, _ := fn["parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)
	if _, ok := props["payload.file.path"]; !ok {
		t.Fatalf("expected flattened payload.file.path in properties: %#v", props)
	}
}

func TestToDeepSeekToolsIncludesApplyPatchFormatInstructions(t *testing.T) {
	ts, err := whaletools.NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	out := toDeepSeekTools(ts.Tools())
	var fn map[string]any
	for _, tool := range out {
		candidate, _ := tool["function"].(map[string]any)
		if candidate["name"] == "apply_patch" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("apply_patch tool not sent to provider")
	}
	desc, _ := fn["description"].(string)
	for _, want := range []string{
		"*** Begin Patch",
		"*** Update File: path/to/file",
		"Do not use unified diff headers",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("apply_patch provider description missing %q:\n%s", want, desc)
		}
	}
	params, _ := fn["parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)
	patchProp, _ := props["patch"].(map[string]any)
	patchDesc, _ := patchProp["description"].(string)
	if !strings.Contains(patchDesc, "*** Begin Patch") || !strings.Contains(patchDesc, "Do not send unified diff") {
		t.Fatalf("patch parameter description missing format guidance: %q", patchDesc)
	}
}

func TestEstimateReasoningReplayTokens(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "reasoning_content": "12345678"},
		{"role": "assistant", "reasoning_content": "1234"},
	}
	got := estimateReasoningReplayTokens(msgs)
	if got != 3 {
		t.Fatalf("expected replay tokens=3, got %d", got)
	}
}

func TestToolResultReplayDiagnosticsCountsCompactionSavings(t *testing.T) {
	large := strings.Repeat("0123456789abcdef", 4096)
	history := []core.Message{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call_1", Name: "shell_run"}}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "call_1", Name: "shell_run", Content: large}}},
	}
	msgs, _ := sanitizeDeepSeekMessagesForRequest(toDeepSeekMessages(history), true)

	diag := toolResultReplayDiagnostics(history, msgs)
	if diag.rawChars != len(large) {
		t.Fatalf("unexpected raw chars: got %d want %d", diag.rawChars, len(large))
	}
	if diag.replayChars <= 0 || diag.replayChars >= diag.rawChars {
		t.Fatalf("expected compacted replay chars below raw chars, got replay=%d raw=%d", diag.replayChars, diag.rawChars)
	}
	if diag.rawTokens <= diag.replayTokens || diag.tokensSaved() <= 0 {
		t.Fatalf("expected token savings, got raw=%d replay=%d saved=%d", diag.rawTokens, diag.replayTokens, diag.tokensSaved())
	}
	if diag.compacted != 1 {
		t.Fatalf("expected one compacted tool result, got %d", diag.compacted)
	}
}

func TestToolResultReplayDiagnosticsIgnoresStrayRawToolResults(t *testing.T) {
	history := []core.Message{
		{Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "orphan", Name: "shell_run", Content: "unused"}}},
	}
	msgs, _ := sanitizeDeepSeekMessagesForRequest(toDeepSeekMessages(history), true)

	diag := toolResultReplayDiagnostics(history, msgs)
	if diag.rawChars != 0 || diag.replayChars != 0 || diag.rawTokens != 0 || diag.replayTokens != 0 {
		t.Fatalf("expected stray tool result to be ignored, got %+v", diag)
	}
}

func TestToDeepSeekMessagesKeepsPriorToolReplayStableWhenAppendingResults(t *testing.T) {
	chunk := strings.Repeat("tool-output ", 650)
	var history []core.Message
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("call_%02d", i)
		content := fmt.Sprintf("result-%02d %s", i, chunk)
		history = append(history,
			core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: id, Name: "shell_run", Input: "{}"}}},
			core.Message{Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: id, Name: "shell_run", Content: content}}},
		)
	}

	before := toDeepSeekMessages(history)
	priorToolContents := make(map[string]string)
	for _, msg := range before {
		if msg["role"] != "tool" {
			continue
		}
		content, _ := msg["content"].(string)
		toolCallID, _ := msg["tool_call_id"].(string)
		priorToolContents[toolCallID] = content
	}
	if len(priorToolContents) != 12 {
		t.Fatalf("expected 12 tool messages, got %d", len(priorToolContents))
	}

	history = append(history,
		core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call_12", Name: "shell_run", Input: "{}"}}},
		core.Message{Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "call_12", Name: "shell_run", Content: "new result"}}},
	)

	after := toDeepSeekMessages(history)
	for _, msg := range after {
		if msg["role"] != "tool" {
			continue
		}
		toolCallID, _ := msg["tool_call_id"].(string)
		want, ok := priorToolContents[toolCallID]
		if !ok {
			continue
		}
		got, _ := msg["content"].(string)
		if got != want {
			t.Fatalf("tool replay content changed for %s", toolCallID)
		}
		if strings.Contains(got, "tool_result_replay_budget_compacted") {
			t.Fatalf("unexpected cumulative budget compaction for %s: %q", toolCallID, got)
		}
	}
}
