package policy

import "strings"

func shellCommandEligibleForScopedAllow(command string) bool {
	if command == "" {
		return false
	}
	return !strings.ContainsAny(command, "\n\r;&|<>$`(){}#")
}
func scopedAllowCommandAllowed(command, rule string) bool {
	rule = normalizeCommandPrefix(rule)
	if !strings.HasPrefix(rule, "gh pr ") {
		return true
	}
	return ghPRScopedAllowCommandAllowed(command, rule)
}
func ghPRScopedAllowCommandAllowed(command, rule string) bool {
	argv := strings.Fields(command)
	if len(argv) < 3 || strings.ToLower(argv[0]) != "gh" || strings.ToLower(argv[1]) != "pr" {
		return false
	}
	for _, arg := range argv[3:] {
		if arg == "--web" || arg == "-w" {
			return false
		}
	}
	switch rule {
	case "gh pr view":
		return ghPRViewScopedAllowArgs(argv[3:])
	case "gh pr list":
		return ghPRListScopedAllowArgs(argv[3:])
	case "gh pr diff":
		return ghPRDiffScopedAllowArgs(argv[3:])
	default:
		return false
	}
}
func ghPRViewScopedAllowArgs(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--comments":
			continue
		case "--json":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || strings.TrimSpace(args[i+1]) == "" {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}
func ghPRListScopedAllowArgs(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "--json", "--state", "--author", "--base", "--head", "--label", "--search":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || strings.TrimSpace(args[i+1]) == "" {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}
func ghPRDiffScopedAllowArgs(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for _, arg := range args[1:] {
		switch arg {
		case "--patch", "--name-only":
			continue
		default:
			if strings.HasPrefix(arg, "--color=") {
				continue
			}
			return false
		}
	}
	return true
}
func hasAllowCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(command, "\n\r") || strings.ContainsAny(rule, "\n\r") {
		return false
	}
	return hasSingleLineCommandPrefix(command, rule)
}
func hasSingleLineCommandPrefix(command, rule string) bool {
	command = normalizeCommandPrefix(command)
	rule = normalizeCommandPrefix(rule)
	if command == "" || rule == "" {
		return false
	}
	return command == rule || strings.HasPrefix(command, rule+" ")
}
func normalizeCommandPrefix(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(v))), " ")
}
