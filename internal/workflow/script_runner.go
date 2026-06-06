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
	JSTimeout       time.Duration
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
		JSTimeout:       defaultWorkflowJSTimeout,
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
	jsrt, err := newWorkflowJSRuntime(workflowJSRuntimeOptions{})
	if err != nil {
		return err
	}
	defer jsrt.Close()
	if err := jsrt.Compile("workflow.js", qjs.Code(code), qjs.FlagAsync()); err != nil {
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
	if phase := strings.TrimSpace(stringProperty(opts, "phase")); phase != "" {
		spec.Phase = phase
	}
	if hasDefinedProperty(opts, "agent") {
		agentDef, err := agentDefinitionProperty(opts, "agent")
		if err != nil {
			return spec, err
		}
		spec.Agent = agentDef
	}
	spec.Model = stringProperty(opts, "model")
	spec.Effort = stringProperty(opts, "effort")
	spec.PermissionMode = stringProperty(opts, "permissionMode")
	if hasDefinedProperty(opts, "maxTurns") {
		maxTurns, err := intProperty(opts, "maxTurns")
		if err != nil {
			return spec, err
		}
		spec.MaxTurns = maxTurns
	}
	if hasDefinedProperty(opts, "background") {
		background, err := boolProperty(opts, "background")
		if err != nil {
			return spec, err
		}
		spec.Background = background
	}
	if hasDefinedProperty(opts, "isolation") {
		spec.Isolation = stringProperty(opts, "isolation")
		spec.Agent.Isolation = spec.Isolation
	}
	if hasDefinedProperty(opts, "skills") {
		skillNames, err := stringArrayProperty(opts, "skills")
		if err != nil {
			return spec, err
		}
		spec.Skills = skillNames
		spec.Agent.Skills = skillNames
	}
	if hasDefinedProperty(opts, "mcpServers") {
		serverNames, err := stringArrayProperty(opts, "mcpServers")
		if err != nil {
			return spec, err
		}
		spec.MCPServers = serverNames
		spec.Agent.MCPServers = serverNames
	}
	if hasDefinedProperty(opts, "initialPrompt") {
		spec.InitialPrompt = stringProperty(opts, "initialPrompt")
		spec.Agent.InitialPrompt = spec.InitialPrompt
	}
	if hasDefinedProperty(opts, "memory") {
		spec.Memory = stringProperty(opts, "memory")
		spec.Agent.Memory = spec.Memory
	}
	if hasDefinedProperty(opts, "hooks") {
		hooks, err := anyProperty(opts, "hooks")
		if err != nil {
			return spec, err
		}
		spec.Agent.Hooks = hooks
	}
	if hasDefinedProperty(opts, "tools") {
		tools, err := stringArrayProperty(opts, "tools")
		if err != nil {
			return spec, err
		}
		spec.Agent.Tools = tools
	}
	if hasDefinedProperty(opts, "disallowedTools") {
		disallowed, err := stringArrayProperty(opts, "disallowedTools")
		if err != nil {
			return spec, err
		}
		spec.Agent.DisallowedTools = disallowed
	}
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
	} else if spec.Agent.Tools != nil {
		spec.Capabilities = cloneStringSlice(spec.Agent.Tools)
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
		Role:            firstNonEmpty(spec.Role, spec.Agent.Name),
		CallKey:         call.resumeCallKey,
		SpecHash:        call.resumeSpecHash,
		Sequence:        call.resumeSequence,
	}
	resumeData := workflowCachedResumeData(*call.cached)
	result := workflowCachedResult(*call.cached, taskID)
	def := workflowAgentDefinition(spec)
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
			"model":             strings.TrimSpace(def.Model),
			"agent":             def,
			"effort":            strings.TrimSpace(def.Effort),
			"permission_mode":   strings.TrimSpace(def.PermissionMode),
			"max_turns":         def.MaxTurns,
			"background":        def.Background,
			"isolation":         strings.TrimSpace(def.Isolation),
			"skills":            def.Skills,
			"mcp_servers":       def.MCPServers,
			"initial_prompt":    strings.TrimSpace(def.InitialPrompt),
			"memory":            strings.TrimSpace(def.Memory),
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

func agentDefinitionProperty(v *qjs.Value, name string) (tasks.AgentDefinition, error) {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return tasks.AgentDefinition{}, nil
	}
	if !prop.IsObject() || prop.IsArray() {
		return tasks.AgentDefinition{}, fmt.Errorf("agent opts %s must be an object", name)
	}
	def, err := qjs.ToGoValue[tasks.AgentDefinition](prop)
	if err != nil {
		return tasks.AgentDefinition{}, fmt.Errorf("agent definition must be JSON-serializable: %w", err)
	}
	return def, nil
}

func anyProperty(v *qjs.Value, name string) (any, error) {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return nil, nil
	}
	value, err := qjs.ToGoValue[any](prop)
	if err != nil {
		return nil, fmt.Errorf("agent opts %s must be JSON-serializable: %w", name, err)
	}
	return value, nil
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

func boolProperty(v *qjs.Value, name string) (bool, error) {
	prop := v.GetPropertyStr(name)
	defer prop.Free()
	if prop.IsUndefined() || prop.IsNull() {
		return false, nil
	}
	value, err := qjs.ToGoValue[bool](prop)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
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

func (r *ScriptRunner) jsTimeout() time.Duration {
	if r == nil || r.JSTimeout == 0 {
		return defaultWorkflowJSTimeout
	}
	if r.JSTimeout < 0 {
		return 0
	}
	return r.JSTimeout
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
