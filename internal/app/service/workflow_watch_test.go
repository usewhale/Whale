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

func TestWorkflowConfirmationFromToolResult(t *testing.T) {
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    "workflow_confirmation_required",
		Data: map[string]any{
			"workflowName":   "review-spa",
			"workflowArgs":   `{"topic":"ok"}`,
			"workflowResume": "run-source",
		},
	})
	if err != nil {
		t.Fatalf("MarshalToolEnvelope: %v", err)
	}
	name, args, resume, script, saveAs, scriptPath, ok := workflowConfirmationFromToolResult(&core.ToolResult{
		Name:      "workflow",
		ModelText: content,
	})
	if !ok || name != "review-spa" || args != `{"topic":"ok"}` || resume != "run-source" || script != "" || saveAs != "" || scriptPath != "" {
		t.Fatalf("confirmation = %q %q %q %q %q %q %v", name, args, resume, script, saveAs, scriptPath, ok)
	}
}
