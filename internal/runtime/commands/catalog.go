package commands

import (
	"strings"
)

type SlashCommandSpec struct {
	Name         string
	Description  string
	ArgumentHint string
	AutoRun      bool
	Options      []SlashCommandOption
}

type SlashCommandOption struct {
	Token       string
	Description string
	InsertText  string
	AutoRun     bool
}

func DefaultSlashCommands() []SlashCommandSpec {
	return []SlashCommandSpec{
		{Name: "/help", Description: "Show help and available commands", AutoRun: true},
		{Name: "/model", Description: "Choose model, effort, and thinking settings", ArgumentHint: "[model]", AutoRun: true},
		{Name: "/permissions", Description: "Configure permission auto-accept", AutoRun: true},
		{Name: "/agent", Description: "Switch to agent mode", AutoRun: true},
		{Name: "/ask", Description: "Switch to ask mode, optionally submit a prompt", ArgumentHint: "[prompt]", AutoRun: true},
		{Name: "/plan", Description: "Switch to plan mode, optionally submit a prompt", ArgumentHint: "[prompt]", AutoRun: true},
		{Name: "/btw", Description: "Ask a side question without changing the conversation", ArgumentHint: "<question>"},
		{Name: "/focus", Description: "Toggle focus view", AutoRun: true},
		{Name: "/diff", Description: "Show current git diff", AutoRun: true},
		{Name: "/copy", Description: "Copy Whale's last response to clipboard (or /copy N for the Nth-latest)", ArgumentHint: "[N]", AutoRun: true},
		{Name: "/open", Description: "Open a file or directory in your editor", ArgumentHint: "[path]", AutoRun: true},
		{Name: "/review", Description: "Open review mode or review a target", ArgumentHint: "[local|branch|pr|commit|<instructions>]", AutoRun: true, Options: []SlashCommandOption{
			{Token: "local", Description: "Review staged, unstaged, and relevant untracked files", AutoRun: true},
			{Token: "branch", Description: "Review current branch against default branch or base", AutoRun: true},
			{Token: "pr", Description: "Review a GitHub PR by number or URL", InsertText: "/review pr "},
			{Token: "commit", Description: "Review one commit by SHA", InsertText: "/review commit "},
		}},
		{Name: "/skills", Description: "Show available skills", AutoRun: true},
		{Name: "/plugins", Description: "Manage plugins", AutoRun: true},
		{Name: "/hooks", Description: "View and trust lifecycle hooks", ArgumentHint: "[trust all|trust <hook-key>...]", AutoRun: true},
		{Name: "/feedback", Description: "Open the Whale issue tracker", AutoRun: true},
		{Name: "/new", Description: "Start a new session", ArgumentHint: "[id]", AutoRun: true},
		{Name: "/fork", Description: "Fork the current session", ArgumentHint: "[name]", AutoRun: true},
		{Name: "/rewind", Description: "Restore code and conversation to an earlier message", AutoRun: true},
		{Name: "/checkpoint", Description: "Alias for /rewind", AutoRun: true},
		{Name: "/resume", Description: "Open the resume picker", AutoRun: true},
		{Name: "/clear", Description: "Clear the visible conversation", AutoRun: true},
		{Name: "/status", Description: "Show session and configuration status", AutoRun: true},
		{Name: "/doctor", Description: "Show session storage and diagnostic information", AutoRun: true},
		{Name: "/stats", Description: "Show usage and tool statistics", ArgumentHint: "[usage|tools|repair|recent|profile|all]", Options: []SlashCommandOption{
			{Token: "usage", Description: "Show token and cost usage", AutoRun: true},
			{Token: "tools", Description: "Show tool-call counts", AutoRun: true},
			{Token: "repair", Description: "Show repair statistics", AutoRun: true},
			{Token: "recent", Description: "Show recent activity", AutoRun: true},
			{Token: "profile", Description: "Profile recent sessions", AutoRun: true},
			{Token: "all", Description: "Show all statistics", AutoRun: true},
		}},
		{Name: "/workflows", Description: "Open workflow runs and progress", AutoRun: true},
		{Name: "/deep-research", Description: "Run source-backed multi-agent web research", ArgumentHint: "[--resume runId] <question>"},
		{Name: "/mcp", Description: "Show MCP server status", AutoRun: true},
		{Name: "/compact", Description: "Compact the current conversation", AutoRun: true},
		{Name: "/init", Description: "Create AGENTS.md from repository context", AutoRun: true},
		{Name: "/exit", Description: "Exit Whale", AutoRun: true},
	}
}

func CommandsHelp() string {
	specs := DefaultSlashCommands()
	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		if hint := strings.TrimSpace(spec.ArgumentHint); hint != "" {
			parts = append(parts, name+" "+hint)
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

func SlashCommandNames(localCommands ...string) []string {
	specs := DefaultSlashCommands()
	out := make([]string, 0, len(specs)+len(localCommands))
	seen := map[string]bool{}
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, cmd := range localCommands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		out = append(out, cmd)
	}
	return out
}
