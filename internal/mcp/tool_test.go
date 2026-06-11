package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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

func TestToolCapabilitiesExposeFilesystemAllowedDirs(t *testing.T) {
	tool := &Tool{allowedDirs: []string{"/tmp", "/workspace"}}
	got := strings.Join(tool.Capabilities(), "\n")
	for _, want := range []string{
		"mcp_filesystem",
		"mcp_filesystem_allowed_dir:/tmp",
		"mcp_filesystem_allowed_dir:/workspace",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capabilities missing %q: %v", want, tool.Capabilities())
		}
	}
}

func TestMCPResultWrapsTextAndMedia(t *testing.T) {
	res := mcpResult(core.ToolCall{ID: "call", Name: "mcp__img__show"}, "img", "show", &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{Text: "hello"},
			&sdk.ImageContent{MIMEType: "image/png", Data: []byte("abc")},
		},
	})
	if res.IsError() {
		t.Fatalf("unexpected error: %+v", res)
	}
	if !strings.Contains(res.ModelText, "hello") || !strings.Contains(res.ModelText, "image/png") {
		t.Fatalf("content: %s", res.ModelText)
	}
}

func TestMCPResultMarksToolError(t *testing.T) {
	res := mcpResult(core.ToolCall{ID: "call", Name: "mcp__fs__read"}, "fs", "read", &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: "failed"}},
		IsError: true,
	})
	if !res.IsError() || !strings.Contains(res.ModelText, "mcp_tool_error") {
		t.Fatalf("result: %+v", res)
	}
}

func TestToolRunRejectsInvalidJSON(t *testing.T) {
	tool := &Tool{registeredName: "mcp__fs__read", serverName: "fs", toolName: "read"}
	res, err := tool.Run(context.Background(), core.ToolCall{ID: "call", Name: tool.Name(), Input: "not-json"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, "invalid_mcp_input") {
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

func TestToolRunPreflightsFilesystemAllowedDirs(t *testing.T) {
	allowed := t.TempDir()
	tool := &Tool{
		registeredName: "mcp__fs__search_files",
		serverName:     "fs",
		toolName:       "search_files",
		manager:        NewManager(Config{}),
		allowedDirs:    []string{allowed},
	}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "call",
		Name:  tool.Name(),
		Input: `{"path":"/Users/goranka/Engineer/ai/dsk/whale","pattern":"init_skill.py"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, `"code":"mcp_allowed_dirs_denied"`) || !strings.Contains(res.ModelText, "Whale built-in file tools") {
		t.Fatalf("expected allowed-dirs denial before manager call, got %+v", res)
	}
}

func TestToolRunAllowsPathsInsideFilesystemAllowedDirs(t *testing.T) {
	allowed := t.TempDir()
	tool := &Tool{
		registeredName: "mcp__fs__read_file",
		serverName:     "fs",
		toolName:       "read_file",
		manager:        NewManager(Config{}),
		allowedDirs:    []string{allowed},
	}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "call",
		Name:  tool.Name(),
		Input: `{"path":` + strconv.Quote(filepath.Join(allowed, "README.md")) + `}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, `"code":"mcp_call_failed"`) {
		t.Fatalf("expected path to reach manager and fail there, got %+v", res)
	}
}

func TestToolRunPreflightCanonicalizesFilesystemAllowedDirs(t *testing.T) {
	root := t.TempDir()
	realAllowed := filepath.Join(root, "real-allowed")
	if err := os.MkdirAll(realAllowed, 0o755); err != nil {
		t.Fatal(err)
	}
	allowedLink := filepath.Join(root, "allowed-link")
	if err := os.Symlink(realAllowed, allowedLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tool := &Tool{
		registeredName: "mcp__fs__read_file",
		serverName:     "fs",
		toolName:       "read_file",
		manager:        NewManager(Config{}),
		allowedDirs:    []string{allowedLink},
	}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "call",
		Name:  tool.Name(),
		Input: `{"path":` + strconv.Quote(filepath.Join(realAllowed, "missing.txt")) + `}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.ModelText, `"code":"mcp_allowed_dirs_denied"`) {
		t.Fatalf("expected symlink-equivalent path to reach manager, got %+v", res)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, `"code":"mcp_call_failed"`) {
		t.Fatalf("expected path to reach manager and fail there, got %+v", res)
	}
}
