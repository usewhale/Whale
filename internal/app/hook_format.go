package app

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
)

func formatHookOutcomeLine(event agent.HookEvent, oc agent.HookOutcome) string {
	msg := hookOutcomeMessage(oc)
	return fmt.Sprintf(
		"[hook] event:%s decision:%s source:%s hook:%s code:%d duration_ms:%d truncated:%v msg:%s",
		event,
		oc.Decision,
		markdownInlineCode(hookOutcomeSource(oc)),
		markdownInlineCode(hookOutcomeName(oc)),
		oc.ExitCode,
		oc.DurationMS,
		oc.Truncated,
		msg,
	)
}

func hookOutcomeName(oc agent.HookOutcome) string {
	if name := strings.TrimSpace(oc.Name); name != "" {
		return name
	}
	return strings.TrimSpace(oc.Hook.Command)
}

func hookOutcomeSource(oc agent.HookOutcome) string {
	if source := strings.TrimSpace(oc.Source); source != "" {
		return source
	}
	if strings.TrimSpace(oc.Hook.Source) != "" {
		return strings.TrimSpace(oc.Hook.Source)
	}
	return "config"
}

func hookOutcomeMessage(oc agent.HookOutcome) string {
	for _, value := range []string{oc.Message, oc.Stderr, oc.Stdout, oc.AdditionalContext} {
		if msg := strings.TrimSpace(value); msg != "" {
			return msg
		}
	}
	return ""
}

func markdownInlineCode(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "\\`") + "`"
}

func formatHookEventLine(tag string, h *agent.HookEventInfo) string {
	if h == nil {
		return "[hook] <nil>"
	}
	decision := strings.TrimSpace(tag)
	if h.Decision != "" {
		decision = string(h.Decision)
	}
	return fmt.Sprintf(
		"[hook] event:%s decision:%s cmd:%s code:%d duration_ms:%d truncated:%v msg:%s",
		h.Event,
		decision,
		markdownInlineCode(h.Name),
		h.ExitCode,
		h.DurationMS,
		h.Truncated,
		strings.TrimSpace(h.Message),
	)
}
