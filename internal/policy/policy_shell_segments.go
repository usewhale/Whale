package policy

import (
	"strings"
)

// normalizeShellWhitespace collapses runs of intra-line whitespace to single
// spaces, matching the legacy command-prefix normalization.
func normalizeShellWhitespace(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// normalizeShellSegments splits a shell command on common shell control
// boundaries and returns one whitespace-normalized segment per non-empty part.
// Splitting before normalizing keeps separators from being folded into spaces,
// so rule matching cannot span two commands. An empty or whitespace-only
// command yields a single empty segment.
func normalizeShellSegments(command string) []string {
	var out []string
	for _, part := range expandShellRuleSegments(command) {
		if seg := normalizeShellSegmentForRule(part); seg != "" {
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}
func normalizeShellSegmentForRule(segment string) string {
	var fields []string
	var current strings.Builder
	var quote rune
	escaped := false
	runes := []rune(segment)

	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}

	for _, r := range runes {
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if escaped {
			escaped = false
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			if quote == '"' || quote == 0 {
				escaped = true
				continue
			}
			current.WriteRune(r)
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case '\'':
			if quote == 0 {
				quote = '\''
			} else {
				current.WriteRune(r)
			}
		case ' ', '\t':
			if quote == 0 {
				flush()
			} else {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return strings.Join(fields, " ")
}
func expandShellRuleSegments(command string) []string {
	var out []string
	for _, part := range splitShellRuleSegments(command) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
func splitShellRuleSegments(command string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	runes := []rune(command)

	flush := func() {
		part := strings.TrimSpace(current.String())
		current.Reset()
		if part != "" {
			parts = append(parts, part)
		}
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
		case '\n', '\r', ';', '|':
			if quote != 0 {
				current.WriteRune(r)
				continue
			}
			if r == '|' && previousNonSpaceRune(runes, i) == '>' {
				current.WriteRune(r)
				continue
			}
			flush()
			if r == '|' && i+1 < len(runes) && runes[i+1] == r {
				i++
			}
		case '&':
			if quote != 0 {
				current.WriteRune(r)
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			if previousNonSpaceRune(runes, i) == '>' || previousNonSpaceRune(runes, i) == '<' {
				current.WriteRune(r)
				continue
			}
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return parts
}
func previousNonSpaceRune(runes []rune, before int) rune {
	for i := before - 1; i >= 0; i-- {
		if runes[i] != ' ' && runes[i] != '\t' {
			return runes[i]
		}
	}
	return 0
}
func shellSegmentRuleMatches(rule PermissionRule, segment string) bool {
	pattern := normalizeShellWhitespace(rule.Pattern)
	if pattern == "*" {
		return true
	}
	return wildcardMatch(pattern, segment)
}
