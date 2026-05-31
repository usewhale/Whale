package tools

import (
	"context"

	"github.com/usewhale/whale/internal/core"
)

type toolFn struct {
	name          string
	description   string
	parameters    map[string]any
	readOnly      bool
	readOnlyCheck func(args map[string]any) bool
	capabilities  []string
	fn            func(context.Context, core.ToolCall) (core.ToolResult, error)
	preview       func(context.Context, core.ToolCall) (map[string]any, error)
}

func (t toolFn) Name() string               { return t.name }
func (t toolFn) Description() string        { return t.description }
func (t toolFn) Parameters() map[string]any { return t.parameters }
func (t toolFn) ReadOnly() bool             { return t.readOnly }
func (t toolFn) Capabilities() []string     { return append([]string(nil), t.capabilities...) }
func (t toolFn) ReadOnlyCheck(args map[string]any) bool {
	if t.readOnlyCheck == nil {
		return t.readOnly
	}
	return t.readOnlyCheck(args)
}
func (t toolFn) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return t.fn(ctx, call)
}

func (t toolFn) Preview(ctx context.Context, call core.ToolCall) (map[string]any, error) {
	if t.preview == nil {
		return nil, nil
	}
	return t.preview(ctx, call)
}
