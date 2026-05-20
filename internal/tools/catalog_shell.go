package tools

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
	"github.com/usewhale/whale/internal/shellsafe"
)

func (b *Toolset) shellTools() []core.Tool {
	rt := shell.DescribeRuntime()
	return []core.Tool{
		toolFn{
			name:        "shell_run",
			description: shellRunDescriptionFor(rt),
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"command":    map[string]any{"type": "string", "description": "Shell command to execute"},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": maxBackgroundShellTimeoutMS, "description": "Command timeout in milliseconds"},
					"background": map[string]any{"type": "boolean", "description": "When true, return immediately with task_id"},
					"cwd":        map[string]any{"type": "string", "description": "Optional working directory relative to the workspace root. Must stay inside the workspace. Use this for subdirectory commands instead of cd."},
				},
				"required": []string{"command"},
			},
			readOnlyCheck: shellReadOnlyCheckFor(rt),
			fn:            b.shellRun,
		},
		toolFn{
			name:        "shell_wait",
			description: "Wait for a background shell task by task_id and return status plus captured output when complete.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"task_id":    map[string]any{"type": "string"},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 120000},
				},
				"required": []string{"task_id"},
			},
			readOnly: true,
			fn:       b.shellWait,
		},
	}
}

func shellRunDescription() string {
	return shellRunDescriptionFor(shell.DescribeRuntime())
}

func shellRunDescriptionFor(rt shell.RuntimeDescription) string {
	base := "Run a shell command from the current Whale workspace. Commands default to the workspace root; do not assume synthetic paths like /workspace. Use relative paths, or set cwd to a subdirectory inside the workspace, instead of prefixing commands with cd."
	if guidance := rt.ToolGuidance(); strings.TrimSpace(guidance) != "" {
		return base + " " + strings.TrimSpace(guidance)
	}
	return base
}

var shellReadOnlyAllowPrefixes = []string{
	"ls", "pwd", "echo", "cat", "head", "tail", "wc", "file", "tree", "find", "grep", "rg",
	"go version",
	"rustc --version",
	"python --version", "python3 --version", "node --version", "npm --version", "npx --version", "cargo --version", "deno --version", "bun --version",
}

func shellReadOnlyCheck(args map[string]any) bool {
	return shellReadOnlyCheckFor(shell.DescribeRuntime())(args)
}

func shellReadOnlyCheckFor(rt shell.RuntimeDescription) func(args map[string]any) bool {
	return func(args map[string]any) bool {
		return shellReadOnlyCheckWithRuntime(rt, args)
	}
}

func shellReadOnlyCheckWithRuntime(rt shell.RuntimeDescription, args map[string]any) bool {
	if rt.Spec.Kind != shell.KindPOSIX {
		return false
	}
	cmd, _ := args["command"].(string)
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	if parts, ok := shellsafe.SplitAndList(cmd); ok {
		for _, part := range parts {
			if !shellReadOnlyCheckWithRuntime(rt, map[string]any{"command": part}) {
				return false
			}
		}
		return true
	}
	if parts, ok := shellsafe.SplitPipeline(cmd); ok {
		for _, part := range parts {
			if !shellReadOnlyCheckWithRuntime(rt, map[string]any{"command": part}) {
				return false
			}
		}
		return true
	}
	argv, ok := parsePOSIXReadOnlyShellCommand(cmd)
	if !ok || len(argv) == 0 {
		return false
	}
	if argv[0] == "git" {
		return shellsafe.GitCommandReadOnly(argv)
	}
	lowerArgv := lowerShellArgv(argv)
	if shellReadOnlyCommandHasUnsafeArgs(lowerArgv) {
		return false
	}
	if lowerArgv[0] == "sed" {
		return sedPrintRangeReadOnly(argv)
	}
	for _, prefix := range shellReadOnlyAllowPrefixes {
		if shellArgvHasPrefix(lowerArgv, prefix) {
			return true
		}
	}
	return false
}

func shellReadOnlyCommandHasUnsafeArgs(argv []string) bool {
	switch {
	case shellArgvHasPrefix(argv, "find"):
		for _, field := range argv {
			switch field {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fls":
				return true
			}
			if strings.HasPrefix(field, "-fprint") {
				return true
			}
		}
	case shellArgvHasPrefix(argv, "rg"):
		for _, field := range argv {
			if field == "--pre" || strings.HasPrefix(field, "--pre=") {
				return true
			}
		}
	}
	return false
}

func shellArgvHasPrefix(argv []string, prefix string) bool {
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

func lowerShellArgv(argv []string) []string {
	lower := make([]string, 0, len(argv))
	for _, arg := range argv {
		lower = append(lower, strings.ToLower(arg))
	}
	return lower
}

func parsePOSIXReadOnlyShellCommand(cmd string) ([]string, bool) {
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

	for _, r := range cmd {
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
		case posixReadOnlyRejectedRune(r):
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
	if len(argv) == 0 {
		return nil, false
	}
	return argv, true
}

func posixReadOnlyRejectedRune(r rune) bool {
	switch r {
	case '\\', '$', '`', ';', '|', '&', '<', '>', '\n', '\r', '(', ')', '{', '}', '#', '*', '?', '[', ']':
		return true
	default:
		return false
	}
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
