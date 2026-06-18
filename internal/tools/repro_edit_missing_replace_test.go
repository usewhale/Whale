package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// Regression for session 019eca69-2c02-7927-83f1-df933068e39d: a deletion edit
// that omits "replace" must be accepted (treated as an empty replacement)
// rather than rejected by schema validation with
// `missing required field "replace"`.
//
// The call is routed through the registry so validateToolInput runs against the
// real tool schema — that validation layer was the one rejecting the call.
func TestEditMissingReplaceDeletesText(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lib.rs")
	const original = "fn main() {\n    let x = 1;\n    drop_me();\n    let y = 2;\n}\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	reg := core.NewToolRegistry(ts.Tools())

	// Deletion intent: replace omitted entirely.
	res, err := reg.Dispatch(context.Background(), core.ToolCall{
		ID:    "tc-edit",
		Name:  "edit",
		Input: `{"file_path":"lib.rs","search":"    drop_me();\n"}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.IsError() {
		t.Fatalf("deletion edit rejected:\n%s", res.ModelText)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	want := "fn main() {\n    let x = 1;\n    let y = 2;\n}\n"
	if string(got) != want {
		t.Fatalf("file not as expected after deletion edit:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Same case for multi_edit: an edit step that omits "replace" deletes its match.
func TestMultiEditMissingReplaceDeletesText(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lib.rs")
	const original = "a\nDELETE_ME\nb\nKEEP\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	reg := core.NewToolRegistry(ts.Tools())

	res, err := reg.Dispatch(context.Background(), core.ToolCall{
		ID:    "tc-multi",
		Name:  "multi_edit",
		Input: `{"file_path":"lib.rs","edits":[{"search":"DELETE_ME\n"},{"search":"KEEP","replace":"KEPT"}]}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.IsError() {
		t.Fatalf("multi_edit with omitted replace rejected:\n%s", res.ModelText)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\nb\nKEPT\n"
	if string(got) != want {
		t.Fatalf("file not as expected:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(string(got), "DELETE_ME") {
		t.Fatalf("DELETE_ME was not deleted")
	}
}
