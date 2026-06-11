//go:build windows

package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	windowsMCPHelperEnv   = "WHALE_MCP_PROCESS_TREE_HELPER"
	windowsMCPChildPIDEnv = "WHALE_MCP_PROCESS_TREE_CHILD_PID"
)

type spawnChildInput struct{}
type spawnChildOutput struct {
	Spawned bool `json:"spawned"`
}

func TestWindowsMCPStdioCloseKillsProcessTree(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"tree": {
				Command: os.Args[0],
				Args:    []string{"-test.run=TestWindowsMCPProcessTreeHelper"},
				Env: map[string]string{
					windowsMCPHelperEnv:   "server",
					windowsMCPChildPIDEnv: pidPath,
				},
				Timeout: 5,
			},
		},
	})
	mgr.Initialize(context.Background())

	if states := mgr.States(); len(states) != 1 || !states[0].Connected {
		t.Fatalf("mcp did not connect: %+v", states)
	}
	if _, err := mgr.CallTool(context.Background(), "tree", "spawn_child", map[string]any{}); err != nil {
		t.Fatalf("call spawn_child: %v", err)
	}

	waitForWindowsMCPFile(t, pidPath, 3*time.Second)
	childPID := strings.TrimSpace(string(mustReadWindowsMCPFile(t, pidPath)))
	if childPID == "" {
		t.Fatal("mcp helper did not write child pid")
	}
	if !windowsMCPPIDExists(t, childPID) {
		t.Fatalf("child process %s was not running before close", childPID)
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	waitForWindowsMCPPIDExit(t, childPID, 4*time.Second)
}

func TestWindowsMCPStdioTimeoutKillsProcessTree(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"hang": {
				Command: os.Args[0],
				Args:    []string{"-test.run=TestWindowsMCPProcessTreeHelper"},
				Env: map[string]string{
					windowsMCPHelperEnv:   "hanging-server",
					windowsMCPChildPIDEnv: pidPath,
				},
				Timeout: 1,
			},
		},
	})
	mgr.Initialize(context.Background())

	waitForWindowsMCPFile(t, pidPath, 3*time.Second)
	childPID := strings.TrimSpace(string(mustReadWindowsMCPFile(t, pidPath)))
	if childPID == "" {
		t.Fatal("mcp helper did not write child pid")
	}
	waitForWindowsMCPPIDExit(t, childPID, 4*time.Second)

	states := mgr.States()
	if len(states) != 1 || states[0].Status != StatusFailed {
		t.Fatalf("expected failed mcp state, got %+v", states)
	}
}

func TestWindowsMCPStdioExpandsPercentEnvInCommandAndArgs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHALE_MCP_TEST_DIR", dir)

	_, cmd, _, err := createTransport(context.Background(), "stdio", ServerConfig{
		Name:    "envpath",
		Command: `%WHALE_MCP_TEST_DIR%\server.exe`,
		Args:    []string{`--config=%WHALE_MCP_TEST_DIR%\mcp.json`},
	})
	if err != nil {
		t.Fatalf("createTransport: %v", err)
	}
	wantCommand := filepath.Join(dir, "server.exe")
	if cmd.Path != wantCommand {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, wantCommand)
	}
	wantArg := "--config=" + filepath.Join(dir, "mcp.json")
	if len(cmd.Args) != 2 || cmd.Args[1] != wantArg {
		t.Fatalf("cmd.Args = %#v, want second arg %q", cmd.Args, wantArg)
	}
}

func TestWindowsMCPProcessTreeHelper(t *testing.T) {
	switch os.Getenv(windowsMCPHelperEnv) {
	case "server":
		os.Exit(runWindowsMCPProcessTreeServer())
	case "hanging-server":
		os.Exit(runWindowsMCPHangingProcessTreeServer())
	case "child":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func runWindowsMCPProcessTreeServer() int {
	server := sdk.NewServer(&sdk.Implementation{Name: "whale-process-tree-test", Version: "v0.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "spawn_child", Description: "spawns a long-running child"}, func(ctx context.Context, req *sdk.CallToolRequest, input spawnChildInput) (*sdk.CallToolResult, spawnChildOutput, error) {
		if err := startWindowsMCPChild(); err != nil {
			return nil, spawnChildOutput{}, err
		}
		return &sdk.CallToolResult{
			ModelText: []sdk.Content{&sdk.TextContent{Text: "spawned"}},
		}, spawnChildOutput{Spawned: true}, nil
	})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

func runWindowsMCPHangingProcessTreeServer() int {
	if err := startWindowsMCPChild(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	time.Sleep(30 * time.Second)
	return 0
}

func startWindowsMCPChild() error {
	pidPath := os.Getenv(windowsMCPChildPIDEnv)
	if pidPath == "" {
		return fmt.Errorf("missing %s", windowsMCPChildPIDEnv)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestWindowsMCPProcessTreeHelper")
	cmd.Env = append(os.Environ(), windowsMCPHelperEnv+"=child")
	if err := cmd.Start(); err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
}

func waitForWindowsMCPPIDExit(t *testing.T, pid string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !windowsMCPPIDExists(t, pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %s survived after %s", pid, timeout)
}

func windowsMCPPIDExists(t *testing.T, pid string) bool {
	t.Helper()
	out, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").CombinedOutput()
	if err != nil {
		t.Fatalf("tasklist failed: %v\n%s", err, string(out))
	}
	return strings.Contains(string(out), `"`+pid+`"`)
}

func waitForWindowsMCPFile(t *testing.T, path string, timeout time.Duration) {
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

func mustReadWindowsMCPFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
