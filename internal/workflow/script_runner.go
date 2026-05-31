package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastschema/qjs"
	"github.com/google/uuid"

	"github.com/usewhale/whale/internal/securefs"
	"github.com/usewhale/whale/internal/tasks"
)

const WorkflowStatusAsyncLaunched = "async_launched"

type workflowExecution struct {
	ctx             context.Context
	runID           RunID
	parentSessionID string
	workflowName    string
	parentTaskID    TaskID
	nestingDepth    int
	agentCalls      *atomic.Int64
	agentGate       *workflowSemaphore
	budget          *workflowBudget
	resume          *workflowResumeState
	resumePrefix    string
	resumeSeq       *atomic.Int64
	resumeOpSeq     *atomic.Int64
}

type workflowSemaphore struct {
	ch chan struct{}
}

func newWorkflowSemaphore(maxConcurrency int) *workflowSemaphore {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &workflowSemaphore{ch: make(chan struct{}, maxConcurrency)}
}

func (s *workflowSemaphore) acquire(ctx context.Context) error {
	if s == nil || s.ch == nil {
		return nil
	}
	select {
	case s.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *workflowSemaphore) release() {
	if s == nil {
		return
	}
	select {
	case <-s.ch:
	default:
	}
}

type ScriptRunner struct {
	Manager         *RunManager
	Store           RunEventStore
	Scheduler       *TaskScheduler
	DataDir         string
	Library         *Library
	Now             func() time.Time
	MaxAgentCalls   int
	CompileValidate bool
	activeMu        sync.Mutex
	activeRuns      map[RunID]*activeWorkflowRun
}

type activeWorkflowRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewScriptRunner(dataDir string, manager *RunManager) *ScriptRunner {
	var store RunEventStore
	var scheduler *TaskScheduler
	if manager != nil {
		store = manager.Store
		scheduler = manager.Scheduler
	}
	return &ScriptRunner{
		Manager:         manager,
		Store:           store,
		Scheduler:       scheduler,
		DataDir:         dataDir,
		Now:             time.Now,
		MaxAgentCalls:   1000,
		CompileValidate: true,
	}
}

func (r *ScriptRunner) StartWorkflow(ctx context.Context, parentSessionID string, input WorkflowInput) (WorkflowOutput, error) {
	if r == nil || r.Manager == nil || r.Store == nil || r.Scheduler == nil {
		return WorkflowOutput{}, errors.New("workflow script runner is not configured")
	}
	resume, err := r.loadResumeState(ctx, parentSessionID, input.ResumeFromRunID)
	if err != nil {
		return WorkflowOutput{Error: err.Error()}, nil
	}
	script, sourcePath, err := r.resolveScript(ctx, input)
	if err != nil {
		return WorkflowOutput{Error: err.Error()}, nil
	}
	parsed, err := parseWorkflowScript(script)
	if err != nil {
		return WorkflowOutput{Error: err.Error(), ScriptPath: sourcePath}, nil
	}
	if r.CompileValidate {
		if err := validateWorkflowCompile(parsed.Executable); err != nil {
			return WorkflowOutput{Error: err.Error(), ScriptPath: sourcePath}, nil
		}
	}
	budgetTokens := input.BudgetTokens
	if budgetTokens == nil && parsed.Meta.DefaultBudgetTokens > 0 {
		defaultBudgetTokens := parsed.Meta.DefaultBudgetTokens
		budgetTokens = &defaultBudgetTokens
	}
	budget, err := newWorkflowBudget(budgetTokens)
	if err != nil {
		return WorkflowOutput{Error: err.Error(), ScriptPath: sourcePath}, nil
	}
	runID, err := r.Manager.StartRun(ctx, parentSessionID, parsed.Meta.Description)
	if err != nil {
		return WorkflowOutput{}, err
	}
	scriptPath := sourcePath
	if scriptPath == "" {
		scriptPath, err = r.writeRunScript(runID, script)
		if err != nil {
			_ = r.recordRunFailed(context.Background(), runID, err.Error())
			return WorkflowOutput{}, err
		}
	}
	taskID := "workflow-" + uuid.NewString()
	if err := r.Store.Append(ctx, RunEvent{
		RunID:   runID,
		TaskID:  TaskID(taskID),
		Type:    EventScriptReady,
		Time:    r.now().UTC(),
		Status:  RunStatusRunning,
		Message: parsed.Meta.Name,
		Data: map[string]any{
			"name":        parsed.Meta.Name,
			"description": parsed.Meta.Description,
			"script_path": scriptPath,
			"budget":      budget.scriptReadyData(),
			"phases":      scriptPhaseEventData(parsed.Meta.Phases),
		},
	}); err != nil {
		return WorkflowOutput{}, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	exec := &workflowExecution{
		ctx:             runCtx,
		runID:           runID,
		parentSessionID: parentSessionID,
		workflowName:    parsed.Meta.Name,
		agentCalls:      &atomic.Int64{},
		agentGate:       newWorkflowSemaphore(DefaultMaxConcurrency),
		budget:          budget,
		resume:          resume,
		resumePrefix:    "root",
		resumeSeq:       &atomic.Int64{},
		resumeOpSeq:     &atomic.Int64{},
	}
	r.registerActiveRun(runID, cancel)
	go r.runScript(exec, parsed, cloneJSONValue(input.Args))
	return WorkflowOutput{
		Status:        WorkflowStatusAsyncLaunched,
		TaskID:        taskID,
		RunID:         runID,
		Summary:       parsed.Meta.Description,
		TranscriptDir: filepath.Dir(scriptPath),
		ScriptPath:    scriptPath,
	}, nil
}

func scriptPhaseEventData(phases []ScriptPhase) []map[string]any {
	if len(phases) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(phases))
	for _, phase := range phases {
		title := strings.TrimSpace(phase.Title)
		if title == "" {
			continue
		}
		item := map[string]any{"title": title}
		if detail := strings.TrimSpace(phase.Detail); detail != "" {
			item["detail"] = detail
		}
		if model := strings.TrimSpace(phase.Model); model != "" {
			item["model"] = model
		}
		out = append(out, item)
	}
	return out
}

func (r *ScriptRunner) CancelRun(ctx context.Context, runID RunID) (string, error) {
	if r == nil || r.Store == nil {
		return "", errors.New("workflow script runner is not configured")
	}
	runID = RunID(sanitizeID(string(runID)))
	if runID == "" {
		return "", errors.New("run_id is required")
	}
	if run, err := r.Store.LoadRun(ctx, runID); err != nil {
		return "", err
	} else if len(run.Events) == 0 {
		return "", fmt.Errorf("workflow run not found: %s", runID)
	} else if run.Status != RunStatusRunning {
		return fmt.Sprintf("workflow %s is already %s", runID, run.Status), nil
	}
	r.activeMu.Lock()
	active := r.activeRuns[runID]
	r.activeMu.Unlock()
	if active == nil {
		return "", fmt.Errorf("workflow %s is running in the event log but is not active in this process", runID)
	}
	active.cancel()
	return fmt.Sprintf("cancelling workflow %s", runID), nil
}

func (r *ScriptRunner) registerActiveRun(runID RunID, cancel context.CancelFunc) {
	if r == nil || cancel == nil {
		return
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.activeRuns == nil {
		r.activeRuns = map[RunID]*activeWorkflowRun{}
	}
	r.activeRuns[runID] = &activeWorkflowRun{cancel: cancel, done: make(chan struct{})}
}

func (r *ScriptRunner) unregisterActiveRun(runID RunID) {
	if r == nil {
		return
	}
	r.activeMu.Lock()
	active := r.activeRuns[runID]
	delete(r.activeRuns, runID)
	r.activeMu.Unlock()
	if active != nil {
		close(active.done)
	}
}

func (r *ScriptRunner) resolveScript(ctx context.Context, input WorkflowInput) (script, sourcePath string, err error) {
	if path := strings.TrimSpace(input.ScriptPath); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", path, fmt.Errorf("read workflow script: %w", err)
		}
		return string(b), path, nil
	}
	if strings.TrimSpace(input.Script) == "" {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return "", "", errors.New("workflow script, scriptPath, or name is required")
		}
		if r.Library == nil {
			return "", "", errors.New("workflow library is not configured")
		}
		resolved, err := r.Library.Resolve(ctx, name)
		if err != nil {
			return "", "", err
		}
		return resolved.Script, resolved.Definition.Path, nil
	}
	return input.Script, "", nil
}

func (r *ScriptRunner) writeRunScript(runID RunID, script string) (string, error) {
	if fs, ok := r.Store.(*FileRunEventStore); ok {
		dir, err := fs.RunDir(runID)
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, "script.js")
		if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
			return "", fmt.Errorf("write workflow script: %w", err)
		}
		return path, nil
	}
	dir := filepath.Join(strings.TrimSpace(r.DataDir), "runs", sanitizeID(string(runID)))
	if strings.TrimSpace(r.DataDir) == "" {
		dir = filepath.Join(os.TempDir(), "whale-workflow-runs", sanitizeID(string(runID)))
	}
	if err := securefs.MkdirPrivate(dir); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	path := filepath.Join(dir, "script.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return "", fmt.Errorf("write workflow script: %w", err)
	}
	return path, nil
}

func validateWorkflowCompile(code string) error {
	rt, err := qjs.New()
	if err != nil {
		return fmt.Errorf("create workflow JS runtime: %w", err)
	}
	defer rt.Close()
	if _, err := rt.Compile("workflow.js", qjs.Code(code), qjs.FlagAsync()); err != nil {
		return fmt.Errorf("workflow script syntax error: %w", err)
	}
	return nil
}

func (r *ScriptRunner) loadResumeState(ctx context.Context, parentSessionID, resumeRunID string) (*workflowResumeState, error) {
	resumeRunID = strings.TrimSpace(resumeRunID)
	if resumeRunID == "" {
		return nil, nil
	}
	if r == nil || r.Store == nil {
		return nil, errors.New("run event store is required")
	}
	source, err := r.Store.LoadRun(ctx, RunID(resumeRunID))
	if err != nil {
		return nil, fmt.Errorf("load resume source run: %w", err)
	}
	if source.ID == "" || len(source.Events) == 0 {
		return nil, fmt.Errorf("resume source run not found: %s", resumeRunID)
	}
	if source.Status == RunStatusRunning {
		return nil, fmt.Errorf("resume source run is still running: %s", resumeRunID)
	}
	sourceSession := strings.TrimSpace(runStartedSession(source.Events))
	parentSessionID = strings.TrimSpace(parentSessionID)
	if sourceSession != "" && parentSessionID != "" && sourceSession != parentSessionID {
		return nil, fmt.Errorf("resume source run belongs to a different session: %s", resumeRunID)
	}
	return newWorkflowResumeState(source), nil
}

func runStartedSession(events []RunEvent) string {
	for _, ev := range events {
		if ev.Type == EventRunStarted {
			return ev.SessionID
		}
	}
	return ""
}

func (r *ScriptRunner) runScript(exec *workflowExecution, parsed parsedWorkflowScript, args any) {
	defer r.unregisterActiveRun(exec.runID)
	result, err := r.executeScript(exec, parsed, args)
	if err != nil {
		if workflowContextCancelled(exec.ctx, err) {
			_ = r.recordRunCancelled(context.Background(), exec.runID, "workflow cancelled")
			return
		}
		_ = r.recordRunFailed(context.Background(), exec.runID, err.Error())
		return
	}
	_ = r.recordRunCompleted(context.Background(), exec.runID, workflowCompletionMessage(result), result)
}

func (r *ScriptRunner) executeScript(exec *workflowExecution, parsed parsedWorkflowScript, args any) (any, error) {
	if exec == nil {
		return nil, errors.New("workflow execution context is required")
	}
	if exec.ctx == nil {
		exec.ctx = context.Background()
	}
	if exec.agentCalls == nil {
		exec.agentCalls = &atomic.Int64{}
	}
	if exec.agentGate == nil {
		exec.agentGate = newWorkflowSemaphore(DefaultMaxConcurrency)
	}
	if exec.budget == nil {
		budget, err := newWorkflowBudget(nil)
		if err != nil {
			return nil, err
		}
		exec.budget = budget
	}
	if exec.resumeSeq == nil {
		exec.resumeSeq = &atomic.Int64{}
	}
	if exec.resumeOpSeq == nil {
		exec.resumeOpSeq = &atomic.Int64{}
	}
	exec.resumePrefix = workflowResumePrefix(exec.resumePrefix)
	rt, err := qjs.New(qjs.Option{Context: exec.ctx, CloseOnContextDone: true})
	if err != nil {
		return nil, fmt.Errorf("create workflow JS runtime: %w", err)
	}
	defer rt.Close()
	jsctx := rt.Context()
	if _, err := jsctx.Eval("workflow-url.js", qjs.Code(`
if (typeof URL === 'undefined') {
  globalThis.URL = class URL {
    constructor(input) {
      const raw = String(input || '')
      const match = raw.match(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/([^\/?#]*)([^?#]*)?/)
      if (!match) throw new Error('Invalid URL')
      this.hostname = match[1]
      this.pathname = match[2] || '/'
    }
  }
}
`)); err != nil {
		return nil, fmt.Errorf("install workflow URL runtime: %w", err)
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("prepare workflow args: %w", err)
	}
	argsVal := jsctx.ParseJSON(string(argsJSON))
	defer argsVal.Free()
	jsctx.Global().SetPropertyStr("args", argsVal)
	if _, err := jsctx.Eval("workflow-args.js", qjs.Code("if (args && typeof args === 'object') Object.freeze(args);")); err != nil {
		return nil, fmt.Errorf("freeze workflow args: %w", err)
	}
	budgetRuntimeInstalled := workflowScriptUsesBudget(parsed.Executable)
	if budgetRuntimeInstalled {
		if err := installBudgetGlobal(jsctx, exec.budget); err != nil {
			return nil, err
		}
	}
	syncBudgetRuntime := func(ctx *qjs.Context) error {
		if !budgetRuntimeInstalled {
			return nil
		}
		return updateWorkflowBudgetState(ctx, exec.budget)
	}
	currentPhase := ""
	type collectFrame struct {
		calls []collectedWorkflowCall
	}
	var collectStack []collectFrame
	var directCallSeq int64
	currentResumeScope := ""
	nextSequence := func() int64 {
		return exec.resumeSeq.Add(1)
	}
	nextOperation := func(kind string) string {
		n := exec.resumeOpSeq.Add(1)
		kind = strings.Trim(strings.TrimSpace(kind), "/")
		if kind == "" {
			kind = "op"
		}
		return fmt.Sprintf("%s-%04d", kind, n)
	}
	nextDirectKey := func(kind string) string {
		directCallSeq++
		return fmt.Sprintf("%s/%s-%04d", exec.resumePrefix, kind, directCallSeq)
	}
	resumeKeyFor := func(kind string) string {
		kind = strings.Trim(strings.TrimSpace(kind), "/")
		if kind == "" {
			kind = "call"
		}
		if currentResumeScope != "" {
			return currentResumeScope + "/" + kind
		}
		return nextDirectKey(kind)
	}
	collectActive := func() bool {
		return len(collectStack) > 0
	}
	collectCurrent := func() *collectFrame {
		if len(collectStack) == 0 {
			return nil
		}
		return &collectStack[len(collectStack)-1]
	}
	beginCollect := func() error {
		collectStack = append(collectStack, collectFrame{})
		return nil
	}
	endCollect := func() []collectedWorkflowCall {
		if len(collectStack) == 0 {
			return nil
		}
		frame := collectStack[len(collectStack)-1]
		collectStack = collectStack[:len(collectStack)-1]
		out := append([]collectedWorkflowCall(nil), frame.calls...)
		return out
	}
	appendCollectedCall := func(call collectedWorkflowCall) int {
		frame := collectCurrent()
		if frame == nil {
			return -1
		}
		frame.calls = append(frame.calls, call)
		return len(frame.calls) - 1
	}
	callMarker := func(ctx *qjs.Context, callIndex int, display string) *qjs.Value {
		marker := ctx.NewObject()
		marker.SetPropertyStr("__workflowCall", ctx.NewInt32(int32(callIndex)))
		thenFn := ctx.Function(func(this *qjs.This) (*qjs.Value, error) {
			if len(this.Args()) != 1 || !this.Args()[0].IsFunction() {
				return nil, fmt.Errorf("%s(...).then(fn) requires a function", display)
			}
			frame := collectCurrent()
			if frame == nil || callIndex < 0 || callIndex >= len(frame.calls) {
				return nil, fmt.Errorf("%s(...).then(fn) can only be used while collecting a workflow stage", display)
			}
			frame.calls[callIndex].postprocessFn = this.Args()[0].Clone()
			return this.Value.Clone(), nil
		})
		marker.SetPropertyStr("then", thenFn)
		catchFn := ctx.Function(func(this *qjs.This) (*qjs.Value, error) {
			if len(this.Args()) != 1 || !this.Args()[0].IsFunction() {
				return nil, fmt.Errorf("%s(...).catch(fn) requires a function", display)
			}
			frame := collectCurrent()
			if frame == nil || callIndex < 0 || callIndex >= len(frame.calls) {
				return nil, fmt.Errorf("%s(...).catch(fn) can only be used while collecting a workflow stage", display)
			}
			frame.calls[callIndex].catchFn = this.Args()[0].Clone()
			return this.Value.Clone(), nil
		})
		marker.SetPropertyStr("catch", catchFn)
		return marker
	}
	prepareAgentCall := func(spec AgentTaskSpec) (collectedWorkflowCall, error) {
		specHash, err := workflowSpecHash(spec)
		if err != nil {
			return collectedWorkflowCall{}, err
		}
		call := collectedWorkflowCall{
			kind:           workflowCallKindAgent,
			agent:          spec,
			resumeCallKey:  resumeKeyFor("agent"),
			resumeSpecHash: specHash,
			resumeSequence: nextSequence(),
		}
		if exec.resume != nil {
			if entry, ok := exec.resume.lookup(call.resumeCallKey, call.resumeSpecHash); ok {
				call.cached = &entry
			}
		}
		return call, nil
	}
	prepareWorkflowCall := func(spec workflowCallSpec) workflowCallSpec {
		spec.resumePrefix = resumeKeyFor("workflow")
		return spec
	}
	spawnCall := func(call collectedWorkflowCall) (AgentTaskResult, error) {
		spec := call.agent
		if call.cached != nil {
			return r.recordCachedAgent(exec, call)
		}
		if err := exec.budget.checkCanStart(); err != nil {
			return AgentTaskResult{Status: TaskStatusFailed, Error: err.Error()}, err
		}
		if err := exec.agentGate.acquire(exec.ctx); err != nil {
			return AgentTaskResult{}, err
		}
		defer exec.agentGate.release()
		res, err := r.Scheduler.SpawnAgent(exec.ctx, ActorContext{
			RunID:           exec.runID,
			TaskID:          TaskID("task-" + uuid.NewString()),
			ParentSessionID: exec.parentSessionID,
			ActorKind:       ActorKindSubagent,
			ParentTaskID:    exec.parentTaskID,
			WorkflowName:    exec.workflowName,
			Phase:           spec.Phase,
			Label:           spec.Label,
			Role:            spec.Role,
			CallKey:         call.resumeCallKey,
			SpecHash:        call.resumeSpecHash,
			Sequence:        call.resumeSequence,
		}, spec)
		if res.Usage.CompletionTokens > 0 {
			spent := exec.budget.addUsage(res.Usage)
			_ = r.Store.Append(context.Background(), RunEvent{
				RunID:        exec.runID,
				Type:         EventBudgetUpdated,
				Time:         r.now().UTC(),
				Status:       RunStatusRunning,
				ParentTaskID: exec.parentTaskID,
				WorkflowName: exec.workflowName,
				Phase:        spec.Phase,
				Label:        spec.Label,
				Role:         spec.Role,
				Message:      workflowBudgetMessage(exec.budget, spent),
				Data:         exec.budget.eventData(res.Usage),
			})
		}
		return res, err
	}
	executeCall := func(call collectedWorkflowCall) (any, error) {
		switch call.kind {
		case workflowCallKindAgent:
			res, err := spawnCall(call)
			if err != nil {
				return nil, err
			}
			return agentResultValue(res), nil
		case workflowCallKindWorkflow:
			return r.runChildWorkflow(exec, call.workflow)
		default:
			return nil, fmt.Errorf("unsupported workflow call kind %q", call.kind)
		}
	}
	postprocessValue := func(fn *qjs.Value, value any) (any, error) {
		if fn.IsUndefined() || fn.IsNull() || !fn.IsFunction() {
			return nil, errors.New("workflow postprocessor is not a function")
		}
		return callWorkflowJSFunction(jsctx, fn, value)
	}
	catchValue := func(fn *qjs.Value, callErr error) (any, error) {
		message := "workflow call failed"
		if callErr != nil {
			message = callErr.Error()
		}
		return callWorkflowJSFunction(jsctx, fn, map[string]any{"message": message})
	}
	thenableValue := func(ctx *qjs.Context, display string, value any) (*qjs.Value, error) {
		obj := ctx.NewObject()
		rawValue, err := jsonValueToJS(ctx, value)
		if err != nil {
			return nil, err
		}
		obj.SetPropertyStr("__workflowValue", rawValue)
		thenFn := ctx.Function(func(this *qjs.This) (*qjs.Value, error) {
			if len(this.Args()) < 1 || !this.Args()[0].IsFunction() {
				return nil, fmt.Errorf("%s(...).then(fn) requires a function", display)
			}
			next, err := callWorkflowJSFunction(ctx, this.Args()[0], value)
			if err != nil {
				return nil, err
			}
			return jsonValueToJS(ctx, next)
		})
		obj.SetPropertyStr("then", thenFn)
		catchFn := ctx.Function(func(this *qjs.This) (*qjs.Value, error) {
			if len(this.Args()) < 1 || !this.Args()[0].IsFunction() {
				return nil, fmt.Errorf("%s(...).catch(fn) requires a function", display)
			}
			return jsonValueToJS(ctx, value)
		})
		obj.SetPropertyStr("catch", catchFn)
		return obj, nil
	}
	workflowReturnValue := func(ctx *qjs.Context, ret *qjs.Value) (any, error) {
		if ret != nil && ret.IsObject() {
			prop := ret.GetPropertyStr("__workflowValue")
			defer prop.Free()
			if !prop.IsUndefined() && !prop.IsNull() {
				return jsValueToGo(ctx, prop)
			}
		}
		return jsValueToGo(ctx, ret)
	}
	freeCallFunctions := func(call collectedWorkflowCall) {
		if call.postprocessFn != nil {
			call.postprocessFn.Free()
		}
		if call.catchFn != nil {
			call.catchFn.Free()
		}
	}
	jsctx.SetFunc("phase", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) == 0 {
			return nil, errors.New("phase(title) requires a title")
		}
		title := strings.TrimSpace(this.Args()[0].String())
		if title == "" {
			return nil, errors.New("phase(title) requires a non-empty title")
		}
		currentPhase = title
		if err := r.Store.Append(exec.ctx, RunEvent{
			RunID:        exec.runID,
			Type:         EventPhaseStarted,
			Time:         r.now().UTC(),
			Status:       RunStatusRunning,
			ParentTaskID: exec.parentTaskID,
			WorkflowName: exec.workflowName,
			Phase:        title,
			Message:      title,
		}); err != nil {
			return nil, err
		}
		return this.Context().NewUndefined(), nil
	})
	jsctx.SetFunc("log", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) == 0 {
			return nil, errors.New("log(message) requires a message")
		}
		msg := strings.TrimSpace(this.Args()[0].String())
		if err := r.Store.Append(exec.ctx, RunEvent{
			RunID:        exec.runID,
			Type:         EventLog,
			Time:         r.now().UTC(),
			Status:       RunStatusRunning,
			ParentTaskID: exec.parentTaskID,
			WorkflowName: exec.workflowName,
			Phase:        currentPhase,
			Message:      msg,
		}); err != nil {
			return nil, err
		}
		return this.Context().NewUndefined(), nil
	})
	jsctx.SetFunc("agent", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) == 0 {
			return nil, errors.New("agent(prompt, opts?) requires a prompt")
		}
		if max := r.maxAgentCalls(); max > 0 && exec.agentCalls.Add(1) > int64(max) {
			return nil, fmt.Errorf("workflow agent call limit exceeded: %d", max)
		}
		prompt := strings.TrimSpace(this.Args()[0].String())
		if prompt == "" {
			return nil, errors.New("agent(prompt, opts?) requires a non-empty prompt")
		}
		spec, err := agentSpecFromJS(prompt, currentPhase, this.Args())
		if err != nil {
			return nil, err
		}
		call, err := prepareAgentCall(spec)
		if err != nil {
			return nil, err
		}
		if collectActive() {
			callIndex := appendCollectedCall(call)
			return callMarker(this.Context(), callIndex, "agent"), nil
		}
		res, err := spawnCall(call)
		if err != nil {
			return nil, err
		}
		if err := syncBudgetRuntime(this.Context()); err != nil {
			return nil, err
		}
		return agentResultToJS(this.Context(), res)
	})
	jsctx.SetFunc("workflow", func(this *qjs.This) (*qjs.Value, error) {
		spec, err := workflowCallSpecFromJS(this.Args())
		if err != nil {
			return nil, err
		}
		if exec.nestingDepth >= 1 {
			return nil, errors.New("workflow() cannot be called from within a child workflow; nesting is limited to one level")
		}
		spec = prepareWorkflowCall(spec)
		if collectActive() {
			callIndex := appendCollectedCall(collectedWorkflowCall{kind: workflowCallKindWorkflow, workflow: spec})
			return callMarker(this.Context(), callIndex, "workflow"), nil
		}
		result, err := r.runChildWorkflow(exec, spec)
		if err != nil {
			return nil, err
		}
		if err := syncBudgetRuntime(this.Context()); err != nil {
			return nil, err
		}
		return jsonValueToJS(this.Context(), result)
	})
	jsctx.SetFunc("parallel", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) == 0 || !this.Args()[0].IsArray() {
			return nil, errors.New("parallel(thunks) requires an array of thunk functions")
		}
		arr, err := this.Args()[0].ToArray()
		if err != nil {
			return nil, errors.New("parallel(thunks) requires an array of thunk functions")
		}
		results := make([]any, arr.Len())
		calls := make([]collectedWorkflowCall, 0, arr.Len())
		callIndexes := make([]int, 0, arr.Len())
		opKey := exec.resumePrefix + "/" + nextOperation("parallel")
		if err := beginCollect(); err != nil {
			return nil, err
		}
		defer endCollect()
		for i := int64(0); i < arr.Len(); i++ {
			fn := arr.Get(i)
			if fn != nil {
				defer fn.Free()
			}
			if fn == nil || !fn.IsFunction() {
				return nil, errors.New("parallel(thunks) requires thunk functions; wrap agent calls as () => agent(...)")
			}
			frame := collectCurrent()
			before := 0
			if frame != nil {
				before = len(frame.calls)
			}
			prevScope := currentResumeScope
			currentResumeScope = fmt.Sprintf("%s/item-%d", opKey, i)
			thisArg := this.Context().NewUndefined()
			ret, err := fn.InvokeJS("call", thisArg)
			currentResumeScope = prevScope
			thisArg.Free()
			frame = collectCurrent()
			added := 0
			if frame != nil {
				added = len(frame.calls) - before
			}
			if err != nil {
				if ret != nil {
					ret.Free()
				}
				return nil, err
			}
			switch added {
			case 0:
				if ret == nil {
					results[i] = nil
					continue
				}
				value, err := workflowReturnValue(this.Context(), ret)
				ret.Free()
				if err != nil {
					return nil, err
				}
				results[i] = value
			case 1:
				if ret != nil {
					ret.Free()
				}
				calls = append(calls, frame.calls[before])
				callIndexes = append(callIndexes, int(i))
			default:
				return nil, fmt.Errorf("parallel thunk at index %d must call agent() or workflow() at most once", i)
			}
		}
		callResults, err := runParallelCalls(exec.ctx, calls, DefaultMaxConcurrency, executeCall)
		if err != nil {
			return nil, err
		}
		var callErrs []error
		for i, result := range callResults {
			call := calls[i]
			if result.err != nil {
				if isFatalWorkflowCallError(result.err) {
					callErrs = append(callErrs, result.err)
					freeCallFunctions(call)
					continue
				}
				if call.catchFn != nil {
					next, err := catchValue(call.catchFn, result.err)
					freeCallFunctions(call)
					if err != nil {
						return nil, err
					}
					results[callIndexes[i]] = next
					continue
				}
				results[callIndexes[i]] = nil
				freeCallFunctions(call)
				continue
			}
			next := result.value
			if call.postprocessFn != nil {
				var err error
				next, err = postprocessValue(call.postprocessFn, next)
				freeCallFunctions(call)
				if err != nil {
					return nil, err
				}
			} else {
				freeCallFunctions(call)
			}
			results[callIndexes[i]] = next
		}
		if len(callErrs) > 0 {
			return nil, errors.Join(callErrs...)
		}
		if err := syncBudgetRuntime(this.Context()); err != nil {
			return nil, err
		}
		return thenableValue(this.Context(), "parallel", results)
	})
	jsctx.SetFunc("pipeline", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) < 2 || !this.Args()[0].IsArray() {
			return nil, errors.New("pipeline(items, ...stages) requires an item array and at least one stage function")
		}
		for i := 1; i < len(this.Args()); i++ {
			if !this.Args()[i].IsFunction() {
				return nil, errors.New("pipeline(items, ...stages) requires stage functions")
			}
		}
		items, err := jsValueToGo(this.Context(), this.Args()[0])
		if err != nil {
			return nil, err
		}
		itemList, ok := items.([]any)
		if !ok && items != nil {
			return nil, errors.New("pipeline(items, ...stages) requires an item array")
		}
		stages := append([]*qjs.Value(nil), this.Args()[1:]...)
		opKey := exec.resumePrefix + "/" + nextOperation("pipeline")
		results, err := runPipelineItems(exec.ctx, itemList, stages, DefaultMaxConcurrency, func(stage *qjs.Value, prev, original any, index, stageIndex int) (pipelineStageResult, error) {
			if err := beginCollect(); err != nil {
				return pipelineStageResult{}, err
			}
			prevVal, err := jsonValueToJS(this.Context(), prev)
			if err != nil {
				endCollect()
				return pipelineStageResult{}, err
			}
			defer prevVal.Free()
			originalVal, err := jsonValueToJS(this.Context(), original)
			if err != nil {
				endCollect()
				return pipelineStageResult{}, err
			}
			defer originalVal.Free()
			indexVal := this.Context().NewInt32(int32(index))
			defer indexVal.Free()
			prevScope := currentResumeScope
			currentResumeScope = fmt.Sprintf("%s/item-%d/stage-%d", opKey, index, stageIndex)
			thisArg := this.Context().NewUndefined()
			ret, err := stage.InvokeJS("call", thisArg, prevVal, originalVal, indexVal)
			currentResumeScope = prevScope
			thisArg.Free()
			calls := endCollect()
			if err != nil {
				if ret != nil && len(calls) == 0 {
					ret.Free()
				}
				return pipelineStageResult{}, err
			}
			out := pipelineStageResult{calls: calls}
			if len(calls) == 0 {
				if ret != nil {
					defer ret.Free()
				}
				value, err := workflowReturnValue(this.Context(), ret)
				if err != nil {
					return pipelineStageResult{}, err
				}
				out.value = value
			}
			return out, nil
		}, executeCall, postprocessValue, catchValue)
		if err != nil {
			return nil, err
		}
		if err := syncBudgetRuntime(this.Context()); err != nil {
			return nil, err
		}
		return thenableValue(this.Context(), "pipeline", results)
	})
	if _, err := jsctx.Eval("workflow-guards.js", qjs.Code(`
Date.now = function() { throw new Error("Date.now() is unavailable in workflow scripts (breaks resume)"); };
Math.random = function() { throw new Error("Math.random() is unavailable in workflow scripts (breaks resume)"); };
`)); err != nil {
		return nil, fmt.Errorf("install workflow runtime guards: %w", err)
	}
	val, err := jsctx.Eval("workflow.js", qjs.Code(parsed.Executable), qjs.FlagAsync())
	if val != nil {
		defer val.Free()
	}
	if err != nil {
		return nil, fmt.Errorf("workflow script failed: %w", err)
	}
	result, err := jsValueToGo(jsctx, val)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func installBudgetGlobal(ctx *qjs.Context, budget *workflowBudget) error {
	if budget == nil {
		var err error
		budget, err = newWorkflowBudget(nil)
		if err != nil {
			return err
		}
	}
	totalLiteral := "null"
	remainingExpr := "Infinity"
	if total, ok := budget.totalValue(); ok {
		totalLiteral = strconv.FormatInt(total, 10)
		remainingExpr = "Math.max(0, " + totalLiteral + " - __workflowBudgetState.spent)"
	}
	_, err := ctx.Eval("workflow-budget.js", qjs.Code(`
Object.defineProperty(globalThis, "budget", {
  value: Object.freeze({
    total: `+totalLiteral+`,
    spent: () => __workflowBudgetState.spent,
    remaining: () => `+remainingExpr+`,
  }),
  writable: false,
  configurable: false,
});
Object.defineProperty(globalThis, "__workflowBudgetState", {
  value: { spent: `+strconv.FormatInt(budget.spentValue(), 10)+` },
  writable: false,
  configurable: false,
});
`))
	if err != nil {
		return fmt.Errorf("install workflow budget runtime: %w", err)
	}
	return nil
}

func workflowScriptUsesBudget(code string) bool {
	return containsWord(maskStringsAndComments(code), "budget")
}

func updateWorkflowBudgetState(ctx *qjs.Context, budget *workflowBudget) error {
	if ctx == nil || budget == nil {
		return nil
	}
	_, err := ctx.Eval("workflow-budget-update.js", qjs.Code(`if (typeof __workflowBudgetState === "object") { __workflowBudgetState.spent = `+strconv.FormatInt(budget.spentValue(), 10)+`; }`))
	if err != nil {
		return fmt.Errorf("update workflow budget runtime: %w", err)
	}
	return nil
}

func workflowResumePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "root"
	}
	return prefix
}

func workflowBudgetMessage(budget *workflowBudget, spent int64) string {
	if total, ok := budget.totalValue(); ok {
		remaining, _ := budget.remainingValue()
		return fmt.Sprintf("budget spent %d / %d completion tokens (%d remaining)", spent, total, remaining)
	}
	return fmt.Sprintf("budget spent %d completion tokens", spent)
}

func agentSpecFromJS(prompt, currentPhase string, args []*qjs.Value) (AgentTaskSpec, error) {
	spec := AgentTaskSpec{Prompt: prompt, Phase: currentPhase}
	if len(args) < 2 || args[1].IsUndefined() || args[1].IsNull() {
		return spec, nil
	}
	opts := args[1]
	if !opts.IsObject() {
		return spec, errors.New("agent opts must be an object")
	}
	if hasDefinedProperty(opts, "schema") {
		schema, err := schemaProperty(opts, "schema")
		if err != nil {
			return spec, err
		}
		if err := validateOutputSchema(schema); err != nil {
			return spec, err
		}
		spec.OutputSchema = schema
	}
	spec.Label = stringProperty(opts, "label")
	if phase := stringProperty(opts, "phase"); phase != "" {
		spec.Phase = phase
	}
	spec.Model = stringProperty(opts, "model")
	if hasDefinedProperty(opts, "max_tool_iters") {
		maxToolIters, err := intProperty(opts, "max_tool_iters")
		if err != nil {
			return spec, err
		}
		spec.MaxToolIters = maxToolIters
	}
	if hasDefinedProperty(opts, "max_tool_calls") {
		maxToolCalls, err := intProperty(opts, "max_tool_calls")
		if err != nil {
			return spec, err
		}
		spec.MaxToolCalls = maxToolCalls
	}
	if hasDefinedProperty(opts, "capabilities") {
		caps, err := stringArrayProperty(opts, "capabilities")
		if err != nil {
			return spec, err
		}
		spec.Capabilities = caps
	} else if caps := inferAgentCapabilities(prompt); len(caps) > 0 {
		spec.Capabilities = caps
	}
	return spec, nil
}

func workflowCallSpecFromJS(args []*qjs.Value) (workflowCallSpec, error) {
	if len(args) == 0 || args[0].IsUndefined() || args[0].IsNull() {
		return workflowCallSpec{}, errors.New("workflow(nameOrRef, args?) requires a workflow name or { scriptPath }")
	}
	ref := args[0]
	spec := workflowCallSpec{}
	if ref.IsObject() && !ref.IsArray() {
		spec.scriptPath = stringProperty(ref, "scriptPath")
		if spec.scriptPath == "" {
			return workflowCallSpec{}, errors.New("workflow({ scriptPath }, args?) requires a non-empty scriptPath")
		}
	} else {
		spec.name = strings.TrimSpace(ref.String())
		if spec.name == "" {
			return workflowCallSpec{}, errors.New("workflow(nameOrRef, args?) requires a non-empty workflow name")
		}
	}
	if len(args) < 2 || args[1].IsUndefined() || args[1].IsNull() {
		spec.args = map[string]any{}
		return spec, nil
	}
	raw, err := args[1].JSONStringify()
	if err != nil {
		return workflowCallSpec{}, fmt.Errorf("read workflow args: %w", err)
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return workflowCallSpec{}, fmt.Errorf("workflow args must be JSON-serializable: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	spec.args = out
	return spec, nil
}

func (r *ScriptRunner) runChildWorkflow(parent *workflowExecution, spec workflowCallSpec) (any, error) {
	if parent == nil {
		return nil, errors.New("parent workflow execution is required")
	}
	if parent.nestingDepth >= 1 {
		return nil, errors.New("workflow() cannot be called from within a child workflow; nesting is limited to one level")
	}
	parsed, sourcePath, err := r.resolveChildWorkflow(parent.ctx, spec)
	if err != nil {
		return nil, err
	}
	childTaskID := TaskID("workflow-" + uuid.NewString())
	if err := r.Store.Append(parent.ctx, RunEvent{
		RunID:        parent.runID,
		TaskID:       childTaskID,
		Type:         EventWorkflowStarted,
		Time:         r.now().UTC(),
		Status:       RunStatusRunning,
		ParentTaskID: parent.parentTaskID,
		WorkflowName: parsed.Meta.Name,
		Message:      parsed.Meta.Description,
		Data: map[string]any{
			"description": parsed.Meta.Description,
			"script_path": sourcePath,
			"name":        parsed.Meta.Name,
		},
	}); err != nil {
		return nil, err
	}
	child := *parent
	child.workflowName = parsed.Meta.Name
	child.parentTaskID = childTaskID
	child.nestingDepth = parent.nestingDepth + 1
	child.resumePrefix = workflowResumePrefix(spec.resumePrefix)
	result, err := r.executeScript(&child, parsed, cloneJSONValue(spec.args))
	if err != nil {
		appendErr := r.Store.Append(context.Background(), RunEvent{
			RunID:        parent.runID,
			TaskID:       childTaskID,
			Type:         EventWorkflowFailed,
			Time:         r.now().UTC(),
			Status:       RunStatusFailed,
			ParentTaskID: parent.parentTaskID,
			WorkflowName: parsed.Meta.Name,
			Message:      err.Error(),
		})
		return nil, firstErr(err, appendErr)
	}
	data := map[string]any{
		"name": parsed.Meta.Name,
	}
	if result != nil {
		data["result"] = result
	}
	if sourcePath != "" {
		data["script_path"] = sourcePath
	}
	if err := r.Store.Append(parent.ctx, RunEvent{
		RunID:        parent.runID,
		TaskID:       childTaskID,
		Type:         EventWorkflowCompleted,
		Time:         r.now().UTC(),
		Status:       RunStatusCompleted,
		ParentTaskID: parent.parentTaskID,
		WorkflowName: parsed.Meta.Name,
		Message:      workflowCompletionMessage(result),
		Data:         data,
	}); err != nil {
		return result, err
	}
	return result, nil
}

func (r *ScriptRunner) recordCachedAgent(exec *workflowExecution, call collectedWorkflowCall) (AgentTaskResult, error) {
	if exec == nil {
		return AgentTaskResult{}, errors.New("workflow execution context is required")
	}
	if call.cached == nil {
		return AgentTaskResult{}, errors.New("cached workflow call is required")
	}
	spec := call.agent
	taskID := TaskID("task-" + uuid.NewString())
	actor := ActorContext{
		RunID:           exec.runID,
		TaskID:          taskID,
		ParentSessionID: exec.parentSessionID,
		ActorKind:       ActorKindSubagent,
		ParentTaskID:    exec.parentTaskID,
		WorkflowName:    exec.workflowName,
		Phase:           spec.Phase,
		Label:           spec.Label,
		Role:            spec.Role,
		CallKey:         call.resumeCallKey,
		SpecHash:        call.resumeSpecHash,
		Sequence:        call.resumeSequence,
	}
	resumeData := workflowCachedResumeData(*call.cached)
	result := workflowCachedResult(*call.cached, taskID)
	if err := r.Store.Append(exec.ctx, RunEvent{
		RunID:        actor.RunID,
		TaskID:       actor.TaskID,
		Type:         EventTaskStarted,
		Time:         r.now().UTC(),
		Status:       TaskStatusRunning,
		ParentTaskID: actor.ParentTaskID,
		WorkflowName: actor.WorkflowName,
		Phase:        actor.Phase,
		Label:        actor.Label,
		Role:         actor.Role,
		Message:      strings.TrimSpace(spec.Prompt),
		Data: map[string]any{
			"actor_kind":        actor.ActorKind,
			"parent_session_id": actor.ParentSessionID,
			"model":             strings.TrimSpace(spec.Model),
			"max_tool_iters":    spec.MaxToolIters,
			"max_tool_calls":    spec.MaxToolCalls,
			"capabilities":      spec.Capabilities,
			"cached":            true,
			"source_run_id":     string(call.cached.SourceRunID),
			"source_task_id":    string(call.cached.SourceTaskID),
			"resume":            resumeData,
		},
	}); err != nil {
		return AgentTaskResult{}, err
	}
	data := map[string]any{
		"tool_calls":     result.ToolCalls,
		"duration_ms":    result.DurationMS,
		"usage":          usageEventData(result.Usage),
		"cached":         true,
		"source_run_id":  string(call.cached.SourceRunID),
		"source_task_id": string(call.cached.SourceTaskID),
		"resume":         resumeData,
	}
	if result.StructuredResult != nil {
		data["structured_result"] = result.StructuredResult
	}
	if err := r.Store.Append(exec.ctx, RunEvent{
		RunID:        actor.RunID,
		TaskID:       actor.TaskID,
		Type:         EventTaskCompleted,
		Time:         r.now().UTC(),
		Status:       result.Status,
		ParentTaskID: actor.ParentTaskID,
		WorkflowName: actor.WorkflowName,
		Phase:        actor.Phase,
		Label:        actor.Label,
		Role:         actor.Role,
		Message:      result.Summary,
		SessionID:    result.ChildSessionID,
		Data:         data,
	}); err != nil {
		return result, err
	}
	return result, nil
}

func (r *ScriptRunner) resolveChildWorkflow(ctx context.Context, spec workflowCallSpec) (parsedWorkflowScript, string, error) {
	input := WorkflowInput{Args: spec.args}
	if strings.TrimSpace(spec.scriptPath) != "" {
		input.ScriptPath = spec.scriptPath
	} else {
		input.Name = spec.name
	}
	script, sourcePath, err := r.resolveScript(ctx, input)
	if err != nil {
		if strings.TrimSpace(spec.name) != "" {
			return parsedWorkflowScript{}, "", r.workflowResolveErrorWithAvailable(ctx, spec.name, err)
		}
		return parsedWorkflowScript{}, "", err
	}
	parsed, err := parseWorkflowScript(script)
	if err != nil {
		return parsedWorkflowScript{}, sourcePath, err
	}
	if r.CompileValidate {
		if err := validateWorkflowCompile(parsed.Executable); err != nil {
			return parsedWorkflowScript{}, sourcePath, err
		}
	}
	return parsed, sourcePath, nil
}

func (r *ScriptRunner) workflowResolveErrorWithAvailable(ctx context.Context, name string, err error) error {
	if r == nil || r.Library == nil || err == nil || !strings.Contains(err.Error(), "workflow not found") {
		return err
	}
	defs, listErr := r.Library.List(ctx)
	if listErr != nil {
		return err
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def.Status == DefinitionReady {
			names = append(names, def.Name)
		}
	}
	if len(names) == 0 {
		return err
	}
	return fmt.Errorf("%w; available workflows: %s", err, strings.Join(names, ", "))
}

func schemaProperty(v *qjs.Value, name string) (map[string]any, error) {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return nil, nil
	}
	if !prop.IsObject() {
		return nil, errors.New("agent opts schema must be an object")
	}
	schema, err := qjs.ToGoValue[map[string]any](prop)
	if err != nil {
		return nil, fmt.Errorf("agent output schema must be JSON-serializable: %w", err)
	}
	if len(schema) == 0 {
		return nil, errors.New("agent output schema must be a non-empty object")
	}
	return schema, nil
}

func stringArrayProperty(v *qjs.Value, name string) ([]string, error) {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return nil, nil
	}
	if !prop.IsArray() {
		return nil, fmt.Errorf("agent opts %s must be an array of strings", name)
	}
	arr, err := prop.ToArray()
	if err != nil {
		return nil, fmt.Errorf("read agent %s: %w", name, err)
	}
	values := make([]string, 0, arr.Len())
	for i := int64(0); i < arr.Len(); i++ {
		item := arr.Get(i)
		if item == nil {
			return nil, fmt.Errorf("agent opts %s[%d] must be a string", name, i)
		}
		value := strings.TrimSpace(item.String())
		item.Free()
		if value == "" {
			return nil, fmt.Errorf("agent opts %s[%d] must be a non-empty string", name, i)
		}
		values = append(values, value)
	}
	return values, nil
}

type parallelCallResult struct {
	value any
	err   error
}

func isFatalWorkflowCallError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"insufficient balance",
		"deepseek 401",
		"deepseek 402",
		"deepseek 403",
		"invalid api key",
		"api key is not configured",
		"request failed:",
		"workflow budget exceeded",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func runParallelCalls(ctx context.Context, calls []collectedWorkflowCall, maxConcurrency int, execute func(collectedWorkflowCall) (any, error)) ([]parallelCallResult, error) {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	results := make([]parallelCallResult, len(calls))
	if len(calls) == 0 {
		return results, nil
	}
	type job struct {
		index int
		call  collectedWorkflowCall
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	workerCount := min(maxConcurrency, len(calls))
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				res, err := execute(j.call)
				results[j.index] = parallelCallResult{value: res, err: err}
			}
		}()
	}
	for i, call := range calls {
		select {
		case jobs <- job{index: i, call: call}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

type pipelineStageResult struct {
	value any
	calls []collectedWorkflowCall
}

const (
	workflowCallKindAgent    = "agent"
	workflowCallKindWorkflow = "workflow"
)

type collectedWorkflowCall struct {
	kind           string
	agent          AgentTaskSpec
	workflow       workflowCallSpec
	postprocessFn  *qjs.Value
	catchFn        *qjs.Value
	resumeCallKey  string
	resumeSpecHash string
	resumeSequence int64
	cached         *workflowResumeEntry
}

func freePipelineCallFunctions(call collectedWorkflowCall) {
	if call.postprocessFn != nil {
		call.postprocessFn.Free()
	}
	if call.catchFn != nil {
		call.catchFn.Free()
	}
}

type workflowCallSpec struct {
	name         string
	scriptPath   string
	args         any
	resumePrefix string
}

func runPipelineItems(
	ctx context.Context,
	items []any,
	stages []*qjs.Value,
	maxConcurrency int,
	invokeStage func(stage *qjs.Value, prev, original any, index, stageIndex int) (pipelineStageResult, error),
	execute func(collectedWorkflowCall) (any, error),
	postprocess func(fn *qjs.Value, value any) (any, error),
	catchError func(fn *qjs.Value, err error) (any, error),
) ([]any, error) {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	results := make([]any, len(items))
	if len(items) == 0 {
		return results, nil
	}
	type itemState struct {
		original any
		prev     any
		stage    int
		done     bool
	}
	type agentRequest struct {
		index int
		call  collectedWorkflowCall
	}
	type agentCompletion struct {
		index  int
		call   collectedWorkflowCall
		result any
		err    error
	}

	states := make([]itemState, len(items))
	for i, item := range items {
		states[i] = itemState{original: item, prev: item}
	}
	pending := []agentRequest{}
	completions := make(chan agentCompletion, len(items))
	activeAgents := 0
	completedItems := 0
	failItem := func(index int) {
		if states[index].done {
			return
		}
		states[index].done = true
		results[index] = nil
		completedItems++
	}
	completeItem := func(index int) {
		if states[index].done {
			return
		}
		states[index].done = true
		results[index] = states[index].prev
		completedItems++
	}
	var advanceItem func(int) error
	advanceItem = func(index int) error {
		state := &states[index]
		for !state.done && state.stage < len(stages) {
			if err := ctx.Err(); err != nil {
				return err
			}
			stageResult, err := invokeStage(stages[state.stage], state.prev, state.original, index, state.stage)
			if err != nil {
				failItem(index)
				return nil
			}
			switch len(stageResult.calls) {
			case 0:
				state.prev = stageResult.value
				state.stage++
			case 1:
				pending = append(pending, agentRequest{index: index, call: stageResult.calls[0]})
				return nil
			default:
				failItem(index)
				return nil
			}
		}
		if !state.done {
			completeItem(index)
		}
		return nil
	}
	startPending := func() {
		for activeAgents < maxConcurrency && len(pending) > 0 {
			req := pending[0]
			pending = pending[1:]
			activeAgents++
			go func(req agentRequest) {
				res, err := execute(req.call)
				completions <- agentCompletion{index: req.index, call: req.call, result: res, err: err}
			}(req)
		}
	}

	for i := range items {
		if err := advanceItem(i); err != nil {
			return results, err
		}
		startPending()
	}
	for completedItems < len(items) {
		startPending()
		if activeAgents == 0 {
			if len(pending) == 0 {
				break
			}
			continue
		}
		select {
		case completion := <-completions:
			activeAgents--
			if completion.err != nil {
				if ctx.Err() != nil {
					freePipelineCallFunctions(completion.call)
					return results, ctx.Err()
				}
				if isFatalWorkflowCallError(completion.err) {
					freePipelineCallFunctions(completion.call)
					return results, completion.err
				}
				if completion.call.catchFn != nil {
					next, err := catchError(completion.call.catchFn, completion.err)
					freePipelineCallFunctions(completion.call)
					if err != nil {
						failItem(completion.index)
						continue
					}
					state := &states[completion.index]
					state.prev = next
					state.stage++
					if err := advanceItem(completion.index); err != nil {
						return results, err
					}
					continue
				}
				freePipelineCallFunctions(completion.call)
				failItem(completion.index)
				continue
			}
			state := &states[completion.index]
			state.prev = completion.result
			if completion.call.postprocessFn != nil {
				next, err := postprocess(completion.call.postprocessFn, state.prev)
				freePipelineCallFunctions(completion.call)
				if err != nil {
					failItem(completion.index)
					continue
				}
				state.prev = next
			} else {
				freePipelineCallFunctions(completion.call)
			}
			state.stage++
			if err := advanceItem(completion.index); err != nil {
				return results, err
			}
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

func agentResultToJS(ctx *qjs.Context, res AgentTaskResult) (*qjs.Value, error) {
	return jsonValueToJS(ctx, agentResultValue(res))
}

func agentResultValue(res AgentTaskResult) any {
	if res.StructuredResult != nil {
		return res.StructuredResult
	}
	return res.Summary
}

func jsonValueToJS(ctx *qjs.Context, value any) (*qjs.Value, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow JS value: %w", err)
	}
	out := ctx.ParseJSON(string(b))
	if out.IsError() {
		return nil, errors.New("parse workflow JS value")
	}
	return out, nil
}

func callWorkflowJSFunction(ctx *qjs.Context, fn *qjs.Value, value any) (any, error) {
	if fn == nil || fn.IsUndefined() || fn.IsNull() || !fn.IsFunction() {
		return nil, errors.New("workflow callback is not a function")
	}
	valueVal, err := jsonValueToJS(ctx, value)
	if err != nil {
		return nil, err
	}
	defer valueVal.Free()
	thisArg := ctx.NewUndefined()
	ret, err := fn.InvokeJS("call", thisArg, valueVal)
	thisArg.Free()
	if ret != nil {
		defer ret.Free()
	}
	if err != nil {
		return nil, err
	}
	return jsValueToGo(ctx, ret)
}

func jsValueToGo(_ *qjs.Context, value *qjs.Value) (any, error) {
	if value == nil || value.IsUndefined() {
		return nil, nil
	}
	if value.IsNull() {
		return nil, nil
	}
	out, err := qjs.ToGoValue[any](value)
	if err == nil {
		return out, nil
	}
	raw, err := value.JSONStringify()
	if err != nil {
		return nil, fmt.Errorf("marshal workflow return value: %w", err)
	}
	raw = strings.TrimSpace(strings.Trim(raw, "\x00"))
	if raw == "" || raw == "undefined" {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("decode workflow return value: %w", err)
	}
	return decoded, nil
}

func hasDefinedProperty(v *qjs.Value, name string) bool {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	return !prop.IsUndefined() && !prop.IsNull()
}

func stringProperty(v *qjs.Value, name string) string {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return ""
	}
	return strings.TrimSpace(prop.String())
}

func intProperty(v *qjs.Value, name string) (int, error) {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return 0, nil
	}
	if !prop.IsNumber() {
		return 0, fmt.Errorf("%s must be a number", name)
	}
	value := prop.Int32()
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return int(value), nil
}

func (r *ScriptRunner) recordRunCompleted(ctx context.Context, runID RunID, message string, result any) error {
	ev := RunEvent{
		RunID:   runID,
		Type:    EventRunCompleted,
		Time:    r.now().UTC(),
		Status:  RunStatusCompleted,
		Message: strings.TrimSpace(message),
	}
	if result != nil {
		ev.Data = map[string]any{"result": result}
	}
	return r.Store.Append(ctx, ev)
}

func (r *ScriptRunner) recordRunFailed(ctx context.Context, runID RunID, message string) error {
	return r.Store.Append(ctx, RunEvent{
		RunID:   runID,
		Type:    EventRunFailed,
		Time:    r.now().UTC(),
		Status:  RunStatusFailed,
		Message: strings.TrimSpace(message),
	})
}

func (r *ScriptRunner) recordRunCancelled(ctx context.Context, runID RunID, message string) error {
	return r.Store.Append(ctx, RunEvent{
		RunID:   runID,
		Type:    EventRunCancelled,
		Time:    r.now().UTC(),
		Status:  RunStatusCancelled,
		Message: strings.TrimSpace(message),
	})
}

func workflowContextCancelled(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (r *ScriptRunner) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func workflowCompletionMessage(result any) string {
	if msg := workflowResultString(result, "answer"); msg != "" {
		return msg
	}
	if msg := workflowResultString(result, "summary"); msg != "" {
		return msg
	}
	return "workflow completed"
}

func workflowResultString(result any, key string) string {
	obj, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	value, _ := obj[key].(string)
	return strings.TrimSpace(value)
}

func inferAgentCapabilities(prompt string) []string {
	p := strings.ToLower(prompt)
	hasSearch := strings.Contains(p, "websearch") || strings.Contains(p, "web_search")
	hasFetch := strings.Contains(p, "webfetch") || strings.Contains(p, "web_fetch")
	caps := []string{}
	if hasSearch {
		caps = append(caps, tasks.CapabilityWebSearch)
	}
	if hasFetch {
		caps = append(caps, tasks.CapabilityWebFetch)
	}
	return caps
}

func (r *ScriptRunner) maxAgentCalls() int {
	if r != nil && r.MaxAgentCalls > 0 {
		return r.MaxAgentCalls
	}
	return 1000
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	b, err := json.Marshal(in)
	if err != nil {
		out := make(map[string]any, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func cloneJSONValue(in any) any {
	if in == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return in
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}
