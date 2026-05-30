package shellrisk

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func autoAllowShellCommandHasUnsafeArgs(argv []string) bool {
	for _, field := range argv[1:] {
		if argContainsUnsafeExpansionMeta(field) {
			return true
		}
	}
	switch {
	case argvHasPrefix(argv, "find"):
		for _, field := range argv {
			switch field {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fls":
				return true
			}
			if strings.HasPrefix(field, "-fprint") {
				return true
			}
		}
	case argvHasPrefix(argv, "git diff"), argvHasPrefix(argv, "git show"), argvHasPrefix(argv, "git log"):
		for _, field := range argv {
			if field == "--output" || strings.HasPrefix(field, "--output=") || field == "--ext-diff" || field == "--external-diff" || field == "--textconv" {
				return true
			}
		}
	case argvHasPrefix(argv, "rg"):
		for _, field := range argv {
			if field == "--pre" || strings.HasPrefix(field, "--pre=") {
				return true
			}
		}
	}
	for _, field := range argv {
		switch field {
		case "--fix", "--write", "--update", "--update-snapshot", "--updatesnapshot":
			return true
		}
		if strings.HasPrefix(field, "--fix=") ||
			strings.HasPrefix(field, "--write=") ||
			strings.HasPrefix(field, "--update=") ||
			strings.HasPrefix(field, "--update-snapshot=") ||
			strings.HasPrefix(field, "--updatesnapshot=") {
			return true
		}
	}
	if (argvHasPrefix(argv, "npx jest") || argvHasPrefix(argv, "npx vitest") || argvHasPrefix(argv, "npx vitest run")) && core.ContainsArg(argv, "-u") {
		return true
	}
	return false
}
func argContainsUnsafeExpansionMeta(arg string) bool {
	return strings.ContainsAny(arg, "$`&<>\n\r")
}
func autoAllowMakeHasExtraArgs(argv []string) bool {
	if len(argv) == 0 || argv[0] != "make" {
		return false
	}
	switch {
	case argvHasPrefix(argv, "make test"),
		argvHasPrefix(argv, "make test-tui"),
		argvHasPrefix(argv, "make test-evals"),
		argvHasPrefix(argv, "make test-windows"),
		argvHasPrefix(argv, "make fmt-check"),
		argvHasPrefix(argv, "make vet"),
		argvHasPrefix(argv, "make build"):
		return len(argv) != 2
	default:
		return false
	}
}
