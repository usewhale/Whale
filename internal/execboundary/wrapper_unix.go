//go:build unix

package execboundary

import (
	"io"
	"os"
	"syscall"

	"github.com/usewhale/whale/internal/execenv"
)

func execProgram(program string, argv []string, _ io.Writer, _ io.Writer) int {
	cleanEnv := execProgramEnv(os.Environ())
	if err := syscall.Exec(program, argv, cleanEnv); err != nil {
		_, _ = os.Stderr.WriteString("whale exec boundary: exec failed: " + err.Error() + "\n")
		return 1
	}
	return 0
}

func execProgramEnv(env []string) []string {
	cleanEnv := env[:0]
	for _, item := range env {
		switch {
		case hasEnvName(item, execenv.WrapperModeEnv):
			continue
		default:
			cleanEnv = append(cleanEnv, item)
		}
	}
	return cleanEnv
}

func hasEnvName(item, name string) bool {
	return len(item) > len(name) && item[:len(name)] == name && item[len(name)] == '='
}
