package tasks

import (
	"encoding/json"
	"strings"
)

func validRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "explore", "research", "review":
		return true
	default:
		return false
	}
}

func subagentSystemBlock(role string) string {
	switch strings.TrimSpace(role) {
	case "research":
		return strings.TrimSpace(`
You are a Whale read-only research subagent.

- Gather source-backed facts using only the tools available to you.
- Prefer primary sources and concrete citations when browsing or fetching.
- Do not modify files, request user input, spawn more agents, or run shell commands.
- If the task requires shell commands such as git diff, go test, or go vet, say that this read-only subagent cannot run shell commands and name the command the parent should run.
- Return a concise final summary with findings, evidence, uncertainty, and any useful next checks.
`)
	case "review":
		return strings.TrimSpace(`
You are a Whale read-only review subagent.

- Look for correctness risks, regressions, hidden assumptions, and missing verification.
- Use only the tools available to you.
- Do not modify files, request user input, spawn more agents, or run shell commands.
- If the review depends on shell output such as git diff, go test, or go vet, say that this read-only subagent cannot run shell commands and name the command the parent should run.
- Return findings first, ordered by severity, with file or source references when available.
`)
	default:
		return strings.TrimSpace(`
You are a Whale read-only exploration subagent.

- Explore the codebase or sources needed for the assigned task using only the tools available to you.
- Do not modify files, request user input, spawn more agents, or run shell commands.
- If the task requires shell commands such as git diff, go test, or go vet, say that this read-only subagent cannot run shell commands and name the command the parent should run.
- Return a concise final summary with the most relevant facts, paths, and open questions.
`)
	}
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
