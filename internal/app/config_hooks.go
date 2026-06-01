package app

import (
	"github.com/usewhale/whale/internal/agent"
	"strings"
)

func hooksFromFileConfig(cfg FileConfig) agent.HookSettings {
	out := agent.HookSettings{Hooks: map[agent.HookEvent][]agent.HookConfig{}}
	for raw, hooks := range cfg.Hooks {
		ev := agent.HookEvent(strings.TrimSpace(raw))
		if agent.KnownHookEvent(ev) {
			out.Hooks[ev] = append(out.Hooks[ev], hooks...)
		}
	}
	return out
}

func countFileConfigHooks(cfg FileConfig) int {
	return countHooks(hooksFromFileConfig(cfg))
}
