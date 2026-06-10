package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

type ToolEnvelope struct {
	OK        bool           `json:"ok"`
	Success   bool           `json:"success"`
	Error     string         `json:"error,omitempty"`
	Message   string         `json:"message,omitempty"`
	Code      string         `json:"code,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func NewToolSuccessEnvelope(data map[string]any) ToolEnvelope {
	return ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    "ok",
		Data:    data,
	}
}

func NewToolErrorEnvelope(code, message string) ToolEnvelope {
	return ToolEnvelope{
		OK:      false,
		Success: false,
		Code:    strings.TrimSpace(code),
		Message: strings.TrimSpace(message),
	}
}

// MarshalToolEnvelope serializes the model-facing tool result. HTML escaping
// must stay off: the model reads this text and copies payload fragments
// (file content, shell commands) back into edit/write inputs, so & < > have
// to survive byte-for-byte. Session 019ead56 documents the failure mode.
func MarshalToolEnvelope(env ToolEnvelope) (string, error) {
	b, err := MarshalToolJSON(env)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MarshalToolJSON serializes any model-visible tool JSON without HTML
// escaping. Use this instead of json.Marshal whenever the output becomes
// ToolResult.Content: the model reads that text raw, so json.Marshal's
// HTML escaping would corrupt every payload containing & < >.
func MarshalToolJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func ParseToolEnvelope(raw string) (ToolEnvelope, bool) {
	var env ToolEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &env); err != nil {
		return ToolEnvelope{}, false
	}
	if env.Data == nil {
		env.Data = map[string]any{}
	}
	if env.Metadata == nil && env.Meta != nil {
		env.Metadata = env.Meta
	}
	if env.Error == "" && env.Message != "" {
		env.Error = env.Message
	}
	return env, true
}
