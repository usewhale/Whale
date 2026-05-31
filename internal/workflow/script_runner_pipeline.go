package workflow

import (
	"context"

	"github.com/fastschema/qjs"
)

func runPipelineItems(
	ctx context.Context,
	items []any,
	stages []*qjs.Value,
	maxConcurrency int,
	invokeStage func(stage *qjs.Value, prev, original any, index, stageIndex int) (pipelineStageResult, error),
	execute func(collectedWorkflowCall) (any, error),
	postprocess func(fn *qjs.Value, value any) (any, error),
	catchError func(fn *qjs.Value, err error) (any, error),
	enterHostWait func() func(),
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
		leaveHostWait := func() {}
		if enterHostWait != nil {
			leaveHostWait = enterHostWait()
		}
		select {
		case completion := <-completions:
			leaveHostWait()
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
			leaveHostWait()
			return results, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}
