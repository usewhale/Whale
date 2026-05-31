package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/llm"
)

type workflowResumeState struct {
	entries     map[string]workflowResumeEntry
	mu          sync.Mutex
	invalidated bool
}

type workflowResumeEntry struct {
	CallKey        string
	SpecHash       string
	Sequence       int64
	SourceRunID    RunID
	SourceTaskID   TaskID
	ChildSessionID string
	Status         string
	Summary        string
	Structured     any
	ToolCalls      []string
	DurationMS     int64
}

func newWorkflowResumeState(source Run) *workflowResumeState {
	state := &workflowResumeState{
		entries: map[string]workflowResumeEntry{},
	}
	for _, ev := range source.Events {
		if ev.Type != EventTaskCompleted || ev.Data == nil {
			continue
		}
		resumeData, _ := ev.Data["resume"].(map[string]any)
		if len(resumeData) == 0 {
			continue
		}
		callKey := strings.TrimSpace(stringAny(resumeData["call_key"]))
		specHash := strings.TrimSpace(stringAny(resumeData["spec_hash"]))
		if callKey == "" || specHash == "" {
			continue
		}
		entry := workflowResumeEntry{
			CallKey:        callKey,
			SpecHash:       specHash,
			Sequence:       int64Any(resumeData["sequence"]),
			SourceRunID:    source.ID,
			SourceTaskID:   ev.TaskID,
			ChildSessionID: ev.SessionID,
			Status:         normalizeStatus(ev.Status, TaskStatusCompleted),
			Summary:        ev.Message,
			Structured:     ev.Data["structured_result"],
			ToolCalls:      stringSliceAny(ev.Data["tool_calls"]),
			DurationMS:     int64Any(ev.Data["duration_ms"]),
		}
		state.entries[callKey] = entry
	}
	return state
}

func (s *workflowResumeState) lookup(callKey, specHash string) (workflowResumeEntry, bool) {
	if s == nil {
		return workflowResumeEntry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidated {
		return workflowResumeEntry{}, false
	}
	entry, ok := s.entries[callKey]
	if !ok || entry.SpecHash != specHash {
		s.invalidated = true
		return workflowResumeEntry{}, false
	}
	return entry, true
}

func workflowSpecHash(spec AgentTaskSpec) (string, error) {
	canonical := struct {
		Prompt       string         `json:"prompt"`
		Role         string         `json:"role,omitempty"`
		Model        string         `json:"model,omitempty"`
		MaxToolIters int            `json:"max_tool_iters,omitempty"`
		MaxToolCalls int            `json:"max_tool_calls,omitempty"`
		Capabilities []string       `json:"capabilities,omitempty"`
		OutputSchema map[string]any `json:"output_schema,omitempty"`
	}{
		Prompt:       strings.TrimSpace(spec.Prompt),
		Role:         strings.TrimSpace(spec.Role),
		Model:        strings.TrimSpace(spec.Model),
		MaxToolIters: spec.MaxToolIters,
		MaxToolCalls: spec.MaxToolCalls,
		Capabilities: cloneStringSlice(spec.Capabilities),
		OutputSchema: cloneMap(spec.OutputSchema),
	}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("hash workflow agent spec: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func workflowResumeData(callKey, specHash string, sequence int64) map[string]any {
	if strings.TrimSpace(callKey) == "" || strings.TrimSpace(specHash) == "" {
		return nil
	}
	return map[string]any{
		"call_key":  callKey,
		"spec_hash": specHash,
		"sequence":  sequence,
	}
}

func workflowCachedResumeData(entry workflowResumeEntry) map[string]any {
	data := workflowResumeData(entry.CallKey, entry.SpecHash, entry.Sequence)
	if data == nil {
		data = map[string]any{}
	}
	data["cached"] = true
	data["source_run_id"] = string(entry.SourceRunID)
	data["source_task_id"] = string(entry.SourceTaskID)
	return data
}

func workflowCachedResult(entry workflowResumeEntry, taskID TaskID) AgentTaskResult {
	return AgentTaskResult{
		TaskID:           taskID,
		ChildSessionID:   entry.ChildSessionID,
		Status:           normalizeStatus(entry.Status, TaskStatusCompleted),
		Summary:          entry.Summary,
		StructuredResult: entry.Structured,
		ToolCalls:        append([]string(nil), entry.ToolCalls...),
		Usage:            llm.Usage{},
		DurationMS:       0,
	}
}

func stringAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func int64Any(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case int32:
		return int64(x)
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	default:
		return 0
	}
}

func stringSliceAny(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(stringAny(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
