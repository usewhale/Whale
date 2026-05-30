package shellrisk

import (
	"strings"
)

func classifySedReadOnly(argv []string) Decision {
	if sedPrintRangeReadOnly(argv) {
		return safeReadDecision("sed range print command", semanticKey("safe", lowerArgv(argv)))
	}
	if sedSubstitutionReadOnly(argv) {
		return safeReadDecision("sed stream substitution command", semanticKey("safe", lowerArgv(argv)))
	}
	return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "sed command is not classified as read-only"}
}
func sedSubstitutionReadOnly(argv []string) bool {
	if len(argv) < 2 || argv[0] != "sed" {
		return false
	}
	i := 1
	for i < len(argv) {
		switch argv[i] {
		case "-E", "-r", "--regexp-extended", "-n", "--quiet", "--silent":
			i++
		case "--":
			i++
			goto script
		default:
			goto script
		}
	}

script:
	if i >= len(argv) || !sedSubstitutionScriptReadOnly(argv[i]) {
		return false
	}
	i++
	for ; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") {
			return false
		}
	}
	return true
}
func sedSubstitutionScriptReadOnly(script string) bool {
	if script == "" || !strings.HasPrefix(script, "s") {
		return false
	}
	runes := []rune(script)
	if len(runes) < 4 {
		return false
	}
	delim := runes[1]
	if delim == '\\' || delim == '\n' || delim == '\r' {
		return false
	}
	parts := 0
	escaped := false
	for i := 2; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == delim {
			parts++
			if parts == 2 {
				flags := string(runes[i+1:])
				return sedSubstitutionFlagsReadOnly(flags)
			}
		}
	}
	return false
}
func sedSubstitutionFlagsReadOnly(flags string) bool {
	for _, r := range flags {
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case 'g', 'p', 'I', 'i', 'M', 'm':
			continue
		default:
			return false
		}
	}
	return true
}
func sedPrintRangeReadOnly(argv []string) bool {
	if len(argv) < 3 || argv[0] != "sed" {
		return false
	}
	i := 1
	sawQuiet := false
	for i < len(argv) {
		switch argv[i] {
		case "-n", "--quiet", "--silent":
			sawQuiet = true
			i++
		case "--":
			i++
			goto script
		default:
			goto script
		}
	}

script:
	if !sawQuiet || i >= len(argv) || !sedRangePrintScript(argv[i]) {
		return false
	}
	i++
	for ; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") {
			return false
		}
	}
	return true
}
func sedRangePrintScript(script string) bool {
	if script == "" || !strings.HasSuffix(script, "p") {
		return false
	}
	addr := strings.TrimSuffix(script, "p")
	parts := strings.Split(addr, ",")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "$" {
			continue
		}
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
