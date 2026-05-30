package shellrisk

import (
	"strings"
)

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
