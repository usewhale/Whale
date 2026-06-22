package tui

import (
	"encoding/json"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"github.com/usewhale/whale/internal/runtime/timeline"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const maxHydratedVisibleMessages = 8
const workflowResultMarkerGuidance = "The background workflow completed. Treat this as the authoritative workflow result for later user questions about this run."

func (m *model) hydrateSessionMessages(msgs []protocol.Message) {
	flushLifecycle := func() {
		if !m.hasPendingLifecycleItems() && len(m.timelineSnapshotMessages()) == 0 {
			return
		}
		m.commitLiveTranscript(true)
	}
	for _, msg := range recentHydrationMessages(msgs, maxHydratedVisibleMessages) {
		switch msg.Role {
		case string(core.RoleUser):
			if runID, text, ok := hiddenWorkflowResultMarker(msg); ok {
				m.discardHydratedWorkflowLaunchLifecycle()
				m.ensureTimeline().HandleEvent(protocol.Event{
					Kind:          protocol.EventWorkflowResult,
					WorkflowRunID: runID,
					Text:          text,
					StartedAt:     msg.CreatedAt,
				})
				m.commitLiveTranscript(true)
				continue
			}
			if strings.TrimSpace(msg.Text) != "" && !msg.Hidden {
				flushLifecycle()
				m.append("you", msg.Text)
			}
		case string(core.RoleAssistant):
			if hasVisibleHydratedAssistantText(msg) {
				flushLifecycle()
			}
			if strings.TrimSpace(msg.Reasoning) != "" {
				m.append("think", msg.Reasoning)
			}
			if len(msg.Parts) > 0 {
				m.hydrateAssistantParts(msg.Parts)
			} else if strings.TrimSpace(msg.Text) != "" && !isEnvironmentInventoryBlock(msg.Text) {
				m.append("assistant", msg.Text)
			} else if isEnvironmentInventoryBlock(msg.Text) {
				m.addEnvironmentSummaryLog("assistant", msg.Text)
			}
			for _, tc := range msg.ToolCalls {
				if tc.Name == "update_plan" {
					continue
				}
				events := timeline.HydrationEventsFromMessage(protocol.Message{ToolCalls: []protocol.ToolCall{tc}, CreatedAt: msg.CreatedAt})
				for _, ev := range events {
					m.ensureTimeline().HandleEvent(ev)
				}
			}
		case string(core.RoleTool):
			for _, tr := range msg.ToolResults {
				body := strings.TrimSpace(tr.Content)
				if body == "" {
					continue
				}
				if tr.Name == "update_plan" {
					if text, ok := hydratedPlanUpdateFromResult(tr); ok {
						if m.assembler == nil {
							m.assembler = tuirender.NewAssembler()
						}
						m.assembler.AddPlanUpdate(text)
						continue
					}
				}
				events := timeline.HydrationEventsFromMessage(protocol.Message{ToolResults: []protocol.ToolResult{tr}, CreatedAt: msg.CreatedAt})
				for _, ev := range events {
					if strings.TrimSpace(ev.Text) == "" {
						continue
					}
					m.ensureTimeline().HandleEvent(ev)
				}
				m.captureDiffMetadata(tr.Name, tr.Metadata)
			}
		}
	}
}

func (m *model) hydrateAssistantParts(parts []protocol.MessagePart) {
	for _, part := range parts {
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		switch part.Type {
		case string(core.MessagePartText):
			if isEnvironmentInventoryBlock(part.Text) {
				m.addEnvironmentSummaryLog("assistant", part.Text)
			} else {
				m.append("assistant", part.Text)
			}
		case string(core.MessagePartPlan):
			if m.assembler == nil {
				m.assembler = tuirender.NewAssembler()
			}
			m.assembler.AddPlan(part.Text)
		}
	}
}

func (m *model) addEnvironmentSummaryLog(source, raw string) {
	m.addLog(logEntry{
		Kind:    "env_summary",
		Source:  source,
		Summary: "environment summary captured",
		Raw:     raw,
	})
}

func hiddenWorkflowResultMarker(msg protocol.Message) (string, string, bool) {
	if !msg.Hidden || msg.Role != string(core.RoleUser) {
		return "", "", false
	}
	text := strings.TrimSpace(msg.Text)
	start := strings.Index(text, "<workflow_result>")
	end := strings.LastIndex(text, "</workflow_result>")
	if start < 0 || end <= start {
		return "", "", false
	}
	inner := strings.TrimSpace(text[start+len("<workflow_result>") : end])
	runID := ""
	for _, line := range strings.Split(inner, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "run: "); ok {
			runID = strings.TrimSpace(value)
			break
		}
	}
	if runID == "" {
		return "", "", false
	}
	body := ""
	if idx := strings.Index(inner, workflowResultMarkerGuidance); idx >= 0 {
		body = strings.TrimSpace(inner[idx+len(workflowResultMarkerGuidance):])
	} else if idx := strings.Index(inner, "\n\n"); idx >= 0 {
		body = strings.TrimSpace(inner[idx+2:])
	}
	if body == "" {
		return "", "", false
	}
	return runID, body, true
}

func (m *model) discardHydratedWorkflowLaunchLifecycle() {
	if !m.hasHydratedWorkflowLaunchLifecycle() {
		m.commitLiveTranscript(true)
		return
	}
	preserveModelOutput := m.hasHydratedVisibleModelOutput()
	if m.assembler != nil {
		snap := m.assembler.Snapshot()
		m.assembler.Reset()
		for _, msg := range snap {
			if isResettableModelOutput(msg) && !preserveModelOutput {
				continue
			}
			m.appendTranscriptMessages([]tuirender.UIMessage{msg})
		}
	}
	m.resetTimeline()
	m.removeNoFinalAnswerStatusMessages()
}

func (m *model) hasHydratedWorkflowLaunchLifecycle() bool {
	if m.timeline == nil {
		return false
	}
	for _, item := range m.timeline.Snapshot().Items {
		if item.Kind == timeline.ItemKindWorkflow || strings.TrimSpace(item.ToolName) == "workflow" {
			return true
		}
	}
	return false
}

func (m *model) hasHydratedVisibleModelOutput() bool {
	if m.assembler == nil {
		return false
	}
	for _, msg := range m.assembler.Snapshot() {
		switch msg.Kind {
		case tuirender.KindText:
			if msg.Role == "assistant" && strings.TrimSpace(msg.Text) != "" {
				return true
			}
		case tuirender.KindPlan, tuirender.KindPlanUpdate:
			if strings.TrimSpace(msg.Text) != "" {
				return true
			}
		}
	}
	return false
}

func hasVisibleHydratedAssistantText(msg protocol.Message) bool {
	if strings.TrimSpace(msg.Reasoning) != "" {
		return true
	}
	for _, part := range msg.Parts {
		if strings.TrimSpace(part.Text) != "" && part.Type != "" {
			return true
		}
	}
	return strings.TrimSpace(msg.Text) != "" && !isEnvironmentInventoryBlock(msg.Text)
}

// hydratedPlanUpdateFromResult restores a plan update from the structured
// payload (both live plain-text results and legacy sessions carry it — the
// legacy decoder backfills Payload from the stored envelope); parsing the
// text remains as a last-resort fallback.
func hydratedPlanUpdateFromResult(tr protocol.ToolResult) (string, bool) {
	if text, ok := hydratedPlanUpdateFromPayload(tr.Payload); ok {
		return text, ok
	}
	return hydratedPlanUpdateText(strings.TrimSpace(tr.Content))
}

func hydratedPlanUpdateFromPayload(payload map[string]any) (string, bool) {
	if payload == nil {
		return "", false
	}
	steps := core.AsAnySlice(payload["plan"])
	if len(steps) == 0 {
		return "", false
	}
	explanation := strings.TrimSpace(core.AsString(payload["explanation"]))
	var b strings.Builder
	if explanation != "" {
		b.WriteString(explanation)
		b.WriteString("\n\n")
	}
	for _, s := range steps {
		step, ok := s.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(core.AsString(step["status"])) {
		case "completed":
			b.WriteString("[x] ")
		case "in_progress":
			b.WriteString("[~] ")
		default:
			b.WriteString("[ ] ")
		}
		b.WriteString(strings.TrimSpace(core.AsString(step["step"])))
		b.WriteString("\n")
	}
	text := strings.TrimSpace(b.String())
	return text, text != ""
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

func recentHydrationMessages(msgs []protocol.Message, maxVisible int) []protocol.Message {
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

func isVisibleHydrationMessage(msg protocol.Message) bool {
	switch msg.Role {
	case string(core.RoleUser):
		if _, _, ok := hiddenWorkflowResultMarker(msg); ok {
			return true
		}
		return strings.TrimSpace(msg.Text) != "" && !msg.Hidden
	case string(core.RoleAssistant):
		if strings.TrimSpace(msg.Reasoning) != "" {
			return true
		}
		if strings.TrimSpace(msg.Text) != "" && !isEnvironmentInventoryBlock(msg.Text) {
			return true
		}
		return len(msg.ToolCalls) > 0
	case string(core.RoleTool):
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
