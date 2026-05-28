package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigMissingFileIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("servers: %+v", cfg.Servers)
	}
}

func TestLoadConfigSupportsServersAndMCPServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{
		"servers": {"fs": {"command": "node", "args": ["server.js"], "disabled_tools": ["write"]}},
		"mcpServers": {"mem": {"command": "memory"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != path {
		t.Fatalf("path: %s", cfg.Path)
	}
	if cfg.Servers["fs"].Name != "fs" || cfg.Servers["mem"].Name != "mem" {
		t.Fatalf("servers: %+v", cfg.Servers)
	}
	if cfg.Servers["fs"].DisabledTools[0] != "write" {
		t.Fatalf("disabled tools: %+v", cfg.Servers["fs"].DisabledTools)
	}
}

func TestLoadConfigAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	b := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{
		"mcpServers": {"mem": {"command": "memory"}}
	}`)...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["mem"].Name != "mem" {
		t.Fatalf("servers: %+v", cfg.Servers)
	}
}

func TestLoadConfigSupportsCommonHTTPServerFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{
		"servers": {
			"context7": {
				"type": "http",
				"url": "https://mcp.context7.com/mcp",
				"headers": {"CONTEXT7_API_KEY": "token"}
			},
			"linear": {
				"url": "https://mcp.linear.app/mcp",
				"headers": {"Authorization": "Bearer token"}
			}
		},
		"mcpServers": {
			"cloudflare": {
				"transport": "streamable-http",
				"url": "https://mcp.cloudflare.com/mcp"
			},
			"atlassian": {
				"transport": "http",
				"url": "https://mcp.atlassian.com/v1/mcp",
				"headers": {"Authorization": "Bearer token"}
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"context7", "linear", "cloudflare", "atlassian"} {
		srv := cfg.Servers[name]
		if srv.Name != name {
			t.Fatalf("%s server not parsed: %+v", name, cfg.Servers)
		}
		kind, err := srv.transportKind()
		if err != nil {
			t.Fatalf("%s transport: %v", name, err)
		}
		if kind != "http" {
			t.Fatalf("%s transport = %q", name, kind)
		}
	}
	if got := cfg.Servers["context7"].Headers["CONTEXT7_API_KEY"]; got != "token" {
		t.Fatalf("header = %q", got)
	}
}

func TestTransportKindRejectsConflictsAndUnsupportedValues(t *testing.T) {
	if _, err := (ServerConfig{Type: "stdio", Transport: "http", Command: "node"}).transportKind(); err == nil {
		t.Fatal("expected conflicting transport error")
	}
	if _, err := (ServerConfig{Type: "sse", URL: "https://example.com/mcp"}).transportKind(); err == nil {
		t.Fatal("expected unsupported transport error")
	}
}

func TestFilesystemAllowedDirsExtractsServerFilesystemArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rel := filepath.Join(t.TempDir(), "workspace")
	cfg := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "~/docs", rel, "--ignored-flag"},
	}

	got := cfg.filesystemAllowedDirs()
	wantHome := filepath.Join(home, "docs")
	wantRel, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != wantHome || got[1] != wantRel {
		t.Fatalf("filesystemAllowedDirs() = %#v, want %#v", got, []string{wantHome, wantRel})
	}
}

func TestFilesystemAllowedDirsUsesExpandedStdioArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	cfg := ServerConfig{
		Command: "npx",
		Args: []string{
			"-y",
			"@modelcontextprotocol/server-filesystem",
			expandStdioValue("%USERPROFILE%/docs", false, "windows", os.Getenv, os.UserHomeDir),
		},
	}

	got := filesystemAllowedDirsFromArgs(expandStdioCommand(cfg.Command), cfg.Args)
	want := filepath.Join(home, "docs")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("filesystemAllowedDirsFromArgs() = %#v, want %#v", got, []string{want})
	}
}

func TestFilesystemAllowedDirsIgnoresNonFilesystemServers(t *testing.T) {
	got := (ServerConfig{Command: "node", Args: []string{"server.js", "/tmp"}}).filesystemAllowedDirs()
	if len(got) != 0 {
		t.Fatalf("filesystemAllowedDirs() = %#v, want empty", got)
	}
}

func TestResolvedHeadersExpandsEnvAndDoesNotLeakMissingValue(t *testing.T) {
	t.Setenv("WHALE_MCP_HEADER_TOKEN", "secret-token")
	headers, err := resolvedHeaders(map[string]string{
		"Authorization": "Bearer ${WHALE_MCP_HEADER_TOKEN}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers["Authorization"]; got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got)
	}
	_, err = resolvedHeaders(map[string]string{
		"Authorization": "Bearer ${WHALE_MISSING_HEADER_TOKEN}",
	})
	if err == nil {
		t.Fatal("expected missing env error")
	}
	if !strings.Contains(err.Error(), "WHALE_MISSING_HEADER_TOKEN") {
		t.Fatalf("error = %q", err)
	}
	if strings.Contains(err.Error(), "Bearer ") {
		t.Fatalf("error leaked header value shape: %q", err)
	}
}

func TestResolvedHeadersRejectsInvalidHeaderNameAndValue(t *testing.T) {
	if _, err := resolvedHeaders(map[string]string{"Bad Header": "x"}); err == nil {
		t.Fatal("expected invalid header name error")
	}
	if _, err := resolvedHeaders(map[string]string{"X-Test": "bad\nvalue"}); err == nil {
		t.Fatal("expected invalid header value error")
	}
}
