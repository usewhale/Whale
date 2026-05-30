package app

import (
	"fmt"
	whalemcp "github.com/usewhale/whale/internal/mcp"
	"strings"
)

func (a *App) buildMCPStatus() string {
	if a == nil || a.mcpManager == nil {
		return "MCP Tools\n\nconfig: unavailable\nservers: none"
	}
	lines := []string{"MCP Tools", "", fmt.Sprintf("config: %s", a.mcpManager.ConfigPath())}
	states := a.mcpManager.States()
	if len(states) == 0 {
		lines = append(lines, "servers: none")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, fmt.Sprintf("servers: %d", len(states)))
	for _, st := range states {
		lines = append(lines, "", fmt.Sprintf("- %s", st.Name))
		lines = append(lines, "  status: "+mcpStatusValue(st))
		lines = append(lines, "  auth: "+mcpAuthValue(st))
		if strings.TrimSpace(st.Command) != "" {
			lines = append(lines, "  command: "+strings.TrimSpace(st.Command))
		}
		if strings.TrimSpace(st.URL) != "" {
			lines = append(lines, "  url: "+strings.TrimSpace(st.URL))
		}
		if len(st.Headers) > 0 {
			lines = append(lines, "  http headers: "+strings.Join(st.Headers, ", "))
		}
		lines = append(lines, "  tools: "+mcpToolsValue(st))
		if st.Error != "" {
			lines = append(lines, "  error: "+st.Error)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) buildMCPLocalResult() *LocalResult {
	text := a.buildMCPStatus()
	fields := []LocalResultField{
		{Label: "Config", Value: "unavailable", Tone: "muted"},
		{Label: "Servers", Value: "none", Tone: "muted"},
	}
	var sections []LocalResultSection
	if a != nil && a.mcpManager != nil {
		states := a.mcpManager.States()
		fields = []LocalResultField{
			{Label: "Config", Value: valueOrDash(a.mcpManager.ConfigPath())},
			{Label: "Servers", Value: mcpServerCountValue(len(states)), Tone: mcpServersTone(states)},
		}
		sections = make([]LocalResultSection, 0, len(states))
		for _, st := range states {
			status := mcpStatusValue(st)
			serverFields := []LocalResultField{
				{Label: "Status", Value: status, Tone: mcpStatusTone(status)},
				{Label: "Auth", Value: mcpAuthValue(st)},
			}
			if strings.TrimSpace(st.Command) != "" {
				serverFields = append(serverFields, LocalResultField{Label: "Command", Value: strings.TrimSpace(st.Command)})
			}
			if strings.TrimSpace(st.URL) != "" {
				serverFields = append(serverFields, LocalResultField{Label: "URL", Value: strings.TrimSpace(st.URL)})
			}
			if len(st.Headers) > 0 {
				serverFields = append(serverFields, LocalResultField{Label: "HTTP headers", Value: strings.Join(st.Headers, ", ")})
			}
			serverFields = append(serverFields, LocalResultField{Label: "Tools", Value: mcpToolsValue(st)})
			if strings.TrimSpace(st.Error) != "" {
				serverFields = append(serverFields, LocalResultField{Label: "Error", Value: st.Error, Tone: "error"})
			}
			sections = append(sections, LocalResultSection{
				Title:  valueOrDash(st.Name),
				Fields: serverFields,
			})
		}
	}
	return &LocalResult{
		Kind:      "mcp",
		Title:     "MCP Tools",
		Fields:    fields,
		Sections:  sections,
		PlainText: text,
	}
}

func mcpServerCountValue(count int) string {
	if count == 0 {
		return "none"
	}
	return fmt.Sprintf("%d", count)
}

func mcpServersTone(states []whalemcp.ServerState) string {
	if len(states) == 0 {
		return "muted"
	}
	for _, st := range states {
		status := mcpStatusValue(st)
		if status == whalemcp.StatusFailed || status == whalemcp.StatusCancelled {
			return "error"
		}
	}
	return "info"
}

func mcpStatusValue(st whalemcp.ServerState) string {
	status := strings.TrimSpace(st.Status)
	if status != "" {
		return status
	}
	if st.Disabled {
		return whalemcp.StatusDisabled
	}
	if st.Connected {
		return whalemcp.StatusConnected
	}
	if strings.TrimSpace(st.Error) != "" {
		return whalemcp.StatusFailed
	}
	return whalemcp.StatusDisabled
}

func mcpAuthValue(st whalemcp.ServerState) string {
	auth := strings.TrimSpace(st.Auth)
	if auth == "" {
		return "Unsupported"
	}
	return auth
}

func mcpToolsValue(st whalemcp.ServerState) string {
	if len(st.ToolNames) > 0 {
		return strings.Join(st.ToolNames, ", ")
	}
	if st.Tools > 0 {
		return fmt.Sprintf("%d tool(s)", st.Tools)
	}
	return "(none)"
}

func mcpStatusTone(status string) string {
	switch status {
	case whalemcp.StatusConnected:
		return "info"
	case whalemcp.StatusPending, whalemcp.StatusStarting:
		return "warn"
	case whalemcp.StatusFailed, whalemcp.StatusCancelled:
		return "error"
	case whalemcp.StatusDisabled:
		return "muted"
	default:
		return ""
	}
}
