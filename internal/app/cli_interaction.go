package app

import "github.com/usewhale/whale/internal/agent"

func renderHookReport(report agent.HookReport) []string {
	out := make([]string, 0)
	for _, oc := range report.Outcomes {
		if oc.Decision == agent.HookDecisionPass {
			continue
		}
		out = append(out, formatHookOutcomeLine(report.Event, oc))
	}
	return out
}
