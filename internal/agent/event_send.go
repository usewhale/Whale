package agent

import "context"

func sendAgentEvent(ctx context.Context, events chan<- AgentEvent, ev AgentEvent) bool {
	// Preserve already-bufferable terminal events after cancellation, but never
	// block forever on an undrained event stream once the turn context is done.
	select {
	case events <- ev:
		return true
	default:
	}
	select {
	case <-ctx.Done():
		return false
	case events <- ev:
		return true
	}
}
