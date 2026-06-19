package core

import (
	"context"
	"strings"
	"testing"
)

type snapshotTestTool struct {
	name    string
	content string
	params  map[string]any
}

func (t snapshotTestTool) Name() string { return t.name }
func (t snapshotTestTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: t.content}, nil
}
func (t snapshotTestTool) Parameters() map[string]any { return t.params }

type dynamicParamsTool struct {
	name  string
	calls int
}

func (t *dynamicParamsTool) Name() string { return t.name }
func (t *dynamicParamsTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok"}, nil
}
func (t *dynamicParamsTool) Parameters() map[string]any {
	t.calls++
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"const": t.calls},
		},
	}
}

type nilParamsTool struct {
	name string
}

func (t nilParamsTool) Name() string { return t.name }
func (t nilParamsTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok"}, nil
}
func (t nilParamsTool) Parameters() map[string]any { return nil }

func TestToolRegistrySnapshotIsStableAfterReplace(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{name: "old", content: "old-ok"}})
	snap := reg.Snapshot()

	if err := reg.ReplaceTools([]Tool{snapshotTestTool{name: "new", content: "new-ok"}}); err != nil {
		t.Fatalf("replace tools: %v", err)
	}

	if got := snap.Get("old"); got == nil {
		t.Fatal("snapshot lost old tool")
	}
	if got := snap.Get("new"); got != nil {
		t.Fatal("snapshot should not include replacement tool")
	}
	res, err := snap.Dispatch(context.Background(), ToolCall{ID: "tc-old", Name: "old"})
	if err != nil {
		t.Fatalf("dispatch snapshot tool: %v", err)
	}
	if res.IsError() || !strings.Contains(res.ModelText, "old-ok") {
		t.Fatalf("unexpected snapshot dispatch result: %+v", res)
	}
}

func TestToolRegistryToolsUseFrozenSpecs(t *testing.T) {
	tool := &dynamicParamsTool{name: "dynamic"}
	reg := NewToolRegistry([]Tool{tool})
	providerTools := reg.Tools()
	if len(providerTools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(providerTools))
	}

	first := DescribeTool(providerTools[0]).Parameters
	second := DescribeTool(providerTools[0]).Parameters
	firstProps := first["properties"].(map[string]any)
	secondProps := second["properties"].(map[string]any)
	firstValue := firstProps["value"].(map[string]any)
	secondValue := secondProps["value"].(map[string]any)
	if firstValue["const"] != secondValue["const"] {
		t.Fatalf("provider tool schema changed: %+v != %+v", first, second)
	}
	if tool.calls != 1 {
		t.Fatalf("dynamic Parameters called %d times, want 1", tool.calls)
	}
}

func TestToolRegistryProviderToolPayloadUsesSessionCache(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"type": "string"},
		},
	}
	reg := NewToolRegistry([]Tool{snapshotTestTool{name: "stable", params: params}})

	firstTools := reg.Tools()
	first := ProviderToolPayload(firstTools[0])
	fn := first["function"].(map[string]any)
	fn["description"] = "mutated by caller"

	second := ProviderToolPayload(firstTools[0])
	secondFn := second["function"].(map[string]any)
	if secondFn["description"] == "mutated by caller" {
		t.Fatal("provider payload mutation leaked into cache")
	}
	if len(reg.providerSchemas.entries) != 1 {
		t.Fatalf("provider schema cache entries = %d, want 1", len(reg.providerSchemas.entries))
	}

	if err := reg.ReplaceTools([]Tool{snapshotTestTool{name: "stable", params: params}}); err != nil {
		t.Fatalf("replace tools: %v", err)
	}
	replaced := ProviderToolPayload(reg.Tools()[0])
	if len(reg.providerSchemas.entries) != 1 {
		t.Fatalf("provider schema cache entries after equivalent replace = %d, want 1", len(reg.providerSchemas.entries))
	}
	if replacedFn := replaced["function"].(map[string]any); replacedFn["description"] != secondFn["description"] {
		t.Fatalf("provider description changed after equivalent replace: %v != %v", replacedFn["description"], secondFn["description"])
	}
}

func TestToolRegistryProviderToolPayloadChangesWhenSpecChanges(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "changing",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	}})
	first := ProviderToolPayload(reg.Tools()[0])

	if err := reg.ReplaceTools([]Tool{snapshotTestTool{
		name: "changing",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "integer"},
			},
		},
	}}); err != nil {
		t.Fatalf("replace tools: %v", err)
	}
	second := ProviderToolPayload(reg.Tools()[0])

	if len(reg.providerSchemas.entries) != 2 {
		t.Fatalf("provider schema cache entries after changed replace = %d, want 2", len(reg.providerSchemas.entries))
	}
	firstParams := first["function"].(map[string]any)["parameters"].(map[string]any)
	secondParams := second["function"].(map[string]any)["parameters"].(map[string]any)
	firstType := firstParams["properties"].(map[string]any)["value"].(map[string]any)["type"]
	secondType := secondParams["properties"].(map[string]any)["value"].(map[string]any)["type"]
	if firstType == secondType {
		t.Fatalf("provider schema did not reflect changed params: %v", secondParams)
	}
}

func TestToolRegistryToolsNormalizeNilParameterSpecs(t *testing.T) {
	reg := NewToolRegistry([]Tool{nilParamsTool{name: "empty"}})
	providerTools := reg.Tools()
	if len(providerTools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(providerTools))
	}

	params := DescribeTool(providerTools[0]).Parameters
	if params == nil {
		t.Fatal("provider tool parameters are nil")
	}
	if got := params["type"]; got != "object" {
		t.Fatalf("parameters type = %v, want object", got)
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters properties = %T, want map[string]any", params["properties"])
	}
	if len(props) != 0 {
		t.Fatalf("properties len = %d, want 0", len(props))
	}
	if got := params["additionalProperties"]; got != true {
		t.Fatalf("additionalProperties = %v, want true", got)
	}
}

func TestSearchFilesUnknownIncludeReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "search_files",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-search",
		Name:  "search_files",
		Input: `{"pattern":"version","include":"*.go","limit":20}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`error (invalid_input)`,
		`unknown field "include"`,
		"search_files has no include field; put the glob directly in pattern (e.g. pattern=**/*.go), or use grep with include to search file contents.",
		`recovery:`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestSearchFilesMissingPatternReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "search_files",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-search",
		Name:  "search_files",
		Input: `{"limit":20}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`error (invalid_input)`,
		`missing required field "pattern"`,
		"search_files requires pattern; provide pattern and path, or use grep for content search.",
		`recovery:`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestGrepMissingPatternReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "grep",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"include": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-grep",
		Name:  "grep",
		Input: `{"limit":20}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`error (invalid_input)`,
		`missing required field "pattern"`,
		"grep requires pattern (a regex); provide pattern and optionally include/path, or use search_files to find file names.",
		`recovery:`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestUnknownFieldReturnsSchemaDerivedRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "shell_run",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command":    map[string]any{"type": "string"},
				"cwd":        map[string]any{"type": "string"},
				"background": map[string]any{"type": "boolean"},
			},
			"required": []string{"command"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-shell",
		Name:  "shell_run",
		Input: `{"command":"git status","description":"Show working tree status"}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`error (invalid_input)`,
		`unknown field "description"`,
		// Hint is derived from the tool's own schema, with fields sorted.
		"description is not a parameter of shell_run; supported parameters: background, command, cwd.",
		`recovery:`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestSchemaFieldRecoveryHintGeneric(t *testing.T) {
	params := map[string]any{
		"properties": map[string]any{
			"alpha": map[string]any{"type": "string"},
			"beta":  map[string]any{"type": "string"},
		},
	}
	got, ok := schemaFieldRecoveryHint("mytool", `missing required field "alpha"`, params)
	if !ok {
		t.Fatalf("expected hint for missing required field")
	}
	if want := "mytool requires the alpha field; supported parameters: alpha, beta."; got != want {
		t.Fatalf("missing-field hint = %q, want %q", got, want)
	}

	got, ok = schemaFieldRecoveryHint("mytool", `unknown field "gamma"`, params)
	if !ok {
		t.Fatalf("expected hint for unknown field")
	}
	if want := "gamma is not a parameter of mytool; supported parameters: alpha, beta."; got != want {
		t.Fatalf("unknown-field hint = %q, want %q", got, want)
	}

	// No schema properties: still names the offending field without a list.
	got, ok = schemaFieldRecoveryHint("mytool", `unknown field "gamma"`, nil)
	if !ok {
		t.Fatalf("expected hint without schema properties")
	}
	if want := "gamma is not a parameter of mytool; remove it."; got != want {
		t.Fatalf("no-props hint = %q, want %q", got, want)
	}

	// Unrelated messages do not produce a generic hint.
	if _, ok := schemaFieldRecoveryHint("mytool", "input must be valid JSON object", params); ok {
		t.Fatalf("did not expect hint for unrelated message")
	}
}

func TestWebFetchMaxResultsReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "web_fetch",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"url":        map[string]any{"type": "string"},
				"prompt":     map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"url", "prompt"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-web-fetch",
		Name:  "web_fetch",
		Input: `{"url":"https://example.com","prompt":"summarize","max_results":5}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`error (invalid_input)`,
		`unknown field "max_results"`,
		"web_fetch does not support max_results; remove it or use web_search when you need multiple search results.",
		`recovery:`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestFetchFormatReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "fetch",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"url":        map[string]any{"type": "string"},
				"prompt":     map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"url", "prompt"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-fetch",
		Name:  "fetch",
		Input: `{"url":"https://example.com","prompt":"summarize","format":"text"}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`error (invalid_input)`,
		`unknown field "format"`,
		"fetch does not support format; omit it and use prompt to request the output shape.",
		`recovery:`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestFetchFileURLReturnsRecoveryHint(t *testing.T) {
	for _, msg := range []string{
		`url scheme must be http or https`,
		`valid url is required`,
	} {
		content := invalidToolInputContent("fetch", nil, errString(msg))
		for _, want := range []string{
			`"code":"invalid_input"`,
			msg,
			"fetch only supports http/https URLs; use read_file for local file paths or tool result files.",
			`"recovery"`,
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("result for %q missing %q:\n%s", msg, want, content)
			}
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
