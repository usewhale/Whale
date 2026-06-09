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
			description: shellRunDescriptionFor(rt, b.foregroundShellWait),
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"command":        map[string]any{"type": "string", "description": "Shell command to execute"},
					"yield_time_ms":  map[string]any{"type": "integer", "minimum": 1, "maximum": b.foregroundShellWait.MaxMS, "description": shellRunYieldTimeDescription(b.foregroundShellWait)},
					"max_runtime_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": maxBackgroundShellTimeoutMS, "description": fmt.Sprintf("Maximum process runtime before Whale cancels the task; defaults to %d and clamps at %d.", defaultBackgroundShellTimeoutMS, maxBackgroundShellTimeoutMS)},
					"timeout_ms":     map[string]any{"type": "integer", "minimum": 1, "maximum": maxBackgroundShellTimeoutMS, "description": shellRunTimeoutDescription(b.foregroundShellWait)},
					"background":     map[string]any{"type": "boolean", "description": "When true, return immediately with task_id after starting the process."},
					"cwd":            map[string]any{"type": "string", "description": "Optional working directory relative to the workspace root. Must stay inside the workspace. Use this for subdirectory commands instead of cd."},
					"mode":           map[string]any{"type": "string", "enum": []string{"auto", "pipe", "pty"}, "description": "Execution transport. auto uses a PTY for recognized interactive or authentication commands and a normal pipe otherwise; pty enables write_stdin interaction."},
				},
				"required": []string{"command"},
			},
			readOnlyCheck: shellReadOnlyCheckFor(rt),
			capabilities:  []string{"shell.read", "shell.run"},
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
			capabilities: []string{"shell.read", "shell.run"},
			fn:           b.shellWait,
		},
		toolFn{
			name:        "write_stdin",
			description: "Write stdin bytes or control keys to a running PTY shell task by task_id, then wait briefly for output. Use shell_wait to poll without writing.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string"},
					"chars":   map[string]any{"type": "string", "description": "Literal bytes to send to the PTY stdin. For control keys, prefer keys instead of escape sequences."},
					"keys": map[string]any{
						"type":        "array",
						"description": "Control keys to send after chars. Use [\"enter\"] to press ENTER.",
						"items":       map[string]any{"type": "string", "enum": []string{"enter", "tab", "escape", "ctrl-c"}},
					},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": maxShellWaitTimeoutMS, "description": shellWaitTimeoutDescription()},
				},
				"required": []string{"task_id"},
			},
			readOnly:     false,
			capabilities: []string{"terminal.write"},
			fn:           b.writeStdin,
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
			capabilities: []string{"shell.read", "shell.run"},
			fn:           b.shellCancel,
		},
	}
}

func shellRunDescriptionFor(rt shell.RuntimeDescription, waitCfg foregroundShellWaitConfig) string {
	base := fmt.Sprintf("Run a shell command from the current Whale workspace as a managed process session. Commands default to the workspace root; do not assume synthetic paths like /workspace. Use relative paths, or set cwd to a subdirectory inside the workspace, instead of prefixing commands with cd. yield_time_ms defaults to %dms and clamps at %dms; when the process is still running Whale returns a task_id and keeps the process alive. Continue with shell_wait instead of rerunning the same command. Before running destructive workspace restore operations such as git checkout -- <path>, git restore, git reset --hard, or git clean, prefer Whale's /rewind command or /checkpoint alias unless the user explicitly asked to discard local changes.", waitCfg.DefaultMS, waitCfg.MaxMS)
	base += " Prefer dedicated Whale tools for ordinary file exploration: use grep for content search, search_files for file names, read_file for file contents, and list_dir for directory listings. Avoid wrapping grep, rg, cat, head, tail, sed, or awk in shell_run for routine workspace reads unless the user explicitly asks for a shell command or the task needs real terminal semantics; split complex file inspection into dedicated tool calls instead of for/while/$() shell scripts."
	if guidance := rt.ToolGuidance(); strings.TrimSpace(guidance) != "" {
		return base + " " + strings.TrimSpace(guidance)
	}
	return base
}

func shellRunTimeoutDescription(waitCfg foregroundShellWaitConfig) string {
	return fmt.Sprintf("Deprecated alias for yield_time_ms when yield_time_ms is omitted. Foreground yield defaults to %d and clamps at %d. Use max_runtime_ms for the process runtime limit.", waitCfg.DefaultMS, waitCfg.MaxMS)
}

func shellRunYieldTimeDescription(waitCfg foregroundShellWaitConfig) string {
	return fmt.Sprintf("How long to wait for output before yielding a task_id. Defaults to %d and clamps at %d. Yield timeout does not cancel the process.", waitCfg.DefaultMS, waitCfg.MaxMS)
}

func shellWaitTimeoutDescription() string {
	return fmt.Sprintf("How long to poll the existing task in milliseconds; defaults to %d and clamps at %d. Timeout does not cancel the process.", defaultShellWaitTimeoutMS, maxShellWaitTimeoutMS)
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
