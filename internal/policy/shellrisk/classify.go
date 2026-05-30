package shellrisk

import "github.com/usewhale/whale/internal/shellsafe"

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
