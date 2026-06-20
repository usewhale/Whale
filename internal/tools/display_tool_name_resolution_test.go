package tools

// Regression for session 019ee359: the model called "Grep" (its strong
// Claude-Code prior for content search), but the tool was registered under the
// lowercase internal name "grep" and only two tools (Bash, Read) had a
// model-facing display name. CanonicalToolName passed "Grep" through unchanged,
// the case-sensitive registry lookup missed, and the call came back as
// {"error":"tool not found"}.
//
// The invariant that prevents the whole class of bug: every model-facing name
// whale puts in the provider schema must canonicalize back to a tool that is
// actually registered. This test ties the two halves together — schema-out and
// canonical-in — across the real toolset, so adding a tool without a matching
// display/alias mapping (or vice versa) fails here rather than at runtime.

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestEveryModelFacingToolNameResolves(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	tools := ts.Tools()

	registered := make(map[string]bool, len(tools))
	for _, tool := range tools {
		registered[tool.Name()] = true
	}

	for _, tool := range tools {
		payload := core.ProviderToolPayload(tool)
		fn, _ := payload["function"].(map[string]any)
		modelName, _ := fn["name"].(string)
		if modelName == "" {
			t.Fatalf("tool %q produced empty model-facing name", tool.Name())
		}
		// The name the model sees must round-trip back to a registered tool.
		if got := core.CanonicalToolName(modelName); !registered[got] {
			t.Errorf("model-facing name %q canonicalizes to %q, which is not a registered tool", modelName, got)
		}
	}
}

// TestGrepModelPriorResolves pins the specific reported failure: the
// conventional capitalized names the model reaches for resolve to whale's
// snake_case/lowercase internal tools.
func TestGrepModelPriorResolves(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	registered := make(map[string]bool)
	for _, tool := range ts.Tools() {
		registered[tool.Name()] = true
	}

	cases := map[string]string{
		"Grep":      "grep",
		"Glob":      "search_files",
		"Edit":      "edit",
		"MultiEdit": "multi_edit",
		"Write":     "write",
		"LS":        "list_dir",
	}
	for modelName, internal := range cases {
		got := core.CanonicalToolName(modelName)
		if got != internal {
			t.Errorf("CanonicalToolName(%q) = %q, want %q", modelName, got, internal)
		}
		if !registered[got] {
			t.Errorf("canonical tool %q for model name %q is not registered", got, modelName)
		}
	}
}
