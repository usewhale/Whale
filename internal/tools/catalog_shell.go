package tools

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy/shellrisk"
	"github.com/usewhale/whale/internal/shell"
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
	decision := shellrisk.Classify(cmd)
	return decision.Allow && decision.Level == shellrisk.LevelSafeRead
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
