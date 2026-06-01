package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

type HookEvent string

const (
	HookEventPreToolUse        HookEvent = "PreToolUse"
	HookEventPermissionRequest HookEvent = "PermissionRequest"
	HookEventPostToolUse       HookEvent = "PostToolUse"
	HookEventPreCompact        HookEvent = "PreCompact"
	HookEventPostCompact       HookEvent = "PostCompact"
	HookEventSessionStart      HookEvent = "SessionStart"
	HookEventUserPromptSubmit  HookEvent = "UserPromptSubmit"
	HookEventSubagentStart     HookEvent = "SubagentStart"
	HookEventSubagentStop      HookEvent = "SubagentStop"
	HookEventStop              HookEvent = "Stop"
)

const (
	DefaultHookOutputCapBytes = 256 * 1024
	DefaultHookTimeout        = 600 * time.Second
	MinimumHookTimeout        = time.Second
)

type HookLifecycleEventInfo struct {
	Event       HookEvent
	Description string
}

func HookEvents() []HookLifecycleEventInfo {
	return []HookLifecycleEventInfo{
		{Event: HookEventPreToolUse, Description: "Before a tool executes"},
		{Event: HookEventPermissionRequest, Description: "When permission is requested"},
		{Event: HookEventPostToolUse, Description: "After a tool executes"},
		{Event: HookEventPreCompact, Description: "Before context compaction"},
		{Event: HookEventPostCompact, Description: "After context compaction"},
		{Event: HookEventSessionStart, Description: "When a new session starts"},
		{Event: HookEventUserPromptSubmit, Description: "When the user submits a prompt"},
		{Event: HookEventSubagentStart, Description: "When a subagent is created"},
		{Event: HookEventSubagentStop, Description: "Right before a subagent ends its turn"},
		{Event: HookEventStop, Description: "Right before Whale ends its turn"},
	}
}

func KnownHookEvent(event HookEvent) bool {
	for _, info := range HookEvents() {
		if info.Event == event {
			return true
		}
	}
	return false
}

type HookTrustStatus string

const (
	HookTrustManaged   HookTrustStatus = "Managed"
	HookTrustUntrusted HookTrustStatus = "Untrusted"
	HookTrustTrusted   HookTrustStatus = "Trusted"
	HookTrustModified  HookTrustStatus = "Modified"
)

type HookState struct {
	TrustedHash string `json:"trusted_hash,omitempty" toml:"trusted_hash,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty" toml:"enabled,omitempty"`
}

type HookStates map[string]HookState

type HookListEntry struct {
	Key         string          `json:"key"`
	Event       HookEvent       `json:"event"`
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Source      string          `json:"source,omitempty"`
	Match       string          `json:"match,omitempty"`
	Command     string          `json:"command,omitempty"`
	Description string          `json:"description,omitempty"`
	TimeoutSec  int             `json:"timeout_sec,omitempty"`
	CWD         string          `json:"cwd,omitempty"`
	Hash        string          `json:"hash,omitempty"`
	Enabled     bool            `json:"enabled"`
	Managed     bool            `json:"managed"`
	Active      bool            `json:"active"`
	Trust       HookTrustStatus `json:"trust"`
}

type HookConfig struct {
	Match       string `json:"match,omitempty" toml:"match,omitempty"`
	Command     string `json:"command" toml:"command,omitempty"`
	Description string `json:"description,omitempty" toml:"description,omitempty"`
	TimeoutSec  int    `json:"timeout,omitempty" toml:"timeout,omitempty"`
	CWD         string `json:"cwd,omitempty" toml:"cwd,omitempty"`
}

type HookSettings struct {
	Hooks map[HookEvent][]HookConfig `json:"hooks"`
}

type ResolvedHook struct {
	HookConfig
	Event   HookEvent
	Source  string
	Managed bool
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
	ApprovalReason    string         `json:"approval_reason,omitempty"`
	ApprovalCode      string         `json:"approval_code,omitempty"`
	CompactSummary    string         `json:"compact_summary,omitempty"`
	MessagesBefore    int            `json:"messages_before,omitempty"`
	MessagesAfter     int            `json:"messages_after,omitempty"`
	SubagentRole      string         `json:"subagent_role,omitempty"`
	SubagentModel     string         `json:"subagent_model,omitempty"`
	SubagentSummary   string         `json:"subagent_summary,omitempty"`
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

func NewPermissionRequestPayload(sessionID, cwd string, call core.ToolCall, reason, code string) HookPayload {
	var toolArgs any
	_ = json.Unmarshal([]byte(call.Input), &toolArgs)
	return HookPayload{
		Event:          HookEventPermissionRequest,
		CWD:            cwd,
		SessionID:      sessionID,
		ToolName:       call.Name,
		ToolArgs:       toolArgs,
		ToolCall:       &call,
		ApprovalReason: reason,
		ApprovalCode:   code,
	}
}

func NewSessionStartPayload(sessionID, cwd string) HookPayload {
	return HookPayload{Event: HookEventSessionStart, CWD: cwd, SessionID: sessionID}
}

func NewPreCompactPayload(sessionID, cwd string, messagesBefore int) HookPayload {
	return HookPayload{Event: HookEventPreCompact, CWD: cwd, SessionID: sessionID, MessagesBefore: messagesBefore}
}

func NewPostCompactPayload(sessionID, cwd, summary string, messagesBefore, messagesAfter int) HookPayload {
	return HookPayload{
		Event:          HookEventPostCompact,
		CWD:            cwd,
		SessionID:      sessionID,
		CompactSummary: summary,
		MessagesBefore: messagesBefore,
		MessagesAfter:  messagesAfter,
	}
}

func NewSubagentHookPayload(event HookEvent, sessionID, cwd, role, model, summary string) HookPayload {
	return HookPayload{
		Event:           event,
		CWD:             cwd,
		SessionID:       sessionID,
		ToolName:        "spawn_subagent",
		SubagentRole:    role,
		SubagentModel:   model,
		SubagentSummary: summary,
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
	ID         string
	Name       string
	Source     string
	Command    string
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
	TimeoutSec  int
	Run         func(context.Context, HookPayload) HookResult
}

type HookSpawnInput struct {
	Command string
	CWD     string
	Stdin   string
	Timeout time.Duration
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

type HookRunStage string

const (
	HookRunStarted   HookRunStage = "started"
	HookRunCompleted HookRunStage = "completed"
	HookRunBlocked   HookRunStage = "blocked"
	HookRunWarned    HookRunStage = "warned"
	HookRunFailed    HookRunStage = "failed"
)

type HookRunObserver func(HookRunStage, HookEventInfo)

type HookRunner struct {
	hooks     []ResolvedHook
	handlers  []HookHandler
	states    HookStates
	spawner   HookSpawner
	workspace string
	outputCap int
}

func NewHookRunner(hooks []ResolvedHook, workspace string) *HookRunner {
	return NewHookRunnerWithState(hooks, workspace, nil)
}

func NewHookRunnerWithState(hooks []ResolvedHook, workspace string, states HookStates) *HookRunner {
	cp := append([]ResolvedHook(nil), hooks...)
	return &HookRunner{hooks: cp, states: cloneHookStates(states), workspace: workspace, spawner: defaultHookSpawner, outputCap: DefaultHookOutputCapBytes}
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

func (r *HookRunner) ListHooks() []HookListEntry {
	if r == nil {
		return nil
	}
	out := make([]HookListEntry, 0, len(r.hooks)+len(r.handlers))
	configOrdinal := map[HookEvent]int{}
	for _, h := range r.hooks {
		ordinal := configOrdinal[h.Event]
		configOrdinal[h.Event] = ordinal + 1
		entry := hookEntryFromResolved(h, r.states, ordinal)
		out = append(out, entry)
	}
	handlerOrdinal := map[HookEvent]int{}
	for _, h := range r.handlers {
		ordinal := handlerOrdinal[h.Event]
		handlerOrdinal[h.Event] = ordinal + 1
		entry := hookEntryFromHandler(h, r.states, ordinal)
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Event != out[j].Event {
			return hookEventRank(out[i].Event) < hookEventRank(out[j].Event)
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *HookRunner) RunHook(ctx context.Context, payload HookPayload) HookReport {
	return r.RunHookWithObserver(ctx, payload, nil)
}

func (r *HookRunner) RunHookWithObserver(ctx context.Context, payload HookPayload, observer HookRunObserver) HookReport {
	out := HookReport{Event: payload.Event, Metadata: map[string]any{}}
	if r.Empty() {
		return out
	}
	configOrdinal := map[HookEvent]int{}
	for _, h := range r.hooks {
		ordinal := configOrdinal[h.Event]
		configOrdinal[h.Event] = ordinal + 1
		if h.Event != payload.Event {
			continue
		}
		if !hookEntryFromResolved(h, r.states, ordinal).Active {
			continue
		}
		if !matchesHook(h, payload.ToolName) {
			continue
		}
		runID := newHookRunID(payload.Event, "command", ordinal)
		name := core.FirstNonEmpty(strings.TrimSpace(h.Description), strings.TrimSpace(h.Command))
		source := core.FirstNonEmpty(strings.TrimSpace(h.Source), "config")
		if observer != nil {
			observer(HookRunStarted, HookEventInfo{
				ID:      runID,
				Name:    name,
				Event:   payload.Event,
				Source:  source,
				Command: strings.TrimSpace(h.Command),
			})
		}
		start := time.Now()
		timeout := resolveHookTimeout(h.TimeoutSec)
		cwd := resolveCWD(r.workspace, h.CWD)
		stdin := payloadToJSONLine(payload)
		res := r.spawner(ctx, HookSpawnInput{Command: h.Command, CWD: cwd, Stdin: stdin, Timeout: timeout})
		parsed := shellHookResult(payload.Event, res)
		oc := HookOutcome{
			Hook:       h,
			ID:         runID,
			Name:       name,
			Source:     source,
			Command:    strings.TrimSpace(h.Command),
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
			oc.Stderr = hookTimeoutMessage(timeout)
		}
		appendHookOutcome(&out, oc)
		emitHookOutcome(observer, payload.Event, oc)
		applyHookUpdatedInput(&payload, oc.UpdatedInput)
		if out.Blocked || out.Halted {
			break
		}
	}
	if !out.Blocked && !out.Halted {
		handlerOrdinal := map[HookEvent]int{}
		for _, h := range r.handlers {
			ordinal := handlerOrdinal[h.Event]
			handlerOrdinal[h.Event] = ordinal + 1
			if h.Event != payload.Event {
				continue
			}
			if !hookEntryFromHandler(h, r.states, ordinal).Active {
				continue
			}
			if !matchesHookPattern(h.Event, h.Match, payload.ToolName) {
				continue
			}
			runID := newHookRunID(payload.Event, "handler", ordinal)
			if observer != nil {
				observer(HookRunStarted, HookEventInfo{
					ID:     runID,
					Name:   h.Name,
					Event:  payload.Event,
					Source: h.Source,
				})
			}
			start := time.Now()
			timeout := resolveHookTimeout(h.TimeoutSec)
			runCtx := ctx
			cancel := func() {}
			if timeout > 0 {
				runCtx, cancel = context.WithTimeout(ctx, timeout)
			}
			res := runHookHandlerWithTimeout(runCtx, h, payload, timeout)
			cancel()
			oc := HookOutcome{
				ID:                runID,
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
			emitHookOutcome(observer, payload.Event, oc)
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

func emitHookOutcome(observer HookRunObserver, event HookEvent, oc HookOutcome) {
	if observer == nil {
		return
	}
	info := HookEventInfo{
		ID:         oc.ID,
		Name:       hookOutcomeName(oc),
		Event:      event,
		Source:     oc.Source,
		Command:    oc.Command,
		Decision:   oc.Decision,
		ExitCode:   oc.ExitCode,
		Message:    hookOutcomeMessage(oc),
		DurationMS: oc.DurationMS,
		Truncated:  oc.Truncated,
	}
	switch oc.Decision {
	case HookDecisionBlock, HookDecisionHalt:
		observer(HookRunBlocked, info)
	case HookDecisionError, HookDecisionTimeout:
		observer(HookRunFailed, info)
	case HookDecisionWarn:
		observer(HookRunWarned, info)
	default:
		observer(HookRunCompleted, info)
	}
}

func newHookRunID(event HookEvent, typ string, ordinal int) string {
	return fmt.Sprintf("%s-%s-%d-%d", event, typ, ordinal, time.Now().UnixNano())
}

func runHookHandlerWithTimeout(ctx context.Context, h HookHandler, payload HookPayload, timeout time.Duration) HookResult {
	ch := make(chan HookResult, 1)
	go func() {
		ch <- h.Run(ctx, payload)
	}()
	select {
	case res := <-ch:
		return res
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return HookResult{Decision: HookDecisionTimeout, Message: hookTimeoutMessage(timeout)}
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
	loaded := make([]string, 0, 3)
	projectPath := filepath.Join(workspaceRoot, ".whale", "config.toml")
	projectHooks, projectLoaded, err := loadHooksFile(projectPath)
	if err != nil {
		return nil, loaded, err
	}
	if projectLoaded && len(projectHooks) > 0 {
		out = append(out, projectHooks...)
		loaded = append(loaded, projectPath)
	}
	projectLocalPath := filepath.Join(workspaceRoot, ".whale", "config.local.toml")
	projectLocalHooks, projectLocalLoaded, err := loadHooksFile(projectLocalPath)
	if err != nil {
		return nil, loaded, err
	}
	if projectLocalLoaded && len(projectLocalHooks) > 0 {
		out = append(out, projectLocalHooks...)
		loaded = append(loaded, projectLocalPath)
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
	out := make([]ResolvedHook, 0)
	for _, info := range HookEvents() {
		ev := info.Event
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

func HookNeedsReview(entry HookListEntry) bool {
	return entry.Trust == HookTrustUntrusted || entry.Trust == HookTrustModified
}

func HookActiveTrust(entry HookListEntry) bool {
	return entry.Trust == HookTrustManaged || entry.Trust == HookTrustTrusted
}

func TrustHookStates(entries []HookListEntry, states HookStates, keys []string) HookStates {
	out := cloneHookStates(states)
	if out == nil {
		out = HookStates{}
	}
	selected := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			selected[key] = true
		}
	}
	trustAll := len(selected) == 0
	for _, entry := range entries {
		if entry.Managed || strings.TrimSpace(entry.Key) == "" || strings.TrimSpace(entry.Hash) == "" {
			continue
		}
		if !trustAll && !selected[entry.Key] {
			continue
		}
		st := out[entry.Key]
		st.TrustedHash = entry.Hash
		out[entry.Key] = st
	}
	return out
}

func SetHookEnabledStates(entries []HookListEntry, states HookStates, keys []string, enabled bool) HookStates {
	out := cloneHookStates(states)
	if out == nil {
		out = HookStates{}
	}
	selected := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			selected[key] = true
		}
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Key) == "" || !selected[entry.Key] {
			continue
		}
		st := out[entry.Key]
		v := enabled
		st.Enabled = &v
		out[entry.Key] = st
	}
	return out
}

func hookEntryFromResolved(h ResolvedHook, states HookStates, ordinal int) HookListEntry {
	name := core.FirstNonEmpty(strings.TrimSpace(h.Description), strings.TrimSpace(h.Command))
	entry := HookListEntry{
		Event:       h.Event,
		Type:        "command",
		Name:        name,
		Source:      core.FirstNonEmpty(strings.TrimSpace(h.Source), "config"),
		Match:       strings.TrimSpace(h.Match),
		Command:     strings.TrimSpace(h.Command),
		Description: strings.TrimSpace(h.Description),
		TimeoutSec:  hookTimeoutSeconds(resolveHookTimeout(h.TimeoutSec)),
		CWD:         strings.TrimSpace(h.CWD),
		Enabled:     true,
		Managed:     h.Managed,
	}
	entry.Hash = hookContentHash(entry)
	entry.Key = hookStableKey(entry, ordinal)
	entry.Trust, entry.Enabled, entry.Active = hookTrustForEntry(entry, states)
	return entry
}

func hookEntryFromHandler(h HookHandler, states HookStates, ordinal int) HookListEntry {
	entry := HookListEntry{
		Event:       h.Event,
		Type:        "agent",
		Name:        strings.TrimSpace(h.Name),
		Source:      core.FirstNonEmpty(strings.TrimSpace(h.Source), "plugin"),
		Match:       strings.TrimSpace(h.Match),
		Description: strings.TrimSpace(h.Description),
		TimeoutSec:  h.TimeoutSec,
		Enabled:     true,
		Managed:     true,
	}
	entry.TimeoutSec = hookTimeoutSeconds(resolveHookTimeout(entry.TimeoutSec))
	entry.Hash = hookContentHash(entry)
	entry.Key = hookStableKey(entry, ordinal)
	entry.Trust, entry.Enabled, entry.Active = hookTrustForEntry(entry, states)
	return entry
}

func hookTrustForEntry(entry HookListEntry, states HookStates) (HookTrustStatus, bool, bool) {
	enabled := true
	if states != nil {
		if st, ok := states[entry.Key]; ok && st.Enabled != nil {
			enabled = *st.Enabled
		}
	}
	if entry.Managed {
		return HookTrustManaged, enabled, enabled
	}
	if states == nil {
		return HookTrustTrusted, enabled, enabled
	}
	trustedHash := strings.TrimSpace(states[entry.Key].TrustedHash)
	switch {
	case trustedHash == "":
		return HookTrustUntrusted, enabled, false
	case trustedHash == entry.Hash:
		return HookTrustTrusted, enabled, enabled
	default:
		return HookTrustModified, enabled, false
	}
}

func hookContentHash(entry HookListEntry) string {
	body := strings.Join([]string{
		string(entry.Event),
		entry.Type,
		entry.Name,
		entry.Source,
		entry.Match,
		entry.Command,
		entry.Description,
		entry.CWD,
		fmt.Sprintf("%d", entry.TimeoutSec),
	}, "\x00")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func hookStableKey(entry HookListEntry, ordinal int) string {
	identity := []string{
		string(entry.Event),
		entry.Type,
		entry.Source,
		entry.Match,
		entry.CWD,
		fmt.Sprintf("%d", ordinal),
	}
	if entry.Type != "command" {
		identity = append(identity, entry.Name)
	}
	body := strings.Join(identity, "\x00")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:16]
}

func hookEventRank(event HookEvent) int {
	for i, info := range HookEvents() {
		if info.Event == event {
			return i
		}
	}
	return len(HookEvents()) + 1
}

func cloneHookStates(in HookStates) HookStates {
	if in == nil {
		return nil
	}
	out := make(HookStates, len(in))
	for k, v := range in {
		out[k] = v
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
		Message:           core.FirstNonEmpty(stringField(body, "reason"), stringField(body, "message"), stringField(body, "systemMessage")),
		AdditionalContext: core.FirstNonEmpty(stringField(body, "additional_context"), stringField(body, "additionalContext"), stringField(body, "context")),
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

func resolveHookTimeout(timeoutSec int) time.Duration {
	if timeoutSec <= 0 {
		return DefaultHookTimeout
	}
	timeout := time.Duration(timeoutSec) * time.Second
	if timeout < MinimumHookTimeout {
		return MinimumHookTimeout
	}
	return timeout
}

func hookTimeoutSeconds(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	return int(timeout.Round(time.Second) / time.Second)
}

func hookTimeoutMessage(timeout time.Duration) string {
	return fmt.Sprintf("hook timed out after %s", timeout.Truncate(time.Millisecond))
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
	return event == HookEventPreToolUse || event == HookEventPermissionRequest || event == HookEventUserPromptSubmit
}

func decideHookOutcome(event HookEvent, res HookSpawnResult) HookDecision {
	if res.SpawnErr != nil {
		return HookDecisionError
	}
	if res.TimedOut {
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
	if in.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, in.Timeout)
	}
	defer cancel()

	spec, err := shell.Resolve(in.Command)
	if err != nil {
		return HookSpawnResult{ExitCode: -1, SpawnErr: err}
	}
	cmd := shell.Command(spec)
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
