package policy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type ApprovalMode string

const (
	ApprovalModeOnRequest ApprovalMode = "on-request"
	ApprovalModeNever     ApprovalMode = "never"
)

func ParseApprovalMode(v string) (ApprovalMode, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "on-request", "on_request":
		return ApprovalModeOnRequest, nil
	case "never", "never-ask", "never_ask":
		return ApprovalModeNever, nil
	default:
		return "", fmt.Errorf("invalid approval mode: %s", v)
	}
}

type PolicyDecision struct {
	Allow            bool
	RequiresApproval bool
	Reason           string
	Code             string
	Phase            string
	MatchedRule      string
}

type ToolPolicy interface {
	Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision
}

type DefaultToolPolicy struct {
	Mode          ApprovalMode
	AllowPrefixes []string
	DenyPrefixes  []string
}

func (p DefaultToolPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	mode := p.Mode
	if mode == "" {
		mode = ApprovalModeOnRequest
	}
	if spec.Name == "shell_run" {
		cmd := shellCommandFromInput(call.Input)
		for _, deny := range p.DenyPrefixes {
			if hasDenyCommandPrefix(cmd, deny) {
				return PolicyDecision{
					Allow:       false,
					Reason:      "command blocked by deny prefix",
					Code:        "policy_denied",
					Phase:       "denied",
					MatchedRule: deny,
				}
			}
		}
		for _, allow := range p.AllowPrefixes {
			if hasAllowCommandPrefix(cmd, allow) {
				return PolicyDecision{
					Allow:            true,
					RequiresApproval: false,
					Code:             "allow_prefix",
					Phase:            "allowed",
					MatchedRule:      allow,
				}
			}
		}
	}
	if mode == ApprovalModeNever {
		return PolicyDecision{Allow: true, Code: "auto_allow", Phase: "allowed"}
	}
	if core.IsReadOnlyToolCall(spec, call) {
		return PolicyDecision{Allow: true, Code: "read_only", Phase: "allowed"}
	}
	if spec.Name == "shell_run" {
		cmd := shellCommandFromInput(call.Input)
		if defaultShellAutoAllow(cmd) {
			return PolicyDecision{Allow: true, Code: "shell_auto_allow", Phase: "allowed"}
		}
	}
	if hasCapability(spec, "mutates_state") {
		return PolicyDecision{
			Allow:            true,
			RequiresApproval: true,
			Reason:           "tool mutates persistent state",
			Code:             "approval_required",
			Phase:            "needs_approval",
		}
	}
	switch spec.Name {
	case "edit", "write", "apply_patch", "shell_run":
	default:
		if strings.HasPrefix(spec.Name, "mcp__") {
			return PolicyDecision{
				Allow:            true,
				RequiresApproval: true,
				Reason:           "MCP tool requires approval",
				Code:             "approval_required",
				Phase:            "needs_approval",
			}
		}
		return PolicyDecision{Allow: true, Code: "non_mutating_default", Phase: "allowed"}
	}
	return PolicyDecision{
		Allow:            true,
		RequiresApproval: true,
		Reason:           "tool requires approval",
		Code:             "approval_required",
		Phase:            "needs_approval",
	}
}

func hasCapability(spec core.ToolSpec, capability string) bool {
	want := strings.TrimSpace(strings.ToLower(capability))
	if want == "" {
		return false
	}
	for _, got := range spec.Capabilities {
		if strings.TrimSpace(strings.ToLower(got)) == want {
			return true
		}
	}
	return false
}

func shellCommandFromInput(input string) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return ""
	}
	cmd, _ := body["command"].(string)
	return strings.TrimSpace(cmd)
}

func hasAllowCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(command, "\n\r") || strings.ContainsAny(rule, "\n\r") {
		return false
	}
	return hasSingleLineCommandPrefix(command, rule)
}

func hasDenyCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(rule, "\n\r") {
		return false
	}
	for _, segment := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if hasSingleLineCommandPrefix(segment, rule) {
			return true
		}
	}
	return false
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

var defaultShellAutoAllowPrefixes = []string{
	"ls", "pwd", "echo", "cat", "head", "tail", "wc", "file", "tree", "find", "grep", "rg",
	"git status", "git diff", "git log", "git show", "git branch", "git remote", "git rev-parse", "git config --get",
	"go version",
	"rustc --version",
	"python --version", "python3 --version", "node --version", "npm --version", "npx --version", "cargo --version", "deno --version", "bun --version",
	"go test", "go vet",
	"make test", "make test-tui", "make test-evals", "make test-windows", "make fmt-check", "make vet", "make build",
	"cargo test", "cargo check", "cargo clippy",
	"npm test", "npm run test", "npm run lint", "npm run typecheck",
	"npx vitest run", "npx vitest", "npx jest", "npx tsc --noEmit",
	"pytest", "python -m pytest",
	"deno test", "bun test",
}

func defaultShellAutoAllow(command string) bool {
	argv, ok := parseSimpleShellCommand(command)
	if !ok || len(argv) == 0 {
		return false
	}
	argv = lowerArgv(argv)
	if autoAllowShellCommandHasUnsafeArgs(argv) {
		return false
	}
	if autoAllowMakeHasExtraArgs(argv) {
		return false
	}
	for _, prefix := range defaultShellAutoAllowPrefixes {
		if argvHasPrefix(argv, prefix) {
			return true
		}
	}
	return false
}

func autoAllowShellCommandHasUnsafeArgs(argv []string) bool {
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
	if (argvHasPrefix(argv, "npx jest") || argvHasPrefix(argv, "npx vitest") || argvHasPrefix(argv, "npx vitest run")) && containsArg(argv, "-u") {
		return true
	}
	return false
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

func containsArg(argv []string, want string) bool {
	for _, got := range argv {
		if got == want {
			return true
		}
	}
	return false
}
