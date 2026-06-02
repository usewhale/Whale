package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
)

type ExecToolSummary struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
}

type ExecResult struct {
	SessionID string            `json:"session_id,omitempty"`
	Model     string            `json:"model,omitempty"`
	Status    string            `json:"status"`
	Output    string            `json:"output,omitempty"`
	Tools     []ExecToolSummary `json:"tools,omitempty"`
	Error     string            `json:"error,omitempty"`
}

func RunExec(ctx context.Context, cfg Config, start StartOptions, prompt string) (ExecResult, error) {
	a, err := New(ctx, cfg, start)
	if err != nil {
		return ExecResult{
			Model:  strings.TrimSpace(cfg.Model),
			Status: "error",
			Error:  err.Error(),
		}, err
	}
	defer a.Close()
	a.InitializeMCP(ctx, nil)
	res, err := a.ExecPrompt(ctx, prompt, false)
	if err != nil {
		return res, err
	}
	if err := a.FinalizeTurn(res.Output, res.Status == "completed"); err != nil {
		res.Status = "error"
		res.Error = err.Error()
		return res, err
	}
	return res, nil
}

func (a *App) ExecPrompt(ctx context.Context, prompt string, hiddenInput bool) (ExecResult, error) {
	result := ExecResult{
		SessionID: a.SessionID(),
		Model:     a.Model(),
		Status:    "completed",
	}
	events, err := a.RunTurn(ctx, prompt, hiddenInput)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return result, err
	}

	var final strings.Builder
	for ev := range events {
		switch ev.Type {
		case agent.AgentEventTypeAssistantDelta:
			final.WriteString(ev.Content)
		case agent.AgentEventTypeToolResult:
			if ev.Result != nil {
				result.Tools = append(result.Tools, ExecToolSummary{
					Name:    ev.Result.Name,
					Success: !ev.Result.IsError,
					Output:  summarizeExecText(ev.Result.Content),
				})
			}
		case agent.AgentEventTypeDone:
			if ev.Message != nil {
				if final.Len() == 0 {
					final.WriteString(ev.Message.Text)
				}
				result.Output = final.String()
			}
		case agent.AgentEventTypeError:
			if ev.Err != nil {
				result.Status = "error"
				result.Error = ev.Err.Error()
				result.Output = final.String()
				return result, ev.Err
			}
		}
	}

	if result.Output == "" {
		result.Output = final.String()
	}
	return result, nil
}

func summarizeExecText(v string) string {
	trimmed := strings.TrimSpace(v)
	if len(trimmed) > 240 {
		return trimmed[:240] + "..."
	}
	return trimmed
}

func (r ExecResult) TextOutput() string {
	return strings.TrimSpace(r.Output)
}

func (r ExecResult) Validate() error {
	if strings.TrimSpace(r.Status) == "" {
		return fmt.Errorf("exec result status is required")
	}
	return nil
}
