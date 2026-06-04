package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usewhale/whale/internal/core"
	whalemcp "github.com/usewhale/whale/internal/mcp"
)

type mcpRuntimeTestInput struct {
	Message string `json:"message"`
}

type mcpRuntimeTestOutput struct {
	Message string `json:"message"`
}

func TestRefreshMCPToolsAllowsIdentityAfterFreeze(t *testing.T) {
	mgr := newMCPRuntimeTestManager(t, "echoes a message")
	app := newMCPRuntimeTestApp(mgr)

	if err := app.refreshMCPTools(); err != nil {
		t.Fatalf("initial refreshMCPTools: %v", err)
	}
	app.freezeMCPToolSignature()
	if err := app.refreshMCPTools(); err != nil {
		t.Fatalf("identity refreshMCPTools: %v", err)
	}
	if app.toolRegistry.Get("mcp__runtime__echo") == nil {
		t.Fatal("mcp tool should remain registered after identity refresh")
	}
}

func TestRefreshMCPToolsRejectsDescriptionChangeAfterFreeze(t *testing.T) {
	first := newMCPRuntimeTestManager(t, "echoes a message")
	app := newMCPRuntimeTestApp(first)
	if err := app.refreshMCPTools(); err != nil {
		t.Fatalf("initial refreshMCPTools: %v", err)
	}
	app.freezeMCPToolSignature()

	spec, ok := app.toolRegistry.Spec("mcp__runtime__echo")
	if !ok {
		t.Fatal("mcp tool spec missing after initial refresh")
	}
	if !strings.Contains(spec.Description, "echoes a message") {
		t.Fatalf("unexpected initial description: %q", spec.Description)
	}

	second := newMCPRuntimeTestManager(t, "echoes a message differently")
	app.mcpManager = second
	err := app.refreshMCPTools()
	if err == nil || !strings.Contains(err.Error(), "restart Whale") {
		t.Fatalf("expected restart-required error, got %v", err)
	}
	if !strings.Contains(err.Error(), "changed mcp__runtime__echo") {
		t.Fatalf("expected changed tool detail, got %v", err)
	}
	spec, ok = app.toolRegistry.Spec("mcp__runtime__echo")
	if !ok {
		t.Fatal("mcp tool spec disappeared after rejected refresh")
	}
	if strings.Contains(spec.Description, "differently") {
		t.Fatalf("rejected refresh changed registry description: %q", spec.Description)
	}
}

func TestMCPToolSetSignatureChangesWithSchema(t *testing.T) {
	first, err := mcpToolSetSignature([]core.Tool{mcpSignatureTestTool{
		name:        "mcp__runtime__echo",
		description: "echoes a message",
		parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
		},
	}})
	if err != nil {
		t.Fatalf("first signature: %v", err)
	}
	second, err := mcpToolSetSignature([]core.Tool{mcpSignatureTestTool{
		name:        "mcp__runtime__echo",
		description: "echoes a message",
		parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}, "upper": map[string]any{"type": "boolean"}},
		},
	}})
	if err != nil {
		t.Fatalf("second signature: %v", err)
	}
	if first == second {
		t.Fatal("schema change should change MCP tool-set signature")
	}
}

func TestMCPToolSetDeltaReportsAddedRemovedChanged(t *testing.T) {
	prev := map[string]string{
		"mcp__old__same":    `{"same":true}`,
		"mcp__old__changed": `{"version":1}`,
		"mcp__old__removed": `{"removed":true}`,
	}
	next := map[string]string{
		"mcp__old__same":    `{"same":true}`,
		"mcp__old__changed": `{"version":2}`,
		"mcp__new__added":   `{"added":true}`,
	}
	got := mcpToolSetDelta(prev, next)
	for _, want := range []string{
		"added mcp__new__added",
		"removed mcp__old__removed",
		"changed mcp__old__changed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("delta missing %q: %s", want, got)
		}
	}
}

func newMCPRuntimeTestApp(mgr *whalemcp.Manager) *App {
	return &App{
		mcpManager:           mgr,
		baseToolRegistry:     core.NewToolRegistry(nil),
		subagentToolRegistry: core.NewToolRegistry(nil),
		toolRegistry:         core.NewToolRegistry(nil),
	}
}

func newMCPRuntimeTestManager(t *testing.T, description string) *whalemcp.Manager {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "whale-app-test-mcp", Version: "v0.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: description}, func(ctx context.Context, req *sdk.CallToolRequest, input mcpRuntimeTestInput) (*sdk.CallToolResult, mcpRuntimeTestOutput, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "echo:" + input.Message}},
		}, mcpRuntimeTestOutput{Message: input.Message}, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	mgr := whalemcp.NewManager(whalemcp.Config{
		Servers: map[string]whalemcp.ServerConfig{
			"runtime": {
				Type:    "http",
				URL:     httpServer.URL,
				Timeout: 5,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

type mcpSignatureTestTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (t mcpSignatureTestTool) Name() string { return t.name }

func (t mcpSignatureTestTool) Description() string { return t.description }

func (t mcpSignatureTestTool) Parameters() map[string]any { return t.parameters }

func (t mcpSignatureTestTool) Run(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
