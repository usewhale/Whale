package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
)

type cacheShapeTool struct {
	name        string
	description string
	params      map[string]any
}

func (t cacheShapeTool) Name() string {
	return t.name
}

func (t cacheShapeTool) Description() string {
	return t.description
}

func (t cacheShapeTool) Parameters() map[string]any {
	return t.params
}

func (t cacheShapeTool) Run(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}

func TestBuildCacheShapeIgnoresMessageIdentityFields(t *testing.T) {
	base := []core.Message{
		{ID: "m1", SessionID: "s1", Role: core.RoleSystem, Text: "system", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0)},
		{ID: "m2", SessionID: "s1", Role: core.RoleUser, Text: "hi", CreatedAt: time.Unix(3, 0), UpdatedAt: time.Unix(4, 0)},
	}
	changedIdentity := []core.Message{
		{ID: "other-1", SessionID: "other", Role: core.RoleSystem, Text: "system", CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(20, 0)},
		{ID: "other-2", SessionID: "other", Role: core.RoleUser, Text: "hi", CreatedAt: time.Unix(30, 0), UpdatedAt: time.Unix(40, 0)},
	}
	tools := core.NewToolRegistry([]core.Tool{cacheShapeTool{
		name:        "lookup",
		description: "lookup data",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
	}})

	got := buildCacheShape(base, tools.Tools(), "")
	got2 := buildCacheShape(changedIdentity, tools.Tools(), "")
	if !reflect.DeepEqual(got, got2) {
		t.Fatalf("identity fields changed cache shape:\n%+v\n%+v", got, got2)
	}

	changedSystem := append([]core.Message(nil), base...)
	changedSystem[0].Text = "system v2"
	if buildCacheShape(changedSystem, tools.Tools(), "").SystemHash == got.SystemHash {
		t.Fatal("expected system text change to alter system hash")
	}

	changedTools := core.NewToolRegistry([]core.Tool{cacheShapeTool{
		name:        "lookup",
		description: "lookup data v2",
		params:      map[string]any{"type": "object"},
	}})
	if buildCacheShape(base, changedTools.Tools(), "").ToolsHash == got.ToolsHash {
		t.Fatal("expected tool schema change to alter tools hash")
	}
}

func TestBuildCacheShapeReportsSystemSegments(t *testing.T) {
	systemBlocks := []string{
		"Agent mode is active.",
		"Current Whale runtime:\n- Current Whale workspace root: /tmp/task-a\n- OS: darwin",
		"Tool use policy.\n\n- Tools are provided through the provider tool schema.",
		"Workflow authoring.",
	}
	history := []core.Message{
		{Role: core.RoleSystem, Text: strings.Join(systemBlocks, "\n\n")},
		{Role: core.RoleUser, Text: "hi"},
	}

	shape := buildCacheShapeWithSystemBlocks(history, nil, "", systemBlocks)
	if shape.SystemHash == "" || shape.SystemBytes == 0 {
		t.Fatalf("missing system aggregate shape: %+v", shape)
	}
	if len(shape.SystemSegments) != len(systemBlocks) {
		t.Fatalf("system segments len = %d, want %d: %+v", len(shape.SystemSegments), len(systemBlocks), shape.SystemSegments)
	}
	runtimeSegment := shape.SystemSegments[1]
	if runtimeSegment.Name != "runtime_context" || runtimeSegment.Stability != "dynamic" {
		t.Fatalf("runtime segment classification = %+v", runtimeSegment)
	}
	if runtimeSegment.Hash == "" || runtimeSegment.Bytes == 0 {
		t.Fatalf("runtime segment missing hash/bytes: %+v", runtimeSegment)
	}
	toolPolicySegment := shape.SystemSegments[2]
	if toolPolicySegment.Name != "tool_policy" || toolPolicySegment.Stability != "immutable" {
		t.Fatalf("tool policy segment classification = %+v", toolPolicySegment)
	}
	workflowSegment := shape.SystemSegments[3]
	if workflowSegment.Name != "workflow_authoring" || workflowSegment.Stability != "immutable" {
		t.Fatalf("workflow segment classification = %+v", workflowSegment)
	}
}

func TestBuildCacheShapeReportsRuntimeSegments(t *testing.T) {
	systemBlocks := []string{
		"Tool use policy.\n\n- Tools are provided through the provider tool schema.",
	}
	runtimeBlocks := []string{
		"Current session mode: agent.",
		"Current Whale runtime:\n- Current Whale workspace root: /tmp/task-a\n- OS: darwin",
	}
	history := []core.Message{
		{Role: core.RoleSystem, Text: strings.Join(systemBlocks, "\n\n")},
		{Role: core.RoleSystem, Text: strings.Join(runtimeBlocks, "\n\n")},
		{Role: core.RoleUser, Text: "hi"},
	}

	shape := buildCacheShapeForRequestWithRuntime(cacheShapeRequestAgent, history, nil, "", systemBlocks, runtimeBlocks)
	if shape.RuntimeHash == "" || shape.RuntimeBytes == 0 {
		t.Fatalf("missing runtime aggregate shape: %+v", shape)
	}
	if len(shape.RuntimeSegments) != len(runtimeBlocks) {
		t.Fatalf("runtime segments len = %d, want %d: %+v", len(shape.RuntimeSegments), len(runtimeBlocks), shape.RuntimeSegments)
	}
	if shape.RuntimeSegments[0].Name != "mode_instructions" || shape.RuntimeSegments[0].Stability != "dynamic" {
		t.Fatalf("mode runtime segment classification = %+v", shape.RuntimeSegments[0])
	}
	if shape.RuntimeSegments[1].Name != "runtime_context" || shape.RuntimeSegments[1].Stability != "dynamic" {
		t.Fatalf("runtime context segment classification = %+v", shape.RuntimeSegments[1])
	}

	changedRuntime := append([]string(nil), runtimeBlocks...)
	changedRuntime[1] = strings.Replace(changedRuntime[1], "/tmp/task-a", "/tmp/task-b", 1)
	changedHistory := []core.Message{
		{Role: core.RoleSystem, Text: strings.Join(systemBlocks, "\n\n")},
		{Role: core.RoleSystem, Text: strings.Join(changedRuntime, "\n\n")},
		{Role: core.RoleUser, Text: "hi"},
	}
	changed := buildCacheShapeForRequestWithRuntime(cacheShapeRequestAgent, changedHistory, nil, "", systemBlocks, changedRuntime)
	if changed.SystemHash != shape.SystemHash {
		t.Fatal("expected runtime change not to alter immutable system hash")
	}
	if changed.RuntimeHash == shape.RuntimeHash {
		t.Fatal("expected runtime change to alter runtime hash")
	}
	if changed.RequestHash == shape.RequestHash {
		t.Fatal("expected runtime change to alter request hash")
	}
	if shape.PrefixHash == "" || shape.PrefixBytes == 0 {
		t.Fatalf("missing provider prefix shape: %+v", shape)
	}
	if changed.PrefixHash == shape.PrefixHash {
		t.Fatal("expected runtime change to alter provider prefix hash")
	}
}

func TestBuildCacheShapeReportsProviderPrefixAndToolSegments(t *testing.T) {
	history := []core.Message{{Role: core.RoleSystem, Text: "system"}, {Role: core.RoleUser, Text: "hi"}}
	tools := core.NewToolRegistry([]core.Tool{
		cacheShapeTool{name: "alpha", description: "first", params: map[string]any{"type": "object"}},
		cacheShapeTool{name: "beta", description: "second", params: map[string]any{"type": "object"}},
	})

	shape := buildCacheShape(history, tools.Tools(), "")
	if shape.PrefixHash == "" || shape.PrefixBytes == 0 {
		t.Fatalf("missing provider prefix hash/bytes: %+v", shape)
	}
	if len(shape.ToolSegments) != 2 {
		t.Fatalf("tool segments len = %d, want 2: %+v", len(shape.ToolSegments), shape.ToolSegments)
	}
	if shape.ToolSegments[0].Name != "alpha" || shape.ToolSegments[0].Hash == "" || shape.ToolSegments[0].Bytes == 0 {
		t.Fatalf("unexpected first tool segment: %+v", shape.ToolSegments[0])
	}

	changedTools := core.NewToolRegistry([]core.Tool{
		cacheShapeTool{name: "alpha", description: "changed", params: map[string]any{"type": "object"}},
		cacheShapeTool{name: "beta", description: "second", params: map[string]any{"type": "object"}},
	})
	changed := buildCacheShape(history, changedTools.Tools(), "")
	if changed.PrefixHash == shape.PrefixHash {
		t.Fatal("expected tool schema change to alter provider prefix hash")
	}
	if changed.ToolSegments[0].Hash == shape.ToolSegments[0].Hash {
		t.Fatal("expected changed tool schema to alter tool segment hash")
	}
}

func TestBuildCacheShapeRequestKindAffectsRequestHash(t *testing.T) {
	history := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleUser, Text: "hi"},
	}

	agentShape := buildCacheShapeForRequest(cacheShapeRequestAgent, history, nil, "", nil)
	compactShape := buildCacheShapeForRequest(cacheShapeRequestCompact, history, nil, "", nil)
	if agentShape.RequestKind != cacheShapeRequestAgent {
		t.Fatalf("agent request kind = %q", agentShape.RequestKind)
	}
	if compactShape.RequestKind != cacheShapeRequestCompact {
		t.Fatalf("compact request kind = %q", compactShape.RequestKind)
	}
	if agentShape.RequestHash == compactShape.RequestHash {
		t.Fatal("expected request kind to affect request hash")
	}
}

func TestBuildCacheShapePreservesToolOrder(t *testing.T) {
	history := []core.Message{{Role: core.RoleSystem, Text: "system"}}
	firstOrder := core.NewToolRegistry([]core.Tool{
		cacheShapeTool{name: "alpha", description: "first", params: map[string]any{"type": "object"}},
		cacheShapeTool{name: "beta", description: "second", params: map[string]any{"type": "object"}},
	})
	secondOrder := core.NewToolRegistry([]core.Tool{
		cacheShapeTool{name: "beta", description: "second", params: map[string]any{"type": "object"}},
		cacheShapeTool{name: "alpha", description: "first", params: map[string]any{"type": "object"}},
	})

	first := buildCacheShape(history, firstOrder.Tools(), "")
	second := buildCacheShape(history, secondOrder.Tools(), "")
	if first.ToolsHash == second.ToolsHash {
		t.Fatal("expected tool order to affect tools hash")
	}
	if first.RequestHash == second.RequestHash {
		t.Fatal("expected tool order to affect request hash")
	}
}

func TestBuildCacheShapeIncludesAssistantPrefix(t *testing.T) {
	history := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleUser, Text: "complete json"},
	}
	tools := core.NewToolRegistry(nil)

	first := buildCacheShape(history, tools.Tools(), "{")
	second := buildCacheShape(history, tools.Tools(), "[")
	if first.AssistantPrefixHash == "" || second.AssistantPrefixHash == "" {
		t.Fatalf("missing assistant prefix hash: %+v %+v", first, second)
	}
	if first.AssistantPrefixHash == second.AssistantPrefixHash {
		t.Fatal("expected different assistant prefixes to alter assistant prefix hash")
	}
	if first.RequestHash == second.RequestHash {
		t.Fatal("expected different assistant prefixes to alter request hash")
	}
}

func TestBuildCacheShapePreservesAssistantPrefixWhitespace(t *testing.T) {
	history := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleUser, Text: "complete json"},
	}
	tools := core.NewToolRegistry(nil)

	compactPrefix := buildCacheShape(history, tools.Tools(), "{")
	newlinePrefix := buildCacheShape(history, tools.Tools(), "{\n")
	if compactPrefix.AssistantPrefixHash == newlinePrefix.AssistantPrefixHash {
		t.Fatal("expected assistant prefix whitespace to affect prefix hash")
	}
	if compactPrefix.RequestHash == newlinePrefix.RequestHash {
		t.Fatal("expected assistant prefix whitespace to affect request hash")
	}
}

func TestBuildCacheShapeHashesReplayedToolResultContent(t *testing.T) {
	rawA := strings.Repeat("a", 4000) + strings.Repeat("x", 4000) + strings.Repeat("z", 4000)
	rawB := strings.Repeat("a", 4000) + strings.Repeat("y", 4000) + strings.Repeat("z", 4000)
	base := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call_1", Name: "shell_run", Input: "{}"}}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "call_1", Name: "shell_run", Content: rawA, Metadata: map[string]any{"ignored": "a"}}}},
	}
	changedRawOnly := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call_1", Name: "shell_run", Input: "{}"}}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "call_1", Name: "shell_run", Content: rawB, Metadata: map[string]any{"ignored": "b"}}}},
	}

	first := buildCacheShape(base, nil, "")
	second := buildCacheShape(changedRawOnly, nil, "")
	if first.RequestHash != second.RequestHash {
		t.Fatalf("raw-only compacted content changed request hash: %s != %s", first.RequestHash, second.RequestHash)
	}
	if first.LogTailHash != second.LogTailHash {
		t.Fatalf("raw-only compacted content changed tail hash: %s != %s", first.LogTailHash, second.LogTailHash)
	}
}

func TestBuildCacheShapeIgnoresReasoningWithoutToolCalls(t *testing.T) {
	base := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleUser, Text: "hi"},
		{Role: core.RoleAssistant, Text: "answer", Reasoning: "local reasoning a"},
	}
	changedReasoning := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleUser, Text: "hi"},
		{Role: core.RoleAssistant, Text: "answer", Reasoning: "local reasoning b"},
	}

	first := buildCacheShape(base, nil, "")
	second := buildCacheShape(changedReasoning, nil, "")
	if first.RequestHash != second.RequestHash {
		t.Fatalf("reasoning-only assistant changed request hash: %s != %s", first.RequestHash, second.RequestHash)
	}
	if first.LogTailHash != second.LogTailHash {
		t.Fatalf("reasoning-only assistant changed tail hash: %s != %s", first.LogTailHash, second.LogTailHash)
	}
}

func TestBuildCacheShapeKeepsReasoningWithToolCalls(t *testing.T) {
	base := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleAssistant, Reasoning: "need tool a", ToolCalls: []core.ToolCall{{ID: "call_1", Name: "shell_run", Input: "{}"}}},
	}
	changedReasoning := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleAssistant, Reasoning: "need tool b", ToolCalls: []core.ToolCall{{ID: "call_1", Name: "shell_run", Input: "{}"}}},
	}

	first := buildCacheShape(base, nil, "")
	second := buildCacheShape(changedReasoning, nil, "")
	if first.RequestHash == second.RequestHash {
		t.Fatal("expected tool-call assistant reasoning to affect request hash")
	}
	if first.LogTailHash == second.LogTailHash {
		t.Fatal("expected tool-call assistant reasoning to affect tail hash")
	}
}

func TestBuildCacheShapeIgnoresBlankToolCallIDStoredResult(t *testing.T) {
	base := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{Name: "shell_run", Input: "{}"}}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{{Name: "shell_run", Content: "result a"}}},
	}
	changedResult := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{Name: "shell_run", Input: "{}"}}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{{Name: "shell_run", Content: "result b"}}},
	}

	first := buildCacheShape(base, nil, "")
	second := buildCacheShape(changedResult, nil, "")
	if first.RequestHash != second.RequestHash {
		t.Fatalf("blank-ID stored tool result changed request hash: %s != %s", first.RequestHash, second.RequestHash)
	}
	if first.LogTailHash != second.LogTailHash {
		t.Fatalf("blank-ID stored tool result changed tail hash: %s != %s", first.LogTailHash, second.LogTailHash)
	}
}

func TestBuildCacheShapeSplitsLogHeadAndTail(t *testing.T) {
	history := []core.Message{{Role: core.RoleSystem, Text: "system"}}
	for i := 0; i < cacheShapeTailMessages+2; i++ {
		history = append(history, core.Message{Role: core.RoleUser, Text: string(rune('a' + i))})
	}

	shape := buildCacheShape(history, nil, "")
	if shape.LogMessages != cacheShapeTailMessages+2 {
		t.Fatalf("log messages = %d, want %d", shape.LogMessages, cacheShapeTailMessages+2)
	}
	if shape.TailMessages != cacheShapeTailMessages {
		t.Fatalf("tail messages = %d, want %d", shape.TailMessages, cacheShapeTailMessages)
	}
	if shape.LogHeadHash == "" || shape.LogTailHash == "" || shape.RequestHash == "" {
		t.Fatalf("missing split hashes: %+v", shape)
	}

	headChanged := append([]core.Message(nil), history...)
	headChanged[1].Text = "head changed"
	headShape := buildCacheShape(headChanged, nil, "")
	if headShape.LogHeadHash == shape.LogHeadHash {
		t.Fatal("expected head change to alter log head hash")
	}
	if headShape.LogTailHash != shape.LogTailHash {
		t.Fatal("expected head change to leave log tail hash stable")
	}

	tailChanged := append([]core.Message(nil), history...)
	tailChanged[len(tailChanged)-1].Text = "tail changed"
	tailShape := buildCacheShape(tailChanged, nil, "")
	if tailShape.LogHeadHash != shape.LogHeadHash {
		t.Fatal("expected tail change to leave log head hash stable")
	}
	if tailShape.LogTailHash == shape.LogTailHash {
		t.Fatal("expected tail change to alter log tail hash")
	}
}

func TestBuildCacheShapeShortHistoryHasNoHeadHash(t *testing.T) {
	history := []core.Message{
		{Role: core.RoleSystem, Text: "system"},
		{Role: core.RoleUser, Text: "hi"},
	}
	shape := buildCacheShape(history, nil, "")
	if shape.LogHeadHash != "" {
		t.Fatalf("short history head hash = %q, want empty", shape.LogHeadHash)
	}
	if shape.LogTailHash == "" || shape.TailMessages != 1 || shape.LogMessages != 1 {
		t.Fatalf("unexpected short history shape: %+v", shape)
	}
}
