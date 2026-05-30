package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/session"
)

func modeDisplay(mode session.Mode) string {
	if mode == session.ModeAsk {
		return "ask"
	}
	if mode == session.ModePlan {
		return "plan"
	}
	return "agent"
}

func modeTitle(mode session.Mode) string {
	if mode == session.ModeAsk {
		return "Ask"
	}
	if mode == session.ModePlan {
		return "Plan"
	}
	return "Agent"
}

func formatContextWindowStatus(a *App) string {
	return "- context window: " + contextWindowStatusValue(a)
}

func contextWindowStatusValue(a *App) string {
	if a == nil || a.msgStore == nil {
		return "unavailable"
	}
	msgs, err := a.msgStore.List(a.ctx, a.sessionID)
	if err != nil {
		return "unavailable"
	}
	used := compact.EstimateMessagesTokens(msgs)
	window := a.contextWindow
	if window < 1 {
		window = 1
	}
	leftPct := 100 - (used*100)/window
	if leftPct < 0 {
		leftPct = 0
	}
	return fmt.Sprintf("%d%% left (%s used / %s)", leftPct, formatTokenCount(used), formatTokenCount(window))
}

func formatTokenCount(v int) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000.0)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(v)/1_000.0)
	}
	return fmt.Sprintf("%d", v)
}
