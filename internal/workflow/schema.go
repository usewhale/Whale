package workflow

import "github.com/usewhale/whale/internal/jsonschema"

func parseAndValidateStructuredOutput(raw string, schema map[string]any) (any, error) {
	return jsonschema.ParseAndValidateString(raw, schema)
}

func validateOutputSchema(schema map[string]any) error {
	return jsonschema.ValidateSchema(schema)
}
