package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/jsonschema"
)

const structuredOutputToolName = "structured_output"

type structuredOutputCapture struct {
	mu      sync.Mutex
	called  bool
	value   any
	lastErr string
}

func (c *structuredOutputCapture) set(value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = true
	c.value = value
	c.lastErr = ""
}

func (c *structuredOutputCapture) get() (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, c.called
}

func (c *structuredOutputCapture) setError(err error) {
	if c == nil || err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr = err.Error()
}

func (c *structuredOutputCapture) error() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

type structuredOutputTool struct {
	schema  map[string]any
	capture *structuredOutputCapture
}

func (t structuredOutputTool) Name() string { return structuredOutputToolName }

func (t structuredOutputTool) Description() string {
	return "Submit the final structured result for this subagent task. Use this exactly once when the task provides an output schema."
}

func (t structuredOutputTool) Parameters() map[string]any {
	return cloneSchemaMap(t.schema)
}

func (t structuredOutputTool) ReadOnly() bool { return true }

func (t structuredOutputTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var value any
	if err := json.Unmarshal([]byte(call.Input), &value); err != nil {
		if t.capture != nil {
			t.capture.setError(err)
		}
		return marshalError(call, "invalid_json", fmt.Sprintf("structured output input must be valid JSON: %v", err))
	}
	if err := jsonschema.ValidateValue(value, t.schema); err != nil {
		if t.capture != nil {
			t.capture.setError(err)
		}
		return marshalError(call, "schema_mismatch", err.Error())
	}
	if t.capture != nil {
		t.capture.set(value)
	}
	return marshalSuccess(call, map[string]any{
		"accepted": true,
		"message":  "structured output captured",
	})
}

func cloneSchemaMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return in
	}
	return out
}
