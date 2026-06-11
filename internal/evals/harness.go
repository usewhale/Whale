package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/tools"
)

type StepSpec struct {
	ID          string
	ToolName    string
	Input       string
	InputFunc   func(history []core.Message) (string, error)
	ExpectError bool
}

type TurnSpec struct {
	Steps []StepSpec
}

type ScenarioSpec struct {
	Name          string
	Suite         Suite
	SessionID     string
	Prompt        string
	Turns         []TurnSpec
	FinalResponse string
	Setup         func(root string) error
	Verify        func(*Run) error
	RecordPath    string
	AgentOptions  []agent.AgentOption
}

type TaskSpec struct {
	ID          string
	Suite       Suite
	Description string
	Scenario    ScenarioSpec
}

type Run struct {
	Name      string
	Suite     Suite
	SessionID string
	Prompt    string
	Root      string
	Messages  []core.Message
	Steps     []StepRun
	Final     core.Message
	Duration  time.Duration
}

func (r *Run) FindStep(id string) *StepRun {
	for i := range r.Steps {
		if r.Steps[i].Spec.ID == id {
			return &r.Steps[i]
		}
	}
	return nil
}

type StepRun struct {
	Turn        int
	Index       int
	Spec        StepSpec
	Call        core.ToolCall
	Result      core.ToolResult
	Envelope    core.ToolEnvelope
	HasEnvelope bool
}

func RunScenario(ctx context.Context, spec ScenarioSpec) (*Run, error) {
	if len(spec.Turns) == 0 {
		return nil, newRunFailure(FailureKindSetup, spec.Name, nil, fmt.Errorf("scenario %q has no turns", spec.Name))
	}
	name := spec.Name
	if name == "" {
		name = "unnamed-scenario"
	}
	suite := spec.Suite
	if suite == "" {
		suite = SuiteScenario
	}
	sessionID := spec.SessionID
	if sessionID == "" {
		sessionID = name
	}
	prompt := spec.Prompt
	if prompt == "" {
		prompt = "run eval"
	}
	finalResponse := spec.FinalResponse
	if finalResponse == "" {
		finalResponse = "done"
	}

	root, err := os.MkdirTemp("", "whale-eval-*")
	if err != nil {
		return nil, newRunFailure(FailureKindSetup, name, nil, fmt.Errorf("create temp workspace: %w", err))
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	if spec.Setup != nil {
		if setupErr := spec.Setup(root); setupErr != nil {
			err = newRunFailure(FailureKindSetup, name, nil, fmt.Errorf("scenario setup: %w", setupErr))
			return nil, err
		}
	}

	toolset, err := tools.NewToolset(root)
	if err != nil {
		return nil, newRunFailure(FailureKindSetup, name, nil, fmt.Errorf("new toolset: %w", err))
	}
	provider := &scriptedProvider{turns: spec.Turns, finalResponse: finalResponse}
	store := store.NewInMemoryStore()
	a := agent.NewAgentWithRegistry(
		provider,
		store,
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
		agent.WithRecoveryPolicy(agent.RecoveryPolicy{Enabled: false}),
		agent.WithProjectMemory(false, 0, nil, root),
		agent.WithSessionsDir(filepath.Join(root, ".sessions")),
		agent.WithUsageLogPath(""),
	)
	for _, opt := range spec.AgentOptions {
		if opt != nil {
			opt(a)
		}
	}

	started := time.Now()
	finalMsg, err := a.RunSession(ctx, sessionID, prompt)
	if err != nil {
		return nil, newRunFailure(FailureKindRuntimeSemantics, name, nil, fmt.Errorf("run scenario %q: %w", name, err))
	}
	msgs, err := store.List(ctx, sessionID)
	if err != nil {
		return nil, newRunFailure(FailureKindRuntimeSemantics, name, nil, fmt.Errorf("list scenario messages: %w", err))
	}

	stepRuns, err := collectStepRuns(provider.resolvedTurns, msgs)
	if err != nil {
		return nil, newRunFailure(FailureKindRuntimeSemantics, name, nil, err)
	}
	run := &Run{
		Name:      name,
		Suite:     suite,
		SessionID: sessionID,
		Prompt:    prompt,
		Root:      root,
		Messages:  msgs,
		Steps:     stepRuns,
		Final:     finalMsg,
		Duration:  time.Since(started),
	}
	if err := validateScenario(spec, run); err != nil {
		return nil, classifyScenarioError(name, run, err)
	}
	if spec.RecordPath != "" {
		if err := writeRecord(spec.RecordPath, run); err != nil {
			return nil, newRunFailure(FailureKindSetup, name, run, err)
		}
	}
	return run, nil
}

func RunTask(ctx context.Context, task TaskSpec) (*Run, error) {
	spec := task.Scenario
	if spec.Name == "" {
		spec.Name = task.ID
	}
	if spec.Suite == "" {
		spec.Suite = task.Suite
	}
	if spec.Suite == "" {
		spec.Suite = SuiteCapability
	}
	return RunScenario(ctx, spec)
}

func validateScenario(spec ScenarioSpec, run *Run) error {
	if run.Final.FinishReason != core.FinishReasonEndTurn {
		return fmt.Errorf("scenario %q did not end cleanly: %s", spec.Name, run.Final.FinishReason)
	}
	if len(run.Steps) != expectedStepCount(spec.Turns) {
		return fmt.Errorf("scenario %q produced %d step results, expected %d", spec.Name, len(run.Steps), expectedStepCount(spec.Turns))
	}
	for _, step := range run.Steps {
		if step.Call.Name != step.Spec.ToolName {
			return fmt.Errorf("scenario %q step %q ran tool %q, expected %q", spec.Name, step.Spec.ID, step.Call.Name, step.Spec.ToolName)
		}
		if step.Spec.ExpectError && !step.Result.IsError() {
			return fmt.Errorf("scenario %q step %q expected tool error", spec.Name, step.Spec.ID)
		}
		if !step.Spec.ExpectError && step.Result.IsError() {
			return fmt.Errorf("scenario %q step %q returned unexpected tool error: %s", spec.Name, step.Spec.ID, step.Result.ModelText)
		}
		if outcome := core.ToolResultOutcome(step.Result); outcome != "" {
			succeeded := outcome == core.OutcomeSuccess || outcome == core.OutcomeNoResult
			if step.Spec.ExpectError && succeeded {
				return fmt.Errorf("scenario %q step %q expected error outcome", spec.Name, step.Spec.ID)
			}
			if !step.Spec.ExpectError && !succeeded {
				return fmt.Errorf("scenario %q step %q returned %s outcome (code %s): %s", spec.Name, step.Spec.ID, outcome, step.Result.Code, core.ToolResultModelText(step.Result))
			}
		}
	}
	if spec.Verify != nil {
		if err := spec.Verify(run); err != nil {
			return fmt.Errorf("scenario %q verification failed: %w", spec.Name, err)
		}
	}
	return nil
}

func collectStepRuns(turns []TurnSpec, msgs []core.Message) ([]StepRun, error) {
	toolMsgs := make([]core.Message, 0, len(turns))
	for _, msg := range msgs {
		if msg.Role == core.RoleTool {
			toolMsgs = append(toolMsgs, msg)
		}
	}
	if len(toolMsgs) != len(turns) {
		return nil, fmt.Errorf("expected %d tool messages, got %d", len(turns), len(toolMsgs))
	}

	out := make([]StepRun, 0, expectedStepCount(turns))
	for turnIdx, turn := range turns {
		msg := toolMsgs[turnIdx]
		if len(msg.ToolResults) != len(turn.Steps) {
			return nil, fmt.Errorf("turn %d produced %d tool results, expected %d", turnIdx+1, len(msg.ToolResults), len(turn.Steps))
		}
		for stepIdx, step := range turn.Steps {
			res := msg.ToolResults[stepIdx]
			call := core.ToolCall{
				ID:    res.ToolCallID,
				Name:  res.Name,
				Input: step.Input,
			}
			env, ok := envelopeViewForResult(res)
			out = append(out, StepRun{
				Turn:        turnIdx + 1,
				Index:       stepIdx,
				Spec:        step,
				Call:        call,
				Result:      res,
				Envelope:    env,
				HasEnvelope: ok,
			})
		}
	}
	return out, nil
}

func expectedStepCount(turns []TurnSpec) int {
	total := 0
	for _, turn := range turns {
		total += len(turn.Steps)
	}
	return total
}

type RecordEntry struct {
	Suite        Suite          `json:"suite,omitempty"`
	Scenario     string         `json:"scenario"`
	Session      string         `json:"session"`
	Prompt       string         `json:"prompt"`
	Turn         int            `json:"turn"`
	Index        int            `json:"index"`
	StepID       string         `json:"step_id"`
	Tool         string         `json:"tool"`
	Input        string         `json:"input"`
	IsError      bool           `json:"is_error"`
	Result       string         `json:"result"`
	ResultDigest string         `json:"result_digest,omitempty"`
	EnvelopeCode string         `json:"envelope_code,omitempty"`
	Envelope     map[string]any `json:"envelope,omitempty"`
}

func writeRecord(path string, run *Run) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir record dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open record file: %w", err)
	}
	defer f.Close()

	for _, step := range run.Steps {
		entry := RecordEntry{
			Suite:        run.Suite,
			Scenario:     run.Name,
			Session:      run.SessionID,
			Prompt:       run.Prompt,
			Turn:         step.Turn,
			Index:        step.Index,
			StepID:       step.Spec.ID,
			Tool:         step.Call.Name,
			Input:        step.Spec.Input,
			IsError:      step.Result.IsError(),
			Result:       core.ToolResultModelText(step.Result),
			ResultDigest: summarizeResult(core.ToolResultModelText(step.Result)),
		}
		if outcome := core.ToolResultOutcome(step.Result); outcome != "" {
			succeeded := outcome == core.OutcomeSuccess || outcome == core.OutcomeNoResult
			entry.EnvelopeCode = step.Result.Code
			entry.Envelope = map[string]any{
				"ok":      succeeded,
				"success": succeeded,
				"code":    step.Result.Code,
				"outcome": string(outcome),
			}
			if payload, ok := step.Result.Payload.(map[string]any); ok {
				entry.Envelope["data"] = payload
			}
		}
		b, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal record entry: %w", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("write record entry: %w", err)
		}
	}
	return nil
}

type scriptedProvider struct {
	turns         []TurnSpec
	resolvedTurns []TurnSpec
	turnIndex     int
	finalResponse string
}

func (p *scriptedProvider) StreamResponse(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	if p.turnIndex < len(p.turns) {
		turn := p.turns[p.turnIndex]
		p.turnIndex++
		calls := make([]core.ToolCall, 0, len(turn.Steps))
		resolved := TurnSpec{Steps: make([]StepSpec, 0, len(turn.Steps))}
		for idx, step := range turn.Steps {
			id := step.ID
			if id == "" {
				id = fmt.Sprintf("turn-%d-step-%d", p.turnIndex, idx+1)
			}
			input := step.Input
			if step.InputFunc != nil {
				resolvedInput, err := step.InputFunc(history)
				if err != nil {
					out <- llm.ProviderEvent{Type: llm.EventError, Err: err}
					close(out)
					return out
				}
				input = resolvedInput
			}
			step.Input = input
			resolved.Steps = append(resolved.Steps, step)
			calls = append(calls, core.ToolCall{
				ID:    id,
				Name:  step.ToolName,
				Input: input,
			})
		}
		p.resolvedTurns = append(p.resolvedTurns, resolved)
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls:    calls,
			},
		}
		close(out)
		return out
	}
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonEndTurn,
			Content:      p.finalResponse,
		},
	}
	close(out)
	return out
}

// envelopeViewForResult builds the legacy envelope view from the structured
// channel so task verifications keep reading step.Envelope.Code.
func envelopeViewForResult(res core.ToolResult) (core.ToolEnvelope, bool) {
	return core.ToolEnvelopeView(res)
}
