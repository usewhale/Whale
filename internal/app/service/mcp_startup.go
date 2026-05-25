package service

import (
	"fmt"
	"strings"

	whalemcp "github.com/usewhale/whale/internal/mcp"
)

func (s *Service) startMCPStartup() {
	if s == nil || s.app == nil {
		return
	}
	s.goTracked(func() {
		s.app.InitializeMCP(s.ctx, func(ev whalemcp.StartupEvent) {
			states := s.app.MCPStates()
			if ev.Complete {
				if len(states) > 0 {
					s.emit(Event{Kind: EventMCPComplete, Text: summarizeMCPComplete(states), Metadata: mcpSummaryMetadata(states)})
				}
				return
			}
			if ev.State.Name == "" {
				return
			}
			text := summarizeMCPStatus(states, ev.State)
			if text == "" {
				return
			}
			s.emit(Event{Kind: EventMCPStatus, Text: text, Status: ev.State.Status, Metadata: map[string]any{
				"server": ev.State.Name,
				"status": ev.State.Status,
				"tools":  ev.State.Tools,
			}})
		})
	})
}

func summarizeMCPStatus(states []whalemcp.ServerState, state whalemcp.ServerState) string {
	switch state.Status {
	case whalemcp.StatusStarting:
		starting := mcpNamesWithStatus(states, whalemcp.StatusStarting)
		if len(starting) == 0 {
			return fmt.Sprintf("Starting MCP server: %s", state.Name)
		}
		connected, failed, _ := countMCPStates(states)
		done := connected + failed
		if len(states) > 1 {
			return fmt.Sprintf("Starting MCP servers (%d/%d): %s", done, len(states), strings.Join(starting, ", "))
		}
		return fmt.Sprintf("Starting MCP server: %s", starting[0])
	case whalemcp.StatusConnected:
		return fmt.Sprintf("MCP server ready: %s (%d tool(s))", state.Name, state.Tools)
	case whalemcp.StatusFailed:
		return fmt.Sprintf("MCP startup failed: %s. Run /mcp for details.", state.Name)
	case whalemcp.StatusCancelled:
		return fmt.Sprintf("MCP startup cancelled: %s", state.Name)
	default:
		return ""
	}
}

func summarizeMCPComplete(states []whalemcp.ServerState) string {
	connected, failed, disabled := countMCPStates(states)
	if failed > 0 {
		return fmt.Sprintf("MCP startup complete: %d connected, %d failed, %d disabled", connected, failed, disabled)
	}
	return fmt.Sprintf("MCP ready: %d connected, %d disabled", connected, disabled)
}

func mcpSummaryMetadata(states []whalemcp.ServerState) map[string]any {
	connected, failed, disabled := countMCPStates(states)
	return map[string]any{
		"servers":   len(states),
		"connected": connected,
		"failed":    failed,
		"disabled":  disabled,
	}
}

func countMCPStates(states []whalemcp.ServerState) (connected, failed, disabled int) {
	for _, st := range states {
		switch st.Status {
		case whalemcp.StatusConnected:
			connected++
		case whalemcp.StatusFailed, whalemcp.StatusCancelled:
			failed++
		case whalemcp.StatusDisabled:
			disabled++
		default:
			if st.Connected {
				connected++
			} else if st.Error != "" {
				failed++
			} else if st.Disabled {
				disabled++
			}
		}
	}
	return connected, failed, disabled
}

func mcpNamesWithStatus(states []whalemcp.ServerState, status string) []string {
	names := []string{}
	for _, st := range states {
		if st.Status == status {
			names = append(names, st.Name)
		}
	}
	return names
}
