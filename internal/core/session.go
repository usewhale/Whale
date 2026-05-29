package core

import "strings"

const (
	// ToolInputEventsSuffix is the filename suffix for tool input event logs.
	ToolInputEventsSuffix = ".tool_input_events.jsonl"
	// ApprovalEventsSuffix is the filename suffix for approval event logs.
	ApprovalEventsSuffix = ".approval_events.jsonl"
)

// SanitizeSessionID sanitizes a session ID for use in filenames.
// Non-alphanumeric characters (except - and _) are replaced with _.
func SanitizeSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	v = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, v)
	return v
}

// IsSessionJSONLName reports whether name is a session JSONL file (not tool input or approval events).
func IsSessionJSONLName(name string) bool {
	return strings.HasSuffix(name, ".jsonl") &&
		!strings.HasSuffix(name, ToolInputEventsSuffix) &&
		!strings.HasSuffix(name, ApprovalEventsSuffix)
}
