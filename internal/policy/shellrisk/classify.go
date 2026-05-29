package shellrisk

import (
	"strings"
	"unicode"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shellsafe"
)

const (
	CodeSafeRead      = "safe_read"
	CodeBoundedWrite  = "bounded_write"
	CodeNeedsApproval = "needs_approval"
	CodeParseFailed   = "parse_failed"
	CodeUnsafeArgs    = "unsafe_args"
)

const (
	LevelSafeRead      = "safe_read"
	LevelBoundedWrite  = "bounded_write"
	LevelNeedsApproval = "needs_approval"
	LevelBlocked       = "blocked"
)

type Decision struct {
	Allow        bool
	Code         string
	Level        string
	Reason       string
	ApprovalKeys []string
	SessionScope string
	WriteScopes  []string
}

var readOnlyPrefixes = []string{
	"ls", "pwd", "echo", "cat", "head", "tail", "wc", "file", "tree", "find", "grep", "rg", "uptime",
	"cal",
	"id", "uname", "whoami", "free", "df", "du", "locale", "groups", "nproc",
	"stat", "strings", "hexdump", "od", "nl",
	"basename", "dirname", "realpath", "readlink",
	"cut", "paste", "tr", "column", "tac", "rev", "fold", "expand", "unexpand", "comm", "cmp", "numfmt",
	"true", "false", "which", "type", "expr", "test", "getconf", "seq", "tsort", "pr",
	"go version",
	"rustc --version",
	"python --version", "python3 --version", "node --version", "npm --version", "npx --version", "cargo --version", "deno --version", "bun --version",
}

func Classify(command string) Decision {
	if base, ok := stripTrailingSafeStderrRedirect(command); ok {
		return Classify(base)
	}
	if parts, ok := shellsafe.SplitSequence(command); ok {
		for _, part := range parts {
			decision := Classify(part)
			if !decision.Allow || decision.Level != LevelSafeRead {
				return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "; list contains a command that is not safe read-only"}
			}
		}
		return safeReadDecision("; list of read-only commands", "shell:safe:sequence")
	}
	if parts, ok := shellsafe.SplitAndList(command); ok {
		for _, part := range parts {
			decision := Classify(part)
			if !decision.Allow || decision.Level != LevelSafeRead {
				return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "&& list contains a command that is not safe read-only"}
			}
		}
		return safeReadDecision("&& list of read-only commands", "shell:safe:and-list")
	}
	if parts, ok := shellsafe.SplitPipeline(command); ok {
		for _, part := range parts {
			decision := Classify(part)
			if !decision.Allow || decision.Level != LevelSafeRead {
				return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "pipeline contains a command that is not safe read-only"}
			}
		}
		return safeReadDecision("pipeline of built-in read-only commands", "shell:safe:pipeline")
	}
	argv, ok := parseSimpleShellCommand(command)
	if !ok || len(argv) == 0 {
		return Decision{Code: CodeParseFailed, Level: LevelNeedsApproval, Reason: "command is not a simple shell command"}
	}
	lower := lowerArgv(argv)
	if autoAllowShellCommandHasUnsafeArgs(lower) {
		return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "command contains arguments that may mutate files or execute arbitrary code"}
	}
	if autoAllowMakeHasExtraArgs(lower) {
		return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "make bounded-write targets must not include extra targets or args"}
	}
	if decision := classifyBuiltinReadOnly(argv, lower); decision.Code != "" {
		return decision
	}
	if lower[0] == "git" {
		if shellsafe.GitCommandReadOnly(argv) {
			return safeReadDecision("git read-only command", semanticKey("safe", lower))
		}
		return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "git command is not classified as read-only"}
	}
	if decision := classifyBoundedWrite(lower); decision.Code != "" {
		return decision
	}
	for _, prefix := range readOnlyPrefixes {
		if argvHasPrefix(lower, prefix) {
			return safeReadDecision("built-in read-only command", semanticKey("safe", lower))
		}
	}
	return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "command is not classified as safe read-only or bounded-write"}
}

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

func classifyPrintf(lower []string) Decision {
	for _, arg := range lower[1:] {
		if strings.ContainsAny(arg, "/") && strings.HasPrefix(arg, "-") {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "printf option is not on the safe display allowlist"}
		}
	}
	return safeReadDecision("printf display command", "shell:safe:printf")
}

func classifyBoundedWrite(lower []string) Decision {
	switch lower[0] {
	case "go":
		if len(lower) >= 2 {
			switch lower[1] {
			case "test":
				if hasAnyFlagPrefix(lower[2:], "-exec", "-toolexec") {
					return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "go test can run an execution wrapper with this option and requires exact approval"}
				}
				if hasAnyFlagPrefix(lower[2:], "-c") {
					return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "go test -c emits a test binary and requires exact approval"}
				}
				if hasAnyFlagPrefix(lower[2:], "-coverprofile", "-cpuprofile", "-memprofile", "-blockprofile", "-mutexprofile", "-trace", "-o") {
					return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "go test writes to an explicit output path with this option"}
				}
				return boundedWriteDecision("go test may write build and test cache files", "shell:bounded:go:test", "Go build/test cache")
			case "build":
				if hasAnyFlagPrefix(lower[2:], "-o") {
					return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "go build writes to an explicit output path with this option"}
				}
				return Decision{Code: CodeNeedsApproval, Level: LevelNeedsApproval, Reason: "go build may emit a workspace binary and requires exact approval"}
			case "vet":
				return boundedWriteDecision("go vet may write build cache files", "shell:bounded:go:vet", "Go build cache")
			}
		}
	case "make":
		if len(lower) == 2 {
			switch lower[1] {
			case "build", "test", "test-tui", "test-evals", "test-windows", "fmt-check", "vet":
				return boundedWriteDecision("make "+lower[1]+" may write project-local build or test artifacts", "shell:bounded:make:"+lower[1], "project-local build/test artifacts")
			}
		}
	case "cargo":
		if len(lower) >= 2 {
			switch lower[1] {
			case "build", "test", "check", "clippy":
				if hasAnyFlagPrefix(lower[2:], "--target-dir") {
					return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "cargo writes to an explicit target directory with this option"}
				}
				return boundedWriteDecision("cargo "+lower[1]+" may write target build artifacts", "shell:bounded:cargo:"+lower[1], "Cargo target directory")
			}
		}
	case "npm":
		if len(lower) >= 2 {
			switch {
			case lower[1] == "test":
				return boundedWriteDecision("npm test may write project-local test artifacts", "shell:bounded:npm:test", "project-local test artifacts")
			case len(lower) >= 3 && lower[1] == "run" && npmBoundedScript(lower[2]):
				return boundedWriteDecision("npm run "+lower[2]+" may write project-local build or test artifacts", "shell:bounded:npm:run-"+lower[2], "project-local build/test artifacts")
			}
		}
	case "pnpm":
		if len(lower) >= 2 {
			switch {
			case lower[1] == "test":
				return boundedWriteDecision("pnpm test may write project-local test artifacts", "shell:bounded:pnpm:test", "project-local test artifacts")
			case len(lower) >= 3 && lower[1] == "run" && npmBoundedScript(lower[2]):
				return boundedWriteDecision("pnpm run "+lower[2]+" may write project-local build or test artifacts", "shell:bounded:pnpm:run-"+lower[2], "project-local build/test artifacts")
			}
		}
	case "npx":
		if len(lower) >= 2 {
			switch lower[1] {
			case "jest", "vitest":
				if hasKnownTestOutputFlag(lower[2:]) {
					return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: lower[1] + " writes to an explicit output path with this option"}
				}
				return boundedWriteDecision("npx "+lower[1]+" may write project-local test artifacts", "shell:bounded:npx:"+lower[1], "project-local test artifacts")
			case "tsc":
				if len(lower) >= 3 && lower[2] == "--noemit" {
					return boundedWriteDecision("npx tsc --noEmit may write compiler cache files", "shell:bounded:npx:tsc-noemit", "project-local compiler cache")
				}
			}
		}
	case "pytest":
		if hasKnownTestOutputFlag(lower[1:]) {
			return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: "pytest writes to an explicit output path with this option"}
		}
		return boundedWriteDecision("pytest may write project-local test artifacts", "shell:bounded:pytest", "project-local test artifacts")
	case "python", "python3":
		if len(lower) >= 3 && lower[1] == "-m" && lower[2] == "pytest" {
			if hasKnownTestOutputFlag(lower[3:]) {
				return Decision{Code: CodeUnsafeArgs, Level: LevelNeedsApproval, Reason: lower[0] + " -m pytest writes to an explicit output path with this option"}
			}
			return boundedWriteDecision(lower[0]+" -m pytest may write project-local test artifacts", "shell:bounded:"+lower[0]+":-m-pytest", "project-local test artifacts")
		}
	case "deno":
		if len(lower) >= 2 && lower[1] == "test" {
			return boundedWriteDecision("deno test may write project-local test artifacts", "shell:bounded:deno:test", "project-local test artifacts")
		}
	case "bun":
		if len(lower) >= 2 && lower[1] == "test" {
			return boundedWriteDecision("bun test may write project-local test artifacts", "shell:bounded:bun:test", "project-local test artifacts")
		}
	}
	return Decision{}
}

func hasKnownTestOutputFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--outputfile" ||
			arg == "--output-file" ||
			strings.HasPrefix(arg, "--outputfile=") ||
			strings.HasPrefix(arg, "--output-file=") ||
			arg == "--junitxml" ||
			arg == "--junit-xml" ||
			strings.HasPrefix(arg, "--junitxml=") ||
			strings.HasPrefix(arg, "--junit-xml=") ||
			arg == "--html" ||
			strings.HasPrefix(arg, "--html=") ||
			strings.HasPrefix(arg, "--cov-report=xml:") ||
			strings.HasPrefix(arg, "--cov-report=html:") ||
			strings.HasPrefix(arg, "--cov-report=lcov:") ||
			strings.HasPrefix(arg, "--cov-report=json:") {
			return true
		}
	}
	return false
}

func safeReadDecision(reason, key string) Decision {
	return Decision{
		Allow:        true,
		Code:         CodeSafeRead,
		Level:        LevelSafeRead,
		Reason:       reason,
		ApprovalKeys: []string{key},
		SessionScope: "this safe shell command family",
	}
}

func boundedWriteDecision(reason, key, writeScope string) Decision {
	return Decision{
		Code:         CodeBoundedWrite,
		Level:        LevelBoundedWrite,
		Reason:       reason,
		ApprovalKeys: []string{key},
		SessionScope: "this bounded shell command family",
		WriteScopes:  []string{writeScope},
	}
}

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

func isCommandName(v string) bool {
	if strings.TrimSpace(v) == "" || strings.Contains(v, "/") || strings.HasPrefix(v, "-") {
		return false
	}
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '_', '.', '-', '+':
			continue
		default:
			return false
		}
	}
	return true
}

func lowerArgv(argv []string) []string {
	lower := make([]string, 0, len(argv))
	for _, arg := range argv {
		lower = append(lower, strings.ToLower(arg))
	}
	return lower
}

func argvHasPrefix(argv []string, prefix string) bool {
	prefixArgv := strings.Fields(strings.ToLower(strings.TrimSpace(prefix)))
	if len(argv) < len(prefixArgv) {
		return false
	}
	for i, want := range prefixArgv {
		if argv[i] != want {
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

func hasAnyFlagPrefix(args []string, prefixes ...string) bool {
	for _, arg := range args {
		for _, prefix := range prefixes {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return true
			}
		}
	}
	return false
}

func npmBoundedScript(script string) bool {
	switch script {
	case "build", "test", "lint", "typecheck":
		return true
	default:
		return false
	}
}

func semanticKey(kind string, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	switch argv[0] {
	case "git":
		if len(argv) >= 2 {
			return "shell:" + kind + ":git:" + argv[1]
		}
	case "python", "python3":
		if len(argv) >= 3 && argv[1] == "-m" {
			return "shell:" + kind + ":" + argv[0] + ":-m-" + argv[2]
		}
	case "npx":
		if len(argv) >= 2 {
			return "shell:" + kind + ":npx:" + argv[1]
		}
	default:
		return "shell:" + kind + ":" + argv[0]
	}
	return "shell:" + kind + ":" + argv[0]
}
