package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/usewhale/whale/internal/telemetry"
)

func TestApprovalTelemetryPersistsRequestAndDecision(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalProvider{}
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithToolPolicy(editApprovalPolicy()),
		WithSessionsDir(dir),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			return ApprovalDeny
		}),
	)

	for range mustRunStream(t, a, "s-approval-events", "go") {
	}

	events := readApprovalEvents(t, dir, "s-approval-events")
	if len(events) < 2 {
		t.Fatalf("expected approval events, got %+v", events)
	}
	if events[0].Event != approvalEventRequired || events[0].Tool != "write" || events[0].ToolCallID != "tc-w-1" {
		t.Fatalf("unexpected first approval event: %+v", events[0])
	}
	if events[1].Event != approvalEventDenied || events[1].Code != "permission_required" {
		t.Fatalf("unexpected second approval event: %+v", events[1])
	}
}

func TestApprovalTelemetryPersistsReadOnlyAutoDenied(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalProvider{}
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithSessionsDir(dir),
	)

	for range mustRunStreamWithOptions(t, a, "s-auto-denied", "review", RunOptions{ReadOnly: true}) {
	}

	events := readApprovalEvents(t, dir, "s-auto-denied")
	if len(events) != 1 {
		t.Fatalf("expected one auto-denied event, got %+v", events)
	}
	if events[0].Event != approvalEventPolicyDenied || events[0].Code != "read_only_turn_denied" {
		t.Fatalf("unexpected auto-denied event: %+v", events[0])
	}
}

func readApprovalEvents(t *testing.T, sessionsDir, sessionID string) []telemetry.ApprovalEvent {
	t.Helper()
	f, err := os.Open(telemetry.ApprovalEventsPath(sessionsDir, sessionID))
	if err != nil {
		t.Fatalf("open approval events: %v", err)
	}
	defer f.Close()
	var out []telemetry.ApprovalEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.ApprovalEvent
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("unmarshal approval event: %v", err)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan approval events: %v", err)
	}
	return out
}

func mustRunStream(t *testing.T, a *Agent, sessionID, input string) <-chan AgentEvent {
	t.Helper()
	events, err := a.RunStream(context.Background(), sessionID, input)
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	return events
}

func mustRunStreamWithOptions(t *testing.T, a *Agent, sessionID, input string, opts RunOptions) <-chan AgentEvent {
	t.Helper()
	events, err := a.RunStreamWithTurnOptions(context.Background(), sessionID, input, opts)
	if err != nil {
		t.Fatalf("run stream with options: %v", err)
	}
	return events
}
