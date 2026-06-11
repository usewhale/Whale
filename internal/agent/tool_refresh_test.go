package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

type namedContentTool struct {
	name    string
	content string
}

func (t namedContentTool) Name() string { return t.name }
func (t namedContentTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: t.content}, nil
}

type toolCaptureProvider struct {
	seen [][]string
}

func (p *toolCaptureProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	p.seen = append(p.seen, toolNames(tools))
	return eventStream(endTurnEvent("done"))
}

func TestRunRefreshesToolsBeforeProviderRequest(t *testing.T) {
	reg := NewToolRegistry(nil)
	provider := &toolCaptureProvider{}
	ag := NewAgentWithRegistry(provider, NewInMemoryStore(), reg,
		WithToolRefresh(func(context.Context) error {
			return reg.ReplaceTools([]core.Tool{namedContentTool{name: "mcp__fs__list_directory"}})
		}),
	)

	if _, err := ag.RunSession(context.Background(), "s-refresh-start", "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(provider.seen) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.seen))
	}
	if !reflect.DeepEqual(provider.seen[0], []string{"mcp__fs__list_directory"}) {
		t.Fatalf("provider saw tools %+v", provider.seen[0])
	}
}

type recursiveRefreshProvider struct {
	seen [][]string
}

func (p *recursiveRefreshProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	p.seen = append(p.seen, toolNames(tools))
	if len(p.seen) == 1 {
		return eventStream(toolUseEvent(toolCall("tc-first", "first", `{}`)))
	}
	return eventStream(endTurnEvent("done"))
}

func TestRunRefreshesToolsBetweenToolIterations(t *testing.T) {
	reg := NewToolRegistry(nil)
	refreshes := 0
	provider := &recursiveRefreshProvider{}
	ag := NewAgentWithRegistry(provider, NewInMemoryStore(), reg,
		WithToolRefresh(func(context.Context) error {
			refreshes++
			tools := []core.Tool{namedContentTool{name: "first", content: "first-ok"}}
			if refreshes > 1 {
				tools = append(tools, namedContentTool{name: "second", content: "second-ok"})
			}
			return reg.ReplaceTools(tools)
		}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
	)

	if _, err := ag.RunSession(context.Background(), "s-refresh-recursive", "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(provider.seen) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(provider.seen))
	}
	if !reflect.DeepEqual(provider.seen[0], []string{"first"}) {
		t.Fatalf("first provider call saw tools %+v", provider.seen[0])
	}
	if !reflect.DeepEqual(provider.seen[1], []string{"first", "second"}) {
		t.Fatalf("second provider call saw tools %+v", provider.seen[1])
	}
}

type mutateDuringStreamProvider struct {
	reg   *core.ToolRegistry
	calls int
}

func (p *mutateDuringStreamProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls > 1 {
		return eventStream(endTurnEvent("done"))
	}
	_ = p.reg.ReplaceTools([]core.Tool{namedContentTool{name: "new", content: "new-ok"}})
	return eventStream(toolUseEvent(toolCall("tc-old", "old", `{}`)))
}

func TestToolDispatchUsesProviderRequestSnapshot(t *testing.T) {
	reg := NewToolRegistry([]core.Tool{namedContentTool{name: "old", content: "old-ok"}})
	provider := &mutateDuringStreamProvider{reg: reg}
	ag := NewAgentWithRegistry(provider, NewInMemoryStore(), reg,
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
	)

	if _, err := ag.RunSession(context.Background(), "s-snapshot-dispatch", "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	all, err := ag.store.List(context.Background(), "s-snapshot-dispatch")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(all) < 3 || len(all[2].ToolResults) != 1 {
		t.Fatalf("expected one tool result in third message, got %+v", all)
	}
	res := all[2].ToolResults[0]
	if res.IsError() || res.Name != "old" || !strings.Contains(res.ModelText, "old-ok") {
		t.Fatalf("expected old tool to dispatch from snapshot, got %+v", res)
	}
}

func toolNames(tools []Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}
