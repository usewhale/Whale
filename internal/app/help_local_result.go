package app

import (
	"strings"

	appcommands "github.com/usewhale/whale/internal/commands"
)

func buildHelpLocalResult() *LocalResult {
	text := BuildHelpText()
	sections := []LocalResultSection{
		{Title: "Mode", Fields: helpFields("/agent", "/ask", "/plan")},
		{Title: "Session", Fields: helpFields("/new", "/fork", "/resume", "/clear", "/compact", "/exit")},
		{Title: "Local info", Fields: helpFields("/status", "/stats", "/mcp", "/diff", "/help")},
		{Title: "Tools and management", Fields: helpFields("/model", "/permissions", "/skills", "/plugins", "/memory", "/open", "/feedback")},
		{Title: "Workflow", Fields: helpFields("/review", "/btw", "/focus", "/init")},
	}
	return &LocalResult{
		Kind:      "help",
		Title:     "Help",
		Sections:  sections,
		PlainText: text,
	}
}

func helpFields(names ...string) []LocalResultField {
	specs := appcommands.DefaultSlashCommands()
	byName := make(map[string]appcommands.SlashCommandSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
	}
	fields := make([]LocalResultField, 0, len(names))
	for _, name := range names {
		spec, ok := byName[name]
		if !ok {
			continue
		}
		label := spec.Name
		if hint := strings.TrimSpace(spec.ArgumentHint); hint != "" {
			label += " " + hint
		}
		fields = append(fields, LocalResultField{Label: label, Value: spec.Description})
	}
	return fields
}
