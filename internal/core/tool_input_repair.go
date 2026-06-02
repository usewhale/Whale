package core

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	ToolInputRepairNullOptionalOmitted  = "null_optional_omitted"
	ToolInputRepairStringifiedArray     = "stringified_array"
	ToolInputRepairBareStringToArray    = "bare_string_to_array"
	ToolInputRepairEmptyObjectToArray   = "empty_object_to_array"
	ToolInputRepairMarkdownAutolinkPath = "markdown_autolink_path"
	ToolInputRepairSemanticBoolean      = "semantic_boolean_string"
	ToolInputRepairSemanticInteger      = "semantic_integer_string"
)

type ToolInputRepair struct {
	Kind       string
	Path       string
	BeforeType string
	AfterType  string
}

type toolInputIssue struct {
	path       string
	expected   string
	schema     map[string]any
	required   bool
	knownField bool
	parent     map[string]any
	key        string
}

// RepairToolInputForSpec applies narrowly scoped, schema-guided repairs to
// common tool-call argument shape mistakes. Valid inputs are returned unchanged.
func RepairToolInputForSpec(spec ToolSpec, raw string) (string, []ToolInputRepair) {
	if strings.TrimSpace(raw) == "" || spec.Parameters == nil {
		return raw, nil
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return raw, nil
	}
	repairs := collectPathStringRepairs(spec.Parameters, in, "")
	issues := collectToolInputIssues(spec.Parameters, in, "", true)
	if len(issues) == 0 && len(repairs) == 0 {
		return raw, nil
	}
	for _, issue := range issues {
		repair, ok := applyToolInputIssueRepair(issue)
		if ok {
			repairs = append(repairs, repair)
		}
	}
	if len(repairs) == 0 {
		return raw, nil
	}
	if issues := collectToolInputIssues(spec.Parameters, in, "", true); len(issues) > 0 {
		return raw, nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return raw, nil
	}
	return string(b), repairs
}

func collectPathStringRepairs(schema map[string]any, value any, path string) []ToolInputRepair {
	switch schemaType(schema) {
	case "object", "":
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		props, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for key := range props {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var out []ToolInputRepair
		for _, key := range keys {
			childSchema, ok := props[key].(map[string]any)
			if !ok {
				continue
			}
			childValue, present := obj[key]
			if !present {
				continue
			}
			childPath := joinToolInputPath(path, key)
			if schemaType(childSchema) == "string" && isPathStringField(key) {
				if s, ok := childValue.(string); ok {
					if fixed, changed := unwrapMarkdownAutolinkPath(s); changed {
						obj[key] = fixed
						out = append(out, ToolInputRepair{
							Kind:       ToolInputRepairMarkdownAutolinkPath,
							Path:       childPath,
							BeforeType: "string",
							AfterType:  "string",
						})
					}
				}
				continue
			}
			out = append(out, collectPathStringRepairs(childSchema, childValue, childPath)...)
		}
		return out
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return nil
		}
		itemSchema, _ := schema["items"].(map[string]any)
		if itemSchema == nil {
			return nil
		}
		var out []ToolInputRepair
		for i, item := range arr {
			itemPath := path + "[" + strconv.Itoa(i) + "]"
			out = append(out, collectPathStringRepairs(itemSchema, item, itemPath)...)
		}
		return out
	default:
		return nil
	}
}

func collectToolInputIssues(schema map[string]any, value any, path string, required bool) []toolInputIssue {
	expected := schemaType(schema)
	if expected == "" {
		if _, ok := schema["properties"].(map[string]any); ok {
			expected = "object"
		}
	}
	if value == nil {
		if typeAllowsNull(schema) {
			return nil
		}
		return []toolInputIssue{{
			path:       path,
			expected:   expected,
			schema:     schema,
			required:   required,
			knownField: path != "",
		}}
	}
	switch expected {
	case "":
		return nil
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return []toolInputIssue{{
				path:       path,
				expected:   expected,
				schema:     schema,
				required:   required,
				knownField: path != "",
			}}
		}
		return collectObjectToolInputIssues(schema, obj, path)
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return []toolInputIssue{{
				path:       path,
				expected:   expected,
				schema:     schema,
				required:   required,
				knownField: path != "",
			}}
		}
		var out []toolInputIssue
		if min, ok := schemaMinItems(schema); ok && len(arr) < min {
			out = append(out, toolInputIssue{
				path:       path,
				expected:   "array",
				schema:     schema,
				required:   required,
				knownField: path != "",
			})
		}
		itemSchema, _ := schema["items"].(map[string]any)
		if itemSchema == nil {
			return out
		}
		for i, item := range arr {
			itemPath := path + "[" + strconv.Itoa(i) + "]"
			out = append(out, collectToolInputIssues(itemSchema, item, itemPath, true)...)
		}
		return out
	case "string":
		if _, ok := value.(string); ok {
			return nil
		}
	case "integer":
		if isJSONInteger(value) {
			return nil
		}
	case "number":
		if _, ok := value.(float64); ok {
			return nil
		}
	case "boolean":
		if _, ok := value.(bool); ok {
			return nil
		}
	default:
		return nil
	}
	return []toolInputIssue{{
		path:       path,
		expected:   expected,
		schema:     schema,
		required:   required,
		knownField: path != "",
	}}
}

func collectObjectToolInputIssues(schema map[string]any, obj map[string]any, path string) []toolInputIssue {
	props, _ := schema["properties"].(map[string]any)
	reqSet := requiredSet(schema["required"])
	var out []toolInputIssue
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		childSchema, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		value, present := obj[key]
		childPath := joinToolInputPath(path, key)
		if !present {
			if reqSet[key] {
				out = append(out, toolInputIssue{
					path:       childPath,
					expected:   schemaType(childSchema),
					schema:     childSchema,
					required:   true,
					knownField: true,
					parent:     obj,
					key:        key,
				})
			}
			continue
		}
		issues := collectToolInputIssues(childSchema, value, childPath, reqSet[key])
		for i := range issues {
			issues[i].knownField = true
			if issues[i].parent == nil {
				issues[i].parent = obj
				issues[i].key = key
			}
		}
		out = append(out, issues...)
	}
	if ap, ok := schema["additionalProperties"].(bool); ok && !ap {
		for key := range obj {
			if _, ok := props[key]; ok {
				continue
			}
			out = append(out, toolInputIssue{
				path:       joinToolInputPath(path, key),
				expected:   "none",
				required:   false,
				knownField: false,
				parent:     obj,
				key:        key,
			})
		}
	}
	return out
}

func applyToolInputIssueRepair(issue toolInputIssue) (ToolInputRepair, bool) {
	if !issue.knownField || issue.parent == nil || issue.key == "" {
		return ToolInputRepair{}, false
	}
	value, present := issue.parent[issue.key]
	if !present {
		return ToolInputRepair{}, false
	}
	if value == nil && !issue.required {
		delete(issue.parent, issue.key)
		return ToolInputRepair{
			Kind:       ToolInputRepairNullOptionalOmitted,
			Path:       issue.path,
			BeforeType: "null",
			AfterType:  "omitted",
		}, true
	}
	if s, ok := value.(string); ok {
		switch issue.expected {
		case "boolean":
			switch s {
			case "true":
				issue.parent[issue.key] = true
			case "false":
				issue.parent[issue.key] = false
			default:
				return ToolInputRepair{}, false
			}
			return ToolInputRepair{
				Kind:       ToolInputRepairSemanticBoolean,
				Path:       issue.path,
				BeforeType: "string",
				AfterType:  "boolean",
			}, true
		case "integer":
			if !isDecimalIntegerLiteral(s) {
				return ToolInputRepair{}, false
			}
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return ToolInputRepair{}, false
			}
			issue.parent[issue.key] = float64(n)
			return ToolInputRepair{
				Kind:       ToolInputRepairSemanticInteger,
				Path:       issue.path,
				BeforeType: "string",
				AfterType:  "number",
			}, true
		}
	}
	if issue.expected != "array" {
		return ToolInputRepair{}, false
	}
	switch v := value.(type) {
	case string:
		var arr []any
		if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &arr); err == nil {
			issue.parent[issue.key] = arr
			return ToolInputRepair{
				Kind:       ToolInputRepairStringifiedArray,
				Path:       issue.path,
				BeforeType: "string",
				AfterType:  "array",
			}, true
		}
		if schemaArrayItemsType(issue.schema) != "string" {
			return ToolInputRepair{}, false
		}
		issue.parent[issue.key] = []any{v}
		return ToolInputRepair{
			Kind:       ToolInputRepairBareStringToArray,
			Path:       issue.path,
			BeforeType: "string",
			AfterType:  "array",
		}, true
	case map[string]any:
		if len(v) != 0 {
			return ToolInputRepair{}, false
		}
		issue.parent[issue.key] = []any{}
		return ToolInputRepair{
			Kind:       ToolInputRepairEmptyObjectToArray,
			Path:       issue.path,
			BeforeType: "object",
			AfterType:  "array",
		}, true
	default:
		return ToolInputRepair{}, false
	}
}

func isDecimalIntegerLiteral(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' {
		if len(s) == 1 {
			return false
		}
		start = 1
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func schemaType(schema map[string]any) string {
	switch v := schema["type"].(type) {
	case string:
		return v
	case []any:
		for _, it := range v {
			if s, ok := it.(string); ok && s != "null" {
				return s
			}
		}
	case []string:
		for _, s := range v {
			if s != "null" {
				return s
			}
		}
	}
	return ""
}

func typeAllowsNull(schema map[string]any) bool {
	switch v := schema["type"].(type) {
	case string:
		return v == "null"
	case []any:
		for _, it := range v {
			if s, ok := it.(string); ok && s == "null" {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == "null" {
				return true
			}
		}
	}
	return false
}

func schemaArrayItemsType(schema map[string]any) string {
	items, _ := schema["items"].(map[string]any)
	if items == nil {
		return ""
	}
	return schemaType(items)
}

func schemaMinItems(schema map[string]any) (int, bool) {
	switch v := schema["minItems"].(type) {
	case int:
		return v, true
	case float64:
		if v >= 0 && math.Trunc(v) == v {
			return int(v), true
		}
	}
	return 0, false
}

func isJSONInteger(value any) bool {
	switch v := value.(type) {
	case float64:
		return math.Trunc(v) == v
	case int:
		return true
	default:
		return false
	}
}

func isPathStringField(key string) bool {
	switch strings.TrimSpace(key) {
	case "file_path", "path", "cwd", "directory":
		return true
	default:
		return false
	}
}

func unwrapMarkdownAutolinkPath(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	start := strings.Index(trimmed, "[")
	if start < 0 {
		return "", false
	}
	prefix := trimmed[:start]
	if prefix != "" && !strings.HasSuffix(prefix, "/") && !strings.HasSuffix(prefix, "\\") {
		return "", false
	}
	rest := trimmed[start:]
	endText := strings.Index(rest, "]")
	if endText <= 1 || endText+1 >= len(rest) || rest[endText+1] != '(' || !strings.HasSuffix(rest, ")") {
		return "", false
	}
	text := rest[1:endText]
	url := rest[endText+2 : len(rest)-1]
	target, ok := stripHTTPProtocol(url)
	if !ok {
		return "", false
	}
	replacement := prefix + text
	normalizedTarget := normalizeMarkdownPathTarget(target)
	if normalizedTarget != normalizeMarkdownPathTarget(text) && normalizedTarget != normalizeMarkdownPathTarget(replacement) {
		return "", false
	}
	if replacement == value {
		return "", false
	}
	return replacement, true
}

func stripHTTPProtocol(value string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"http://", "https://"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix)), true
		}
	}
	return "", false
}

func normalizeMarkdownPathTarget(value string) string {
	return strings.TrimSpace(value)
}

func joinToolInputPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
