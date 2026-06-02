package tasks

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
)

// ResolveAgentHooks converts durable agent-definition hook metadata into the
// concrete hook runner used by child agents. It accepts Claude Code style
// command, shell, prompt, http, and agent hooks; runtime execution is handled by
// the agent hook runner plus task-runner model executors.
func ResolveAgentHooks(def AgentDefinition) ([]agent.ResolvedHook, error) {
	if def.Hooks == nil {
		return nil, nil
	}
	raw, err := normalizeHookObject(def.Hooks)
	if err != nil {
		return nil, err
	}
	sourceName := strings.TrimSpace(def.Name)
	if sourceName == "" {
		sourceName = "agent"
	}
	source := "agent:" + sourceName
	out := make([]agent.ResolvedHook, 0)
	for eventName, value := range raw {
		event, err := normalizeAgentHookEvent(eventName)
		if err != nil {
			return nil, err
		}
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("agent hooks %s must be an array", eventName)
		}
		for i, item := range items {
			hooks, err := resolveAgentHookEntry(event, item, source)
			if err != nil {
				return nil, fmt.Errorf("agent hooks %s[%d]: %w", eventName, i, err)
			}
			out = append(out, hooks...)
		}
	}
	return out, nil
}

func normalizeHookObject(v any) (map[string]any, error) {
	switch t := v.(type) {
	case map[string]any:
		return t, nil
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(t, &out); err != nil {
			return nil, fmt.Errorf("agent hooks must be an object: %w", err)
		}
		return out, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("agent hooks must be JSON-serializable: %w", err)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("agent hooks must be an object: %w", err)
		}
		if out == nil {
			return nil, fmt.Errorf("agent hooks must be an object")
		}
		return out, nil
	}
}

func normalizeAgentHookEvent(raw string) (agent.HookEvent, error) {
	switch agent.HookEvent(strings.TrimSpace(raw)) {
	case agent.HookEventPreToolUse:
		return agent.HookEventPreToolUse, nil
	case agent.HookEventPostToolUse:
		return agent.HookEventPostToolUse, nil
	case agent.HookEventSubagentStart:
		return agent.HookEventSubagentStart, nil
	case agent.HookEventSubagentStop, agent.HookEventStop:
		return agent.HookEventSubagentStop, nil
	default:
		return "", fmt.Errorf("unsupported agent hook event %q", strings.TrimSpace(raw))
	}
}

func resolveAgentHookEntry(event agent.HookEvent, raw any, source string) ([]agent.ResolvedHook, error) {
	entry, ok := rawMap(raw)
	if !ok {
		return nil, fmt.Errorf("hook entry must be an object")
	}
	if rawHooks, ok := entry["hooks"]; ok {
		return resolveClaudeHookMatcher(event, entry, rawHooks, source)
	}
	cfg, err := hookConfigFromCommandObject(event, entry, "", false)
	if err != nil {
		return nil, err
	}
	return []agent.ResolvedHook{{HookConfig: cfg, Event: event, Source: source}}, nil
}

func resolveClaudeHookMatcher(event agent.HookEvent, entry map[string]any, rawHooks any, source string) ([]agent.ResolvedHook, error) {
	items, ok := rawHooks.([]any)
	if !ok {
		return nil, fmt.Errorf("matcher hooks must be an array")
	}
	matcher := stringAny(entry, "matcher")
	out := make([]agent.ResolvedHook, 0, len(items))
	for i, item := range items {
		hookObj, ok := rawMap(item)
		if !ok {
			return nil, fmt.Errorf("matcher hook %d must be an object", i)
		}
		cfg, err := hookConfigFromCommandObject(event, hookObj, matcher, true)
		if err != nil {
			return nil, fmt.Errorf("matcher hook %d: %w", i, err)
		}
		out = append(out, agent.ResolvedHook{HookConfig: cfg, Event: event, Source: source})
	}
	return out, nil
}

func hookConfigFromCommandObject(event agent.HookEvent, obj map[string]any, inheritedMatch string, claudeShape bool) (agent.HookConfig, error) {
	hookType := strings.TrimSpace(stringAny(obj, "type"))
	if hookType == "shell" && strings.TrimSpace(stringAny(obj, "command")) == "" {
		obj["command"] = stringAny(obj, "shell")
		obj["shell"] = ""
		hookType = "command"
	}
	if hookType == "" {
		hookType = "command"
	}
	if hookType != "command" && hookType != "shell" && hookType != "prompt" && hookType != "http" && hookType != "agent" {
		return agent.HookConfig{}, fmt.Errorf("unsupported hook type %q", hookType)
	}
	match := core.FirstNonEmpty(strings.TrimSpace(stringAny(obj, "match")), strings.TrimSpace(inheritedMatch))
	timeout, err := hookTimeoutSec(obj)
	if err != nil {
		return agent.HookConfig{}, err
	}
	cfg := agent.HookConfig{
		Type:           hookType,
		Match:          match,
		If:             strings.TrimSpace(stringAny(obj, "if")),
		Command:        strings.TrimSpace(stringAny(obj, "command")),
		Prompt:         strings.TrimSpace(stringAny(obj, "prompt")),
		URL:            strings.TrimSpace(stringAny(obj, "url")),
		Model:          strings.TrimSpace(stringAny(obj, "model")),
		Description:    strings.TrimSpace(stringAny(obj, "description")),
		TimeoutSec:     timeout,
		CWD:            strings.TrimSpace(stringAny(obj, "cwd")),
		Shell:          strings.TrimSpace(stringAny(obj, "shell")),
		Once:           boolAny(obj, "once"),
		Async:          boolAny(obj, "async"),
		AsyncRewake:    boolAny(obj, "asyncRewake"),
		Headers:        stringMapAny(obj, "headers"),
		AllowedEnvVars: stringSliceAny(obj, "allowedEnvVars"),
	}
	switch hookType {
	case "command", "shell":
		if cfg.Command == "" {
			return agent.HookConfig{}, fmt.Errorf("command hook requires command")
		}
	case "prompt", "agent":
		if cfg.Prompt == "" {
			return agent.HookConfig{}, fmt.Errorf("%s hook requires prompt", hookType)
		}
	case "http":
		if cfg.URL == "" {
			return agent.HookConfig{}, fmt.Errorf("http hook requires url")
		}
	}
	if cfg.Description == "" {
		cfg.Description = strings.TrimSpace(stringAny(obj, "statusMessage"))
	}
	if event == agent.HookEventSubagentStart || event == agent.HookEventSubagentStop {
		cfg.Match = ""
	}
	return cfg, nil
}

func stringMapAny(m map[string]any, key string) map[string]string {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	switch t := raw.(type) {
	case map[string]string:
		out := map[string]string{}
		for k, v := range t {
			out[k] = v
		}
		return out
	case map[string]any:
		out := map[string]string{}
		for k, v := range t {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func stringSliceAny(m map[string]any, key string) []string {
	raw, ok := m[key]
	if !ok {
		return nil
	}
	switch t := raw.(type) {
	case []string:
		return cloneStrings(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func hookTimeoutSec(obj map[string]any) (int, error) {
	v, ok := obj["timeout"]
	if !ok {
		return 0, nil
	}
	n, ok := numberAny(v)
	if !ok {
		return 0, fmt.Errorf("hook timeout must be a number")
	}
	if n <= 0 {
		return 0, fmt.Errorf("hook timeout must be positive")
	}
	if n > math.MaxInt32 {
		return 0, fmt.Errorf("hook timeout is too large")
	}
	return int(math.Round(n)), nil
}

func rawMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	default:
		return nil, false
	}
}

func stringAny(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func numberAny(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case json.Number:
		n, err := t.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func boolAny(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}
