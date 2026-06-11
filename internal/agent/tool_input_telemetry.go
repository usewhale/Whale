package agent

import (
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/telemetry"
)

const (
	toolInputEventRepaired = "tool_input_repaired"
	toolInputEventInvalid  = "tool_input_invalid"
)

var toolInputTelemetryAppendMu sync.Mutex

func (a *Agent) recordToolInputRepair(sessionID, model, assistantMessageID string, call core.ToolCall, repairKind string) {
	a.recordToolInputRepairDetail(sessionID, model, assistantMessageID, call, core.ToolInputRepair{Kind: repairKind})
}

func (a *Agent) recordToolInputRepairDetail(sessionID, model, assistantMessageID string, call core.ToolCall, repair core.ToolInputRepair) {
	a.recordToolInputEvent(telemetry.ToolInputEvent{
		Session:            sessionID,
		Model:              model,
		AssistantMessageID: assistantMessageID,
		ToolCallID:         call.ID,
		Tool:               call.Name,
		Event:              toolInputEventRepaired,
		RepairKind:         repair.Kind,
		Path:               repair.Path,
		BeforeType:         repair.BeforeType,
		AfterType:          repair.AfterType,
	})
}

func (a *Agent) recordToolInputInvalid(sessionID, model, assistantMessageID string, call core.ToolCall, errorCode string) {
	a.recordToolInputEvent(telemetry.ToolInputEvent{
		Session:            sessionID,
		Model:              model,
		AssistantMessageID: assistantMessageID,
		ToolCallID:         call.ID,
		Tool:               call.Name,
		Event:              toolInputEventInvalid,
		ErrorCode:          errorCode,
	})
}

func (a *Agent) recordToolInputEvent(rec telemetry.ToolInputEvent) {
	if a == nil || strings.TrimSpace(a.sessionsDir) == "" {
		return
	}
	toolInputTelemetryAppendMu.Lock()
	defer toolInputTelemetryAppendMu.Unlock()
	_ = telemetry.AppendToolInputEvent(a.sessionsDir, rec, time.Now())
}

func toolInputInvalidCode(res core.ToolResult) string {
	if !res.IsError() {
		return ""
	}
	code := strings.TrimSpace(res.Code)
	if code == "" {
		env, ok := core.ParseToolEnvelope(core.ToolResultModelText(res))
		if !ok {
			return ""
		}
		code = strings.TrimSpace(env.Code)
	}
	switch code {
	case "invalid_input", "invalid_args":
		return code
	default:
		return ""
	}
}
