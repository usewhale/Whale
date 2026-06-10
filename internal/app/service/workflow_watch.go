package service

import (
	"strings"
	"time"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/core"
)

const workflowWatchInterval = time.Second

func (s *Service) maybeWatchWorkflowRun(result *app.LocalResult) {
	runID := workflowRunIDFromLocalResult(result)
	if runID == "" {
		return
	}
	s.workflowWatchMu.Lock()
	if _, ok := s.workflowReports[runID]; ok {
		s.workflowWatchMu.Unlock()
		return
	}
	if _, ok := s.workflowWatches[runID]; ok {
		s.workflowWatchMu.Unlock()
		return
	}
	s.workflowWatches[runID] = struct{}{}
	s.workflowWatchMu.Unlock()
	s.goTracked(func() { s.watchWorkflowRun(runID) })
}

func workflowSnapshotEvent(result *app.LocalResult) (Event, bool) {
	if result == nil || result.WorkflowPanelSnapshot == nil {
		return Event{}, false
	}
	runID := strings.TrimSpace(result.WorkflowPanelSnapshot.RunID)
	if runID == "" {
		runID = workflowRunIDFromLocalResult(result)
	}
	if runID == "" {
		return Event{}, false
	}
	text := strings.TrimSpace(result.WorkflowPanelSnapshot.Summary)
	if text == "" {
		text = strings.TrimSpace(result.PlainText)
	}
	return Event{
		Kind:          EventWorkflowSnapshot,
		WorkflowRunID: runID,
		Text:          text,
		Status:        strings.TrimSpace(result.WorkflowPanelSnapshot.Status),
		LocalResult:   protocolLocalResult(result),
	}, true
}

func (s *Service) emitWorkflowSnapshotForResult(result *app.LocalResult) {
	if ev, ok := workflowSnapshotEvent(result); ok {
		s.emit(ev)
		return
	}
	runID := workflowRunIDFromLocalResult(result)
	if runID == "" {
		return
	}
	panel := s.app.WorkflowPanelLocalResult(runID)
	if ev, ok := workflowSnapshotEvent(panel); ok {
		s.emit(ev)
	}
}

func workflowRunIDFromLocalResult(result *app.LocalResult) string {
	if result == nil || result.Kind != "workflow-run" {
		return ""
	}
	for _, field := range result.Fields {
		if strings.EqualFold(strings.TrimSpace(field.Label), "run") {
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}

func (s *Service) maybeWatchWorkflowToolResult(result *core.ToolResult) {
	runID := workflowRunIDFromToolResult(result)
	if runID == "" {
		return
	}
	s.maybeWatchWorkflowRun(&app.LocalResult{
		Kind:   "workflow-run",
		Fields: []app.LocalResultField{{Label: "Run", Value: runID}},
	})
}

func (s *Service) maybeEmitWorkflowLaunchConfirmationToolResult(result *core.ToolResult) bool {
	name, args, resume, script, saveAs, scriptPath, ok := workflowConfirmationFromToolResult(result)
	if !ok {
		return false
	}
	if strings.TrimSpace(script) == "" && strings.TrimSpace(scriptPath) == "" {
		trusted, err := s.app.WorkflowTrusted(name)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return true
		}
		if trusted {
			out, err := s.app.StartWorkflowFromConfirmation(name, args, resume, false)
			if err != nil {
				s.emit(Event{Kind: EventError, Text: err.Error()})
				return true
			}
			s.maybeWatchWorkflowRun(out)
			s.emitWorkflowSnapshotForResult(out)
			ev := localSubmitResultEvent("info", out.PlainText)
			ev.LocalResult = protocolLocalResult(out)
			s.emit(ev)
			return true
		}
	}
	var (
		out *app.LocalResult
		err error
	)
	if strings.TrimSpace(script) != "" {
		out, err = s.app.BuildGeneratedWorkflowLaunchConfirmation(script, saveAs, args, resume)
	} else if strings.TrimSpace(scriptPath) != "" {
		out, err = s.app.BuildScriptPathWorkflowLaunchConfirmation(scriptPath, args, resume)
	} else {
		out, err = s.app.BuildWorkflowLaunchConfirmation(name, args, resume)
	}
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		return true
	}
	ev := localSubmitResultEvent("info", out.PlainText)
	ev.LocalResult = protocolLocalResult(out)
	s.emit(ev)
	return true
}

func workflowConfirmationFromToolResult(result *core.ToolResult) (name, args, resume, script, saveAs, scriptPath string, ok bool) {
	if result == nil || strings.TrimSpace(result.Name) != "workflow" {
		return "", "", "", "", "", "", false
	}
	data, code := workflowResultData(result)
	if strings.TrimSpace(code) != "workflow_confirmation_required" {
		return "", "", "", "", "", "", false
	}
	name = strings.TrimSpace(asWorkflowString(data["workflowName"]))
	if name == "" {
		return "", "", "", "", "", "", false
	}
	args = strings.TrimSpace(asWorkflowString(data["workflowArgs"]))
	resume = strings.TrimSpace(asWorkflowString(data["workflowResume"]))
	script = asWorkflowString(data["workflowScript"])
	saveAs = strings.TrimSpace(asWorkflowString(data["workflowSaveAs"]))
	scriptPath = strings.TrimSpace(asWorkflowString(data["workflowScriptPath"]))
	return name, args, resume, script, saveAs, scriptPath, true
}

// workflowResultData returns the structured payload and code of a workflow
// tool result, parsing the model text only for legacy results that predate
// the channel separation.
func workflowResultData(result *core.ToolResult) (map[string]any, string) {
	if result.Outcome != "" {
		payload, _ := result.Payload.(map[string]any)
		return payload, result.Code
	}
	env, parsed := core.ParseToolEnvelope(core.ToolResultModelText(*result))
	if !parsed {
		return nil, ""
	}
	return env.Data, env.Code
}

func workflowRunIDFromToolResult(result *core.ToolResult) string {
	if result == nil || strings.TrimSpace(result.Name) != "workflow" {
		return ""
	}
	if result.Metadata == nil {
		return ""
	}
	if v, ok := result.Metadata["workflow_run_id"]; ok {
		if runID := strings.TrimSpace(asWorkflowString(v)); runID != "" {
			return runID
		}
	}
	return ""
}

func asWorkflowString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return ""
	}
}

func (s *Service) watchWorkflowRun(runID string) {
	defer func() {
		s.workflowWatchMu.Lock()
		delete(s.workflowWatches, runID)
		s.workflowWatchMu.Unlock()
	}()
	ticker := time.NewTicker(workflowWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			result := s.app.BuildWorkflowTerminalLocalResult(runID)
			if result == nil {
				continue
			}
			s.workflowWatchMu.Lock()
			if _, ok := s.workflowReports[runID]; ok {
				s.workflowWatchMu.Unlock()
				return
			}
			s.workflowReports[runID] = struct{}{}
			s.workflowWatchMu.Unlock()
			if ev, ok := workflowSnapshotEvent(result); ok {
				s.emit(ev)
			}
			if err := s.app.RecordWorkflowResult(runID, result.PlainText); err != nil {
				s.emit(Event{Kind: EventError, Text: err.Error()})
			}
			s.emit(Event{Kind: EventWorkflowResult, WorkflowRunID: runID, Text: result.PlainText, LocalResult: protocolLocalResult(result)})
			return
		}
	}
}
