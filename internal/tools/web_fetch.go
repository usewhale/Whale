package tools

import (
	"context"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) webFetch(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return b.runWebFetch(ctx, call)
}
