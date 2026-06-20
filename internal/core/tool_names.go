package core

import (
	"regexp"
	"strings"
)

// displayToolNames maps whale's internal tool name to the conventional,
// model-facing name exposed in tool schemas. Models carry strong priors for
// these conventional names (Bash, Read, Grep, ...), so presenting them reduces
// invented or unknown tool-call names — e.g. a model reaching for "Grep" (its
// Claude-Code prior) when whale registered the lowercase "grep", which a
// case-sensitive registry lookup would then miss and report as "tool not
// found".
//
// Only the model ever sees the display name. Internal code, persisted sessions,
// permission allowlists, hook payloads, and approval events continue to use the
// internal name, which is why the rename is confined to the provider boundary
// and requires no churn in the many switch/case sites that key off tool names.
var displayToolNames = map[string]string{
	"shell_run":    "Bash",
	"read_file":    "Read",
	"grep":         "Grep",
	"search_files": "Glob",
	"edit":         "Edit",
	"multi_edit":   "MultiEdit",
	"write":        "Write",
	"list_dir":     "LS",
	"web_search":   "WebSearch",
	"web_fetch":    "WebFetch",
}

// extraInboundAliases are additional model-facing names — beyond the canonical
// display names, which are derived automatically below — that should resolve to
// an internal tool. These are typically CLI names a model invents. Keys are
// matched case-insensitively.
var extraInboundAliases = map[string]string{
	"head": "read_file",
	"cat":  "read_file",
	"sh":   "shell_run",
	"rg":   "grep",
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

// displayReplacements holds the word-boundary substitutions ApplyDisplayToolNames
// performs on free-form prose. Only snake_case internal names are eligible:
// they are distinct tokens that never appear inside ordinary words, so swapping
// them is safe. Single English-word names (edit, write, grep) are deliberately
// excluded — they collide with ordinary verbs and with capability literals like
// "workspace.write", so they are mapped only at the schema and canonical layers,
// never in prose.
var displayReplacements = buildDisplayReplacements()

type displayReplacement struct {
	re      *regexp.Regexp
	display string
}

func buildDisplayReplacements() []displayReplacement {
	out := make([]displayReplacement, 0, len(displayToolNames))
	for internal, display := range displayToolNames {
		if internal == display || !strings.Contains(internal, "_") {
			continue
		}
		out = append(out, displayReplacement{
			re:      regexp.MustCompile(`\b` + regexp.QuoteMeta(internal) + `\b`),
			display: display,
		})
	}
	return out
}

// ApplyDisplayToolNames rewrites internal tool names to their model-facing
// display names within free-form text (tool descriptions, guidance prose) so
// authored copy stays consistent with the schema without every string having to
// call DisplayToolName itself. Only snake_case names are rewritten, with word
// boundaries, so ordinary prose (and capability literals such as
// "workspace.write") is never corrupted. This is cosmetic: even if a name slips
// through, the model-supplied name still resolves because the tool remains
// registered under its internal name.
func ApplyDisplayToolNames(text string) string {
	if text == "" {
		return text
	}
	for _, r := range displayReplacements {
		text = r.re.ReplaceAllString(text, r.display)
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
