package tui

import (
	"strings"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

func (m *model) syncModelEffortFromInfo(text string) {
	if strings.Contains(text, "\n") {
		return
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "model: ") {
		m.model = strings.TrimSpace(strings.TrimPrefix(text, "model: "))
	}
	if strings.HasPrefix(text, "mode:") {
		m.chatMode = chatModeDisplay(strings.TrimSpace(strings.TrimPrefix(text, "mode:")))
	}
	if strings.HasPrefix(text, "effort: ") {
		m.effort = strings.TrimSpace(strings.TrimPrefix(text, "effort: "))
	}
	if strings.HasPrefix(text, "thinking: ") {
		m.thinking = strings.TrimSpace(strings.TrimPrefix(text, "thinking: "))
	}
	if strings.HasPrefix(text, "model set: ") {
		// format: model set: <m>  effort: <e>  thinking: <on|off>
		rest := strings.TrimSpace(strings.TrimPrefix(text, "model set: "))
		parts := strings.Split(rest, "  effort: ")
		if len(parts) == 2 {
			m.model = strings.TrimSpace(parts[0])
			right := strings.Split(parts[1], "  thinking: ")
			m.effort = strings.TrimSpace(right[0])
			if len(right) == 2 {
				m.thinking = strings.TrimSpace(right[1])
			}
		}
	}
	if strings.HasPrefix(text, "status: ") && strings.Contains(text, " thinking=") {
		parts := strings.Split(text, " thinking=")
		if len(parts) == 2 {
			m.thinking = strings.TrimSpace(parts[1])
		}
	}
	switch strings.TrimSpace(text) {
	case "Plan mode enabled":
		m.chatMode = "plan"
	case "Ask mode enabled":
		m.chatMode = "ask"
	case "Agent mode enabled":
		m.chatMode = "agent"
	}
}

func (m *model) syncModelEffortFromLocalResult(result *protocol.LocalResult) {
	if result == nil {
		return
	}
	switch result.Kind {
	case "new_session":
		if mode := localResultField(result, "Mode"); mode != "" {
			m.chatMode = chatModeDisplay(mode)
		}
	case "status":
		if mode := localResultField(result, "Mode"); mode != "" {
			m.chatMode = chatModeDisplay(mode)
		}
		if model := localResultField(result, "Model"); model != "" {
			m.model = model
		}
		if effort := localResultField(result, "Effort"); effort != "" {
			m.effort = effort
		}
		if thinking := localResultField(result, "Thinking"); thinking != "" {
			m.thinking = thinking
		}
	}
}

func localResultField(result *protocol.LocalResult, label string) string {
	if result == nil {
		return ""
	}
	for _, field := range result.Fields {
		if strings.EqualFold(strings.TrimSpace(field.Label), label) {
			return strings.TrimSpace(field.Value)
		}
	}
	for _, section := range result.Sections {
		for _, field := range section.Fields {
			if strings.EqualFold(strings.TrimSpace(field.Label), label) {
				return strings.TrimSpace(field.Value)
			}
		}
	}
	return ""
}

func chatModeDisplay(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ask":
		return "ask"
	case "plan":
		return "plan"
	default:
		return "agent"
	}
}

func visibleSubmittedText(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "/ask ") {
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "/ask"))
		if payload != "" {
			return payload
		}
	}
	if !strings.HasPrefix(trimmed, "/plan ") {
		return value
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "/plan"))
	if payload == "" || payload == "show" || payload == "on" || payload == "off" {
		return value
	}
	return payload
}
