package tools

import (
	"fmt"
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
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": maxBackgroundShellTimeoutMS, "description": shellRunTimeoutDescription()},
					"background": map[string]any{"type": "boolean", "description": fmt.Sprintf("When true, return immediately with task_id. The task runtime defaults to %dms.", defaultBackgroundShellTimeoutMS)},
					"cwd":        map[string]any{"type": "string", "description": "Optional working directory relative to the workspace root. Must stay inside the workspace. Use this for subdirectory commands instead of cd."},
				},
				"required": []string{"command"},
			},
			readOnlyCheck: shellReadOnlyCheckFor(rt),
			capabilities:  []string{"shell.read", "shell.write"},
			fn:            b.shellRun,
		},
		toolFn{
			name:        "shell_wait",
			description: "Wait for a background shell task by task_id. If it is still running, return partial output and any diagnosis; if complete, return the final output.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"task_id":    map[string]any{"type": "string"},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": maxShellWaitTimeoutMS, "description": shellWaitTimeoutDescription()},
				},
				"required": []string{"task_id"},
			},
			readOnly:     true,
			capabilities: []string{"shell.read", "shell.write"},
			fn:           b.shellWait,
		},
		toolFn{
			name:        "shell_cancel",
			description: "Cancel a running background shell task by task_id.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string"},
				},
				"required": []string{"task_id"},
			},
			readOnly:     true,
			capabilities: []string{"shell.read", "shell.write"},
			fn:           b.shellCancel,
		},
	}
}

func shellRunDescription() string {
	return shellRunDescriptionFor(shell.DescribeRuntime())
}

func shellRunDescriptionFor(rt shell.RuntimeDescription) string {
	base := fmt.Sprintf("Run a shell command from the current Whale workspace. Commands default to the workspace root; do not assume synthetic paths like /workspace. Use relative paths, or set cwd to a subdirectory inside the workspace, instead of prefixing commands with cd. Foreground wait defaults to %dms and clamps at %dms; long-running commands can return a background task_id when the foreground wait expires. Continue with shell_wait instead of rerunning the same command. Before running destructive workspace restore operations such as git checkout -- <path>, git restore, git reset --hard, or git clean, prefer Whale's /rewind command or /checkpoint alias unless the user explicitly asked to discard local changes.", defaultForegroundShellWaitMS, maxForegroundShellWaitMS)
	if guidance := rt.ToolGuidance(); strings.TrimSpace(guidance) != "" {
		return base + " " + strings.TrimSpace(guidance)
	}
	return base
}

func shellRunTimeoutDescription() string {
	return fmt.Sprintf("Foreground wait in milliseconds for normal shell_run calls; defaults to %d and clamps at %d. Long-running commands may return a background task_id when the foreground wait expires. With background=true, this is the task runtime limit; defaults to %d and clamps at %d.", defaultForegroundShellWaitMS, maxForegroundShellWaitMS, defaultBackgroundShellTimeoutMS, maxBackgroundShellTimeoutMS)
}

func shellWaitTimeoutDescription() string {
	return fmt.Sprintf("How long to wait for completion in milliseconds; defaults to %d and clamps at %d.", defaultShellWaitTimeoutMS, maxShellWaitTimeoutMS)
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
