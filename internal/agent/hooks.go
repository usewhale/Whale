package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

var resolveHookShell = shell.Resolve

type HookEvent string

const (
	HookEventPreToolUse       HookEvent = "PreToolUse"
	HookEventPostToolUse      HookEvent = "PostToolUse"
	HookEventUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookEventStop             HookEvent = "Stop"
)

const (
	DefaultHookOutputCapBytes = 256 * 1024
)

var defaultHookTimeouts = map[HookEvent]time.Duration{
	HookEventPreToolUse:       5 * time.Second,
	HookEventUserPromptSubmit: 5 * time.Second,
	HookEventPostToolUse:      30 * time.Second,
	HookEventStop:             30 * time.Second,
}

type HookConfig struct {
	Match       string `json:"match,omitempty"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	TimeoutMS   int    `json:"timeout,omitempty"`
	CWD         string `json:"cwd,omitempty"`
}

type HookSettings struct {
	Hooks map[HookEvent][]HookConfig `json:"hooks"`
}

type ResolvedHook struct {
	HookConfig
	Event  HookEvent
	Source string
}

type HookPayload struct {
	Event             HookEvent      `json:"event"`
	CWD               string         `json:"cwd"`
	SessionID         string         `json:"session_id,omitempty"`
	ToolName          string         `json:"tool_name,omitempty"`
	ToolArgs          any            `json:"tool_args,omitempty"`
	ToolResult        string         `json:"tool_result,omitempty"`
	Prompt            string         `json:"prompt,omitempty"`
	LastAssistantText string         `json:"last_assistant_text,omitempty"`
	Turn              int            `json:"turn,omitempty"`
	ToolCall          *core.ToolCall `json:"tool_call,omitempty"`
}

func NewUserPromptSubmitPayload(sessionID, cwd, prompt string) HookPayload {
	return HookPayload{
		Event:     HookEventUserPromptSubmit,
		CWD:       cwd,
		SessionID: sessionID,
		Prompt:    prompt,
	}
}

func NewStopPayload(sessionID, cwd, lastAssistantText string, turn int) HookPayload {
	return HookPayload{
		Event:             HookEventStop,
		CWD:               cwd,
		SessionID:         sessionID,
		LastAssistantText: lastAssistantText,
		Turn:              turn,
	}
}

func NewPreToolUsePayload(sessionID string, call core.ToolCall, toolArgs any) HookPayload {
	return HookPayload{
		Event:     HookEventPreToolUse,
		SessionID: sessionID,
		ToolName:  call.Name,
		ToolArgs:  toolArgs,
		ToolCall:  &call,
	}
}

func NewPostToolUsePayload(sessionID string, call core.ToolCall, toolArgs any, toolResult string) HookPayload {
	return HookPayload{
		Event:      HookEventPostToolUse,
		SessionID:  sessionID,
		ToolName:   call.Name,
		ToolArgs:   toolArgs,
		ToolResult: toolResult,
		ToolCall:   &call,
	}
}

type HookDecision string

const (
	HookDecisionPass    HookDecision = "pass"
	HookDecisionBlock   HookDecision = "block"
	HookDecisionWarn    HookDecision = "warn"
	HookDecisionTimeout HookDecision = "timeout"
	HookDecisionError   HookDecision = "error"
)

type HookOutcome struct {
	Hook       ResolvedHook
	Decision   HookDecision
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMS int64
	Truncated  bool
}

type HookReport struct {
	Event    HookEvent
	Outcomes []HookOutcome
	Blocked  bool
}

type HookSpawnInput struct {
	Command   string
	CWD       string
	Stdin     string
	TimeoutMS int
}

type HookSpawnResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	TimedOut  bool
	SpawnErr  error
	Truncated bool
}

type HookSpawner func(ctx context.Context, in HookSpawnInput) HookSpawnResult

type HookRunner struct {
	hooks     []ResolvedHook
	spawner   HookSpawner
	workspace string
	outputCap int
}

func NewHookRunner(hooks []ResolvedHook, workspace string) *HookRunner {
	cp := append([]ResolvedHook(nil), hooks...)
	return &HookRunner{hooks: cp, workspace: workspace, spawner: defaultHookSpawner, outputCap: DefaultHookOutputCapBytes}
}

func (r *HookRunner) Empty() bool {
	return r == nil || len(r.hooks) == 0
}

func (r *HookRunner) Run(ctx context.Context, payload HookPayload) HookReport {
	out := HookReport{Event: payload.Event}
	if r.Empty() {
		return out
	}
	stdin := payloadToJSONLine(payload)
	for _, h := range r.hooks {
		if h.Event != payload.Event {
			continue
		}
		if !matchesHook(h, payload.ToolName) {
			continue
		}
		start := time.Now()
		timeout := resolveTimeoutMS(h, payload.Event)
		cwd := resolveCWD(r.workspace, h.CWD)
		res := r.spawner(ctx, HookSpawnInput{Command: h.Command, CWD: cwd, Stdin: stdin, TimeoutMS: timeout})
		decision := decideHookOutcome(payload.Event, res)
		oc := HookOutcome{
			Hook:       h,
			Decision:   decision,
			ExitCode:   res.ExitCode,
			Stdout:     strings.TrimSpace(res.Stdout),
			Stderr:     strings.TrimSpace(res.Stderr),
			DurationMS: time.Since(start).Milliseconds(),
			Truncated:  res.Truncated,
		}
		if oc.Stderr == "" && res.SpawnErr != nil {
			oc.Stderr = res.SpawnErr.Error()
		}
		if oc.Stderr == "" && res.TimedOut {
			oc.Stderr = fmt.Sprintf("hook timed out after %dms", timeout)
		}
		out.Outcomes = append(out.Outcomes, oc)
		if decision == HookDecisionBlock {
			out.Blocked = true
			break
		}
	}
	return out
}

func LoadProjectHooks(workspaceRoot string) ([]ResolvedHook, error) {
	hooks, _, err := LoadHooks(workspaceRoot, "")
	return hooks, err
}

func LoadHooks(workspaceRoot, homeDir string) ([]ResolvedHook, []string, error) {
	out := make([]ResolvedHook, 0)
	loaded := make([]string, 0, 2)
	projectPath := filepath.Join(workspaceRoot, ".whale", "settings.json")
	projectHooks, projectLoaded, err := loadHooksFile(projectPath)
	if err != nil {
		return nil, loaded, err
	}
	if projectLoaded {
		out = append(out, projectHooks...)
		loaded = append(loaded, projectPath)
	}
	home := strings.TrimSpace(homeDir)
	if home == "" {
		if v, err := os.UserHomeDir(); err == nil {
			home = v
		}
	}
	if home != "" {
		globalPath := filepath.Join(home, ".whale", "settings.json")
		globalHooks, globalLoaded, err := loadHooksFile(globalPath)
		if err != nil {
			return nil, loaded, err
		}
		if globalLoaded {
			out = append(out, globalHooks...)
			loaded = append(loaded, globalPath)
		}
	}
	return out, loaded, nil
}

func loadHooksFile(path string) ([]ResolvedHook, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var st HookSettings
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, false, nil
	}
	return resolveHooks(st, path), true, nil
}

func resolveHooks(st HookSettings, source string) []ResolvedHook {
	events := []HookEvent{HookEventPreToolUse, HookEventPostToolUse, HookEventUserPromptSubmit, HookEventStop}
	out := make([]ResolvedHook, 0)
	for _, ev := range events {
		list := st.Hooks[ev]
		for _, cfg := range list {
			if strings.TrimSpace(cfg.Command) == "" {
				continue
			}
			out = append(out, ResolvedHook{HookConfig: cfg, Event: ev, Source: source})
		}
	}
	return out
}

func matchesHook(h ResolvedHook, toolName string) bool {
	if h.Event != HookEventPreToolUse && h.Event != HookEventPostToolUse {
		return true
	}
	m := strings.TrimSpace(h.Match)
	if m == "" || m == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + m + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(toolName)
}

func resolveTimeoutMS(h ResolvedHook, event HookEvent) int {
	if h.TimeoutMS > 0 {
		return h.TimeoutMS
	}
	if d, ok := defaultHookTimeouts[event]; ok {
		return int(d / time.Millisecond)
	}
	return int((10 * time.Second) / time.Millisecond)
}

func resolveCWD(workspaceRoot, hookCWD string) string {
	if strings.TrimSpace(hookCWD) == "" {
		if workspaceRoot == "" {
			wd, _ := os.Getwd()
			return wd
		}
		return workspaceRoot
	}
	if filepath.IsAbs(hookCWD) {
		return hookCWD
	}
	if workspaceRoot == "" {
		wd, _ := os.Getwd()
		return filepath.Join(wd, hookCWD)
	}
	return filepath.Join(workspaceRoot, hookCWD)
}

func payloadToJSONLine(payload HookPayload) string {
	b, _ := json.Marshal(payload)
	return string(b) + "\n"
}

func isBlockingEvent(event HookEvent) bool {
	return event == HookEventPreToolUse || event == HookEventUserPromptSubmit
}

func decideHookOutcome(event HookEvent, res HookSpawnResult) HookDecision {
	if res.SpawnErr != nil {
		if isBlockingEvent(event) {
			return HookDecisionBlock
		}
		return HookDecisionError
	}
	if res.TimedOut {
		if isBlockingEvent(event) {
			return HookDecisionBlock
		}
		return HookDecisionTimeout
	}
	if res.ExitCode == 0 {
		return HookDecisionPass
	}
	if res.ExitCode == 2 && isBlockingEvent(event) {
		return HookDecisionBlock
	}
	return HookDecisionWarn
}

type cappedBuffer struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.cap <= 0 {
		return len(p), nil
	}
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

func defaultHookSpawner(parent context.Context, in HookSpawnInput) HookSpawnResult {
	ctx := parent
	cancel := func() {}
	if in.TimeoutMS > 0 {
		ctx, cancel = context.WithTimeout(parent, time.Duration(in.TimeoutMS)*time.Millisecond)
	}
	defer cancel()

	spec, err := resolveHookShell(in.Command)
	if err != nil {
		return HookSpawnResult{
			ExitCode: -1,
			SpawnErr: err,
		}
	}

	cmd := exec.CommandContext(ctx, spec.Bin, spec.Args...)
	if in.CWD != "" {
		cmd.Dir = in.CWD
	}
	stdin := strings.NewReader(in.Stdin)
	cmd.Stdin = stdin
	outBuf := &cappedBuffer{cap: DefaultHookOutputCapBytes}
	errBuf := &cappedBuffer{cap: DefaultHookOutputCapBytes}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	err = cmd.Run()
	exitCode := 0
	spawnErr := error(nil)
	timedOut := false
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else {
			spawnErr = err
			exitCode = -1
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		timedOut = true
	}
	return HookSpawnResult{
		ExitCode:  exitCode,
		Stdout:    outBuf.buf.String(),
		Stderr:    errBuf.buf.String(),
		TimedOut:  timedOut,
		SpawnErr:  spawnErr,
		Truncated: outBuf.truncated || errBuf.truncated,
	}
}
