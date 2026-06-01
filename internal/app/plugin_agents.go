package app

import (
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/tasks"
)

func taskAgentDefinitions(in []plugins.AgentDefinition) []tasks.AgentDefinition {
	if len(in) == 0 {
		return nil
	}
	out := make([]tasks.AgentDefinition, 0, len(in))
	for _, agent := range in {
		tools := append([]string(nil), agent.Capabilities...)
		tools = append(tools, agent.AllowedTools...)
		out = append(out, tasks.AgentDefinition{
			Name:            agent.Name,
			Description:     agent.Description,
			Prompt:          agent.SystemPrompt,
			Model:           agent.Model,
			Effort:          agent.Effort,
			MaxToolIters:    agent.MaxToolIters,
			MaxToolCalls:    agent.MaxToolCalls,
			Tools:           tools,
			DisallowedTools: append([]string(nil), agent.DisallowedTools...),
		})
	}
	return out
}
