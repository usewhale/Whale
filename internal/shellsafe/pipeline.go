package shellsafe

import "strings"

func SplitPipeline(command string) ([]string, bool) {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	sawPipe := false

	flush := func() bool {
		part := strings.TrimSpace(current.String())
		current.Reset()
		if part == "" {
			return false
		}
		parts = append(parts, part)
		return true
	}

	for _, r := range strings.TrimSpace(command) {
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}
		if escaped {
			escaped = false
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			if quote == '"' {
				escaped = true
			}
			current.WriteRune(r)
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			}
			current.WriteRune(r)
		case '\'':
			if quote == 0 {
				quote = '\''
			}
			current.WriteRune(r)
		case '|':
			if quote != 0 {
				current.WriteRune(r)
				continue
			}
			if !flush() {
				return nil, false
			}
			sawPipe = true
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 || escaped || !sawPipe || !flush() {
		return nil, false
	}
	return parts, true
}

// SplitAndList splits on `&&` only. `||` is intentionally not handled: its
// semantics are "run the right side if the left side fails", which makes it
// impossible to prove statically that all branches stay read-only. Callers
// that need to classify `||` chains as safe must do so explicitly.
func SplitAndList(command string) ([]string, bool) {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	sawAnd := false
	runes := []rune(strings.TrimSpace(command))

	flush := func() bool {
		part := strings.TrimSpace(current.String())
		current.Reset()
		if part == "" {
			return false
		}
		parts = append(parts, part)
		return true
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}
		if escaped {
			escaped = false
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			if quote == '"' {
				escaped = true
			}
			current.WriteRune(r)
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			}
			current.WriteRune(r)
		case '\'':
			if quote == 0 {
				quote = '\''
			}
			current.WriteRune(r)
		case '&':
			if quote != 0 || i+1 >= len(runes) || runes[i+1] != '&' {
				current.WriteRune(r)
				continue
			}
			if !flush() {
				return nil, false
			}
			sawAnd = true
			i++
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 || escaped || !sawAnd || !flush() {
		return nil, false
	}
	return parts, true
}
