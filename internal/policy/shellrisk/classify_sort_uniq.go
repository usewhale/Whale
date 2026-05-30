package shellrisk

import (
	"strings"
)

func classifySort(lower []string) Decision {
	endOptions := false
	for i := 1; i < len(lower); i++ {
		arg := lower[i]
		if endOptions || !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		if arg == "--" {
			endOptions = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			if arg == "--output" || strings.HasPrefix(arg, "--output=") {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "sort can write to an explicit output path with this option"}
			}
			if arg == "--compress-program" || strings.HasPrefix(arg, "--compress-program=") {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "sort can execute an external compressor with this option"}
			}
			if arg == "--temporary-directory" || strings.HasPrefix(arg, "--temporary-directory=") {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "sort can write temporary files outside the input stream with this option"}
			}
			if sortLongOptionConsumesNext(arg) && !strings.Contains(arg, "=") {
				i++
			}
			if !sortLongOptionSafe(arg) {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "sort option is not on the safe display allowlist"}
			}
			continue
		}
		if !sortShortOptionsSafe(arg) {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "sort option is not on the safe display allowlist"}
		}
	}
	return safeReadDecision("sort display command", "shell:safe:sort")
}
func classifyUniq(argv []string) Decision {
	operands := 0
	endOptions := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if endOptions || !strings.HasPrefix(arg, "-") || arg == "-" {
			operands++
			if operands > 1 {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "uniq can write to an output file when given a second operand"}
			}
			continue
		}
		if arg == "--" {
			endOptions = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			if uniqLongOptionConsumesNext(arg) && !strings.Contains(arg, "=") {
				i++
			}
			if !uniqLongOptionSafe(arg) {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "uniq option is not on the safe display allowlist"}
			}
			continue
		}
		consumedNext, ok := uniqShortOptionsSafe(arg)
		if !ok {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "uniq option is not on the safe display allowlist"}
		}
		if consumedNext {
			i++
		}
	}
	return safeReadDecision("uniq display command", "shell:safe:uniq")
}
func uniqLongOptionSafe(arg string) bool {
	name := arg
	if before, _, ok := strings.Cut(arg, "="); ok {
		name = before
	}
	switch name {
	case "--count",
		"--repeated",
		"--all-repeated",
		"--unique",
		"--ignore-case",
		"--zero-terminated",
		"--group",
		"--skip-fields",
		"--skip-chars",
		"--check-chars":
		return true
	default:
		return false
	}
}
func uniqLongOptionConsumesNext(arg string) bool {
	name := arg
	if before, _, ok := strings.Cut(arg, "="); ok {
		name = before
	}
	switch name {
	case "--skip-fields", "--skip-chars", "--check-chars":
		return true
	default:
		return false
	}
}
func uniqShortOptionsSafe(arg string) (consumesNext bool, ok bool) {
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'c', 'd', 'u', 'i', 'z':
			continue
		case 'f', 's', 'w':
			return i == len(arg)-1, true
		default:
			return false, false
		}
	}
	return false, len(arg) > 1
}
func sortLongOptionSafe(arg string) bool {
	name := arg
	if before, _, ok := strings.Cut(arg, "="); ok {
		name = before
	}
	switch name {
	case "--ignore-leading-blanks",
		"--dictionary-order",
		"--ignore-nonprinting",
		"--ignore-case",
		"--general-numeric-sort",
		"--human-numeric-sort",
		"--month-sort",
		"--numeric-sort",
		"--reverse",
		"--unique",
		"--stable",
		"--version-sort",
		"--zero-terminated",
		"--check",
		"--check=quiet",
		"--key",
		"--field-separator":
		return true
	default:
		return false
	}
}
func sortLongOptionConsumesNext(arg string) bool {
	name := arg
	if before, _, ok := strings.Cut(arg, "="); ok {
		name = before
	}
	switch name {
	case "--key", "--field-separator":
		return true
	default:
		return false
	}
}
func sortShortOptionsSafe(arg string) bool {
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'b', 'c', 'C', 'd', 'f', 'g', 'h', 'i', 'M', 'm', 'n', 'r', 's', 'u', 'V', 'z':
			continue
		case 'o':
			return false
		case 'T':
			return false
		case 'k', 't':
			return true
		default:
			return false
		}
	}
	return len(arg) > 1
}
