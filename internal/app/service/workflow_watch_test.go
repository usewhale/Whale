package service

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestWorkflowRunIDFromToolResultUsesWorkflowMetadata(t *testing.T) {
	got := workflowRunIDFromToolResult(&core.ToolResult{
		Name: "workflow",
		Metadata: map[string]any{
			"workflow_run_id": " run-123 ",
		},
	})
	if got != "run-123" {
		t.Fatalf("workflow run id = %q, want run-123", got)
	}
}

func TestWorkflowRunIDFromToolResultIgnoresNonWorkflowTools(t *testing.T) {
	got := workflowRunIDFromToolResult(&core.ToolResult{
		Name: "shell_run",
		Metadata: map[string]any{
			"workflow_run_id": "run-123",
		},
	})
	if got != "" {
		t.Fatalf("non-workflow tool should not start workflow watch, got %q", got)
	}
}
