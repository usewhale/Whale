package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

func runSubagentStartHooks(ctx context.Context, hooks []agent.ResolvedHook, sessionID, workspaceRoot, role, prompt string, promptExecutor, agentExecutor agent.HookExecutor) (string, error) {
	if len(hooks) == 0 {
		return "", nil
	}
	runner := agent.NewHookRunner(hooks, workspaceRoot)
	runner.SetExecutors(promptExecutor, agentExecutor)
	payload := agent.NewSubagentHookPayload(agent.HookEventSubagentStart, sessionID, workspaceRoot, role, "", "")
	payload.Prompt = prompt
	report := runner.RunHook(ctx, payload)
	if report.Blocked {
		msg := firstHookMessage(report, "subagent start hook blocked")
		return "", errors.New(msg)
	}
	ctxText := strings.TrimSpace(report.AdditionalContext)
	if ctxText == "" {
		return "", nil
	}
	return "Subagent start hook context:\n" + ctxText, nil
}

func runSubagentStopHooks(ctx context.Context, hooks []agent.ResolvedHook, sessionID, workspaceRoot, role, summary string, promptExecutor, agentExecutor agent.HookExecutor) error {
	if len(hooks) == 0 {
		return nil
	}
	runner := agent.NewHookRunner(hooks, workspaceRoot)
	runner.SetExecutors(promptExecutor, agentExecutor)
	report := runner.RunHook(ctx, agent.NewSubagentHookPayload(agent.HookEventSubagentStop, sessionID, workspaceRoot, role, "", summary))
	if report.Halted {
		return errors.New(firstHookMessage(report, "subagent stop hook halted"))
	}
	return nil
}

func firstHookMessage(report agent.HookReport, fallback string) string {
	for _, oc := range report.Outcomes {
		if strings.TrimSpace(oc.Message) != "" {
			return strings.TrimSpace(oc.Message)
		}
		if strings.TrimSpace(oc.Stderr) != "" {
			return strings.TrimSpace(oc.Stderr)
		}
	}
	return fallback
}

func (r *Runner) hookModelExecutor(defaultModel, defaultEffort, hookKind string) agent.HookExecutor {
	return func(ctx context.Context, cfg agent.HookConfig, payload agent.HookPayload) agent.HookResult {
		model := strings.TrimSpace(cfg.Model)
		if model == "" {
			model = defaultModel
		}
		model, err := normalizeAgentModel(model)
		if err != nil {
			return agent.HookResult{Decision: agent.HookDecisionError, Message: err.Error()}
		}
		provider, err := r.newProvider(model, 0, defaultEffort)
		if err != nil {
			return agent.HookResult{Decision: agent.HookDecisionError, Message: err.Error()}
		}
		prompt := buildHookModelPrompt(cfg, payload, hookKind)
		content, usage, err := completeHookModel(ctx, provider, prompt)
		if err != nil {
			return agent.HookResult{Decision: agent.HookDecisionError, Message: err.Error()}
		}
		res := parseHookModelResult(payload.Event, content)
		if res.Metadata == nil {
			res.Metadata = map[string]any{}
		}
		res.Metadata["model"] = model
		res.Metadata["hook_type"] = hookKind
		res.Metadata["usage"] = usage
		return res
	}
}

func buildHookModelPrompt(cfg agent.HookConfig, payload agent.HookPayload, hookKind string) string {
	payloadJSON, _ := json.MarshalIndent(payload, "", "  ")
	return strings.TrimSpace(cfg.Prompt) + "\n\n" +
		"Hook type: " + hookKind + "\n" +
		"Hook event: " + string(payload.Event) + "\n" +
		"Hook payload JSON:\n" + string(payloadJSON) + "\n\n" +
		"Return only JSON with this shape:\n" +
		"{\"decision\":\"pass|warn|block|halt\",\"reason\":\"short explanation\",\"additionalContext\":\"optional context\",\"updatedInput\":\"optional rewritten user prompt or tool args JSON\"}\n" +
		"You may also return {\"ok\":true|false,\"reason\":\"...\"}; ok:false maps to block and ok:true maps to pass."
}

func completeHookModel(ctx context.Context, provider llm.Provider, prompt string) (string, llm.Usage, error) {
	var content strings.Builder
	var usage llm.Usage
	sawDelta := false
	for ev := range provider.StreamResponse(ctx, []core.Message{{Role: core.RoleUser, Text: prompt}}, nil) {
		switch ev.Type {
		case llm.EventContentDelta:
			content.WriteString(ev.Content)
			sawDelta = true
		case llm.EventComplete:
			if ev.Response != nil {
				if !sawDelta && strings.TrimSpace(ev.Response.Content) != "" {
					content.WriteString(ev.Response.Content)
				}
				usage = addUsage(usage, ev.Response.Usage)
			}
		case llm.EventError:
			if ev.Err != nil {
				return "", usage, ev.Err
			}
			return "", usage, errors.New("hook model failed")
		}
	}
	out := strings.TrimSpace(content.String())
	if out == "" {
		return "", usage, errors.New("hook model returned empty response")
	}
	return out, usage, nil
}

func parseHookModelResult(event agent.HookEvent, content string) agent.HookResult {
	raw := strings.TrimSpace(content)
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return agent.HookResult{Decision: agent.HookDecisionError, Message: "hook model returned invalid JSON: " + err.Error(), Stdout: raw}
	}
	out := agent.HookResult{
		Message:           firstStringValue(body, "reason", "message", "systemMessage"),
		AdditionalContext: firstStringValue(body, "additionalContext", "additional_context", "context"),
		UpdatedInput:      jsonHookValueString(body, "updatedInput", "updated_input"),
		Metadata:          map[string]any{},
	}
	if okRaw, exists := body["ok"]; exists {
		ok, isBool := okRaw.(bool)
		if !isBool {
			return agent.HookResult{Decision: agent.HookDecisionError, Message: "hook model ok must be boolean", Stdout: raw}
		}
		if ok {
			out.Decision = agent.HookDecisionPass
		} else {
			out.Decision = agent.HookDecisionBlock
		}
		return out
	}
	decision := strings.ToLower(strings.TrimSpace(firstStringValue(body, "decision")))
	switch decision {
	case "", "pass", "none", "continue":
		out.Decision = agent.HookDecisionPass
	case "warn", "warning":
		out.Decision = agent.HookDecisionWarn
	case "block", "deny", "denied":
		out.Decision = agent.HookDecisionBlock
	case "halt", "stop":
		out.Decision = agent.HookDecisionHalt
	default:
		return agent.HookResult{Decision: agent.HookDecisionError, Message: fmt.Sprintf("unsupported hook decision %q", decision), Stdout: raw}
	}
	if event == agent.HookEventPostToolUse && out.Decision == agent.HookDecisionBlock {
		out.Decision = agent.HookDecisionWarn
	}
	return out
}

func firstStringValue(body map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func jsonHookValueString(body map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := body[key]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
	}
	return ""
}
