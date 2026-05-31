package jsonschema

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

var supportedSchemaKeys = map[string]bool{
	"type":                 true,
	"properties":           true,
	"required":             true,
	"items":                true,
	"enum":                 true,
	"minItems":             true,
	"maxItems":             true,
	"additionalProperties": true,
}

func ParseAndValidateString(raw string, schema map[string]any) (any, error) {
	var value any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &value); err != nil {
		extracted, extractErr := ExtractJSONValue(raw)
		if extractErr != nil {
			return nil, fmt.Errorf("agent output does not match schema: final response must be valid JSON: %w", err)
		}
		if err := json.Unmarshal([]byte(extracted), &value); err != nil {
			return nil, fmt.Errorf("agent output does not match schema: final response must contain valid JSON: %w", err)
		}
	}
	if err := ValidateSchema(schema); err != nil {
		return nil, err
	}
	if err := ValidateValue(value, schema); err != nil {
		return nil, fmt.Errorf("agent output does not match schema: %w", err)
	}
	return value, nil
}

func ValidateSchema(schema map[string]any) error {
	return validateSchemaNode(schema, "$schema")
}

func ValidateValue(value any, schema map[string]any) error {
	if err := ValidateSchema(schema); err != nil {
		return err
	}
	return validateJSONValue(value, schema, "$")
}

func ExtractJSONValue(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("empty output")
	}
	for start, r := range text {
		if r != '{' && r != '[' {
			continue
		}
		end, ok := balancedJSONEnd(text[start:])
		if !ok {
			continue
		}
		candidate := text[start : start+end]
		var value any
		if err := json.Unmarshal([]byte(candidate), &value); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no valid JSON object or array found")
}

func balancedJSONEnd(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	open := text[0]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return 0, false
	}
	stack := []byte{close}
	inString := false
	escaped := false
	for i := 1; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func validateSchemaNode(schema map[string]any, path string) error {
	if len(schema) == 0 {
		return fmt.Errorf("%s must be a non-empty schema object", path)
	}
	for key := range schema {
		if !supportedSchemaKeys[key] {
			return fmt.Errorf("unsupported schema keyword %q at %s", key, path)
		}
	}
	typ, ok := schemaTypeName(schema)
	if !ok {
		return fmt.Errorf("%s.type must be one of object, array, string, number, integer, boolean, null", path)
	}
	if props, ok := schema["properties"]; ok {
		propsMap, ok := props.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties must be an object", path)
		}
		for name, prop := range propsMap {
			propSchema, ok := prop.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be an object", path, name)
			}
			if err := validateSchemaNode(propSchema, path+".properties."+name); err != nil {
				return err
			}
		}
	}
	if req, ok := schema["required"]; ok {
		items, ok := req.([]any)
		if !ok {
			return fmt.Errorf("%s.required must be an array of strings", path)
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("%s.required must be an array of strings", path)
			}
		}
	}
	if items, ok := schema["items"]; ok {
		itemSchema, ok := items.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be an object", path)
		}
		if err := validateSchemaNode(itemSchema, path+".items"); err != nil {
			return err
		}
	}
	if enum, ok := schema["enum"]; ok {
		if _, ok := enum.([]any); !ok {
			return fmt.Errorf("%s.enum must be an array", path)
		}
	}
	if minItems, ok := schema["minItems"]; ok {
		if _, ok := nonNegativeInteger(minItems); !ok {
			return fmt.Errorf("%s.minItems must be a non-negative integer", path)
		}
		if typ != "array" {
			return fmt.Errorf("%s.minItems requires array type", path)
		}
	}
	if maxItems, ok := schema["maxItems"]; ok {
		if _, ok := nonNegativeInteger(maxItems); !ok {
			return fmt.Errorf("%s.maxItems must be a non-negative integer", path)
		}
		if typ != "array" {
			return fmt.Errorf("%s.maxItems requires array type", path)
		}
	}
	if additional, ok := schema["additionalProperties"]; ok {
		if _, ok := additional.(bool); !ok {
			return fmt.Errorf("%s.additionalProperties must be a boolean", path)
		}
	}
	return nil
}

func validateJSONValue(value any, schema map[string]any, path string) error {
	typ, _ := schemaTypeName(schema)
	if !matchesSchemaType(value, typ) {
		return fmt.Errorf("%s must be %s", path, typ)
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, allowed := range enum {
			if reflect.DeepEqual(value, allowed) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s must match one of the enum values", path)
		}
	}
	switch typ {
	case "object":
		obj, _ := value.(map[string]any)
		if err := validateRequiredProperties(obj, schema, path); err != nil {
			return err
		}
		props, _ := schema["properties"].(map[string]any)
		for key, propSchemaAny := range props {
			if child, ok := obj[key]; ok {
				propSchema, _ := propSchemaAny.(map[string]any)
				if err := validateJSONValue(child, propSchema, path+"."+key); err != nil {
					return err
				}
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for key := range obj {
				if _, ok := props[key]; !ok {
					return fmt.Errorf("%s.%s is not allowed by additionalProperties=false", path, key)
				}
			}
		}
	case "array":
		items, ok := schema["items"].(map[string]any)
		arr, _ := value.([]any)
		if minItems, ok := nonNegativeInteger(schema["minItems"]); ok && len(arr) < minItems {
			return fmt.Errorf("%s must contain at least %d items", path, minItems)
		}
		if maxItems, ok := nonNegativeInteger(schema["maxItems"]); ok && len(arr) > maxItems {
			return fmt.Errorf("%s must contain at most %d items", path, maxItems)
		}
		if ok {
			for i, item := range arr {
				if err := validateJSONValue(item, items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateRequiredProperties(obj map[string]any, schema map[string]any, path string) error {
	req, _ := schema["required"].([]any)
	for _, item := range req {
		name, _ := item.(string)
		if _, ok := obj[name]; !ok {
			return fmt.Errorf("%s.%s is required", path, name)
		}
	}
	return nil
}

func schemaTypeName(schema map[string]any) (string, bool) {
	raw, ok := schema["type"].(string)
	if !ok {
		switch {
		case schema["properties"] != nil:
			raw = "object"
		case schema["items"] != nil || schema["minItems"] != nil || schema["maxItems"] != nil:
			raw = "array"
		case schema["enum"] != nil:
			return "", true
		default:
			return "", false
		}
	}
	switch raw {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return raw, true
	default:
		return raw, false
	}
}

func nonNegativeInteger(value any) (int, bool) {
	switch n := value.(type) {
	case float64:
		if math.Trunc(n) != n || n < 0 {
			return 0, false
		}
		return int(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return n, true
	case int64:
		if n < 0 {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func matchesSchemaType(value any, typ string) bool {
	if typ == "" {
		return true
	}
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}
