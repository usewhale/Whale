//go:build unix

package execboundary

import (
	"io"
	"os"
	"syscall"

	"github.com/usewhale/whale/internal/execenv"
)

func execProgram(program string, argv []string, _ io.Writer, _ io.Writer) int {
	env := os.Environ()
	cleanEnv := env[:0]
	for _, item := range env {
		switch {
		case hasEnvName(item, execenv.WrapperModeEnv),
			hasEnvName(item, execenv.RulesEnv),
			hasEnvName(item, execenv.SocketEnv),
			hasEnvName(item, execenv.ShellEnv),
			hasEnvName(item, execenv.WrapperPathEnv),
			hasEnvName(item, execenv.ExecWrapperEnv):
			continue
		default:
			cleanEnv = append(cleanEnv, item)
		}
	}
	if err := syscall.Exec(program, argv, cleanEnv); err != nil {
		_, _ = os.Stderr.WriteString("whale exec boundary: exec failed: " + err.Error() + "\n")
		return 1
	}
	return 0
}

func hasEnvName(item, name string) bool {
	return len(item) > len(name) && item[:len(name)] == name && item[len(name)] == '='
}
