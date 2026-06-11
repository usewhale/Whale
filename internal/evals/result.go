package evals

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type Suite string

const (
	SuiteScenario   Suite = "scenario"
	SuiteCapability Suite = "capability"
	SuiteRegression Suite = "regression"
	SuiteRuntime    Suite = "runtime"
)

type RunStatus string

const (
	RunStatusPass RunStatus = "pass"
	RunStatusFail RunStatus = "fail"
)

type FailureKind string

const (
	FailureKindSetup             FailureKind = "setup"
	FailureKindToolError         FailureKind = "tool_error"
	FailureKindRuntimeSemantics  FailureKind = "runtime_semantics"
	FailureKindVerification      FailureKind = "verification"
	FailureKindUnexpectedSuccess FailureKind = "unexpected_success"
	FailureKindUnexpectedFailure FailureKind = "unexpected_failure"
)

type StepSummary struct {
	ID           string
	Tool         string
	Turn         int
	Index        int
	IsError      bool
	EnvelopeCode string
	ResultDigest string
}

type RunSummary struct {
	Suite       Suite
	Name        string
	SessionID   string
	Status      RunStatus
	FailureKind FailureKind
	TurnCount   int
	StepCount   int
	DurationMS  int64
	Steps       []StepSummary
}

func (r *Run) Summary() RunSummary {
	steps := make([]StepSummary, 0, len(r.Steps))
	for _, step := range r.Steps {
		steps = append(steps, StepSummary{
			ID:           step.Spec.ID,
			Tool:         step.Call.Name,
			Turn:         step.Turn,
			Index:        step.Index,
			IsError:      step.Result.IsError(),
			EnvelopeCode: step.Envelope.Code,
			ResultDigest: summarizeResult(step.Result.ModelText),
		})
	}
	return RunSummary{
		Suite:      r.Suite,
		Name:       r.Name,
		SessionID:  r.SessionID,
		Status:     RunStatusPass,
		TurnCount:  countToolTurns(r.Messages),
		StepCount:  len(r.Steps),
		DurationMS: r.Duration.Milliseconds(),
		Steps:      steps,
	}
}

func countToolTurns(messages []core.Message) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == core.RoleTool {
			count++
		}
	}
	return count
}

type runFailure struct {
	Kind    FailureKind
	Name    string
	Summary *RunSummary
	Err     error
}

func newRunFailure(kind FailureKind, name string, run *Run, err error) *runFailure {
	var summary *RunSummary
	if run != nil {
		s := run.Summary()
		s.Status = RunStatusFail
		s.FailureKind = kind
		summary = &s
	}
	return &runFailure{Kind: kind, Name: name, Summary: summary, Err: err}
}

func (e *runFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.Summary == nil {
		return fmt.Sprintf("%s: %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("%s: %v [%s]", e.Kind, e.Err, FormatRunSummary(*e.Summary))
}

func (e *runFailure) Unwrap() error { return e.Err }

func FailureKindFromError(err error) FailureKind {
	var rf *runFailure
	if errors.As(err, &rf) {
		return rf.Kind
	}
	return ""
}

func classifyScenarioError(name string, run *Run, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "verification failed"):
		return newRunFailure(FailureKindVerification, name, run, err)
	case strings.Contains(msg, "expected tool error"):
		return newRunFailure(FailureKindUnexpectedSuccess, name, run, err)
	case strings.Contains(msg, "unexpected tool error"),
		strings.Contains(msg, "returned non-ok envelope"):
		return newRunFailure(FailureKindUnexpectedFailure, name, run, err)
	case strings.Contains(msg, "did not end cleanly"),
		strings.Contains(msg, "produced"),
		strings.Contains(msg, "ran tool"):
		return newRunFailure(FailureKindRuntimeSemantics, name, run, err)
	default:
		return newRunFailure(FailureKindToolError, name, run, err)
	}
}

func FormatRunSummary(summary RunSummary) string {
	parts := []string{
		fmt.Sprintf("suite=%s", summary.Suite),
		fmt.Sprintf("status=%s", summary.Status),
		fmt.Sprintf("steps=%d", summary.StepCount),
	}
	if summary.FailureKind != "" {
		parts = append(parts, fmt.Sprintf("failure=%s", summary.FailureKind))
	}
	if len(summary.Steps) > 0 {
		stepParts := make([]string, 0, len(summary.Steps))
		for _, step := range summary.Steps {
			label := step.ID
			if label == "" {
				label = step.Tool
			}
			if step.EnvelopeCode != "" {
				label = label + ":" + step.EnvelopeCode
			}
			stepParts = append(stepParts, label)
		}
		parts = append(parts, "path="+strings.Join(stepParts, "->"))
	}
	return strings.Join(parts, " ")
}

var (
	tempDirPattern    = regexp.MustCompile(`/[A-Za-z0-9._-]*whale-eval-[^/\s"]+`)
	sessionIDPattern  = regexp.MustCompile(`"task_id":"[^"]+"`)
	whitespacePattern = regexp.MustCompile(`\s+`)
)

func summarizeResult(content string) string {
	normalized := normalizeRecordString(content)
	if len(normalized) <= 120 {
		return normalized
	}
	return normalized[:117] + "..."
}

func normalizeRecordString(v string) string {
	v = tempDirPattern.ReplaceAllString(v, "/<TMP>")
	v = sessionIDPattern.ReplaceAllString(v, `"task_id":"<TASK_ID>"`)
	v = whitespacePattern.ReplaceAllString(v, " ")
	return strings.TrimSpace(v)
}
