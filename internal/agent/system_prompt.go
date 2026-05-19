package agent

import (
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/agent/planning"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/shell"
	"github.com/usewhale/whale/internal/skills"
)

func (a *Agent) buildTurnProviderHistory(sessionID string, rt *memory.RuntimeState) []core.Message {
	out := rt.BuildProviderHistory()
	return out
}

func (a *Agent) buildImmutableSystemBlocks() []string {
	systemBlocks := make([]string, 0, len(a.extraSystemBlocks)+2)
	for _, block := range a.extraSystemBlocks {
		if trimmed := strings.TrimSpace(block); trimmed != "" {
			systemBlocks = append(systemBlocks, trimmed)
		}
	}
	if a.mode == session.ModePlan {
		systemBlocks = append(systemBlocks, planning.ModeInstructions())
	} else if a.mode == session.ModeAsk {
		systemBlocks = append(systemBlocks, strings.TrimSpace(`
Ask mode is active.

- Answer questions about the codebase, architecture, behavior, bugs, and possible changes.
- You may use read-only tools, including file reads/search, read-only shell commands, and web lookup/fetch tools, when they help answer the question.
- Do not modify files, do not call mutating tools, and do not act as though you are implementing changes right now.
- If code changes are needed, explain them, summarize them, or outline them briefly instead of attempting to make them.
`))
	} else {
		systemBlocks = append(systemBlocks, strings.TrimSpace(`
	Agent mode is active.

- You have access to all tools, including read-only and write tools.
- You may read, edit, and create files, run shell commands, and use all other available tools to accomplish the user's request.
- When mode restrictions blocked a previous turn, you are no longer constrained by those restrictions — carry out the request fully.
- For implementation work with more than one step, use update_plan to initialize and maintain a concise execution checklist. Keep at most one item in_progress and mark steps completed promptly.
	`))
	}
	systemBlocks = append(systemBlocks, renderDelegationPolicyBlock())
	systemBlocks = append(systemBlocks, renderRuntimeBlock(a.workspaceRoot, shell.DescribeRuntime()))
	systemBlocks = append(systemBlocks, renderToolSpecsBlock(a.tools.Specs()))
	if strings.TrimSpace(a.workspaceRoot) != "" {
		discovered := skills.Filter(skills.Discover(skills.DefaultRoots(a.workspaceRoot)), a.disabledSkills)
		discovered = append(discovered, skills.Filter(a.extraSkills, a.disabledSkills)...)
		discovered = skills.Sort(skills.Deduplicate(discovered))
		if rendered := skills.RenderAvailableSkills(discovered); rendered != "" {
			systemBlocks = append(systemBlocks, rendered)
		}
	}
	systemBlocks = append(systemBlocks, "For branch decisions or key assumptions requiring user choice, call request_user_input instead of presenting long A/B/C prose menus.")
	if a.projectMemoryEnabled {
		if mem, ok := memory.ReadProjectMemory(a.workspaceRoot, a.projectMemoryFileOrder, a.projectMemoryMaxChars); ok {
			systemBlocks = append(systemBlocks,
				"# Project Memory\n\nThe user pinned these notes about this project. Treat them as authoritative context for this workspace:\n\n```\n"+mem.Content+"\n```",
			)
		}
	}
	return systemBlocks
}

func renderRuntimeBlock(workspaceRoot string, rt shell.RuntimeDescription) string {
	var b strings.Builder
	b.WriteString("Current Whale runtime:\n")
	if strings.TrimSpace(workspaceRoot) != "" {
		b.WriteString("- Current Whale workspace root: ")
		b.WriteString(workspaceRoot)
		b.WriteString("\n")
	}
	if strings.TrimSpace(rt.GOOS) != "" {
		b.WriteString("- OS: ")
		b.WriteString(strings.TrimSpace(rt.GOOS))
		b.WriteString("\n")
	}
	b.WriteString("- Shell: ")
	b.WriteString(rt.ShellSummary())
	b.WriteString("\n")
	b.WriteString("Shell commands run from the current Whale workspace by default. Do not assume a synthetic path such as /workspace; use relative paths or the shell_run cwd parameter for subdirectories.")
	if guidance := rt.CommandGuidance(); strings.TrimSpace(guidance) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(guidance))
	}
	return strings.TrimSpace(b.String())
}

func renderDelegationPolicyBlock() string {
	return strings.TrimSpace(`
Delegation policy.

- Do not use parallel_reason or spawn_subagent just because they are available.
- Use a single agent for direct questions, known-file reads, small localized edits, tightly coupled work, or tasks where the next step depends on the current result.
- Use parallel_reason for 2-8 independent, cheap, model-only subqueries that need comparison, classification, critique, or brainstorming and do not need tools, files, shell, or web access.
- Use spawn_subagent for one bounded read-only exploration, research, or review task that needs file reads/search or web lookup/fetch. Subagents do not have shell access.
- Do not ask the user to name these tools. Infer the right path from natural language such as "parallelize this" or "send a reviewer/explorer".
- If the user explicitly asks for a subagent, delegated reviewer, or explorer, spawn the appropriate subagent directly. Do not load a skill first unless the user explicitly names one.
- The parent agent owns the final answer. Summarize and reconcile child results before responding to the user.
- Do not delegate writable or high-risk work unless the runtime explicitly provides an isolated writable worker capability.
`)
}

func renderToolSpecsBlock(specs []core.ToolSpec) string {
	if len(specs) == 0 {
		return "No tools are available."
	}
	var b strings.Builder
	b.WriteString("Available tools (source of truth from registry):\n")
	for _, s := range specs {
		mode := "write"
		if s.ReadOnly {
			mode = "read-only"
		}
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(" [")
		b.WriteString(mode)
		b.WriteString("]")
		if strings.TrimSpace(s.Description) != "" {
			b.WriteString(": ")
			b.WriteString(strings.TrimSpace(s.Description))
		}
		if s.Parameters != nil {
			if propsAny, ok := s.Parameters["properties"]; ok {
				if props, ok := propsAny.(map[string]any); ok && len(props) > 0 {
					keys := make([]string, 0, len(props))
					for k := range props {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					max := len(keys)
					if max > 5 {
						max = 5
					}
					b.WriteString(" args:")
					b.WriteString(strings.Join(keys[:max], ","))
				}
			}
		}
		if strings.TrimSpace(s.ApprovalHint) != "" {
			b.WriteString(" approval:")
			b.WriteString(strings.TrimSpace(s.ApprovalHint))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
