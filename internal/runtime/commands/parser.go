package commands

import (
	"strings"
)

func LooksLikeSlashCommand(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false
	}
	head := line
	if idx := strings.IndexAny(head, " \t"); idx >= 0 {
		head = head[:idx]
	}
	if len(head) <= 1 {
		return true
	}
	return !strings.Contains(head[1:], "/")
}

func ExpandUniqueSlashPrefix(line, help string, localCommands ...string) string {
	line = strings.TrimSpace(line)
	if !LooksLikeSlashCommand(line) || strings.Contains(line, " ") {
		return line
	}
	commands := append(ParseSlashCommands(help), localCommands...)
	matches := make([]string, 0, 1)
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, line) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) != 1 {
		return line
	}
	return matches[0]
}

func ParseSlashCommands(help string) []string {
	parts := strings.Split(help, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		field := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(field, "/") || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}
