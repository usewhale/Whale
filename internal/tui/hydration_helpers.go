package tui

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/tui/history"
)

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func hasInt(v any) bool {
	switch v.(type) {
	case int, int64, float64:
		return true
	default:
		return false
	}
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func formatDurationMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	sec := float64(ms) / 1000.0
	if sec < 10 {
		return fmt.Sprintf("%.1fs", sec)
	}
	return fmt.Sprintf("%ds", int(sec+0.5))
}

func firstNonEmptyAny(values ...any) any {
	for _, v := range values {
		switch x := v.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(x) != "" {
				return x
			}
		case []string:
			if len(x) > 0 {
				return x
			}
		case []any:
			if len(x) > 0 {
				return x
			}
		default:
			return x
		}
	}
	return nil
}

func stringSlice(v any) []string {
	switch xs := v.(type) {
	case []string:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if strings.TrimSpace(x) != "" {
				out = append(out, x)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s := strings.TrimSpace(core.AsString(x)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func truncateDisplayText(text string, maxLines int) string {
	lines := make([]string, 0, maxLines+1)
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if len([]rune(line)) > 220 {
			r := []rune(line)
			line = string(r[:220]) + "…"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return ""
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	if maxLines == 1 {
		return fmt.Sprintf("… +%d lines", len(lines))
	}
	head := maxLines / 2
	tail := maxLines - head - 1
	out := make([]string, 0, maxLines)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("… +%d lines", len(lines)-head-tail))
	out = append(out, lines[len(lines)-tail:]...)
	return strings.Join(out, "\n")
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func isEnvironmentInventoryBlock(text string) bool {
	return history.IsEnvironmentInventoryBlock(text)
}

func normalizeToolCallLabel(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return t
	}
	if strings.HasPrefix(t, "shell_run:") {
		cmd := strings.TrimSpace(strings.TrimPrefix(t, "shell_run:"))
		if cmd != "" {
			return "Running " + cmd
		}
		return "Running shell command"
	}
	if t == "shell_run" {
		return "Running shell command"
	}
	return t
}
