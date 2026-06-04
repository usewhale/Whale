package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
		if ev.Complete {
			a.freezeMCPToolSignature()
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

	mcpTools := a.mcpManager.Tools()
	if err := a.guardMCPToolSignatureLocked(mcpTools); err != nil {
		return err
	}
	base := append([]core.Tool{}, a.baseTools...)
	base = append(base, mcpTools...)
	if err := a.baseToolRegistry.ReplaceTools(base); err != nil {
		return err
	}
	subagent := append([]core.Tool{}, base...)
	subagent = append(subagent, a.pluginTools...)
	if a.subagentToolRegistry != nil {
		if err := a.subagentToolRegistry.ReplaceTools(subagent); err != nil {
			return err
		}
	}
	full := append([]core.Tool{}, subagent...)
	full = append(full, a.taskTools...)
	full = append(full, a.goalTools...)
	full = append(full, a.workflowTools...)
	return a.toolRegistry.ReplaceTools(full)
}

func (a *App) freezeMCPToolSignature() {
	if a == nil {
		return
	}
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	a.mcpSigFrozen = true
}

func (a *App) guardMCPToolSignatureLocked(tools []core.Tool) error {
	next, payloads, err := mcpToolSetSnapshot(tools)
	if err != nil {
		return err
	}
	if a.mcpSig == "" {
		a.mcpSig = next
		a.mcpToolPayloads = payloads
		return nil
	}
	if next == a.mcpSig {
		return nil
	}
	if !a.mcpSigFrozen {
		a.mcpSig = next
		a.mcpToolPayloads = payloads
		return nil
	}
	return fmt.Errorf("MCP tool set changed after startup: %s; restart Whale to apply updated MCP tools without changing the active provider prefix", mcpToolSetDelta(a.mcpToolPayloads, payloads))
}

func mcpToolSetSignature(tools []core.Tool) (string, error) {
	sig, _, err := mcpToolSetSnapshot(tools)
	return sig, err
}

func mcpToolSetSnapshot(tools []core.Tool) (string, map[string]string, error) {
	payloads := make([]map[string]any, 0, len(tools))
	byName := make(map[string]string, len(tools))
	for _, tool := range tools {
		payload := core.ProviderToolPayload(tool)
		payloads = append(payloads, payload)
		b, err := json.Marshal(payload)
		if err != nil {
			return "", nil, fmt.Errorf("hash mcp tool %s: %w", tool.Name(), err)
		}
		byName[tool.Name()] = string(b)
	}
	b, err := json.Marshal(payloads)
	if err != nil {
		return "", nil, fmt.Errorf("hash mcp tool set: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), byName, nil
}

func mcpToolSetDelta(prev, next map[string]string) string {
	var added, removed, changed []string
	for name, payload := range next {
		if prevPayload, ok := prev[name]; !ok {
			added = append(added, name)
		} else if prevPayload != payload {
			changed = append(changed, name)
		}
	}
	for name := range prev {
		if _, ok := next[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(changed) > 0 {
		parts = append(parts, "changed "+strings.Join(changed, ", "))
	}
	if len(parts) == 0 {
		return "tool order changed"
	}
	return strings.Join(parts, "; ")
}

func (a *App) MCPStates() []whalemcp.ServerState {
	if a == nil || a.mcpManager == nil {
		return nil
	}
	return a.mcpManager.States()
}
