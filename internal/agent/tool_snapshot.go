package agent

import (
	"context"
	"fmt"

	"github.com/usewhale/whale/internal/core"
)

func (a *Agent) refreshToolSnapshot(ctx context.Context) (*core.ToolRegistry, error) {
	if a == nil {
		return core.NewToolRegistry(nil), nil
	}
	if a.toolRefresh != nil {
		if err := a.toolRefresh(ctx); err != nil {
			return nil, fmt.Errorf("refresh tools: %w", err)
		}
	}
	return a.tools.Snapshot(), nil
}
