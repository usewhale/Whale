package core

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Name() string
	Run(ctx context.Context, call ToolCall) (ToolResult, error)
}

type SubagentStep struct {
	ToolName string `json:"tool_name,omitempty"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

type ToolProgress struct {
	ToolCallID       string
	ToolName         string
	Status           string
	Summary          string
	Role             string
	Model            string
	Count            int
	DurationMS       int64
	Metadata         map[string]any
	ProgressMessages []SubagentStep
}

type ToolProgressRunner interface {
	RunWithProgress(ctx context.Context, call ToolCall, progress func(ToolProgress)) (ToolResult, error)
}

type ToolPreviewer interface {
	Preview(ctx context.Context, call ToolCall) (map[string]any, error)
}

type ToolSpec struct {
	Name             string
	Description      string
	Parameters       map[string]any
	ReadOnly         bool
	ReadOnlyCheck    func(args map[string]any) bool
	Capabilities     []string
	ApprovalHint     string
	SupportsParallel bool
}

type ToolDescriber interface {
	Description() string
}

type ToolParamSpec interface {
	Parameters() map[string]any
}

type ToolReadOnly interface {
	ReadOnly() bool
}

type ToolReadOnlyCheck interface {
	ReadOnlyCheck(args map[string]any) bool
}

type ToolCapabilities interface {
	Capabilities() []string
}

type ToolApprovalHint interface {
	ApprovalHint() string
}

type ToolSupportsParallel interface {
	SupportsParallel() bool
}

func DescribeTool(t Tool) ToolSpec {
	spec := ToolSpec{
		Name:        t.Name(),
		Description: "tool " + t.Name(),
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": true,
		},
		ReadOnly:         false,
		Capabilities:     nil,
		ApprovalHint:     "",
		SupportsParallel: false,
	}
	if d, ok := t.(ToolDescriber); ok {
		if v := d.Description(); v != "" {
			spec.Description = v
		}
	}
	if p, ok := t.(ToolParamSpec); ok {
		if v := p.Parameters(); v != nil {
			spec.Parameters = v
		}
	}
	if ro, ok := t.(ToolReadOnly); ok {
		spec.ReadOnly = ro.ReadOnly()
	}
	if chk, ok := t.(ToolReadOnlyCheck); ok {
		spec.ReadOnlyCheck = chk.ReadOnlyCheck
	}
	if caps, ok := t.(ToolCapabilities); ok {
		spec.Capabilities = caps.Capabilities()
	}
	if hint, ok := t.(ToolApprovalHint); ok {
		spec.ApprovalHint = hint.ApprovalHint()
	}
	if p, ok := t.(ToolSupportsParallel); ok {
		spec.SupportsParallel = p.SupportsParallel()
	}
	return spec
}

func IsReadOnlyToolCall(spec ToolSpec, call ToolCall) bool {
	if spec.ReadOnlyCheck != nil {
		args := map[string]any{}
		if err := json.Unmarshal([]byte(call.Input), &args); err == nil {
			if spec.ReadOnlyCheck(args) {
				return true
			}
			return false
		}
		return false
	}
	return spec.ReadOnly
}
