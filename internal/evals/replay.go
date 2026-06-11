package evals

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/usewhale/whale/internal/core"
)

type RunDiff struct {
	Equal       bool
	Differences []string
}

func ReadRecord(path string) ([]RecordEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open record file: %w", err)
	}
	defer f.Close()

	var out []RecordEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry RecordEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("decode record entry: %w", err)
		}
		out = append(out, entry.normalized())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan record file: %w", err)
	}
	return out, nil
}

func RecordEntriesFromRun(run *Run) []RecordEntry {
	entries := make([]RecordEntry, 0, len(run.Steps))
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
		entries = append(entries, entry.normalized())
	}
	return entries
}

func DiffRunAgainstRecord(path string, run *Run) (RunDiff, error) {
	baseline, err := ReadRecord(path)
	if err != nil {
		return RunDiff{}, err
	}
	return DiffRecords(baseline, RecordEntriesFromRun(run)), nil
}

func DiffRecords(a, b []RecordEntry) RunDiff {
	diff := RunDiff{Equal: true}
	if len(a) != len(b) {
		diff.Equal = false
		diff.Differences = append(diff.Differences, fmt.Sprintf("step_count: %d != %d", len(a), len(b)))
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		left := a[i].normalized()
		right := b[i].normalized()
		stepLabel := left.StepID
		if stepLabel == "" {
			stepLabel = fmt.Sprintf("step[%d]", i)
		}
		if left.Tool != right.Tool {
			diff.Equal = false
			diff.Differences = append(diff.Differences, fmt.Sprintf("%s tool: %s != %s", stepLabel, left.Tool, right.Tool))
		}
		if left.Input != right.Input {
			diff.Equal = false
			diff.Differences = append(diff.Differences, fmt.Sprintf("%s input: %s != %s", stepLabel, left.Input, right.Input))
		}
		if left.EnvelopeCode != right.EnvelopeCode {
			diff.Equal = false
			diff.Differences = append(diff.Differences, fmt.Sprintf("%s envelope_code: %s != %s", stepLabel, left.EnvelopeCode, right.EnvelopeCode))
		}
		if left.ResultDigest != right.ResultDigest {
			diff.Equal = false
			diff.Differences = append(diff.Differences, fmt.Sprintf("%s result_digest: %s != %s", stepLabel, left.ResultDigest, right.ResultDigest))
		}
	}
	return diff
}

func (e RecordEntry) normalized() RecordEntry {
	e.Session = ""
	e.Prompt = normalizeRecordString(e.Prompt)
	e.Input = normalizeRecordString(e.Input)
	e.Result = normalizeRecordString(e.Result)
	if e.ResultDigest == "" {
		e.ResultDigest = summarizeResult(e.Result)
	} else {
		e.ResultDigest = normalizeRecordString(e.ResultDigest)
	}
	return e
}
