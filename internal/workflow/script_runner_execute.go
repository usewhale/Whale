package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/fastschema/qjs"
	"github.com/google/uuid"
)

func (r *ScriptRunner) executeScript(exec *workflowExecution, parsed parsedWorkflowScript, args any) (any, error) {
	if err := prepareWorkflowExecution(exec); err != nil {
		return nil, err
	}
	jsrt, err := newWorkflowJSRuntime(workflowJSRuntimeOptions{Context: exec.ctx, Timeout: r.jsTimeout()})
	if err != nil {
		return nil, err
	}
	defer jsrt.Close()
	jsctx := jsrt.Context()
	if _, err := jsrt.Eval("workflow-url.js", qjs.Code(`
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
	defer freeWorkflowJSValue(argsVal)
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
		res, err := workflowJSHostWait(jsrt.watchdog, func() (AgentTaskResult, error) {
			return spawnCall(call)
		})
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
		result, err := workflowJSHostWait(jsrt.watchdog, func() (any, error) {
			return r.runChildWorkflow(exec, spec)
		})
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
		callResults, err := workflowJSHostWait(jsrt.watchdog, func() ([]parallelCallResult, error) {
			return runParallelCalls(exec.ctx, calls, DefaultMaxConcurrency, executeCall)
		})
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
			if jsrt.watchdog != nil {
				jsrt.watchdog.beat()
			}
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
		}, executeCall, postprocessValue, catchValue, func() func() {
			if jsrt.watchdog == nil {
				return func() {}
			}
			jsrt.watchdog.enterHostWait()
			return jsrt.watchdog.leaveHostWait
		})
		if err != nil {
			return nil, err
		}
		if err := syncBudgetRuntime(this.Context()); err != nil {
			return nil, err
		}
		return thenableValue(this.Context(), "pipeline", results)
	})
	if _, err := jsrt.Eval("workflow-guards.js", qjs.Code(`
Date.now = function() { throw new Error("Date.now() is unavailable in workflow scripts (breaks resume)"); };
Math.random = function() { throw new Error("Math.random() is unavailable in workflow scripts (breaks resume)"); };
`)); err != nil {
		return nil, fmt.Errorf("install workflow runtime guards: %w", err)
	}
	val, err := jsrt.Eval("workflow.js", qjs.Code(parsed.Executable), qjs.FlagAsync())
	if val != nil {
		defer freeWorkflowJSValue(val)
	}
	if err != nil {
		if watchdogErr := jsrt.watchdogErr(); watchdogErr != nil {
			return nil, fmt.Errorf("workflow script failed: %w", watchdogErr)
		}
		return nil, fmt.Errorf("workflow script failed: %w", err)
	}
	result, err := jsValueToGo(jsctx, val)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func prepareWorkflowExecution(exec *workflowExecution) error {
	if exec == nil {
		return errors.New("workflow execution context is required")
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
			return err
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
	return nil
}
