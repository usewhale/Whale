package tools

import (
	"context"
	"os"
	"sort"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) listDir(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	abs, err := b.safeReadPath(in.Path)
	if err != nil {
		return b.marshalReadPathError(call, in.Path, err), nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return b.marshalPathNotFound(call, in.Path, abs, err.Error()), nil
		}
		return marshalToolError(call, "read_failed", err.Error()), nil
	}
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		items = append(items, n)
	}
	sort.Strings(items)
	return marshalToolResult(call, map[string]any{"items": items})
}
