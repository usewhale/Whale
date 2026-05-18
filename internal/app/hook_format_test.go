package app

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
)

func TestFormatHookOutcomeLineIncludesCoreFields(t *testing.T) {
	line := formatHookOutcomeLine(agent.HookEventPreToolUse, agent.HookOutcome{
		Hook:       agent.ResolvedHook{HookConfig: agent.HookConfig{Command: "echo hi"}},
		Decision:   agent.HookDecisionWarn,
		ExitCode:   9,
		Stderr:     "problem",
		DurationMS: 42,
		Truncated:  true,
	})
	for _, p := range []string{
		"event:PreToolUse",
		"decision:warn",
		"source:`config`",
		"hook:`echo hi`",
		"code:9",
		"duration_ms:42",
		"truncated:true",
		"msg:problem",
	} {
		if !strings.Contains(line, p) {
			t.Fatalf("missing %q in %q", p, line)
		}
	}
}

func TestFormatHookEventLinePrefersDecisionField(t *testing.T) {
	line := formatHookEventLine("started", &agent.HookEventInfo{
		Event:      agent.HookEventStop,
		Decision:   agent.HookDecisionTimeout,
		Name:       "echo end",
		ExitCode:   -1,
		DurationMS: 100,
		Truncated:  false,
		Message:    "timeout",
	})
	if !strings.Contains(line, "decision:timeout") {
		t.Fatalf("expected decision from struct, got %q", line)
	}
}

func TestFormatHookOutcomeLineEscapesWindowsSourcePath(t *testing.T) {
	line := formatHookOutcomeLine(agent.HookEventUserPromptSubmit, agent.HookOutcome{
		Source:   `C:\tmp\repo\.whale\config.toml`,
		Name:     `cmd /c "echo hi"`,
		Decision: agent.HookDecisionBlock,
	})
	if !strings.Contains(line, "source:`C:\\tmp\\repo\\.whale\\config.toml`") {
		t.Fatalf("expected source path in inline code, got %q", line)
	}
	if !strings.Contains(line, "hook:`cmd /c \"echo hi\"`") {
		t.Fatalf("expected hook command in inline code, got %q", line)
	}
}

func TestRenderHookReportUsesFormattedOutcomeLine(t *testing.T) {
	lines := renderHookReport(agent.HookReport{
		Event: agent.HookEventUserPromptSubmit,
		Outcomes: []agent.HookOutcome{{
			Source:   `C:\tmp\repo\.whale\config.toml`,
			Name:     `cmd /c "echo hi"`,
			Decision: agent.HookDecisionBlock,
		}},
	})
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "source:`C:\\tmp\\repo\\.whale\\config.toml`") {
		t.Fatalf("expected formatted source path, got %q", got)
	}
}
