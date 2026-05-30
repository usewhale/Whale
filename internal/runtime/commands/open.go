package commands

import (
	"strings"
)

func IsOpenCommandLine(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) > 0 && fields[0] == "/open"
}
