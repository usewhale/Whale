//go:build windows

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/usewhale/whale/internal/shell"
)

func TestWindowsHookRunnerRealShellPreToolBlock(t *testing.T) {
	command := windowsExitCommand(t, 2)
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: command}, Event: HookEventPreToolUse}}, ".")

	report := r.RunHook(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "shell_run"})
	if !report.Blocked {
		t.Fatal("expected blocked report")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionBlock {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func windowsExitCommand(t *testing.T, code int) string {
	t.Helper()

	spec, err := shell.Resolve("")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	switch spec.Kind {
	case shell.KindPowerShell:
		return fmt.Sprintf("exit %d", code)
	case shell.KindCmd:
		return fmt.Sprintf("exit /b %d", code)
	default:
		t.Fatalf("unexpected Windows shell kind: %q", spec.Kind)
		return ""
	}
}
