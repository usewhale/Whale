package agent

import "strings"

func (a *Agent) emitHookReport(events chan<- AgentEvent, report HookReport) {
	for _, oc := range report.Outcomes {
		events <- AgentEvent{
			Type: AgentEventTypeHookStarted,
			Hook: &HookEventInfo{
				Name:  hookOutcomeName(oc),
				Event: report.Event,
			},
		}
		msg := hookOutcomeMessage(oc)
		switch oc.Decision {
		case HookDecisionBlock, HookDecisionHalt:
			events <- AgentEvent{
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
			}
		case HookDecisionError, HookDecisionTimeout:
			events <- AgentEvent{
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
			}
		case HookDecisionWarn:
			events <- AgentEvent{
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
			}
		}
		events <- AgentEvent{
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
		}
	}
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
