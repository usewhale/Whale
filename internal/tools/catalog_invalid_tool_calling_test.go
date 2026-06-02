package tools

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestWebSearchSchemaKeepsDeepSeekTUICompatibleFields(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	spec := describeToolByName(t, ts.Tools(), "web_search")
	props := schemaProperties(t, spec.Parameters)
	for _, want := range []string{"query", "q", "search_query", "max_results", "timeout_ms"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("web_search schema missing %q: %+v", want, props)
		}
	}
	searchQuery, ok := props["search_query"].(map[string]any)
	if !ok {
		t.Fatalf("search_query schema is not an object: %#v", props["search_query"])
	}
	items, ok := searchQuery["items"].(map[string]any)
	if !ok {
		t.Fatalf("search_query items schema missing: %#v", searchQuery)
	}
	itemProps := schemaProperties(t, items)
	for _, want := range []string{"q", "query", "max_results"} {
		if _, ok := itemProps[want]; !ok {
			t.Fatalf("search_query item schema missing %q: %+v", want, itemProps)
		}
	}
}

func TestFetchSchemasKeepSearchOptionsOutOfFetchTools(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	for _, toolName := range []string{"fetch", "web_fetch"} {
		spec := describeToolByName(t, ts.Tools(), toolName)
		props := schemaProperties(t, spec.Parameters)
		for _, unsupported := range []string{"max_results", "search_query"} {
			if _, ok := props[unsupported]; ok {
				t.Fatalf("%s schema should not accept search option %q: %+v", toolName, unsupported, props)
			}
		}
	}
}

func TestFetchSchemaKeepsFormatOutUntilSupported(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	spec := describeToolByName(t, ts.Tools(), "fetch")
	props := schemaProperties(t, spec.Parameters)
	if _, ok := props["format"]; ok {
		t.Fatalf("fetch schema should not advertise format unless the tool implements it: %+v", props)
	}
}

func describeToolByName(t *testing.T, tools []core.Tool, name string) core.ToolSpec {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return core.DescribeTool(tool)
		}
	}
	t.Fatalf("tool %q not found", name)
	return core.ToolSpec{}
}

func schemaProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing properties: %#v", schema)
	}
	return props
}
