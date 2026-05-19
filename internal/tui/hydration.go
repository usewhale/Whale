package tui

import (
	"encoding/json"
	"strings"

	"github.com/usewhale/whale/internal/core"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const maxHydratedVisibleMessages = 8

func (m *model) hydrateSessionMessages(msgs []core.Message) {
	for _, msg := range recentHydrationMessages(msgs, maxHydratedVisibleMessages) {
		switch msg.Role {
		case core.RoleUser:
			if strings.TrimSpace(msg.Text) != "" && !msg.Hidden {
				m.append("you", msg.Text)
			}
		case core.RoleAssistant:
			hasVisibleText := strings.TrimSpace(msg.Text) != "" && !isEnvironmentInventoryBlock(msg.Text)
			if strings.TrimSpace(msg.Reasoning) != "" {
				m.append("think", msg.Reasoning)
			}
			if hasVisibleText {
				if plan, ok := core.ExtractProposedPlanText(msg.Text); ok {
					normal := strings.TrimSpace(core.StripProposedPlanBlocks(msg.Text))
					if normal != "" {
						m.append("assistant", normal)
					}
					m.assembler.AddPlan(plan)
				} else {
					m.append("assistant", msg.Text)
				}
			} else if isEnvironmentInventoryBlock(msg.Text) {
				m.addLog(logEntry{
					Kind:    "env_summary",
					Source:  "assistant",
					Summary: "environment summary captured",
					Raw:     msg.Text,
				})
			}
			for _, tc := range msg.ToolCalls {
				if tc.Name == "update_plan" {
					continue
				}
				m.appendToolCall(tc.ID, tc.Name, summarizeHydratedToolCall(tc))
			}
		case core.RoleTool:
			for _, tr := range msg.ToolResults {
				body := strings.TrimSpace(tr.Content)
				if body == "" {
					continue
				}
				if tr.Name == "update_plan" {
					if text, ok := hydratedPlanUpdateText(body); ok {
						if m.assembler == nil {
							m.assembler = tuirender.NewAssembler()
						}
						m.assembler.AddPlanUpdate(text)
						continue
					}
				}
				role, text := summarizeToolResultForChat(tr.Name, body)
				if !m.updateToolCallFromResult(tr.ToolCallID, tr.Name, tr.Content, role, text, tr.Metadata) {
					m.markToolCallResolved(tr.ToolCallID)
					if shouldShowUnmatchedToolResult(tr.Name, role, text) {
						m.assembler.AddToolResultWithRole("", text, role)
					}
				}
				m.captureDiffMetadata(tr.Name, tr.Metadata)
			}
		}
	}
}

func hydratedPlanUpdateText(body string) (string, bool) {
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Explanation string `json:"explanation"`
			Plan        []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil || !payload.Success || len(payload.Data.Plan) == 0 {
		return "", false
	}
	var b strings.Builder
	if strings.TrimSpace(payload.Data.Explanation) != "" {
		b.WriteString(strings.TrimSpace(payload.Data.Explanation))
		b.WriteString("\n\n")
	}
	for _, step := range payload.Data.Plan {
		switch strings.TrimSpace(step.Status) {
		case "completed":
			b.WriteString("[x] ")
		case "in_progress":
			b.WriteString("[~] ")
		default:
			b.WriteString("[ ] ")
		}
		b.WriteString(strings.TrimSpace(step.Step))
		b.WriteString("\n")
	}
	text := strings.TrimSpace(b.String())
	return text, text != ""
}

func recentHydrationMessages(msgs []core.Message, maxVisible int) []core.Message {
	if maxVisible <= 0 || len(msgs) == 0 {
		return nil
	}
	visible := 0
	start := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if isVisibleHydrationMessage(msgs[i]) {
			visible++
		}
		start = i
		if visible >= maxVisible {
			break
		}
	}
	return msgs[start:]
}

func isVisibleHydrationMessage(msg core.Message) bool {
	switch msg.Role {
	case core.RoleUser:
		return strings.TrimSpace(msg.Text) != "" && !msg.Hidden
	case core.RoleAssistant:
		if strings.TrimSpace(msg.Reasoning) != "" {
			return true
		}
		if strings.TrimSpace(msg.Text) != "" && !isEnvironmentInventoryBlock(msg.Text) {
			return true
		}
		return len(msg.ToolCalls) > 0
	case core.RoleTool:
		for _, tr := range msg.ToolResults {
			if strings.TrimSpace(tr.Content) != "" {
				return true
			}
		}
		return false
	default:
		return false
	}
}
