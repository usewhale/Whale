package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// requestUserInputSpec mirrors the parameters declared in
// internal/tools/catalog_runtime.go for the request_user_input tool.
func requestUserInputSpec() core.ToolSpec {
	return core.ToolSpec{
		Name: "request_user_input",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"questions": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 3,
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"id":       map[string]any{"type": "string"},
							"header":   map[string]any{"type": "string"},
							"question": map[string]any{"type": "string"},
							"options": map[string]any{
								"type":     "array",
								"minItems": 2,
								"maxItems": 3,
								"items": map[string]any{
									"type":                 "object",
									"additionalProperties": false,
									"properties": map[string]any{
										"label":       map[string]any{"type": "string"},
										"description": map[string]any{"type": "string"},
									},
									"required": []string{"label", "description"},
								},
							},
						},
						"required": []string{"id", "header", "question", "options"},
					},
				},
			},
			"required": []string{"questions"},
		},
	}
}

// TestReproInvalidRequestUserInput feeds malformed shapes that DeepSeek-class
// models emit through the SAME path the agent runtime uses:
//  1. core.RepairToolInputForSpec  (repairDispatchInput)
//  2. json.Unmarshal into core.UserInputRequest (handleRequestUserInput)
//
// Any case where the final unmarshal still fails reproduces the
// "invalid request_user_input input" error.
func TestReproInvalidRequestUserInput(t *testing.T) {
	spec := requestUserInputSpec()

	cases := []struct {
		name string
		raw  string
	}{
		{
			// Whole questions array stringified — repairable in isolation.
			"stringified_questions_array",
			`{"questions":"[{\"id\":\"a\",\"header\":\"H\",\"question\":\"Q\",\"options\":[{\"label\":\"L1\",\"description\":\"D1\"},{\"label\":\"L2\",\"description\":\"D2\"}]}]"}`,
		},
		{
			// options stringified inside an otherwise-valid question.
			"stringified_options_array",
			`{"questions":[{"id":"a","header":"H","question":"Q","options":"[{\"label\":\"L1\",\"description\":\"D1\"},{\"label\":\"L2\",\"description\":\"D2\"}]"}]}`,
		},
		{
			// An option given as a bare string instead of {label,description}.
			"option_as_bare_string",
			`{"questions":[{"id":"a","header":"H","question":"Q","options":["just yes","just no"]}]}`,
		},
		{
			// question text given as an object (model nested a struct).
			"question_field_as_object",
			`{"questions":[{"id":"a","header":"H","question":{"text":"Q"},"options":[{"label":"L1","description":"D1"},{"label":"L2","description":"D2"}]}]}`,
		},
		{
			// Mixed: stringified options AND a non-string header → all-or-nothing.
			"stringified_options_plus_bad_header",
			`{"questions":[{"id":"a","header":123,"question":"Q","options":"[{\"label\":\"L1\",\"description\":\"D1\"},{\"label\":\"L2\",\"description\":\"D2\"}]"}]}`,
		},
		{
			// REAL capture from session 019ee483: DeepSeek wrote unescaped inner
			// double-quotes inside a description value, so the whole arguments
			// string is syntactically invalid JSON.
			"real_unescaped_inner_quotes",
			`{"questions": [{"id": "default_option", "header": "更新提示默认选项", "question": "你想把默认高亮改成哪个选项？", "options": [{"label": "Update now（第一个）", "description": "默认选中"立即更新"，Enter 直接触发更新"}, {"label": "保持 Skip（当前）", "description": "默认选中"跳过"，Enter 跳过更新继续使用"}]}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixed, repairs := core.RepairToolInputForSpec(spec, tc.raw)

			var in core.UserInputRequest
			unmarshalErr := json.Unmarshal([]byte(fixed), &in)

			validateErr := error(nil)
			if unmarshalErr == nil {
				validateErr = validateUserInputRequest(in)
			}

			switch {
			case unmarshalErr != nil:
				// Drive the REAL handler so this test guards the production code
				// path: a malformed payload must come back with the concrete parse
				// reason, otherwise the model retries blind. Constructing the
				// expected string locally would let a handler regression pass.
				a := &Agent{}
				events := make(chan AgentEvent, 4)
				res, err := a.handleRequestUserInput(
					context.Background(),
					core.ToolCall{ID: "repro", Name: "request_user_input", Input: fixed},
					"repro-session",
					events,
				)
				if err != nil {
					t.Fatalf("handler returned error: %v", err)
				}
				if res.Code != "invalid_request_user_input" {
					t.Fatalf("expected code invalid_request_user_input, got %q (text=%s)", res.Code, res.ModelText)
				}
				if !strings.Contains(res.ModelText, "failed to parse request_user_input arguments as JSON") {
					t.Fatalf("handler output lost the parse detail: %s", res.ModelText)
				}
				if !strings.Contains(res.ModelText, unmarshalErr.Error()) {
					t.Fatalf("handler output dropped the concrete serde reason %q: %s", unmarshalErr.Error(), res.ModelText)
				}
				t.Logf("REPRO ❌ unmarshal failed -> handler returned informed message | repairs=%d detail=%q", len(repairs), unmarshalErr.Error())
			case validateErr != nil:
				t.Logf("validation failed (different error): %v | repairs=%d", validateErr, len(repairs))
			default:
				t.Logf("OK ✅ repaired & valid | repairs=%d", len(repairs))
			}
		})
	}
}
