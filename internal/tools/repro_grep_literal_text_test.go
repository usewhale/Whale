package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// TestGrepHasNoLiteralTextField guards the fix for session 019ed8ba: the grep
// schema used to expose a boolean `literal_text` flag whose value-like name led
// the model to put its query there and omit the required `pattern`, failing with
// `missing required field "pattern"`. The field is gone — pattern is the only
// query input (always a regular expression) — so the misleading shape can no
// longer be generated from the schema.
func TestGrepHasNoLiteralTextField(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	var grep core.Tool
	for _, tool := range ts.Tools() {
		if tool.Name() == "grep" {
			grep = tool
			break
		}
	}
	if grep == nil {
		t.Fatal("grep tool not found")
	}

	payload := core.ProviderToolPayload(grep)
	fn, _ := payload["function"].(map[string]any)
	params, _ := fn["parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)
	if _, ok := props["literal_text"]; ok {
		t.Fatalf("grep schema still exposes literal_text: %#v", props)
	}
	if _, ok := props["pattern"]; !ok {
		t.Fatalf("grep schema missing pattern: %#v", props)
	}
}

// TestGrepPatternSearchStillWorks confirms ordinary regex/literal-safe queries
// continue to match through the registry dispatch path after the field removal.
func TestGrepPatternSearchStillWorks(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	if err := writeFileForTest(t, ts, "a.go", "const CapabilityWorkspaceRead = \"workspace.read\"\n"); err != nil {
		t.Fatal(err)
	}
	reg := core.NewToolRegistry(ts.Tools())

	res, err := reg.Dispatch(context.Background(), core.ToolCall{
		ID:    "tc-1",
		Name:  "grep",
		Input: `{"pattern":"CapabilityWorkspaceRead","include":"*.go"}`,
	})
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if res.IsError() {
		t.Fatalf("grep should succeed: %s", res.ModelText)
	}
	if !strings.Contains(res.ModelText, "CapabilityWorkspaceRead") {
		t.Fatalf("expected match in result: %s", res.ModelText)
	}
}

func writeFileForTest(t *testing.T, ts *Toolset, rel, content string) error {
	t.Helper()
	_, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": rel,
		"content":   content,
	}))
	return err
}
