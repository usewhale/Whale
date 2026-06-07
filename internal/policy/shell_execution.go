package policy

import (
	"encoding/json"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type ShellExecutionRequest struct {
	Source  string
	Command string
	CWD     string
}

func shellExecutionRequestFromToolCall(call core.ToolCall) (ShellExecutionRequest, bool) {
	if call.Name != "shell_run" {
		return ShellExecutionRequest{}, false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return ShellExecutionRequest{}, false
	}
	cmd, _ := body["command"].(string)
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ShellExecutionRequest{}, false
	}
	cwd, _ := body["cwd"].(string)
	return ShellExecutionRequest{
		Source:  "shell_run",
		Command: cmd,
		CWD:     strings.TrimSpace(cwd),
	}, true
}

func shellCommandFromInput(input string) string {
	req, ok := shellExecutionRequestFromToolCall(core.ToolCall{Name: "shell_run", Input: input})
	if !ok {
		return ""
	}
	return req.Command
}
