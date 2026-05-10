package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

func TestHookRunnerPreToolBlockByExitCode2(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "deny"}, Event: HookEventPreToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "denied"}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "bash"})
	if !report.Blocked {
		t.Fatal("expected blocked report")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionBlock {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func TestHookRunnerPostToolWarnByExitCode2(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "post"}, Event: HookEventPostToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "warn"}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPostToolUse, ToolName: "echo"})
	if report.Blocked {
		t.Fatal("post hook should not block on exit 2")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionWarn {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

type preBlockProvider struct{ calls int }

func (p *preBlockProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonToolUse, ToolCalls: []ToolCall{{ID: "tc-1", Name: "echo", Input: `{"x":1}`}}}}
		close(out)
		return out
	}
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "done"}}
	close(out)
	return out
}

func TestAgentPreToolHookBlockSkipsDispatch(t *testing.T) {
	store := NewInMemoryStore()
	toolCalled := false
	tool := staticTool{name: "echo", run: func(_ context.Context, _ ToolCall) (ToolResult, error) {
		toolCalled = true
		return ToolResult{ToolCallID: "tc-1", Name: "echo", Content: "ok"}, nil
	}}
	a := NewAgentWithRegistry(&preBlockProvider{}, store, core.NewToolRegistry([]core.Tool{tool}), WithHooks([]ResolvedHook{{HookConfig: HookConfig{Command: "deny"}, Event: HookEventPreToolUse}}, "."))
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "nope"}
	}
	_, err := a.Run(context.Background(), "s-pre-block", "hi")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if toolCalled {
		t.Fatal("tool should not be called when PreToolUse blocks")
	}
}

func TestLoadHooksProjectThenGlobalOrder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(filepath.Join(home, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir home hooks failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir workspace hooks failed: %v", err)
	}
	projectCfg := `{"hooks":{"PreToolUse":[{"command":"echo project"}]}}`
	globalCfg := `{"hooks":{"PreToolUse":[{"command":"echo global"}]}}`
	if err := os.WriteFile(filepath.Join(ws, ".whale", "settings.json"), []byte(projectCfg), 0o600); err != nil {
		t.Fatalf("write project config failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".whale", "settings.json"), []byte(globalCfg), 0o600); err != nil {
		t.Fatalf("write global config failed: %v", err)
	}
	hooks, loaded, err := LoadHooks(ws, home)
	if err != nil {
		t.Fatalf("load hooks failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 loaded sources, got %d", len(loaded))
	}
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Command != "echo project" || hooks[1].Command != "echo global" {
		t.Fatalf("unexpected order: %+v", hooks)
	}
}

func TestHookRunnerBlockShortCircuitsFollowingHooks(t *testing.T) {
	hooks := []ResolvedHook{
		{HookConfig: HookConfig{Command: "first"}, Event: HookEventPreToolUse},
		{HookConfig: HookConfig{Command: "second"}, Event: HookEventPreToolUse},
	}
	r := NewHookRunner(hooks, ".")
	calls := 0
	r.spawner = func(_ context.Context, in HookSpawnInput) HookSpawnResult {
		calls++
		if in.Command == "first" {
			return HookSpawnResult{ExitCode: 2, Stderr: "blocked"}
		}
		return HookSpawnResult{ExitCode: 0}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "bash"})
	if !report.Blocked {
		t.Fatal("expected blocked")
	}
	if calls != 1 {
		t.Fatalf("expected short-circuit after first hook, calls=%d", calls)
	}
}

func TestAgentDoesNotTriggerUserPromptOrStopHooks(t *testing.T) {
	store := NewInMemoryStore()
	provider := &noToolProvider{}
	hooks := []ResolvedHook{
		{HookConfig: HookConfig{Command: "exit 2"}, Event: HookEventUserPromptSubmit},
		{HookConfig: HookConfig{Command: "exit 2"}, Event: HookEventStop},
	}
	a := NewAgentWithRegistry(provider, store, core.NewToolRegistry(nil), WithHooks(hooks, "."))
	calls := 0
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		calls++
		return HookSpawnResult{ExitCode: 2}
	}
	_, err := a.Run(context.Background(), "s-no-app-hooks", "hello")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected 0 hook invocations in agent for UserPromptSubmit/Stop, got %d", calls)
	}
}

func TestDefaultHookSpawnerUsesResolvedUnixShell(t *testing.T) {
	oldResolveHookShell := resolveHookShell
	resolveHookShell = shell.Resolver{GOOS: "linux"}.Resolve
	defer func() {
		resolveHookShell = oldResolveHookShell
	}()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "stdout.txt")
	res := defaultHookSpawner(context.Background(), HookSpawnInput{
		Command:   "printf %s \"$PWD\" > stdout.txt",
		CWD:       dir,
		TimeoutMS: 1000,
	})
	if res.SpawnErr != nil {
		t.Fatalf("SpawnErr = %v", res.SpawnErr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read stdout file failed: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != dir {
		t.Fatalf("PWD = %q, want %q", got, dir)
	}
}

func TestDefaultHookSpawnerUsesResolvedWindowsPowerShellArgs(t *testing.T) {
	oldResolveHookShell := resolveHookShell
	var gotCommand string
	resolveHookShell = func(command string) (shell.ShellSpec, error) {
		gotCommand = command
		return shell.Resolver{
			GOOS: "windows",
			LookPath: func(file string) (string, error) {
				if file == "pwsh" {
					return "echo", nil
				}
				return "", errors.New("not found")
			},
		}.Resolve(command)
	}
	defer func() {
		resolveHookShell = oldResolveHookShell
	}()

	res := defaultHookSpawner(context.Background(), HookSpawnInput{Command: "Write-Output hi", TimeoutMS: 1000})
	if res.SpawnErr != nil {
		t.Fatalf("SpawnErr = %v", res.SpawnErr)
	}
	wantStdout := "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command Write-Output hi\n"
	if res.Stdout != wantStdout {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, wantStdout)
	}
	if gotCommand != "Write-Output hi" {
		t.Fatalf("resolver command = %q", gotCommand)
	}
}

func TestDefaultHookSpawnerResolverFailureReturnsSpawnErr(t *testing.T) {
	oldResolveHookShell := resolveHookShell
	resolveHookShell = func(command string) (shell.ShellSpec, error) {
		return shell.ShellSpec{}, errors.New("PowerShell is required on Windows")
	}
	defer func() {
		resolveHookShell = oldResolveHookShell
	}()

	res := defaultHookSpawner(context.Background(), HookSpawnInput{Command: "Write-Output hi"})
	if res.SpawnErr == nil {
		t.Fatal("expected SpawnErr")
	}
	if !strings.Contains(res.SpawnErr.Error(), "PowerShell is required") {
		t.Fatalf("SpawnErr = %v", res.SpawnErr)
	}
	if res.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", res.ExitCode)
	}
}

func TestHookRunnerBlocksOnHookResolverFailure(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "Write-Output hi"}, Event: HookEventPreToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: -1, SpawnErr: errors.New("PowerShell is required on Windows")}
	}

	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "exec_shell"})
	if !report.Blocked {
		t.Fatal("expected resolver failure to block PreToolUse")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionBlock {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
	if !strings.Contains(report.Outcomes[0].Stderr, "PowerShell is required") {
		t.Fatalf("stderr = %q", report.Outcomes[0].Stderr)
	}
}

func TestHookShellResolverProducesExpectedPlatformArgs(t *testing.T) {
	tests := []struct {
		name     string
		resolver shell.Resolver
		command  string
		wantBin  string
		wantArgs []string
	}{
		{
			name:     "unix",
			resolver: shell.Resolver{GOOS: "linux"},
			command:  "echo hi",
			wantBin:  "/bin/sh",
			wantArgs: []string{"-lc", "echo hi"},
		},
		{
			name: "windows",
			resolver: shell.Resolver{
				GOOS: "windows",
				LookPath: func(file string) (string, error) {
					if file == "pwsh" {
						return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
					}
					return "", errors.New("not found")
				},
			},
			command: "Write-Output hi",
			wantBin: `C:\Program Files\PowerShell\7\pwsh.exe`,
			wantArgs: []string{
				"-NoLogo",
				"-NoProfile",
				"-NonInteractive",
				"-ExecutionPolicy",
				"Bypass",
				"-Command",
				"Write-Output hi",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := tt.resolver.Resolve(tt.command)
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if spec.Bin != tt.wantBin {
				t.Fatalf("Bin = %q, want %q", spec.Bin, tt.wantBin)
			}
			if !reflect.DeepEqual(spec.Args, tt.wantArgs) {
				t.Fatalf("Args = %#v, want %#v", spec.Args, tt.wantArgs)
			}
		})
	}
}

type noToolProvider struct{}

func (p *noToolProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "ok"}}
	close(out)
	return out
}

func TestHookRunnerRealShellPreToolBlock(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "echo blocked >&2; exit 2"}, Event: HookEventPreToolUse}}, ".")
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "bash"})
	if !report.Blocked {
		t.Fatal("expected blocked")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionBlock {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func TestHookRunnerRealShellPostToolWarn(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "echo post-warn >&2; exit 5"}, Event: HookEventPostToolUse}}, ".")
	report := r.Run(context.Background(), HookPayload{Event: HookEventPostToolUse, ToolName: "echo"})
	if report.Blocked {
		t.Fatal("post tool should not block")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionWarn {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func TestHookRunnerStopPayloadCarriesAssistantTextAndTurn(t *testing.T) {
	tmp := t.TempDir()
	capture := filepath.Join(tmp, "payload.json")
	cmd := "cat > " + capture + "; exit 0"
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: cmd}, Event: HookEventStop}}, ".")
	payload := NewStopPayload("s1", tmp, "final answer", 3)
	report := r.Run(context.Background(), payload)
	if report.Blocked {
		t.Fatal("stop should not block")
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read payload failed: %v", err)
	}
	var got HookPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if got.Event != HookEventStop || got.LastAssistantText != "final answer" || got.Turn != 3 || got.SessionID != "s1" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

type staticTool struct {
	name string
	run  func(context.Context, ToolCall) (ToolResult, error)
}

func (t staticTool) Name() string { return t.name }
func (t staticTool) Run(ctx context.Context, call ToolCall) (ToolResult, error) {
	return t.run(ctx, call)
}
