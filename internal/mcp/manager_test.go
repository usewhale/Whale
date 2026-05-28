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
	if len(states[0].ToolNames) != 1 || states[0].ToolNames[0] != "echo" {
		t.Fatalf("tool names: %+v", states[0].ToolNames)
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

func TestManagerStatesIncludePendingConfiguredServers(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"context7": {Command: "context7-mcp"},
			"fs":       {Command: "fs-mcp"},
			"off":      {Command: "off-mcp", Disabled: true},
		},
	})

	states := mgr.States()
	if len(states) != 3 {
		t.Fatalf("states: %+v", states)
	}
	got := map[string]string{}
	for _, st := range states {
		got[st.Name] = st.Status
	}
	if got["context7"] != StatusPending || got["fs"] != StatusPending || got["off"] != StatusDisabled {
		t.Fatalf("unexpected initial states: %+v", states)
	}
}

func TestManagerStatesIncludeDisplayConfig(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"fs": {
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
			"remote": {
				URL: "https://example.com/mcp?secret=hidden",
				Headers: map[string]string{
					"Authorization": "Bearer ${TOKEN}",
					"X-API-Key":     "${API_KEY}",
				},
			},
		},
	})

	states := mgr.States()
	byName := map[string]ServerState{}
	for _, st := range states {
		byName[st.Name] = st
	}
	if got := byName["fs"].Command; got != "npx -y @modelcontextprotocol/server-filesystem /tmp" {
		t.Fatalf("command = %q", got)
	}
	if got := byName["remote"].Auth; got != "Bearer token" {
		t.Fatalf("auth = %q", got)
	}
	if got := byName["remote"].URL; got != "https://example.com/mcp" {
		t.Fatalf("url = %q", got)
	}
	if got := strings.Join(byName["remote"].Headers, ", "); got != "Authorization=*****, X-API-Key=*****" {
		t.Fatalf("headers = %q", got)
	}
}

func TestManagerDisplayCommandDoesNotExpandOrLeakSecrets(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"secret": {
				Command: `%USERPROFILE%\bin\server.exe`,
				Args: []string{
					"--token", "%MCP_TOKEN%",
					"--api-key=sk-secret-value",
					"Authorization: Bearer secret-value",
					"--header=Authorization: Bearer header-secret-value",
					"--header=X-API-Key: header-api-key-value",
					"--header=Content-Type: application/json",
					"--config=%APPDATA%\\server\\config.json",
				},
			},
		},
	})

	states := mgr.States()
	if len(states) != 1 {
		t.Fatalf("states: %+v", states)
	}
	got := states[0].Command
	for _, want := range []string{
		`%USERPROFILE%\bin\server.exe`,
		"--token *****",
		"--api-key=*****",
		"Authorization:*****",
		"--header=*****",
		"--header=Content-Type: application/json",
		`--config=%APPDATA%\server\config.json`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected command to contain %q, got %q", want, got)
		}
	}
	for _, leaked := range []string{"sk-secret-value", "Bearer secret-value", "header-secret-value", "header-api-key-value"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("display command leaked %q: %q", leaked, got)
		}
	}
}

func TestManagerKeepsAllServersVisibleWhileServersStart(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"context7": {
				Command: "definitely-not-a-whale-mcp-command",
				Timeout: 1,
			},
			"fs": {
				Command: "also-not-a-whale-mcp-command",
				Timeout: 1,
			},
		},
	})

	checked := false
	mgr.InitializeWithEvents(context.Background(), func(ev StartupEvent) {
		if ev.State.Status != StatusStarting {
			return
		}
		states := mgr.States()
		if len(states) != 2 {
			t.Fatalf("states while first server starts: %+v", states)
		}
		got := map[string]ServerState{}
		for _, st := range states {
			got[st.Name] = st
		}
		if got["context7"].Name != "context7" || got["fs"].Name != "fs" {
			t.Fatalf("configured servers not all visible: %+v", states)
		}
		checked = true
	})
	t.Cleanup(func() { _ = mgr.Close() })
	if !checked {
		t.Fatal("did not observe starting event")
	}
}

func TestManagerStartsServersConcurrently(t *testing.T) {
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		http.Error(w, "slow failure", http.StatusInternalServerError)
	}))
	t.Cleanup(slowServer.Close)

	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"aaa-slow": {
				URL:     slowServer.URL,
				Timeout: 5,
			},
			"zzz-fast": {
				Command: os.Args[0],
				Args:    []string{"-test.run=^$"},
				Env:     map[string]string{runMCPTestServerEnv: "1"},
				Timeout: 5,
			},
		},
	})
	t.Cleanup(func() { _ = mgr.Close() })

	fastConnected := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		mgr.InitializeWithEvents(context.Background(), func(ev StartupEvent) {
			if ev.State.Name == "zzz-fast" && ev.State.Status == StatusConnected {
				select {
				case fastConnected <- struct{}{}:
				default:
				}
			}
		})
		close(done)
	}()

	select {
	case <-fastConnected:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("fast server did not connect before slow server completed")
	}

	tools := mgr.Tools()
	if len(tools) != 1 || tools[0].Name() != "mcp__zzz_fast__echo" {
		t.Fatalf("tools after fast connection: %+v", tools)
	}

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("manager initialization did not complete")
	}

	states := mgr.States()
	byName := map[string]ServerState{}
	for _, st := range states {
		byName[st.Name] = st
	}
	if !byName["zzz-fast"].Connected || byName["aaa-slow"].Status != StatusFailed {
		t.Fatalf("states: %+v", states)
	}
}

func TestManagerRegistersToolsInDeterministicOrder(t *testing.T) {
	mgr := NewManager(Config{
		Servers: map[string]ServerConfig{
			"b": {
				Command: os.Args[0],
				Args:    []string{"-test.run=^$"},
				Env:     map[string]string{runMCPTestServerEnv: "1"},
				Timeout: 5,
			},
			"a": {
				Command: os.Args[0],
				Args:    []string{"-test.run=^$"},
				Env:     map[string]string{runMCPTestServerEnv: "1"},
				Timeout: 5,
			},
		},
	})
	mgr.Initialize(context.Background())
	t.Cleanup(func() { _ = mgr.Close() })

	tools := mgr.Tools()
	if len(tools) != 2 {
		t.Fatalf("tools: %+v", tools)
	}
	got := []string{tools[0].Name(), tools[1].Name()}
	want := []string{"mcp__a__echo", "mcp__b__echo"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tool order = %+v, want %+v", got, want)
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
