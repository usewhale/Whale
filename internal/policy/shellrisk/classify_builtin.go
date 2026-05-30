package shellrisk

import (
	"strings"
)

func classifyBuiltinReadOnly(argv, lower []string) Decision {
	switch lower[0] {
	case "date":
		return classifyDate(argv, lower)
	case "uname":
		return classifyUname(lower)
	case "whoami":
		return classifyWhoami(lower)
	case "id":
		return classifyID(lower)
	case "which":
		return classifyCommandLookup(lower[1:])
	case "command":
		if len(lower) >= 2 && lower[1] == "-v" {
			return classifyCommandLookup(lower[2:])
		}
	case "sed":
		return classifySedReadOnly(argv)
	case "sort":
		return classifySort(argv)
	case "uniq":
		return classifyUniq(argv)
	case "printf":
		return classifyPrintf(lower)
	}
	return Decision{}
}
func classifyDate(argv, lower []string) Decision {
	flagsWithValues := map[string]bool{
		"-d": true, "--date": true, "-r": true, "--reference": true, "--rfc-3339": true,
	}
	safeNoValueFlags := map[string]bool{
		"-u": true, "--utc": true, "--universal": true,
		"-I": true, "-R": true, "--iso-8601": true, "--rfc-email": true, "--debug": true, "--help": true, "--version": true,
	}
	for i := 1; i < len(lower); i++ {
		raw := argv[i]
		arg := lower[i]
		switch {
		case raw == "-s" || arg == "--set" || strings.HasPrefix(arg, "--set=") || raw == "-f" || arg == "--file" || strings.HasPrefix(arg, "--file="):
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "date can set system time or read batch dates with this option"}
		case strings.HasPrefix(raw, "+"):
			continue
		case flagsWithValues[raw] || (strings.HasPrefix(raw, "--") && flagsWithValues[arg]):
			i++
			if i >= len(lower) {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "date flag requires a value"}
			}
		case strings.HasPrefix(arg, "--date="), strings.HasPrefix(arg, "--reference="), strings.HasPrefix(arg, "--iso-8601="), strings.HasPrefix(arg, "--rfc-3339="):
			continue
		case safeNoValueFlags[raw] || safeNoValueFlags[arg]:
			continue
		case strings.HasPrefix(raw, "-"):
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "date option is not on the safe display allowlist"}
		default:
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "date positional arguments can set system time"}
		}
	}
	return safeReadDecision("date display command", "shell:safe:date")
}
func classifyUname(lower []string) Decision {
	safeLong := map[string]bool{
		"--all": true, "--kernel-name": true, "--nodename": true, "--kernel-release": true, "--kernel-version": true,
		"--machine": true, "--processor": true, "--hardware-platform": true, "--operating-system": true, "--help": true, "--version": true,
	}
	for _, arg := range lower[1:] {
		if safeLong[arg] {
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			for _, r := range arg[1:] {
				if !strings.ContainsRune("asnrvmpio", r) {
					return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "uname option is not on the safe display allowlist"}
				}
			}
			continue
		}
		return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "uname only supports safe display flags in auto-allow"}
	}
	return safeReadDecision("uname display command", "shell:safe:uname")
}
func classifyWhoami(lower []string) Decision {
	for _, arg := range lower[1:] {
		if arg != "--help" && arg != "--version" {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "whoami only supports help/version args in auto-allow"}
		}
	}
	return safeReadDecision("whoami display command", "shell:safe:whoami")
}
func classifyID(lower []string) Decision {
	safeLong := map[string]bool{
		"--user": true, "--group": true, "--groups": true, "--name": true, "--real": true, "--zero": true, "--help": true, "--version": true,
	}
	for _, arg := range lower[1:] {
		if safeLong[arg] || isCommandName(arg) {
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			for _, r := range arg[1:] {
				if !strings.ContainsRune("uggnrz", r) {
					return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "id option is not on the safe display allowlist"}
				}
			}
			continue
		}
		return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "id argument is not safe for auto-allow"}
	}
	return safeReadDecision("id display command", "shell:safe:id")
}
func classifyCommandLookup(args []string) Decision {
	if len(args) == 0 {
		return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "command lookup requires at least one command name"}
	}
	for _, arg := range args {
		if !isCommandName(arg) {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "command lookup operands must be simple command names"}
		}
	}
	return safeReadDecision("command lookup", "shell:safe:command-lookup")
}
func classifyPrintf(lower []string) Decision {
	for _, arg := range lower[1:] {
		if strings.ContainsAny(arg, "/") && strings.HasPrefix(arg, "-") {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "printf option is not on the safe display allowlist"}
		}
	}
	return safeReadDecision("printf display command", "shell:safe:printf")
}
