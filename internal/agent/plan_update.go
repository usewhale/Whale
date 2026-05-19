package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

const (
	planStatusPending    = "pending"
	planStatusInProgress = "in_progress"
	planStatusCompleted  = "completed"
)

func (a *Agent) handleUpdatePlan(call core.ToolCall, events chan<- AgentEvent) (core.ToolResult, error) {
	update, err := parsePlanUpdate(call.Input)
	if err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    fmt.Sprintf(`{"success":false,"error":%q,"code":"invalid_update_plan"}`, err.Error()),
			IsError:    true,
		}, nil
	}
	events <- AgentEvent{Type: AgentEventTypePlanUpdate, PlanUpdate: &update}
	payload, _ := json.Marshal(map[string]any{
		"success": true,
		"data":    update,
	})
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: string(payload)}, nil
}

func parsePlanUpdate(input string) (PlanUpdateInfo, error) {
	var in struct {
		Explanation string           `json:"explanation"`
		Plan        []PlanUpdateStep `json:"plan"`
	}
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return PlanUpdateInfo{}, err
	}
	if len(in.Plan) == 0 {
		return PlanUpdateInfo{}, fmt.Errorf("plan must include at least one step")
	}
	inProgress := 0
	for i := range in.Plan {
		in.Plan[i].Step = strings.TrimSpace(in.Plan[i].Step)
		in.Plan[i].Status = strings.TrimSpace(in.Plan[i].Status)
		if in.Plan[i].Step == "" {
			return PlanUpdateInfo{}, fmt.Errorf("plan step %d is empty", i)
		}
		switch in.Plan[i].Status {
		case planStatusPending, planStatusCompleted:
		case planStatusInProgress:
			inProgress++
		default:
			return PlanUpdateInfo{}, fmt.Errorf("plan step %d has invalid status %q", i, in.Plan[i].Status)
		}
	}
	if inProgress > 1 {
		return PlanUpdateInfo{}, fmt.Errorf("plan can have at most one in_progress step")
	}
	return PlanUpdateInfo{Explanation: strings.TrimSpace(in.Explanation), Plan: in.Plan}, nil
}

func formatPlanUpdate(update PlanUpdateInfo) string {
	var b strings.Builder
	if strings.TrimSpace(update.Explanation) != "" {
		b.WriteString(strings.TrimSpace(update.Explanation))
		b.WriteString("\n\n")
	}
	for _, step := range update.Plan {
		switch step.Status {
		case planStatusCompleted:
			b.WriteString("[x] ")
		case planStatusInProgress:
			b.WriteString("[~] ")
		default:
			b.WriteString("[ ] ")
		}
		b.WriteString(strings.TrimSpace(step.Step))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func FormatPlanUpdateForDisplay(update PlanUpdateInfo) string {
	return formatPlanUpdate(update)
}
