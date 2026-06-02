package core

import (
	"encoding/json"
	"testing"
)

func TestRepairToolInputForSpecLeavesValidInputUnchanged(t *testing.T) {
	spec := repairTestSpec()
	raw := `{"prompts":["a"],"content":"[\"not an arg array\"]"}`
	out, repairs := RepairToolInputForSpec(spec, raw)
	if out != raw {
		t.Fatalf("valid input changed: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecOmitsOptionalNull(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":["a"],"limit":null}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairNullOptionalOmitted || repairs[0].Path != "limit" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	got := decodeRepairOutput(t, out)
	if _, ok := got["limit"]; ok {
		t.Fatalf("expected optional null field to be omitted: %s", out)
	}
}

func TestRepairToolInputForSpecDoesNotOmitRequiredNull(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":null}`)
	if out != `{"prompts":null}` {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecParsesStringifiedArrayBeforeWrapping(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":"[\"a\",\"b\"]"}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairStringifiedArray || repairs[0].Path != "prompts" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	if repairs[0].BeforeType != "string" || repairs[0].AfterType != "array" {
		t.Fatalf("unexpected type telemetry: %+v", repairs[0])
	}
	got := decodeRepairOutput(t, out)
	prompts, ok := got["prompts"].([]any)
	if !ok || len(prompts) != 2 || prompts[0] != "a" || prompts[1] != "b" {
		t.Fatalf("unexpected prompts: %#v from %s", got["prompts"], out)
	}
}

func TestRepairToolInputForSpecWrapsBareStringForStringArray(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":"a"}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairBareStringToArray || repairs[0].Path != "prompts" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	got := decodeRepairOutput(t, out)
	prompts, ok := got["prompts"].([]any)
	if !ok || len(prompts) != 1 || prompts[0] != "a" {
		t.Fatalf("unexpected prompts: %#v from %s", got["prompts"], out)
	}
}

func TestRepairToolInputForSpecRejectsEmptyArrayWhenMinItemsFails(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":{}}`)
	if out != `{"prompts":{}}` {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecRepairsEmptyObjectToOptionalArray(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":["a"],"ignore":{}}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairEmptyObjectToArray || repairs[0].Path != "ignore" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	got := decodeRepairOutput(t, out)
	ignore, ok := got["ignore"].([]any)
	if !ok || len(ignore) != 0 {
		t.Fatalf("unexpected ignore: %#v from %s", got["ignore"], out)
	}
}

func TestRepairToolInputForSpecLeavesUnknownFieldInvalid(t *testing.T) {
	spec := repairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"prompts":"a","extra":true}`)
	if out != `{"prompts":"a","extra":true}` {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecCoercesSemanticBooleanString(t *testing.T) {
	spec := grepRepairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"pattern":"needle","literal_text":"false"}`)
	if len(repairs) != 1 || repairs[0].Path != "literal_text" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	if repairs[0].BeforeType != "string" || repairs[0].AfterType != "boolean" {
		t.Fatalf("unexpected type telemetry: %+v", repairs[0])
	}
	got := decodeRepairOutput(t, out)
	if got["literal_text"] != false {
		t.Fatalf("expected literal_text=false, got %#v from %s", got["literal_text"], out)
	}
}

func TestRepairToolInputForSpecCoercesSemanticIntegerString(t *testing.T) {
	spec := grepRepairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"pattern":"needle","limit":"30"}`)
	if len(repairs) != 1 || repairs[0].Path != "limit" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	if repairs[0].BeforeType != "string" || repairs[0].AfterType != "number" {
		t.Fatalf("unexpected type telemetry: %+v", repairs[0])
	}
	got := decodeRepairOutput(t, out)
	if got["limit"] != float64(30) {
		t.Fatalf("expected limit=30, got %#v from %s", got["limit"], out)
	}
}

func TestRepairToolInputForSpecLeavesInvalidSemanticBooleanString(t *testing.T) {
	spec := grepRepairTestSpec()
	raw := `{"pattern":"needle","literal_text":"no"}`
	out, repairs := RepairToolInputForSpec(spec, raw)
	if out != raw {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecUnwrapsMarkdownAutolinkFilePath(t *testing.T) {
	spec := pathRepairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"file_path":"[README.md](http://README.md)","content":"x"}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairMarkdownAutolinkPath || repairs[0].Path != "file_path" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	if repairs[0].BeforeType != "string" || repairs[0].AfterType != "string" {
		t.Fatalf("unexpected type telemetry: %+v", repairs[0])
	}
	got := decodeRepairOutput(t, out)
	if got["file_path"] != "README.md" {
		t.Fatalf("unexpected file_path: %#v from %s", got["file_path"], out)
	}
}

func TestRepairToolInputForSpecUnwrapsMarkdownAutolinkPathWithPrefix(t *testing.T) {
	spec := pathRepairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"path":"sub/[a.txt](http://a.txt)","content":"x"}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairMarkdownAutolinkPath || repairs[0].Path != "path" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	got := decodeRepairOutput(t, out)
	if got["path"] != "sub/a.txt" {
		t.Fatalf("unexpected path: %#v from %s", got["path"], out)
	}
}

func TestRepairToolInputForSpecUnwrapsMarkdownAutolinkCWD(t *testing.T) {
	spec := pathRepairTestSpec()
	out, repairs := RepairToolInputForSpec(spec, `{"cwd":"[internal](http://internal)","content":"x"}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairMarkdownAutolinkPath || repairs[0].Path != "cwd" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	got := decodeRepairOutput(t, out)
	if got["cwd"] != "internal" {
		t.Fatalf("unexpected cwd: %#v from %s", got["cwd"], out)
	}
}

func TestRepairToolInputForSpecDoesNotUnwrapMarkdownInContent(t *testing.T) {
	spec := pathRepairTestSpec()
	raw := `{"file_path":"README.md","content":"[README.md](http://README.md)"}`
	out, repairs := RepairToolInputForSpec(spec, raw)
	if out != raw {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecDoesNotUnwrapNormalMarkdownLink(t *testing.T) {
	spec := pathRepairTestSpec()
	raw := `{"file_path":"[click](https://example.com)","content":"x"}`
	out, repairs := RepairToolInputForSpec(spec, raw)
	if out != raw {
		t.Fatalf("unexpected output: %s", out)
	}
	if len(repairs) != 0 {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
}

func TestRepairToolInputForSpecRepairsNestedArrayPath(t *testing.T) {
	spec := ToolSpec{
		Name: "request_user_input",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"options": map[string]any{
								"type":  "array",
								"items": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
			"required":             []string{"questions"},
			"additionalProperties": false,
		},
	}
	out, repairs := RepairToolInputForSpec(spec, `{"questions":[{"options":"[\"yes\",\"no\"]"}]}`)
	if len(repairs) != 1 || repairs[0].Kind != ToolInputRepairStringifiedArray || repairs[0].Path != "questions[0].options" {
		t.Fatalf("unexpected repairs: %+v", repairs)
	}
	got := decodeRepairOutput(t, out)
	questions := got["questions"].([]any)
	first := questions[0].(map[string]any)
	options, ok := first["options"].([]any)
	if !ok || len(options) != 2 || options[0] != "yes" || options[1] != "no" {
		t.Fatalf("unexpected options: %#v from %s", first["options"], out)
	}
}

func grepRepairTestSpec() ToolSpec {
	return ToolSpec{
		Name: "grep",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":      map[string]any{"type": "string"},
				"literal_text": map[string]any{"type": "boolean"},
				"limit":        map[string]any{"type": "integer"},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},
	}
}

func pathRepairTestSpec() ToolSpec {
	return ToolSpec{
		Name: "write",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string"},
				"path":      map[string]any{"type": "string"},
				"cwd":       map[string]any{"type": "string"},
				"content":   map[string]any{"type": "string"},
			},
			"required":             []string{"content"},
			"additionalProperties": false,
		},
	}
}

func repairTestSpec() ToolSpec {
	return ToolSpec{
		Name: "parallel_reason",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompts": map[string]any{
					"type":     "array",
					"items":    map[string]any{"type": "string"},
					"minItems": 1,
				},
				"ignore":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"limit":   map[string]any{"type": "integer"},
				"content": map[string]any{"type": "string"},
			},
			"required":             []string{"prompts"},
			"additionalProperties": false,
		},
	}
}

func decodeRepairOutput(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("invalid json output %q: %v", raw, err)
	}
	return out
}
