//go:build windows

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usewhale/whale/internal/app"
)

const (
	windowsServiceMCPHelperEnv   = "WHALE_SERVICE_MCP_PROCESS_TREE_HELPER"
	windowsServiceMCPChildPIDEnv = "WHALE_SERVICE_MCP_PROCESS_TREE_CHILD_PID"
)

func TestWindowsServiceCloseKillsMCPProcessTree(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	mcpPath := filepath.Join(dir, "mcp.json")
	writeWindowsServiceMCPConfig(t, mcpPath, pidPath)

	cfg := app.DefaultConfig()
	cfg.DataDir = dir
	cfg.MCPConfigPath = mcpPath
	svc, err := New(context.Background(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatal(err)
	}

	waitForWindowsServiceMCPState(t, svc, "tree", "connected", 5*time.Second)
	waitForWindowsServiceMCPFile(t, pidPath, 3*time.Second)
	childPID := strings.TrimSpace(string(mustReadWindowsServiceMCPFile(t, pidPath)))
	if childPID == "" {
		t.Fatal("mcp helper did not write child pid")
	}
	if !windowsServiceMCPPIDExists(t, childPID) {
		t.Fatalf("child process %s was not running before service close", childPID)
	}

	if err := svc.Close(); err != nil {
		t.Fatalf("close service: %v", err)
	}
	waitForWindowsServiceMCPPIDExit(t, childPID, 4*time.Second)
}

func TestWindowsServiceMCPProcessTreeHelper(t *testing.T) {
	switch os.Getenv(windowsServiceMCPHelperEnv) {
	case "server":
		os.Exit(runWindowsServiceMCPProcessTreeServer())
	case "child":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func runWindowsServiceMCPProcessTreeServer() int {
	if err := startWindowsServiceMCPChild(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "whale-service-process-tree-test", Version: "v0.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "noop", Description: "noop"}, func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, struct{}{}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	return 0
}

func startWindowsServiceMCPChild() error {
	pidPath := os.Getenv(windowsServiceMCPChildPIDEnv)
	if pidPath == "" {
		return fmt.Errorf("missing %s", windowsServiceMCPChildPIDEnv)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestWindowsServiceMCPProcessTreeHelper")
	cmd.Env = append(os.Environ(), windowsServiceMCPHelperEnv+"=child")
	if err := cmd.Start(); err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
}

func writeWindowsServiceMCPConfig(t *testing.T, path, pidPath string) {
	t.Helper()
	cfg := map[string]any{
		"servers": map[string]any{
			"tree": map[string]any{
				"command": os.Args[0],
				"args":    []string{"-test.run=TestWindowsServiceMCPProcessTreeHelper"},
				"env": map[string]string{
					windowsServiceMCPHelperEnv:   "server",
					windowsServiceMCPChildPIDEnv: pidPath,
				},
				"timeout": 5,
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForWindowsServiceMCPState(t *testing.T, svc *Service, name, status string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, st := range svc.app.MCPStates() {
			if st.Name == name && st.Status == status {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for mcp state %s=%s; got %+v", name, status, svc.app.MCPStates())
}

func waitForWindowsServiceMCPPIDExit(t *testing.T, pid string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !windowsServiceMCPPIDExists(t, pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %s survived after %s", pid, timeout)
}

func windowsServiceMCPPIDExists(t *testing.T, pid string) bool {
	t.Helper()
	out, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").CombinedOutput()
	if err != nil {
		t.Fatalf("tasklist failed: %v\n%s", err, string(out))
	}
	return strings.Contains(string(out), `"`+pid+`"`)
}

func waitForWindowsServiceMCPFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func mustReadWindowsServiceMCPFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
