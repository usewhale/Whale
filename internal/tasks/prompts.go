package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
)

func agentDefinitionSystemBlock(def AgentDefinition, requestedTools, resolvedTools []string, toolMode string) string {
	name := strings.TrimSpace(def.Name)
	if name == "" {
		name = "explore"
	}
	var b strings.Builder
	b.WriteString("You are a Whale child agent.\n\n")
	b.WriteString("Agent definition:\n")
	b.WriteString("- name: ")
	b.WriteString(name)
	b.WriteString("\n")
	if desc := strings.TrimSpace(def.Description); desc != "" {
		b.WriteString("- description: ")
		b.WriteString(desc)
		b.WriteString("\n")
	}
	if when := strings.TrimSpace(def.WhenToUse); when != "" {
		b.WriteString("- whenToUse: ")
		b.WriteString(when)
		b.WriteString("\n")
	}
	if prompt := strings.TrimSpace(def.Prompt); prompt != "" {
		b.WriteString("\nAgent system prompt:\n")
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	if len(requestedTools) > 0 {
		b.WriteString("- requestedTools: ")
		b.WriteString(strings.Join(requestedTools, ", "))
		b.WriteString("\n")
	} else {
		b.WriteString("- requestedTools: []\n")
	}
	if len(resolvedTools) > 0 {
		b.WriteString("- resolvedTools: ")
		b.WriteString(strings.Join(resolvedTools, ", "))
		b.WriteString("\n")
	} else {
		b.WriteString("- resolvedTools: none\n")
	}
	if mode := strings.TrimSpace(toolMode); mode != "" {
		b.WriteString("- toolMode: ")
		b.WriteString(mode)
		b.WriteString("\n")
	}
	if effort := strings.TrimSpace(def.Effort); effort != "" {
		b.WriteString("- effort: ")
		b.WriteString(effort)
		b.WriteString("\n")
	}
	if mode := strings.TrimSpace(def.PermissionMode); mode != "" {
		b.WriteString("- permissionMode: ")
		b.WriteString(mode)
		b.WriteString("\n")
	}
	if len(def.MCPServers) > 0 {
		b.WriteString("- mcpServers: ")
		b.WriteString(strings.Join(def.MCPServers, ", "))
		b.WriteString("\n")
	}
	b.WriteString("\nInstructions:\n")
	b.WriteString("- Complete only the assigned task and return a concise final summary.\n")
	b.WriteString("- Use only the tools that are actually available in this child agent session.\n")
	if strings.TrimSpace(toolMode) == "model_only" {
		b.WriteString("- No tools are available. Answer directly from the prompt and your model knowledge only.\n")
		b.WriteString("- Do not write pseudo tool calls, shell commands, XML/DSML tool-call markup, or text that implies a tool was executed.\n")
	}
	b.WriteString("- Do not request user input or spawn more agents.\n")
	b.WriteString("- Do not modify files unless this agent definition explicitly includes an editing capability and the runtime exposes editing tools.\n")
	b.WriteString("- If required evidence cannot be gathered with the available tools, say exactly what is missing and which capability or command the parent should provide.\n")
	return strings.TrimSpace(b.String())
}

func outputSchemaSystemBlock(schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(`
This task requires structured output.

- Your final structured result must be submitted by calling the ` + structuredOutputToolName + ` tool.
- Call ` + structuredOutputToolName + ` exactly once after any research or tool use is complete.
- Do not use a prose or Markdown final answer as a substitute for this tool call.
- The tool input must match this JSON Schema exactly.

Schema:
` + string(b))
}

func preloadedSkillsSystemBlock(workspaceRoot string, names, disabled []string, extra []*skills.Skill) string {
	if len(names) == 0 {
		return ""
	}
	roots := skills.DefaultRoots(workspaceRoot)
	var b strings.Builder
	b.WriteString("Preloaded agent skills:\n\n")
	loaded := 0
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if core.SkillNameDisabled(name, disabled) {
			b.WriteString("- ")
			b.WriteString(name)
			b.WriteString(": disabled; instructions were not loaded.\n")
			continue
		}
		skill, ok := findAgentSkill(roots, name, disabled, extra)
		if !ok {
			b.WriteString("- ")
			b.WriteString(name)
			b.WriteString(": not found; instructions were not loaded.\n")
			continue
		}
		loaded++
		b.WriteString("## ")
		b.WriteString(skill.Name)
		if strings.TrimSpace(skill.Description) != "" {
			b.WriteString("\nDescription: ")
			b.WriteString(strings.TrimSpace(skill.Description))
		}
		if strings.TrimSpace(skill.SkillFilePath) != "" {
			b.WriteString("\nFile: ")
			b.WriteString(strings.TrimSpace(skill.SkillFilePath))
		}
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(skill.Instructions))
		b.WriteString("\n\n")
	}
	if loaded == 0 {
		b.WriteString("\nNo requested skills were loaded. Continue with the available task context and mention the missing or disabled skill only if it affects the result.")
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Follow the loaded skill instructions when they are relevant to the assigned task. If a named skill was missing, continue with the available task context and mention the missing skill in the final summary only if it affects the result.")
	return strings.TrimSpace(b.String())
}

func findAgentSkill(roots []string, name string, disabled []string, extra []*skills.Skill) (*skills.Skill, bool) {
	if skill, _, ok := skills.Find(roots, name); ok {
		return skill, true
	}
	for _, candidate := range skills.Filter(extra, disabled) {
		if candidate == nil {
			continue
		}
		if candidate.Name == name || strings.HasSuffix(candidate.Name, ":"+name) {
			cp := *candidate
			return &cp, true
		}
	}
	return nil, false
}

func agentMemorySystemBlock(workspaceRoot, agentName, scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	path := agentMemoryPath(workspaceRoot, agentName, scope)
	if path == "" {
		return ""
	}
	var content string
	if b, err := os.ReadFile(path); err == nil {
		content = strings.TrimSpace(string(b))
	}
	var out strings.Builder
	out.WriteString("Persistent Agent Memory:\n")
	out.WriteString("- scope: ")
	out.WriteString(scope)
	out.WriteString("\n- file: ")
	out.WriteString(path)
	out.WriteString("\n")
	out.WriteString("Use this file as this child agent's durable memory scope. Keep memory entries factual, concise, and specific to this agent's role.")
	if content != "" {
		out.WriteString("\n\nExisting memory:\n```\n")
		out.WriteString(content)
		out.WriteString("\n```")
	} else {
		out.WriteString("\n\nNo existing memory file was found for this agent.")
	}
	return strings.TrimSpace(out.String())
}

func agentMemoryPath(workspaceRoot, agentName, scope string) string {
	agentName = safeSessionPart(agentName)
	if agentName == "" {
		agentName = "agent"
	}
	switch strings.TrimSpace(scope) {
	case "project":
		return filepath.Join(workspaceRoot, ".whale", "agent-memory", agentName, "MEMORY.md")
	case "local":
		return filepath.Join(workspaceRoot, ".whale", "agent-memory-local", agentName, "MEMORY.md")
	case "user":
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		return filepath.Join(home, ".whale", "agent-memory", agentName, "MEMORY.md")
	default:
		return ""
	}
}

func structuredOutputRepairPrompt(lastErr, previousSummary string) string {
	var b strings.Builder
	b.WriteString("Your previous response did not satisfy the required structured output contract.\n\n")
	if strings.TrimSpace(lastErr) != "" {
		b.WriteString("Last structured_output error:\n")
		b.WriteString(strings.TrimSpace(lastErr))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(previousSummary) != "" {
		b.WriteString("Previous response summary:\n")
		b.WriteString(strings.TrimSpace(previousSummary))
		b.WriteString("\n\n")
	}
	b.WriteString("Do not do more research or call any source/tool other than structured_output. ")
	b.WriteString("Use the information already gathered in this subagent session and call structured_output exactly once now. ")
	b.WriteString("If a field cannot be supported from the existing context, use the schema's empty or conservative value instead of adding unsupported claims.")
	return b.String()
}
