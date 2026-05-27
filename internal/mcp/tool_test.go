package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usewhale/whale/internal/core"
)

func TestToolParametersCoerceSchema(t *testing.T) {
	tool := &Tool{registeredName: "mcp__fs__read", spec: &sdk.Tool{Name: "read"}}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Fatalf("params: %+v", params)
	}
}

func TestToolReadOnlyUsesMCPAnnotation(t *testing.T) {
	tests := []struct {
		name string
		spec *sdk.Tool
		want bool
	}{
		{
			name: "read only hint true",
			spec: &sdk.Tool{
				Name:        "read",
				Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
			},
			want: true,
		},
		{
			name: "read only hint false",
			spec: &sdk.Tool{
				Name:        "write",
				Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false},
			},
			want: false,
		},
		{
			name: "no annotations",
			spec: &sdk.Tool{Name: "unknown"},
			want: false,
		},
		{
			name: "nil spec",
			spec: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &Tool{registeredName: "mcp__fs__" + tt.name, spec: tt.spec}
			if got := tool.ReadOnly(); got != tt.want {
				t.Fatalf("ReadOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMCPResultWrapsTextAndMedia(t *testing.T) {
	res := mcpResult(core.ToolCall{ID: "call", Name: "mcp__img__show"}, "img", "show", &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{Text: "hello"},
			&sdk.ImageContent{MIMEType: "image/png", Data: []byte("abc")},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	if !strings.Contains(res.Content, "hello") || !strings.Contains(res.Content, "image/png") {
		t.Fatalf("content: %s", res.Content)
	}
}

func TestMCPResultMarksToolError(t *testing.T) {
	res := mcpResult(core.ToolCall{ID: "call", Name: "mcp__fs__read"}, "fs", "read", &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: "failed"}},
		IsError: true,
	})
	if !res.IsError || !strings.Contains(res.Content, "mcp_tool_error") {
		t.Fatalf("result: %+v", res)
	}
}

func TestToolRunRejectsInvalidJSON(t *testing.T) {
	tool := &Tool{registeredName: "mcp__fs__read", serverName: "fs", toolName: "read"}
	res, err := tool.Run(context.Background(), core.ToolCall{ID: "call", Name: tool.Name(), Input: "not-json"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid_mcp_input") {
		t.Fatalf("result: %+v", res)
	}
}

func TestToolDescriptionIncludesServerAndTool(t *testing.T) {
	tool := &Tool{
		registeredName: "mcp__fs__search_files",
		serverName:     "fs",
		toolName:       "search_files",
		spec:           &sdk.Tool{Name: "search_files", Description: "Search files"},
	}
	desc := tool.Description()
	for _, want := range []string{"Search files", "MCP server: fs", "tool: search_files"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("expected %q in description: %s", want, desc)
		}
	}
}

func TestToolRunDoesNotPreflightFilesystemPaths(t *testing.T) {
	tool := &Tool{
		registeredName: "mcp__fs__search_files",
		serverName:     "fs",
		toolName:       "search_files",
		manager:        NewManager(Config{}),
	}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "call",
		Name:  tool.Name(),
		Input: `{"path":"/Users/goranka/Engineer/ai/dsk/whale","pattern":"init_skill.py"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, `"code":"mcp_call_failed"`) {
		t.Fatalf("expected call to reach manager and fail there, got %+v", res)
	}
}
