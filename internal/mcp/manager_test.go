package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usewhale/whale/internal/core"
)

const runMCPTestServerEnv = "WHALE_RUN_MCP_TEST_SERVER"

type echoInput struct {
	Message string `json:"message"`
}

type echoOutput struct {
	Message string `json:"message"`
}

func TestMain(m *testing.M) {
	if os.Getenv(runMCPTestServerEnv) == "1" {
		os.Unsetenv(runMCPTestServerEnv)
		os.Exit(runTestMCPServer())
	}
	os.Exit(m.Run())
}

func runTestMCPServer() int {
	server := newEchoMCPServer()
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

func newEchoMCPServer() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "whale-test-mcp", Version: "v0.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "echoes a message"}, func(ctx context.Context, req *sdk.CallToolRequest, input echoInput) (*sdk.CallToolResult, echoOutput, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "echo:" + input.Message}},
		}, echoOutput{Message: input.Message}, nil
	})
	return server
}

func TestManagerInitializesAndCallsStdioTool(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"local": {
				Command: os.Args[0],
				Args:    []string{"-test.run=^$"},
				Env:     map[string]string{runMCPTestServerEnv: "1"},
				Timeout: 5,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	states := mgr.States()
	if len(states) != 1 {
		t.Fatalf("states: %+v", states)
	}
	if !states[0].Connected || states[0].Error != "" || states[0].Tools != 1 {
		t.Fatalf("state: %+v", states[0])
	}

	tools := mgr.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools: %+v", tools)
	}
	if got := tools[0].Name(); got != "mcp__local__echo" {
		t.Fatalf("tool name = %q", got)
	}

	res, err := tools[0].Run(context.Background(), core.ToolCall{
		ID:    "call-1",
		Name:  tools[0].Name(),
		Input: `{"message":"hi"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("invalid envelope: %s", res.Content)
	}
	if text, _ := env.Data["text"].(string); !strings.Contains(text, "echo:hi") {
		t.Fatalf("text = %q, envelope = %+v", text, env)
	}
}

func TestManagerRecordsFailedServer(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"broken": {
				Command: "definitely-not-a-whale-mcp-command",
				Timeout: 1,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	if tools := mgr.Tools(); len(tools) != 0 {
		t.Fatalf("tools: %+v", tools)
	}
	states := mgr.States()
	if len(states) != 1 || states[0].Error == "" || states[0].Connected {
		t.Fatalf("states: %+v", states)
	}
}

func TestExpandStdioValueExpandsHomeAndWindowsPercentEnv(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "USERPROFILE":
			return `C:\Users\tester`
		case "APPDATA":
			return `C:\Users\tester\AppData\Roaming`
		default:
			return ""
		}
	}
	userHomeDir := func() (string, error) { return `C:\Users\tester`, nil }

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "userprofile", in: `%USERPROFILE%\bin\server.exe`, want: `C:\Users\tester\bin\server.exe`},
		{name: "appdata in arg", in: `--config=%APPDATA%\Whale\mcp.json`, want: `--config=C:\Users\tester\AppData\Roaming\Whale\mcp.json`},
		{name: "missing env preserved", in: `%MISSING_VAR%\server.exe`, want: `%MISSING_VAR%\server.exe`},
		{name: "windows home backslash", in: `~\bin\server.exe`, want: `C:\Users\tester/bin\server.exe`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expandStdioValue(tc.in, true, "windows", getenv, userHomeDir)
			if got != tc.want {
				t.Fatalf("expandStdioValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if got := expandStdioValue(`~\bin\server`, true, "linux", getenv, userHomeDir); got != `~\bin\server` {
		t.Fatalf("non-windows backslash home = %q, want unchanged", got)
	}
}

func TestManagerInitializesAndCallsStreamableHTTPToolWithHeaders(t *testing.T) {
	t.Setenv("WHALE_MCP_TEST_TOKEN", "ctx-test-token")
	server := newEchoMCPServer()
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	var mu sync.Mutex
	var sawToken bool
	var sawStatic bool
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Header.Get("CONTEXT7_API_KEY") == "ctx-test-token" {
			sawToken = true
		}
		if r.Header.Get("X-Static") == "ok" {
			sawStatic = true
		}
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"context7": {
				Type: "http",
				URL:  httpServer.URL,
				Headers: map[string]string{
					"CONTEXT7_API_KEY": "${WHALE_MCP_TEST_TOKEN}",
					"X-Static":         "ok",
				},
				Timeout: 5,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	states := mgr.States()
	if len(states) != 1 || !states[0].Connected || states[0].Tools != 1 || states[0].Error != "" {
		t.Fatalf("states: %+v", states)
	}
	tools := mgr.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools: %+v", tools)
	}
	res, err := tools[0].Run(context.Background(), core.ToolCall{
		ID:    "call-http",
		Name:  tools[0].Name(),
		Input: `{"message":"remote"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	if !strings.Contains(res.Content, "echo:remote") {
		t.Fatalf("content = %s", res.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawToken || !sawStatic {
		t.Fatalf("headers not received: sawToken=%v sawStatic=%v", sawToken, sawStatic)
	}
}

func TestManagerRecordsHTTPConfigErrorWithoutLeakingHeaderValue(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"remote": {
				URL:     "http://127.0.0.1:1/mcp",
				Headers: map[string]string{"Authorization": "Bearer ${WHALE_MISSING_SECRET}"},
				Timeout: 1,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	states := mgr.States()
	if len(states) != 1 || states[0].Error == "" || states[0].Connected {
		t.Fatalf("states: %+v", states)
	}
	if !strings.Contains(states[0].Error, "WHALE_MISSING_SECRET") {
		t.Fatalf("missing env var not named in error: %q", states[0].Error)
	}
	if strings.Contains(states[0].Error, "Bearer ") {
		t.Fatalf("error leaked header value shape: %q", states[0].Error)
	}
}

func TestManagerRecordsHTTPStatusErrorWithoutBodyPreview(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("missing Authorization: Bearer ctx-secret-token token=abc123\nretry later"))
	}))
	t.Cleanup(httpServer.Close)

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"remote": {
				URL:     httpServer.URL + "/mcp?token=query-secret",
				Headers: map[string]string{"Authorization": "Bearer request-secret"},
				Timeout: 1,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	errText := singleFailedStateError(t, mgr)
	for _, want := range []string{`mcp server "remote"`, "transport=http", "/mcp", "401 Unauthorized"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error missing %q: %q", want, errText)
		}
	}
	for _, secret := range []string{"ctx-secret-token", "abc123", "request-secret", "query-secret", "retry later"} {
		if strings.Contains(errText, secret) {
			t.Fatalf("error leaked secret %q: %q", secret, errText)
		}
	}
}

func TestManagerRecordsHTTPNotFoundBody(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not an MCP endpoint", http.StatusNotFound)
	}))
	t.Cleanup(httpServer.Close)

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"remote": {URL: httpServer.URL + "/wrong", Timeout: 1},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	errText := singleFailedStateError(t, mgr)
	for _, want := range []string{"404 Not Found", "/wrong"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error missing %q: %q", want, errText)
		}
	}
	if strings.Contains(errText, "not an MCP endpoint") {
		t.Fatalf("error included response body: %q", errText)
	}
}

func TestHeaderRoundTripperDoesNotMutateHTTPErrorBody(t *testing.T) {
	body := strings.Repeat("x", 220)
	rt := headerRoundTripper{
		serverName: "remote",
		diag:       &httpDiagnostics{},
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://example.com/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("body mutated: len=%d want=%d", len(got), len(body))
	}
	if summary := rt.diag.summary(); !strings.Contains(summary, "500 Internal Server Error") || strings.Contains(summary, body) {
		t.Fatalf("unexpected diagnostics summary: %q", summary)
	}
}

func TestNormalizedCommandBase(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "unix npx", command: "npx", want: "npx"},
		{name: "windows npx cmd", command: `C:\Program Files\nodejs\npx.cmd`, want: "npx"},
		{name: "windows npm exe", command: `C:\Program Files\nodejs\npm.exe`, want: "npm"},
		{name: "windows npm bat", command: `C:\Program Files\nodejs\npm.bat`, want: "npm"},
		{name: "relative dotted server", command: "./server", want: "server"},
		{name: "case folded", command: "/usr/local/bin/NPX.CMD", want: "npx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizedCommandBase(tt.command); got != tt.want {
				t.Fatalf("normalizedCommandBase(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestManagerRecordsHTTPStartupTimeout(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	})

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"slow": {URL: httpServer.URL, Timeout: 1},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	errText := singleFailedStateError(t, mgr)
	for _, want := range []string{`mcp server "slow"`, "timed out after 1s", "connect"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error missing %q: %q", want, errText)
		}
	}
}

func TestStartupTimeoutForNpxIncludesActionableHint(t *testing.T) {
	for _, command := range []string{"npx", "npm", "npx.cmd", "npm.cmd", `C:\Program Files\nodejs\npx.cmd`, "npm.exe", "npx.bat"} {
		t.Run(command, func(t *testing.T) {
			err := startupTimeoutErr(ServerConfig{
				Name:    "fs",
				Command: command,
				Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
				Timeout: 15,
			}, "connect")

			errText := err.Error()
			for _, want := range []string{"npx/npm", "download packages", "consume stdio", "point command at its binary", "increase the server timeout"} {
				if !strings.Contains(errText, want) {
					t.Fatalf("error missing %q: %q", want, errText)
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func singleFailedStateError(t *testing.T, mgr *Manager) string {
	t.Helper()
	if tools := mgr.Tools(); len(tools) != 0 {
		t.Fatalf("tools: %+v", tools)
	}
	states := mgr.States()
	if len(states) != 1 || states[0].Error == "" || states[0].Connected {
		t.Fatalf("states: %+v", states)
	}
	return states[0].Error
}
