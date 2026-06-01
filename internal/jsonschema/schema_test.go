package jsonschema

import "testing"

func TestValidateSchemaAllowsAnnotationKeywords(t *testing.T) {
	schema := map[string]any{
		"type":        "object",
		"title":       "Review result",
		"description": "Structured review output.",
		"properties": map[string]any{
			"summary": map[string]any{
				"type":        "string",
				"description": "Short summary.",
			},
		},
	}
	if err := ValidateSchema(schema); err != nil {
		t.Fatalf("ValidateSchema: %v", err)
	}
}
