package agent

import (
	"context"
	"strings"
)

func (a *Agent) emitHookReport(ctx context.Context, events chan<- AgentEvent, report HookReport) bool {
	for _, oc := range report.Outcomes {
		if !sendAgentEvent(ctx, events, AgentEvent{
			Type: AgentEventTypeHookStarted,
			Hook: &HookEventInfo{
				Name:  hookOutcomeName(oc),
				Event: report.Event,
			},
		}) {
			return false
		}
		msg := hookOutcomeMessage(oc)
		switch oc.Decision {
		case HookDecisionBlock, HookDecisionHalt:
			if !sendAgentEvent(ctx, events, AgentEvent{
				Type: AgentEventTypeHookBlocked,
				Hook: &HookEventInfo{
					Name:       hookOutcomeName(oc),
					Event:      report.Event,
					Decision:   oc.Decision,
					ExitCode:   oc.ExitCode,
					Message:    msg,
					DurationMS: oc.DurationMS,
					Truncated:  oc.Truncated,
				},
			}) {
				return false
			}
		case HookDecisionError, HookDecisionTimeout:
			if !sendAgentEvent(ctx, events, AgentEvent{
				Type: AgentEventTypeHookFailed,
				Hook: &HookEventInfo{
					Name:       hookOutcomeName(oc),
					Event:      report.Event,
					Decision:   oc.Decision,
					ExitCode:   oc.ExitCode,
					Message:    msg,
					DurationMS: oc.DurationMS,
					Truncated:  oc.Truncated,
				},
			}) {
				return false
			}
		case HookDecisionWarn:
			if !sendAgentEvent(ctx, events, AgentEvent{
				Type: AgentEventTypeHookWarned,
				Hook: &HookEventInfo{
					Name:       hookOutcomeName(oc),
					Event:      report.Event,
					Decision:   oc.Decision,
					ExitCode:   oc.ExitCode,
					Message:    msg,
					DurationMS: oc.DurationMS,
					Truncated:  oc.Truncated,
				},
			}) {
				return false
			}
		}
		if !sendAgentEvent(ctx, events, AgentEvent{
			Type: AgentEventTypeHookCompleted,
			Hook: &HookEventInfo{
				Name:       hookOutcomeName(oc),
				Event:      report.Event,
				Decision:   oc.Decision,
				ExitCode:   oc.ExitCode,
				Message:    msg,
				DurationMS: oc.DurationMS,
				Truncated:  oc.Truncated,
			},
		}) {
			return false
		}
	}
	return true
}

func hookOutcomeName(oc HookOutcome) string {
	if strings.TrimSpace(oc.Name) != "" {
		return strings.TrimSpace(oc.Name)
	}
	return strings.TrimSpace(oc.Hook.Command)
}

func hookOutcomeMessage(oc HookOutcome) string {
	for _, value := range []string{oc.Message, oc.Stderr, oc.Stdout, oc.AdditionalContext} {
		if msg := strings.TrimSpace(value); msg != "" {
			return msg
		}
	}
	return ""
}
