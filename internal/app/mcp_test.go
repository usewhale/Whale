package app

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestHandleLocalCommandMCPShowsEmptyStatus(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	a, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handled, out, _, err := a.HandleLocalCommand("/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /mcp handled")
	}
	if !strings.Contains(out, "MCP") || !strings.Contains(out, "servers: none") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestHandleLocalCommandMCPShowsConfiguredServersBeforeStartup(t *testing.T) {
	dir := t.TempDir()
	mcpPath := dir + "/mcp.json"
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{"context7":{"command":"context7-mcp"},"fs":{"command":"fs-mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.MCPConfigPath = mcpPath
	a, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	handled, out, _, err := a.HandleLocalCommand("/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected /mcp handled")
	}
	for _, want := range []string{"servers: 2", "- context7", "  status: pending", "- fs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestNewDoesNotBlockOnMCPStartup(t *testing.T) {
	dir := t.TempDir()
	mcpPath := dir + "/mcp.json"
	if err := os.WriteFile(mcpPath, []byte(`{"servers":{"slow":{"command":"sh","args":["-c","sleep 2"],"timeout":2}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.MCPConfigPath = mcpPath

	start := time.Now()
	a, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("New blocked on MCP startup for %s", elapsed)
	}
}
