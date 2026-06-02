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
	env, parsed := core.ParseToolEnvelope(result.Content)
	if !parsed || strings.TrimSpace(env.Code) != "workflow_confirmation_required" {
		return "", "", "", "", "", "", false
	}
	name = strings.TrimSpace(asWorkflowString(env.Data["workflowName"]))
	if name == "" {
		return "", "", "", "", "", "", false
	}
	args = strings.TrimSpace(asWorkflowString(env.Data["workflowArgs"]))
	resume = strings.TrimSpace(asWorkflowString(env.Data["workflowResume"]))
	script = asWorkflowString(env.Data["workflowScript"])
	saveAs = strings.TrimSpace(asWorkflowString(env.Data["workflowSaveAs"]))
	scriptPath = strings.TrimSpace(asWorkflowString(env.Data["workflowScriptPath"]))
	return name, args, resume, script, saveAs, scriptPath, true
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
			s.emit(Event{Kind: EventWorkflowTerminal, Text: result.PlainText, LocalResult: protocolLocalResult(result)})
			return
		}
	}
}
