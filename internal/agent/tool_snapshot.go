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

func (a *Agent) refreshToolSnapshotForTurn(ctx context.Context, opts RunOptions) (*core.ToolRegistry, error) {
	snapshot, err := a.refreshToolSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return filterToolSnapshotForTurn(snapshot, opts)
}

func filterToolSnapshotForTurn(snapshot *core.ToolRegistry, opts RunOptions) (*core.ToolRegistry, error) {
	if !opts.WorkflowAuthoring {
		return snapshot, nil
	}
	if snapshot == nil {
		return core.NewToolRegistry(nil), nil
	}
	workflowTool := snapshot.Get("workflow")
	if workflowTool == nil {
		return core.NewToolRegistry(nil), nil
	}
	return core.NewToolRegistryChecked([]core.Tool{workflowTool})
}
