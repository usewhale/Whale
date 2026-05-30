package app

import (
	"github.com/usewhale/whale/internal/plugins"
)

func (a *App) PluginStatuses() []plugins.PluginStatus {
	if a == nil || a.pluginManager == nil {
		return nil
	}
	return a.pluginManager.Statuses()
}
