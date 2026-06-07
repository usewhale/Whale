//go:build !unix

package execboundary

import (
	"fmt"
	"io"
)

func execProgram(program string, argv []string, _ io.Writer, stderr io.Writer) int {
	_, _ = fmt.Fprintf(stderr, "whale exec boundary: unsupported platform for %s %v\n", program, argv)
	return 1
}
