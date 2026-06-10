package core

import "strings"

// Shell exit-code semantics shared by the tool layer (which decides what the
// model is told) and the TUI (which decides how the result is rendered).
// Many search commands use exit code 1 to mean "searched fine, found
// nothing" — that is an answer, not a failure. Treating it as a failure
// makes the model retry or rephrase and shows the user a wall of red
// "Command failed" lines (session 019ead56: a dozen findstr/where/dir
// no-match results surfaced as tool call errors).

// ShellExitMeansNoMatches reports whether a finished command's exit status
// conveys "no matches / no results" rather than failure. It keys on the
// last command segment because that is what determines the exit code.
// Commands frequently merge streams with 2>&1, so the rules must not assume
// error text stays on stderr — each command gets the strongest signal its
// actual behavior allows.
func ShellExitMeansNoMatches(command string, exitCode int, stdout, stderr string) bool {
	if exitCode != 1 {
		return false
	}
	base := ShellSegmentBaseCommand(LastShellCommandSegment(command))
	switch base {
	case "grep", "rg", "git grep":
		// exit 1 = no match, exit 2+ = real error; errors go to stderr.
		return strings.TrimSpace(stderr) == ""
	case "where":
		// Documented exit codes: 0 = found, 1 = not found, 2 = error. The
		// "INFO: Could not find files" line goes to stderr, so exit 1 is
		// the signal — do not treat that stderr line as a complaint.
		return true
	case "findstr":
		// A true no-match prints nothing. Errors print FINDSTR:-prefixed
		// lines and can still exit 1 (e.g. "FINDSTR: Cannot open file"),
		// landing on stdout when streams are merged.
		return strings.TrimSpace(stderr) == "" && !strings.Contains(stdout, "FINDSTR:")
	case "dir":
		// dir exits 1 both for "no files match" and for real errors, so
		// require positive evidence: the fixed "File Not Found" message
		// (on stderr, or stdout under 2>&1). Localized systems fall back
		// to the failure rendering, which is the safe direction.
		return containsFold(stderr, "File Not Found") || containsFold(stdout, "File Not Found")
	}
	return false
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// LastShellCommandSegment returns the final command in a pipe/&&/; chain,
// skipping separators inside quotes. The last segment determines the chain's
// exit code.
func LastShellCommandSegment(command string) string {
	command = strings.TrimSpace(command)
	start := 0
	last := command
	var quote rune
	escaped := false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		switch runes[i] {
		case '|', ';':
			if segment := strings.TrimSpace(string(runes[start:i])); segment != "" {
				last = segment
			}
			if runes[i] == '|' && i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
			start = i + 1
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				if segment := strings.TrimSpace(string(runes[start:i])); segment != "" {
					last = segment
				}
				i++
				start = i + 1
			} else if i == 0 || runes[i-1] != '>' {
				// cmd.exe chains commands with a single & and the final
				// command still determines the exit code. The guard skips
				// the & inside stream redirects such as 2>&1.
				if segment := strings.TrimSpace(string(runes[start:i])); segment != "" {
					last = segment
				}
				start = i + 1
			}
		}
	}
	if segment := strings.TrimSpace(string(runes[start:])); segment != "" {
		last = segment
	}
	return last
}

// ShellSegmentBaseCommand extracts the command word a segment's exit-code
// semantics are keyed on ("git grep" is kept as a unit).
func ShellSegmentBaseCommand(segment string) string {
	fields := strings.Fields(strings.TrimSpace(segment))
	if len(fields) == 0 {
		return ""
	}
	if fields[0] == "git" && len(fields) > 1 && fields[1] == "grep" {
		return "git grep"
	}
	return fields[0]
}
