package app

import (
	"testing"
	"time"

	"github.com/usewhale/whale/internal/telemetry"
)

func TestReadToolInputStatsDeduplicatesRepeatedInvalidAttempts(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	events := []telemetry.ToolInputEvent{
		{
			Session:    "s1",
			Model:      "deepseek-v4-flash",
			ToolCallID: "tc-web-fetch",
			Tool:       "web_fetch",
			Event:      "tool_input_invalid",
			ErrorCode:  "invalid_args",
		},
		{
			Session:    "s1",
			Model:      "deepseek-v4-flash",
			ToolCallID: "tc-web-fetch",
			Tool:       "web_fetch",
			Event:      "tool_input_invalid",
			ErrorCode:  "invalid_args",
		},
		{
			Session:    "s1",
			Model:      "deepseek-v4-flash",
			ToolCallID: "tc-grep",
			Tool:       "grep",
			Event:      "tool_input_invalid",
			ErrorCode:  "invalid_input",
		},
	}
	for _, ev := range events {
		if err := telemetry.AppendToolInputEvent(dir, ev, now); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	stats := readToolInputStats(dir)
	if stats.Invalid != 2 {
		t.Fatalf("expected duplicate invalid attempts to count once, got invalid=%d stats=%+v", stats.Invalid, stats)
	}
	if got := stats.ByTool["web_fetch"].Invalid; got != 1 {
		t.Fatalf("expected web_fetch duplicate invalid attempts to count once, got %d", got)
	}
	if got := stats.ByErrorCode["invalid_args"]; got != 1 {
		t.Fatalf("expected invalid_args duplicate attempts to count once, got %d", got)
	}
}
