package shellrisk

import (
	"strings"
)

func parseSimpleShellCommand(command string) ([]string, bool) {
	var argv []string
	var word strings.Builder
	var quote rune
	inWord := false

	flush := func() {
		if inWord {
			argv = append(argv, word.String())
			word.Reset()
			inWord = false
		}
	}

	for _, r := range strings.TrimSpace(command) {
		switch quote {
		case '\'':
			if r == '\'' {
				quote = 0
				continue
			}
			word.WriteRune(r)
			continue
		case '"':
			switch r {
			case '"':
				quote = 0
				continue
			case '\\', '$', '`':
				return nil, false
			}
			word.WriteRune(r)
			continue
		}

		switch {
		case r == ' ' || r == '\t':
			flush()
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case rejectedAutoAllowShellRune(r):
			return nil, false
		default:
			inWord = true
			word.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return argv, len(argv) > 0
}
func rejectedAutoAllowShellRune(r rune) bool {
	switch r {
	case '\\', '$', '`', ';', '|', '&', '<', '>', '\n', '\r', '(', ')', '{', '}', '#', '*', '?', '[', ']':
		return true
	default:
		return false
	}
}
func stripTrailingSafeStderrRedirect(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	for _, redirect := range []string{"2>&1", "2>/dev/null", "2> /dev/null"} {
		if base, ok := stripTrailingRedirect(trimmed, redirect); ok {
			return base, true
		}
	}
	return "", false
}
func stripTrailingRedirect(command, redirect string) (string, bool) {
	if !strings.HasSuffix(command, redirect) {
		return "", false
	}
	start := len(command) - len(redirect)
	if start == 0 || !isShellWhitespace(rune(command[start-1])) {
		return "", false
	}
	if !shellOffsetOutsideQuotes(command, start) {
		return "", false
	}
	base := strings.TrimSpace(command[:start])
	if base == "" {
		return "", false
	}
	return base, true
}
func shellOffsetOutsideQuotes(command string, offset int) bool {
	var quote rune
	escaped := false
	for i, r := range command {
		if i >= offset {
			break
		}
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			if quote == '"' {
				escaped = true
			}
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			}
		case '\'':
			if quote == 0 {
				quote = '\''
			}
		}
	}
	return quote == 0 && !escaped
}
func isShellWhitespace(r rune) bool {
	return r == ' ' || r == '\t'
}
