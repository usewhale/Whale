package commands

import runtimecommands "github.com/usewhale/whale/internal/runtime/commands"

type SlashCommandSpec = runtimecommands.SlashCommandSpec
type SlashCommandOption = runtimecommands.SlashCommandOption

func DefaultSlashCommands() []SlashCommandSpec {
	return runtimecommands.DefaultSlashCommands()
}

func CommandsHelp() string {
	return runtimecommands.CommandsHelp()
}

func SlashCommandNames(localCommands ...string) []string {
	return runtimecommands.SlashCommandNames(localCommands...)
}

func LooksLikeSlashCommand(line string) bool {
	return runtimecommands.LooksLikeSlashCommand(line)
}

func ExpandUniqueSlashPrefix(line, help string, localCommands ...string) string {
	return runtimecommands.ExpandUniqueSlashPrefix(line, help, localCommands...)
}

func ParseSlashCommands(help string) []string {
	return runtimecommands.ParseSlashCommands(help)
}
