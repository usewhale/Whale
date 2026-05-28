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

func TestDefaultToolPolicyApplyPatchAllowedByDefault(t *testing.T) {
	p := DefaultToolPolicy{}
	spec := ToolSpec{Name: "apply_patch"}
	d := p.Decide(spec, ToolCall{Name: "apply_patch", Input: `{"patch":"*** Begin Patch\n*** End Patch"}`})
	if !d.Allow || d.RequiresApproval || d.Code != "permission_allow" {
		t.Fatalf("unexpected apply_patch policy decision: %+v", d)
	}
}
