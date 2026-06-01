package app

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
)

func TestExecuteHooksCommandShowsReviewCounts(t *testing.T) {
	a := &App{
		workspaceRoot: t.TempDir(),
		hookRunner: agent.NewHookRunnerWithState([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo project"},
			Event:      agent.HookEventPreToolUse,
			Source:     ".whale/config.toml",
		}}, ".", agent.HookStates{}),
	}
	res, err := a.ExecuteLocalCommand("/hooks")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !res.Handled || res.LocalResult == nil || res.LocalResult.Kind != "hooks" {
		t.Fatalf("unexpected hooks result: %+v", res)
	}
	for _, want := range []string{"Hooks", "PreToolUse", "Installed", "Review", "1 hook(s) need review"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("expected hooks output to contain %q:\n%s", want, res.Text)
		}
	}
}

func TestTrustHooksActivatesConfigHook(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		cfg:           Config{DataDir: dir},
		workspaceRoot: t.TempDir(),
		hooks: []agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo project"},
			Event:      agent.HookEventPreToolUse,
			Source:     ".whale/config.toml",
		}},
		hookStates: agent.HookStates{},
	}
	a.hookRunner = agent.NewHookRunnerWithState(a.hooks, a.workspaceRoot, a.hookStates)
	res, err := a.ExecuteLocalCommand("/hooks trust all")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !res.Mutated || !strings.Contains(res.Text, "trusted 1 hook") {
		t.Fatalf("unexpected trust result: %+v", res)
	}
	entries := a.HookEntries()
	if len(entries) != 1 || entries[0].Trust != agent.HookTrustTrusted || !entries[0].Active {
		t.Fatalf("expected trusted active hook, got %+v", entries)
	}
}

func TestHooksEnableDisableUpdatesState(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		cfg:           Config{DataDir: dir},
		workspaceRoot: t.TempDir(),
		hooks: []agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo project"},
			Event:      agent.HookEventSessionStart,
			Source:     ".whale/config.toml",
		}},
		hookStates: agent.HookStates{},
	}
	a.hookRunner = agent.NewHookRunnerWithState(a.hooks, a.workspaceRoot, a.hookStates)
	keys := []string{a.HookEntries()[0].Key}
	if _, err := a.TrustHooks(keys); err != nil {
		t.Fatalf("TrustHooks: %v", err)
	}
	res, err := a.ExecuteLocalCommand("/hooks disable " + keys[0])
	if err != nil {
		t.Fatalf("disable hook: %v", err)
	}
	if !res.Mutated || !strings.Contains(res.Text, "disabled 1 hook") {
		t.Fatalf("unexpected disable result: %+v", res)
	}
	if entries := a.HookEntries(); len(entries) != 1 || entries[0].Enabled || entries[0].Active {
		t.Fatalf("expected disabled inactive hook, got %+v", entries)
	}
	res, err = a.ExecuteLocalCommand("/hooks enable " + keys[0])
	if err != nil {
		t.Fatalf("enable hook: %v", err)
	}
	if !res.Mutated || !strings.Contains(res.Text, "enabled 1 hook") {
		t.Fatalf("unexpected enable result: %+v", res)
	}
	if entries := a.HookEntries(); len(entries) != 1 || !entries[0].Enabled || !entries[0].Active {
		t.Fatalf("expected enabled active hook, got %+v", entries)
	}
}
