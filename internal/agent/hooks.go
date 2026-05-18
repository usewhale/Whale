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

	"github.com/BurntSushi/toml"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

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
	Match       string `json:"match,omitempty" toml:"match,omitempty"`
	Command     string `json:"command" toml:"command,omitempty"`
	Description string `json:"description,omitempty" toml:"description,omitempty"`
	TimeoutMS   int    `json:"timeout,omitempty" toml:"timeout,omitempty"`
	CWD         string `json:"cwd,omitempty" toml:"cwd,omitempty"`
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
	HookDecisionHalt    HookDecision = "halt"
	HookDecisionWarn    HookDecision = "warn"
	HookDecisionTimeout HookDecision = "timeout"
	HookDecisionError   HookDecision = "error"
)

type HookOutcome struct {
	Hook       ResolvedHook
	Name       string
	Source     string
	Decision   HookDecision
	ExitCode   int
	Stdout     string
	Stderr     string
	Message    string
	DurationMS int64
	Truncated  bool

	AdditionalContext string
	UpdatedInput      string
	Metadata          map[string]any
}

type HookReport struct {
	Event             HookEvent
	Outcomes          []HookOutcome
	Blocked           bool
	Halted            bool
	AdditionalContext string
	UpdatedInput      string
	Metadata          map[string]any
}

type HookResult struct {
	Decision HookDecision
	Message  string
	Stdout   string
	Stderr   string

	AdditionalContext string
	UpdatedInput      string
	Metadata          map[string]any
}

type HookHandler struct {
	Event       HookEvent
	Match       string
	Name        string
	Source      string
	Description string
	TimeoutMS   int
	Run         func(context.Context, HookPayload) HookResult
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
	handlers  []HookHandler
	spawner   HookSpawner
	workspace string
	outputCap int
}

func NewHookRunner(hooks []ResolvedHook, workspace string) *HookRunner {
	cp := append([]ResolvedHook(nil), hooks...)
	return &HookRunner{hooks: cp, workspace: workspace, spawner: defaultHookSpawner, outputCap: DefaultHookOutputCapBytes}
}

func (r *HookRunner) AddHandlers(handlers ...HookHandler) {
	if r == nil {
		return
	}
	for _, h := range handlers {
		h.Event = HookEvent(strings.TrimSpace(string(h.Event)))
		h.Name = strings.TrimSpace(h.Name)
		h.Source = strings.TrimSpace(h.Source)
		if h.Name == "" || h.Run == nil || h.Event == "" {
			continue
		}
		if h.Source == "" {
			h.Source = "plugin"
		}
		r.handlers = append(r.handlers, h)
	}
}

func (r *HookRunner) Empty() bool {
	return r == nil || (len(r.hooks) == 0 && len(r.handlers) == 0)
}

func (r *HookRunner) Run(ctx context.Context, payload HookPayload) HookReport {
	out := HookReport{Event: payload.Event, Metadata: map[string]any{}}
	if r.Empty() {
		return out
	}
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
		stdin := payloadToJSONLine(payload)
		res := r.spawner(ctx, HookSpawnInput{Command: h.Command, CWD: cwd, Stdin: stdin, TimeoutMS: timeout})
		parsed := shellHookResult(payload.Event, res)
		oc := HookOutcome{
			Hook:       h,
			Name:       firstNonEmptyString(strings.TrimSpace(h.Description), strings.TrimSpace(h.Command)),
			Source:     firstNonEmptyString(strings.TrimSpace(h.Source), "config"),
			Decision:   parsed.Decision,
			ExitCode:   res.ExitCode,
			Stdout:     strings.TrimSpace(res.Stdout),
			Stderr:     strings.TrimSpace(res.Stderr),
			Message:    strings.TrimSpace(parsed.Message),
			DurationMS: time.Since(start).Milliseconds(),
			Truncated:  res.Truncated,

			AdditionalContext: strings.TrimSpace(parsed.AdditionalContext),
			UpdatedInput:      strings.TrimSpace(parsed.UpdatedInput),
			Metadata:          parsed.Metadata,
		}
		if oc.Stderr == "" && res.SpawnErr != nil {
			oc.Stderr = res.SpawnErr.Error()
		}
		if oc.Stderr == "" && res.TimedOut {
			oc.Stderr = fmt.Sprintf("hook timed out after %dms", timeout)
		}
		appendHookOutcome(&out, oc)
		applyHookUpdatedInput(&payload, oc.UpdatedInput)
		if out.Blocked || out.Halted {
			break
		}
	}
	if !out.Blocked && !out.Halted {
		for _, h := range r.handlers {
			if h.Event != payload.Event {
				continue
			}
			if !matchesHookPattern(h.Event, h.Match, payload.ToolName) {
				continue
			}
			start := time.Now()
			timeout := h.TimeoutMS
			if timeout <= 0 {
				timeout = int(defaultHookTimeouts[payload.Event] / time.Millisecond)
			}
			runCtx := ctx
			cancel := func() {}
			if timeout > 0 {
				runCtx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
			}
			res := runHookHandlerWithTimeout(runCtx, h, payload, timeout)
			cancel()
			oc := HookOutcome{
				Name:              h.Name,
				Source:            h.Source,
				Decision:          normalizeHookDecision(payload.Event, res.Decision),
				Stdout:            strings.TrimSpace(res.Stdout),
				Stderr:            strings.TrimSpace(res.Stderr),
				Message:           strings.TrimSpace(res.Message),
				DurationMS:        time.Since(start).Milliseconds(),
				AdditionalContext: strings.TrimSpace(res.AdditionalContext),
				UpdatedInput:      strings.TrimSpace(res.UpdatedInput),
				Metadata:          res.Metadata,
			}
			appendHookOutcome(&out, oc)
			applyHookUpdatedInput(&payload, oc.UpdatedInput)
			if out.Blocked || out.Halted {
				break
			}
		}
	}
	return out
}

func appendHookOutcome(report *HookReport, oc HookOutcome) {
	oc.Decision = normalizeHookDecision(report.Event, oc.Decision)
	report.Outcomes = append(report.Outcomes, oc)
	if strings.TrimSpace(oc.AdditionalContext) != "" {
		report.AdditionalContext = joinNonEmpty(report.AdditionalContext, oc.AdditionalContext)
	}
	if strings.TrimSpace(oc.UpdatedInput) != "" {
		report.UpdatedInput = strings.TrimSpace(oc.UpdatedInput)
	}
	if len(oc.Metadata) > 0 {
		if report.Metadata == nil {
			report.Metadata = map[string]any{}
		}
		for k, v := range oc.Metadata {
			report.Metadata[k] = v
		}
	}
	if oc.Decision == HookDecisionHalt {
		report.Halted = true
		report.Blocked = true
	}
	if oc.Decision == HookDecisionBlock || (isBlockingEvent(report.Event) && (oc.Decision == HookDecisionTimeout || oc.Decision == HookDecisionError)) {
		report.Blocked = true
	}
}

func runHookHandlerWithTimeout(ctx context.Context, h HookHandler, payload HookPayload, timeoutMS int) HookResult {
	ch := make(chan HookResult, 1)
	go func() {
		ch <- h.Run(ctx, payload)
	}()
	select {
	case res := <-ch:
		return res
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return HookResult{Decision: HookDecisionTimeout, Message: fmt.Sprintf("hook timed out after %dms", timeoutMS)}
		}
		return HookResult{Decision: HookDecisionError, Message: ctx.Err().Error()}
	}
}

func applyHookUpdatedInput(payload *HookPayload, updatedInput string) {
	updatedInput = strings.TrimSpace(updatedInput)
	if payload == nil || updatedInput == "" {
		return
	}
	if payload.Event == HookEventUserPromptSubmit {
		payload.Prompt = updatedInput
		return
	}
	var toolArgs any
	if err := json.Unmarshal([]byte(updatedInput), &toolArgs); err == nil {
		payload.ToolArgs = toolArgs
	}
	if payload.ToolCall != nil {
		cp := *payload.ToolCall
		cp.Input = updatedInput
		payload.ToolCall = &cp
	}
}

func LoadProjectHooks(workspaceRoot string) ([]ResolvedHook, error) {
	hooks, _, err := LoadHooks(workspaceRoot, "")
	return hooks, err
}

func LoadHooks(workspaceRoot, dataDir string) ([]ResolvedHook, []string, error) {
	out := make([]ResolvedHook, 0)
	loaded := make([]string, 0, 2)
	projectPath := filepath.Join(workspaceRoot, ".whale", "config.toml")
	projectHooks, projectLoaded, err := loadHooksFile(projectPath)
	if err != nil {
		return nil, loaded, err
	}
	if projectLoaded && len(projectHooks) > 0 {
		out = append(out, projectHooks...)
		loaded = append(loaded, projectPath)
	}
	globalDir := strings.TrimSpace(dataDir)
	if globalDir == "" {
		if v, err := os.UserHomeDir(); err == nil {
			globalDir = filepath.Join(v, ".whale")
		}
	}
	if globalDir != "" {
		globalPath := filepath.Join(globalDir, "config.toml")
		globalHooks, globalLoaded, err := loadHooksFile(globalPath)
		if err != nil {
			return nil, loaded, err
		}
		if globalLoaded && len(globalHooks) > 0 {
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
	var raw struct {
		Hooks map[string][]HookConfig `toml:"hooks"`
	}
	if err := toml.Unmarshal(b, &raw); err != nil {
		return nil, true, fmt.Errorf("parse hooks config %s: %w", path, err)
	}
	st := HookSettings{Hooks: map[HookEvent][]HookConfig{}}
	for ev, hooks := range raw.Hooks {
		st.Hooks[HookEvent(strings.TrimSpace(ev))] = append(st.Hooks[HookEvent(strings.TrimSpace(ev))], hooks...)
	}
	return ResolveHooks(st, path), true, nil
}

func ResolveHooks(st HookSettings, source string) []ResolvedHook {
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
	return matchesHookPattern(h.Event, h.Match, toolName)
}

func matchesHookPattern(event HookEvent, match, toolName string) bool {
	if event != HookEventPreToolUse && event != HookEventPostToolUse {
		return true
	}
	m := strings.TrimSpace(match)
	if m == "" || m == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + m + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(toolName)
}

func shellHookResult(event HookEvent, res HookSpawnResult) HookResult {
	result := HookResult{
		Decision: decideHookOutcome(event, res),
		Stdout:   strings.TrimSpace(res.Stdout),
		Stderr:   strings.TrimSpace(res.Stderr),
	}
	if parsed, ok := parseHookJSONOutput(event, res.Stdout); ok {
		if parsed.Decision != "" && res.SpawnErr == nil && !res.TimedOut {
			result.Decision = parsed.Decision
		}
		result.Message = parsed.Message
		result.AdditionalContext = parsed.AdditionalContext
		result.UpdatedInput = parsed.UpdatedInput
		result.Metadata = parsed.Metadata
	}
	result.Decision = normalizeHookDecision(event, result.Decision)
	return result
}

func parseHookJSONOutput(event HookEvent, stdout string) (HookResult, bool) {
	raw := strings.TrimSpace(stdout)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return HookResult{}, false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return HookResult{}, false
	}
	out := HookResult{
		Message:           firstNonEmptyString(stringField(body, "reason"), stringField(body, "message"), stringField(body, "systemMessage")),
		AdditionalContext: firstNonEmptyString(stringField(body, "additional_context"), stringField(body, "additionalContext"), stringField(body, "context")),
		Metadata:          mapField(body, "metadata"),
	}
	if decision, ok := hookDecisionField(event, body, "decision"); ok {
		out.Decision = decision
	}
	if v, ok := body["updated_input"]; ok {
		out.UpdatedInput = jsonValueString(v)
	} else if v, ok := body["updatedInput"]; ok {
		out.UpdatedInput = jsonValueString(v)
	}
	return out, true
}

func hookDecisionField(event HookEvent, body map[string]any, key string) (HookDecision, bool) {
	raw, ok := body[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	decision := parseHookDecision(event, value)
	if decision == "" && strings.TrimSpace(value) != "" {
		return "", false
	}
	return decision, true
}

func parseHookDecision(event HookEvent, raw string) HookDecision {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pass", "none", "continue", "":
		return HookDecisionPass
	case "warn", "warning":
		return HookDecisionWarn
	case "block", "deny", "denied":
		return HookDecisionBlock
	case "halt", "stop":
		return HookDecisionHalt
	case "allow", "approve", "approved":
		if isBlockingEvent(event) {
			return HookDecisionPass
		}
		return HookDecisionPass
	case "error":
		return HookDecisionError
	default:
		return ""
	}
}

func normalizeHookDecision(event HookEvent, decision HookDecision) HookDecision {
	switch decision {
	case HookDecisionPass, HookDecisionWarn, HookDecisionError, HookDecisionTimeout, HookDecisionHalt:
		return decision
	case HookDecisionBlock:
		if isBlockingEvent(event) {
			return HookDecisionBlock
		}
		return HookDecisionWarn
	default:
		return HookDecisionPass
	}
}

func stringField(body map[string]any, key string) string {
	v, _ := body[key].(string)
	return strings.TrimSpace(v)
}

func mapField(body map[string]any, key string) map[string]any {
	v, _ := body[key].(map[string]any)
	return v
}

func jsonValueString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func joinNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
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

	spec, err := shell.Resolve(in.Command)
	if err != nil {
		return HookSpawnResult{ExitCode: -1, SpawnErr: err}
	}
	cmd := exec.Command(spec.Bin, spec.Args...)
	if in.CWD != "" {
		cmd.Dir = in.CWD
	}
	stdin := strings.NewReader(in.Stdin)
	cmd.Stdin = stdin
	outBuf := &cappedBuffer{cap: DefaultHookOutputCapBytes}
	errBuf := &cappedBuffer{cap: DefaultHookOutputCapBytes}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	err = shell.RunCommand(ctx, cmd)
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
