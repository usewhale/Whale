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

func TestStreamResponseRetriesDisconnectedSSE(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"old\"}}]}\n\n")
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
	var sawRetry bool
	var complete string
	for ev := range c.StreamResponse(context.Background(), []core.Message{{Role: core.RoleUser, Text: "hi"}}, nil) {
		switch ev.Type {
		case llm.EventError:
			t.Fatalf("provider error: %v", ev.Err)
		case llm.EventContentDelta:
			if ev.Content == "old" {
				sawOld = true
			}
		case llm.EventRetryScheduled:
			sawRetry = true
			if ev.Retry == nil || ev.Retry.Stage != "stream" || !ev.Retry.StreamReset || ev.Retry.Attempt != 1 || ev.Retry.MaxAttempts != 2 {
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
	if !sawOld || !sawRetry {
		t.Fatalf("sawOld=%v sawRetry=%v", sawOld, sawRetry)
	}
	if complete != "ok" {
		t.Fatalf("complete content: %q", complete)
	}
}

func TestStreamResponseExhaustsDisconnectedSSE(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
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
