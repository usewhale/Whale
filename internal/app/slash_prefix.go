package app

import appcommands "github.com/usewhale/whale/internal/commands"

func expandUniqueSlashPrefix(line string) string {
	return appcommands.ExpandUniqueSlashPrefix(line, CommandsHelp, "/mcp")
}

func parseSlashCommands(help string) []string {
	return appcommands.SlashCommandNames("/mcp")
}
