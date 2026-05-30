package app

import (
	"strings"

	appcommands "github.com/usewhale/whale/internal/commands"
)

type HelpCommand struct {
	Name        string
	Description string
}

func HelpCommands() []HelpCommand {
	specs := appcommands.DefaultSlashCommands()
	out := make([]HelpCommand, 0, len(specs))
	for _, spec := range specs {
		name := spec.Name
		if spec.ArgumentHint != "" {
			name += " " + spec.ArgumentHint
		}
		out = append(out, HelpCommand{Name: name, Description: spec.Description})
	}
	return out
}

func BuildHelpText() string {
	var b strings.Builder
	b.WriteString("Whale help\n\n")
	b.WriteString("Browse default commands:\n\n")
	for i, cmd := range HelpCommands() {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- `")
		b.WriteString(cmd.Name)
		b.WriteString("`\n")
		b.WriteString("  ")
		b.WriteString(cmd.Description)
	}
	b.WriteString("\n\nFor more help: https://github.com/usewhale/whale")
	return b.String()
}
