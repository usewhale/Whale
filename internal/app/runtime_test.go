package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
)

func TestHandleLocalCommandCompactUsageError(t *testing.T) {
	a := &App{sessionID: "s1", workspaceRoot: t.TempDir()}
	handled, out, _, err := a.HandleLocalCommand("/compact extra")
	if !handled {
		t.Fatal("expected /compact extra to be handled")
	}
	if out != "" {
		t.Fatalf("expected empty output on usage error, got %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "usage: /compact") {
		t.Fatalf("expected /compact usage error, got %v", err)
	}
}

func TestRunUserPromptSubmitHookBlockedOutput(t *testing.T) {
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s1",
		workspaceRoot: ".",
		hookRunner: agent.NewHookRunner([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo blocked prompt >&2; exit 2"},
			Event:      agent.HookEventUserPromptSubmit,
		}}, "."),
	}

	blocked, out, updated := a.RunUserPromptSubmitHook("deploy")
	if !blocked {
		t.Fatal("expected prompt hook to block")
	}
	if updated != "deploy" {
		t.Fatalf("blocked hook should preserve input, got %q", updated)
	}
	if !strings.Contains(out, "decision:block") || !strings.Contains(out, "assistant> blocked by UserPromptSubmit hook") {
		t.Fatalf("unexpected blocked hook output: %q", out)
	}
}

func TestRunUserPromptSubmitHookWarnOutput(t *testing.T) {
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s1",
		workspaceRoot: ".",
		hookRunner: agent.NewHookRunner([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo warn prompt >&2; exit 1"},
			Event:      agent.HookEventUserPromptSubmit,
		}}, "."),
	}

	blocked, out, _ := a.RunUserPromptSubmitHook("deploy")
	if blocked {
		t.Fatal("expected prompt hook warning not to block")
	}
	if !strings.Contains(out, "decision:warn") || !strings.Contains(out, "warn prompt") {
		t.Fatalf("unexpected warn hook output: %q", out)
	}
}

func TestRunUserPromptSubmitHookReturnsUpdatedInput(t *testing.T) {
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s1",
		workspaceRoot: ".",
		hookRunner: agent.NewHookRunner([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: `printf '{"updated_input":"rewritten prompt"}'`},
			Event:      agent.HookEventUserPromptSubmit,
		}}, "."),
	}

	blocked, _, updated := a.RunUserPromptSubmitHook("original prompt")
	if blocked {
		t.Fatal("expected prompt rewrite hook not to block")
	}
	if updated != "rewritten prompt" {
		t.Fatalf("updated input = %q", updated)
	}
}

func TestRunStopHookIncludesAssistantTextAndTurn(t *testing.T) {
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload.jsonl")
	cmd := "cat > " + payloadPath
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s-stop",
		workspaceRoot: dir,
		hookRunner:    agent.NewHookRunner([]agent.ResolvedHook{{HookConfig: agent.HookConfig{Command: cmd}, Event: agent.HookEventStop}}, dir),
	}

	out := a.RunStopHook("final answer", 7)
	if out != "" {
		t.Fatalf("expected successful stop hook to produce no rendered output, got %q", out)
	}
	b, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read stop payload: %v", err)
	}
	if !strings.Contains(string(b), `"last_assistant_text":"final answer"`) || !strings.Contains(string(b), `"turn":7`) {
		t.Fatalf("unexpected stop payload: %s", string(b))
	}
}

func TestOfficialPluginNoopStopHooksDoNotRenderOutput(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	out := app.RunStopHook("final answer", 1)
	if out != "" {
		t.Fatalf("expected plugin pass hooks to render no output, got %q", out)
	}
}
