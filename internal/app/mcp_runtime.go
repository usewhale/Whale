package app

import (
	"context"

	"github.com/usewhale/whale/internal/core"
	whalemcp "github.com/usewhale/whale/internal/mcp"
)

func (a *App) InitializeMCP(ctx context.Context, emit func(whalemcp.StartupEvent)) {
	if a == nil || a.mcpManager == nil {
		return
	}
	a.mcpInitMu.Lock()
	if a.mcpInitStarted {
		a.mcpInitMu.Unlock()
		return
	}
	a.mcpInitStarted = true
	a.mcpInitMu.Unlock()

	a.mcpManager.InitializeWithEvents(ctx, func(ev whalemcp.StartupEvent) {
		if ev.State.Connected || ev.Complete {
			_ = a.refreshMCPTools()
		}
		if emit != nil {
			emit(ev)
		}
	})
}

func (a *App) refreshMCPTools() error {
	if a == nil {
		return nil
	}
	a.toolMu.Lock()
	defer a.toolMu.Unlock()

	base := append([]core.Tool{}, a.baseTools...)
	base = append(base, a.mcpManager.Tools()...)
	if err := a.baseToolRegistry.ReplaceTools(base); err != nil {
		return err
	}
	full := append([]core.Tool{}, base...)
	full = append(full, a.pluginTools...)
	full = append(full, a.taskTools...)
	full = append(full, a.goalTools...)
	full = append(full, a.workflowTools...)
	return a.toolRegistry.ReplaceTools(full)
}

func (a *App) MCPStates() []whalemcp.ServerState {
	if a == nil || a.mcpManager == nil {
		return nil
	}
	return a.mcpManager.States()
}
