package tools

import (
	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) Tools() []core.Tool {
	tools := []core.Tool{}
	tools = append(tools, b.fileDiscoveryTools()...)
	tools = append(tools, b.searchTools()...)
	tools = append(tools, b.webTools()...)
	tools = append(tools, b.requestInputTools()...)
	tools = append(tools, b.fileMutationTools()...)
	tools = append(tools, b.shellTools()...)
	tools = append(tools, b.planRuntimeTools()...)
	tools = append(tools, b.todoRuntimeTools()...)
	return tools
}
