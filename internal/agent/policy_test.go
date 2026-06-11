package agent

import "testing"

func TestDefaultToolPolicyShellRules(t *testing.T) {
	p := DefaultToolPolicy{}
	spec := ToolSpec{Name: "shell_run"}
	allow := p.Decide(spec, ToolCall{Name: "shell_run", Input: `{"command":"echo hi"}`})
	if !allow.Allow || allow.RequiresApproval {
		t.Fatalf("expected default shell allow decision: %+v", allow)
	}
	deny := p.Decide(spec, ToolCall{Name: "shell_run", Input: `{"command":"rm -rf /tmp/x"}`})
	if deny.Allow || deny.Code != "permission_denied" {
		t.Fatalf("expected rm -rf deny decision: %+v", deny)
	}
}

func TestDefaultToolPolicyMultiEditAllowedByDefault(t *testing.T) {
	p := DefaultToolPolicy{}
	spec := ToolSpec{Name: "multi_edit"}
	d := p.Decide(spec, ToolCall{Name: "multi_edit", Input: `{"file_path":"a.txt","edits":[{"search":"old","replace":"new"}]}`})
	if !d.Allow || d.RequiresApproval || d.Code != "permission_allow" {
		t.Fatalf("unexpected multi_edit policy decision: %+v", d)
	}
}
