package tools

import (
	"runtime"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

func (b *Toolset) shellTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "exec_shell",
			description: execShellDescription(runtime.GOOS),
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
			readOnlyCheck: shellReadOnlyCheck,
			fn:            b.execShell,
		},
		toolFn{
			name:        "exec_shell_wait",
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
			fn:       b.execShellWait,
		},
	}
}

func execShellDescription(goos string) string {
	var b strings.Builder
	b.WriteString("Run a shell command from the current Whale workspace. ")
	if goos == "windows" {
		b.WriteString("Commands are executed with PowerShell on Windows. ")
		b.WriteString("Use PowerShell syntax, Windows-compatible paths, and Windows environment variables. ")
		b.WriteString("Do not assume /tmp, grep -r, bash syntax, or Linux-only shell behavior. ")
		b.WriteString("Prefer native commands such as Get-ChildItem, Get-Content, Select-String, Test-Path, and Measure-Object. ")
	} else {
		b.WriteString("Commands are executed with ")
		b.WriteString(shell.ShellDisplayNameForGOOS(goos))
		b.WriteString(". ")
	}
	b.WriteString("Commands default to the workspace root; do not assume synthetic paths like /workspace. ")
	b.WriteString("Use relative paths, or set cwd to a subdirectory inside the workspace, instead of prefixing commands with cd.")
	return b.String()
}

var shellUnixReadOnlyAllowPrefixes = []string{
	"ls", "pwd", "echo", "cat", "head", "tail", "wc", "file", "tree", "find", "grep", "rg",
	"cargo test", "cargo check", "cargo clippy", "rustc --version",
	"python3 --version",
}

var shellWindowsReadOnlyAllowPrefixes = []string{
	"Get-ChildItem", "Get-Location", "Write-Output", "Get-Content", "Select-String",
	"Measure-Object", "Test-Path", "Resolve-Path", "Get-Item", "Get-Command",
}

var shellDevelopmentReadOnlyAllowPrefixes = []string{
	"git status", "git diff", "git log", "git show", "git branch", "git remote", "git rev-parse", "git config --get",
	"go test", "go vet", "go version",
	"python --version", "node --version", "npm --version", "npx --version",
}

func shellReadOnlyAllowPrefixesForGOOS(goos string) []string {
	prefixes := make([]string, 0, len(shellDevelopmentReadOnlyAllowPrefixes)+len(shellUnixReadOnlyAllowPrefixes)+len(shellWindowsReadOnlyAllowPrefixes))
	switch goos {
	case "windows":
		prefixes = append(prefixes, shellWindowsReadOnlyAllowPrefixes...)
	default:
		prefixes = append(prefixes, shellUnixReadOnlyAllowPrefixes...)
	}
	prefixes = append(prefixes, shellDevelopmentReadOnlyAllowPrefixes...)
	return prefixes
}

func shellReadOnlyCheck(args map[string]any) bool {
	return shellReadOnlyCheckForGOOS(runtime.GOOS, args)
}

func shellReadOnlyCheckForGOOS(goos string, args map[string]any) bool {
	cmd, _ := args["command"].(string)
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if cmd == "" {
		return false
	}
	for _, prefix := range shellReadOnlyAllowPrefixesForGOOS(goos) {
		p := strings.ToLower(strings.TrimSpace(prefix))
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return true
		}
	}
	return false
}
