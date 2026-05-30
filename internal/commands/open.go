package commands

import runtimecommands "github.com/usewhale/whale/internal/runtime/commands"

func IsOpenCommandLine(line string) bool {
	return runtimecommands.IsOpenCommandLine(line)
}
