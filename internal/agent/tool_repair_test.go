package agent

import (
	"github.com/usewhale/whale/internal/core"
	"testing"
)

func TestRepairTruncatedJSON_ClosesTruncatedJSON(t *testing.T) {
	in := `{"file_path":"README.md","offset":0`
	res := repairTruncatedJSON(in)
	if !res.changed {
		t.Fatalf("expected changed=true")
	}
	if res.repaired != `{"file_path":"README.md","offset":0}` {
		t.Fatalf("unexpected repaired input: %s", res.repaired)
	}
}

func TestRepairTruncatedJSON_LeavesValidJSONUntouched(t *testing.T) {
	in := `{"k":"v"}`
	res := repairTruncatedJSON(in)
	if res.changed {
		t.Fatalf("expected changed=false")
	}
	if res.repaired != in {
		t.Fatalf("expected unchanged input")
	}
}

func TestRepairTruncatedJSON_FillsTruncatedKeyValueWithNull(t *testing.T) {
	in := `{"file_path":"README.md","offset":0,"limit":`
	res := repairTruncatedJSON(in)
	if !res.changed {
		t.Fatalf("expected changed=true")
	}
	if res.repaired != `{"file_path":"README.md","offset":0,"limit": null}` {
		t.Fatalf("unexpected repaired input: %s", res.repaired)
	}
}

func TestRepairTruncatedJSON_RemovesTrailingCommaAtStructuralBoundary(t *testing.T) {
	in := `{"file_path":"README.md","offset":0,`
	res := repairTruncatedJSON(in)
	if !res.changed {
		t.Fatalf("expected changed=true")
	}
	if res.repaired != `{"file_path":"README.md","offset":0}` {
		t.Fatalf("unexpected repaired input: %s", res.repaired)
	}
}

func TestRepairTruncatedJSON_DoesNotSalvageMalformedPrefix(t *testing.T) {
	in := `{"edits":[{"replace":"(check that nono can read this file)","search":"(check that nono can read this file)"},{"replace":""security add-generic-password -s \"nono\" -a","search":"security add-generic-password -s \"nono\" -a"}],"file_path":"veto/veto-proxy/src/route.rs"}`
	res := repairTruncatedJSON(in)
	if res.changed {
		t.Fatalf("expected changed=false")
	}
	if res.repaired != in {
		t.Fatalf("expected malformed input to be preserved, got: %s", res.repaired)
	}
}

func TestToolCallRepair_BlocksRepeatedCalls(t *testing.T) {
	r := newToolCallRepair(stormConfig{WindowSize: 6, Threshold: 3})
	calls := []ToolCall{
		{ID: "1", Name: "list_dir", Input: `{"path":"."}`},
		{ID: "2", Name: "list_dir", Input: `{"path":"."}`},
		{ID: "3", Name: "list_dir", Input: `{"path":"."}`},
		{ID: "4", Name: "list_dir", Input: `{"path":"."}`},
	}
	allowed := map[string]bool{"list_dir": true}
	declared := make([]core.ToolCall, 0, len(calls))
	for _, c := range calls {
		declared = append(declared, core.ToolCall(c))
	}
	kept, dropped, rep := r.process(declared, "", "", allowed, nil)
	if len(kept) != 3 {
		t.Fatalf("expected 3 kept, got %d", len(kept))
	}
	if len(dropped) != 1 {
		t.Fatalf("expected 1 dropped, got %d", len(dropped))
	}
	if !dropped[0].IsError() {
		t.Fatalf("expected dropped call to be error")
	}
	if dropped[0].ToolCallID != "4" {
		t.Fatalf("unexpected dropped call id: %s", dropped[0].ToolCallID)
	}
	if rep.stormsBroken != 1 {
		t.Fatalf("expected 1 storm broken, got %d", rep.stormsBroken)
	}
}

func TestScavengeToolCalls_RecoversCall(t *testing.T) {
	reasoning := `I should call tool: {"name":"read_file","arguments":{"file_path":"README.md","offset":0}}`
	allowed := map[string]bool{"read_file": true}
	calls, count := scavengeToolCalls(reasoning, "", allowed, nil)
	if count != 1 {
		t.Fatalf("expected 1 scavenged call, got %d", count)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("unexpected scavenged calls: %+v", calls)
	}
	if calls[0].Input != `{"file_path":"README.md","offset":0}` {
		t.Fatalf("unexpected input: %s", calls[0].Input)
	}
}

func TestScavengeToolCalls_RecoversFunctionCallShape(t *testing.T) {
	reasoning := `trace: {"function_call":{"name":"read_file","arguments":"{\"file_path\":\"README.md\",\"offset\":0}"}}`
	allowed := map[string]bool{"read_file": true}
	calls, count := scavengeToolCalls(reasoning, "", allowed, nil)
	if count != 1 {
		t.Fatalf("expected 1 scavenged call, got %d", count)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("unexpected name: %s", calls[0].Name)
	}
	if calls[0].Input != `{"file_path":"README.md","offset":0}` {
		t.Fatalf("unexpected input: %s", calls[0].Input)
	}
}

func TestScavengeToolCalls_RecoversFunctionWrapperShape(t *testing.T) {
	reasoning := `trace: {"function":{"name":"grep","arguments":{"pattern":"repair","path":"internal"}}}`
	allowed := map[string]bool{"grep": true}
	calls, count := scavengeToolCalls(reasoning, "", allowed, nil)
	if count != 1 {
		t.Fatalf("expected 1 scavenged call, got %d", count)
	}
	if calls[0].Name != "grep" {
		t.Fatalf("unexpected name: %s", calls[0].Name)
	}
	if calls[0].Input != `{"path":"internal","pattern":"repair"}` && calls[0].Input != `{"pattern":"repair","path":"internal"}` {
		t.Fatalf("unexpected input: %s", calls[0].Input)
	}
}
