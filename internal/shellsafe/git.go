package shellsafe

import "strings"

func GitCommandReadOnly(argv []string) bool {
	if len(argv) < 2 || argv[0] != "git" {
		return false
	}
	for _, field := range argv[1:] {
		if ArgContainsUnsafeMeta(field) {
			return false
		}
	}
	subcommandIndex := 1
	for subcommandIndex < len(argv) {
		arg := argv[subcommandIndex]
		switch {
		case arg == "-c" || arg == "--config-env" || strings.HasPrefix(arg, "-c") || strings.HasPrefix(arg, "--config-env="):
			return false
		case arg == "-C":
			if subcommandIndex+1 >= len(argv) || !gitRelativePathAllowed(argv[subcommandIndex+1], false) {
				return false
			}
			subcommandIndex += 2
			continue
		case strings.HasPrefix(arg, "-C"):
			if !gitRelativePathAllowed(strings.TrimPrefix(arg, "-C"), false) {
				return false
			}
			subcommandIndex++
			continue
		case strings.HasPrefix(arg, "-"):
			return false
		default:
			goto found
		}
	}
	return false

found:
	subcommand := argv[subcommandIndex]
	args := argv[subcommandIndex+1:]
	switch subcommand {
	case "status", "rev-parse":
		return gitArgsAreReadOnly(args)
	case "symbolic-ref":
		return gitArgsAreReadOnly(args) && gitSymbolicRefArgsAreReadOnly(args)
	case "branch":
		return gitArgsAreReadOnly(args) && gitBranchArgsAreReadOnly(args)
	case "remote":
		return gitArgsAreReadOnly(args) && gitRemoteArgsAreReadOnly(args)
	case "config":
		return len(args) >= 1 && args[0] == "--get" && gitArgsAreReadOnly(args)
	case "diff":
		return gitArgsAreReadOnly(args) && gitDiffArgsAreReadOnly(args)
	case "show", "log":
		return gitArgsAreReadOnly(args)
	default:
		return false
	}
}

func gitSymbolicRefArgsAreReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	refs := 0
	for _, arg := range args {
		switch arg {
		case "--short", "-q", "--quiet":
			continue
		default:
			if strings.HasPrefix(arg, "-") {
				return false
			}
			refs++
		}
	}
	return refs == 1
}

func gitBranchArgsAreReadOnly(args []string) bool {
	sawList := false
	for _, arg := range args {
		switch arg {
		case "--show-current", "--all", "--remotes", "--list", "--verbose", "--color", "--no-color", "-a", "-r", "-l", "-v", "-vv":
			if arg == "--list" || arg == "-l" {
				sawList = true
			}
			continue
		default:
			if strings.HasPrefix(arg, "--color=") {
				continue
			}
			if sawList && !strings.HasPrefix(arg, "-") {
				continue
			}
			return false
		}
	}
	return true
}

func gitRemoteArgsAreReadOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if len(args) == 1 && args[0] == "-v" {
		return true
	}
	if len(args) >= 2 && args[0] == "get-url" {
		return true
	}
	return false
}

func gitArgsAreReadOnly(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--output=") {
			return false
		}
		switch arg {
		case "--output", "--ext-diff", "--external-diff", "--textconv":
			return false
		}
	}
	return true
}

func gitDiffArgsAreReadOnly(args []string) bool {
	if !containsArg(args, "--no-index") {
		return true
	}
	paths := gitDiffNoIndexPaths(args)
	if len(paths) != 2 {
		return false
	}
	return gitRelativePathAllowed(paths[0], true) && gitRelativePathAllowed(paths[1], false)
}

func gitDiffNoIndexPaths(args []string) []string {
	paths := make([]string, 0, 2)
	endOfOptions := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !endOfOptions && arg == "--" {
			endOfOptions = true
			continue
		}
		if !endOfOptions && strings.HasPrefix(arg, "-") {
			if gitDiffFlagConsumesNextArg(arg) && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		paths = append(paths, arg)
	}
	return paths
}

func gitDiffFlagConsumesNextArg(arg string) bool {
	// Conservative list of read-only git diff flags that consume the next arg.
	// If this list misses an option, --no-index parsing may reject the command
	// rather than accidentally treating an option value as a safe path.
	switch arg {
	case "--relative", "--diff-filter", "--word-diff-regex", "--color-words", "--ws-error-highlight", "--abbrev", "--break-rewrites", "--find-renames", "--find-copies", "--diff-algorithm", "--inter-hunk-context", "-S", "-G", "-O":
		return true
	default:
		return false
	}
}

func gitRelativePathAllowed(path string, allowDevNull bool) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if allowDevNull && path == "/dev/null" {
		return true
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~") || strings.HasPrefix(path, "-") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func ArgContainsUnsafeMeta(arg string) bool {
	return strings.ContainsAny(arg, "$`;&|<>\n\r")
}

func containsArg(argv []string, want string) bool {
	for _, got := range argv {
		if got == want {
			return true
		}
	}
	return false
}
