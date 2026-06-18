package core

import "strings"

// displayToolNames maps whale's internal tool name to the conventional,
// model-facing name exposed in tool schemas. Models carry strong priors for
// these conventional names (Bash, Read, ...), so presenting them reduces
// invented or unknown tool-call names (e.g. a model reaching for "bash" or
// "head" when whale registered "shell_run"/"read_file").
//
// Only the model ever sees the display name. Internal code, persisted sessions,
// and user permission configs continue to use the internal name, which is why
// the rename is confined to the provider boundary and requires no churn in the
// many switch/case sites that key off tool names.
var displayToolNames = map[string]string{
	"shell_run": "Bash",
	"read_file": "Read",
}

// extraInboundAliases are additional model-facing names — beyond the canonical
// display names, which are derived automatically below — that should resolve to
// an internal tool. These are typically CLI names a model invents. Keys are
// matched case-insensitively.
var extraInboundAliases = map[string]string{
	"head": "read_file",
	"cat":  "read_file",
	"sh":   "shell_run",
}

// canonicalToolNames maps a lowercased model-facing name back to whale's
// internal tool name. It is derived from displayToolNames (so the forward and
// reverse directions cannot drift) plus extraInboundAliases.
var canonicalToolNames = buildCanonicalToolNames()

func buildCanonicalToolNames() map[string]string {
	m := make(map[string]string, len(displayToolNames)+len(extraInboundAliases))
	for internal, display := range displayToolNames {
		m[strings.ToLower(display)] = internal
	}
	for alias, internal := range extraInboundAliases {
		m[strings.ToLower(alias)] = internal
	}
	return m
}

// ApplyDisplayToolNames rewrites internal tool names to their model-facing
// display names within free-form text (tool descriptions, guidance prose) so
// authored copy stays consistent with the schema without every string having to
// call DisplayToolName itself. Internal names are distinct snake_case tokens
// that do not appear as substrings of ordinary words, so a plain replacement is
// safe. This is cosmetic: even if a name slips through, the model-supplied name
// still resolves because the tool remains registered under its internal name.
func ApplyDisplayToolNames(text string) string {
	if text == "" {
		return text
	}
	for internal, display := range displayToolNames {
		if internal == display {
			continue
		}
		text = strings.ReplaceAll(text, internal, display)
	}
	return text
}

// DisplayToolName returns the model-facing name for an internal tool name.
// Names without a mapping pass through unchanged.
func DisplayToolName(internal string) string {
	if d, ok := displayToolNames[internal]; ok {
		return d
	}
	return internal
}

// CanonicalToolName normalizes a model-supplied tool name to whale's internal
// name, accepting the conventional display name and known aliases
// case-insensitively. Unrecognized names pass through unchanged (trimmed), so
// genuinely unknown tools still surface as "tool not found" downstream.
func CanonicalToolName(modelName string) string {
	trimmed := strings.TrimSpace(modelName)
	if c, ok := canonicalToolNames[strings.ToLower(trimmed)]; ok {
		return c
	}
	return trimmed
}
