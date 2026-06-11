package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/telemetry"
)

type telemetryToolProvider struct {
	calls int
	tool  string
	input string
}

func (p *telemetryToolProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls == 1 {
		ev := toolUseEvent(toolCall("tc-telemetry", p.tool, p.input))
		ev.Response.Model = "deepseek-v4-pro"
		return eventStream(ev)
	}
	ev := endTurnEvent("done")
	ev.Response.Model = "deepseek-v4-pro"
	return eventStream(ev)
}

type telemetryNestedTool struct{}

func (t telemetryNestedTool) Name() string { return "nested" }

func (t telemetryNestedTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payload": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"additionalProperties": true,
	}
}

func (t telemetryNestedTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":true,"code":"ok"}`}, nil
}

type requiredPathTool struct{}

func (t requiredPathTool) Name() string { return "needs_path" }

func (t requiredPathTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
		},
		"required":             []string{"file_path"},
		"additionalProperties": true,
	}
}

func (t requiredPathTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":true,"code":"ok"}`}, nil
}

type decodeArgsTool struct{}

func (t decodeArgsTool) Name() string { return "decode_args" }

func (t decodeArgsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"additionalProperties": true,
	}
}

func (t decodeArgsTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	var in struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"bad count","code":"invalid_args"}`, Outcome: core.OutcomeFailure}, nil
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":true,"code":"ok"}`}, nil
}

type telemetryArrayTool struct{}

func (t telemetryArrayTool) Name() string { return "array_args" }

func (t telemetryArrayTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompts": map[string]any{
				"type":     "array",
				"items":    map[string]any{"type": "string"},
				"minItems": 1,
			},
		},
		"required":             []string{"prompts"},
		"additionalProperties": false,
	}
}

func (t telemetryArrayTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	var in struct {
		Prompts []string `json:"prompts"`
	}
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"bad prompts","code":"invalid_args"}`, Outcome: core.OutcomeFailure}, nil
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":true,"code":"ok"}`}, nil
}

type telemetryPathTool struct{}

func (t telemetryPathTool) Name() string { return "path_args" }

func (t telemetryPathTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
		},
		"required":             []string{"file_path"},
		"additionalProperties": false,
	}
}

func (t telemetryPathTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"bad path","code":"invalid_args"}`, Outcome: core.OutcomeFailure}, nil
	}
	if in.FilePath != "README.md" {
		return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"path was not repaired","code":"invalid_args"}`, Outcome: core.OutcomeFailure}, nil
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":true,"code":"ok"}`}, nil
}

func TestToolInputTelemetryRecordsTruncatedJSONRepair(t *testing.T) {
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&telemetryToolProvider{tool: "write", input: `{"file_path":"a.txt","content":"x"`},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
	)
	drainAgentEvents(t, a, "s-truncated")

	events := readToolInputEvents(t, dir, "s-truncated")
	assertToolInputEvent(t, events, "tool_input_repaired", "truncated_json", "")
}

func TestToolInputTelemetryRecordsRenestFlatInputRepair(t *testing.T) {
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&telemetryToolProvider{tool: "nested", input: `{"payload.target.path":"a.txt"}`},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{telemetryNestedTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
	)
	drainAgentEvents(t, a, "s-renest")

	events := readToolInputEvents(t, dir, "s-renest")
	assertToolInputEvent(t, events, "tool_input_repaired", "renest_flat_input", "")
}

func TestToolInputTelemetryRecordsSchemaGuidedRepairDetails(t *testing.T) {
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&telemetryToolProvider{tool: "array_args", input: `{"prompts":"[\"a\",\"b\"]"}`},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{telemetryArrayTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
		WithRecoveryPolicy(RecoveryPolicy{Enabled: false}),
	)
	drainAgentEvents(t, a, "s-schema-repair")

	events := readToolInputEvents(t, dir, "s-schema-repair")
	assertToolInputRepairDetail(t, events, "stringified_array", "prompts", "string", "array")
	assertNoToolInputInvalidEvent(t, events)
}

func TestToolInputTelemetryRecordsMarkdownAutolinkPathRepair(t *testing.T) {
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&telemetryToolProvider{tool: "path_args", input: `{"file_path":"[README.md](http://README.md)"}`},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{telemetryPathTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
		WithRecoveryPolicy(RecoveryPolicy{Enabled: false}),
	)
	drainAgentEvents(t, a, "s-path-repair")

	events := readToolInputEvents(t, dir, "s-path-repair")
	assertToolInputRepairDetail(t, events, "markdown_autolink_path", "file_path", "string", "string")
	assertNoToolInputInvalidEvent(t, events)
}

func TestToolInputTelemetryRecordsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&telemetryToolProvider{tool: "needs_path", input: `{}`},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{requiredPathTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
		WithRecoveryPolicy(RecoveryPolicy{Enabled: false}),
	)
	drainAgentEvents(t, a, "s-invalid-input")

	events := readToolInputEvents(t, dir, "s-invalid-input")
	assertToolInputEvent(t, events, "tool_input_invalid", "", "invalid_input")
}

func TestToolInputTelemetryRecordsInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&telemetryToolProvider{tool: "decode_args", input: `{"count":"bad"}`},
		NewInMemoryStore(),
		NewToolRegistry([]Tool{decodeArgsTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
		WithRecoveryPolicy(RecoveryPolicy{Enabled: false}),
	)
	drainAgentEvents(t, a, "s-invalid-args")

	events := readToolInputEvents(t, dir, "s-invalid-args")
	assertToolInputEvent(t, events, "tool_input_invalid", "", "invalid_args")
}

func TestInvalidArgsDoesNotTriggerRecoveryRetry(t *testing.T) {
	dir := t.TempDir()
	provider := &telemetryToolProvider{tool: "decode_args", input: `{"count":"bad"}`}
	a := NewAgentWithRegistry(
		provider,
		NewInMemoryStore(),
		NewToolRegistry([]Tool{decodeArgsTool{}}),
		WithSessionsDir(dir),
		WithToolPolicy(policyNever()),
	)
	stream, err := a.RunStream(context.Background(), "s-invalid-args-no-retry", "go")
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	var sawRecovery bool
	for ev := range stream {
		switch ev.Type {
		case AgentEventTypeError:
			t.Fatalf("agent error: %v", ev.Err)
		case AgentEventTypeToolRecoveryScheduled, AgentEventTypeToolRecoveryAttempt, AgentEventTypeToolRecoveryExhausted, AgentEventTypeReplanRequiredSet:
			sawRecovery = true
		}
	}
	if sawRecovery {
		t.Fatal("invalid_args should be returned to the model without recovery retry")
	}
	if provider.calls != 2 {
		t.Fatalf("expected one follow-up model turn after invalid tool result, got provider calls=%d", provider.calls)
	}
	events := readToolInputEvents(t, dir, "s-invalid-args-no-retry")
	if len(events) != 1 {
		t.Fatalf("expected one invalid telemetry event, got %+v", events)
	}
	assertToolInputEvent(t, events, "tool_input_invalid", "", "invalid_args")
}

func TestToolInputTelemetryConcurrentInvalidInputAppend(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{sessionsDir: dir}
	const count = 64

	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		i := i
		go func() {
			defer wg.Done()
			a.recordToolInputInvalid("s-concurrent-invalid", "deepseek-v4-pro", "msg-concurrent", core.ToolCall{
				ID:   fmt.Sprintf("tc-%02d", i),
				Name: "needs_path",
			}, "invalid_input")
		}()
	}
	wg.Wait()

	events := readToolInputEvents(t, dir, "s-concurrent-invalid")
	if len(events) != count {
		t.Fatalf("expected %d events, got %d: %+v", count, len(events), events)
	}
	seen := make(map[string]bool, count)
	for _, ev := range events {
		if ev.Session != "s-concurrent-invalid" || ev.Event != "tool_input_invalid" || ev.ErrorCode != "invalid_input" {
			t.Fatalf("unexpected event: %+v", ev)
		}
		if seen[ev.ToolCallID] {
			t.Fatalf("duplicate tool call id %q in %+v", ev.ToolCallID, events)
		}
		seen[ev.ToolCallID] = true
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("tc-%02d", i)
		if !seen[id] {
			t.Fatalf("missing tool call id %q in %+v", id, events)
		}
	}
}

func drainAgentEvents(t *testing.T, a *Agent, sessionID string) {
	t.Helper()
	ch, err := a.RunStream(context.Background(), sessionID, "go")
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	for ev := range ch {
		if ev.Type == AgentEventTypeError {
			t.Fatalf("agent error: %v", ev.Err)
		}
	}
}

func readToolInputEvents(t *testing.T, sessionsDir, sessionID string) []telemetry.ToolInputEvent {
	t.Helper()
	b, err := os.ReadFile(telemetry.ToolInputEventsPath(sessionsDir, sessionID))
	if err != nil {
		t.Fatalf("read tool input events: %v", err)
	}
	lines := splitNonEmptyLines(string(b))
	events := make([]telemetry.ToolInputEvent, 0, len(lines))
	for _, line := range lines {
		var ev telemetry.ToolInputEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal event %q: %v", line, err)
		}
		events = append(events, ev)
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("unmarshal raw event %q: %v", line, err)
		}
		if _, ok := raw["input"]; ok {
			t.Fatalf("tool input event must not contain raw input: %v", raw)
		}
	}
	return events
}

func assertToolInputEvent(t *testing.T, events []telemetry.ToolInputEvent, event, repairKind, errorCode string) {
	t.Helper()
	for _, ev := range events {
		if ev.Event == event && ev.RepairKind == repairKind && ev.ErrorCode == errorCode {
			if ev.Session == "" || ev.ToolCallID == "" || ev.Tool == "" || ev.AssistantMessageID == "" {
				t.Fatalf("event missing identity fields: %+v", ev)
			}
			return
		}
	}
	t.Fatalf("missing event=%s repair=%s error=%s in %+v", event, repairKind, errorCode, events)
}

func assertToolInputRepairDetail(t *testing.T, events []telemetry.ToolInputEvent, repairKind, path, beforeType, afterType string) {
	t.Helper()
	for _, ev := range events {
		if ev.Event == "tool_input_repaired" && ev.RepairKind == repairKind {
			if ev.Path != path || ev.BeforeType != beforeType || ev.AfterType != afterType {
				t.Fatalf("unexpected repair detail: %+v", ev)
			}
			if ev.Session == "" || ev.ToolCallID == "" || ev.Tool == "" || ev.AssistantMessageID == "" {
				t.Fatalf("event missing identity fields: %+v", ev)
			}
			return
		}
	}
	t.Fatalf("missing repair=%s in %+v", repairKind, events)
}

func assertNoToolInputInvalidEvent(t *testing.T, events []telemetry.ToolInputEvent) {
	t.Helper()
	for _, ev := range events {
		if ev.Event == "tool_input_invalid" {
			t.Fatalf("unexpected invalid event after repair: %+v", ev)
		}
	}
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func policyNever() policy.ToolPolicy {
	return policy.RulePolicy{Default: policy.PermissionAllow}
}

func TestToolInputEventsUseSessionSidecarName(t *testing.T) {
	dir := t.TempDir()
	path := telemetry.ToolInputEventsPath(dir, "s1")
	if filepath.Base(path) != "s1.tool_input_events.jsonl" {
		t.Fatalf("unexpected sidecar path: %s", path)
	}
}
