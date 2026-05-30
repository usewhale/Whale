package commands

import runtimecommands "github.com/usewhale/whale/internal/runtime/commands"

type SubmitClass = runtimecommands.SubmitClass
type SubmitClassification = runtimecommands.SubmitClassification

const (
	SubmitText          = runtimecommands.SubmitText
	SubmitLocalReadOnly = runtimecommands.SubmitLocalReadOnly
	SubmitLocalUI       = runtimecommands.SubmitLocalUI
	SubmitLocalMode     = runtimecommands.SubmitLocalMode
	SubmitLocalMutating = runtimecommands.SubmitLocalMutating
	SubmitExit          = runtimecommands.SubmitExit
	SubmitTurnStarting  = runtimecommands.SubmitTurnStarting
	SubmitUsageError    = runtimecommands.SubmitUsageError
)

func ClassifySubmit(line, help string, localCommands ...string) SubmitClassification {
	return runtimecommands.ClassifySubmit(line, help, localCommands...)
}
