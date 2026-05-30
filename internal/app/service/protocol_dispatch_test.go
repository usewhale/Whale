package service

import (
	"testing"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

func TestServiceIntentKindMapsProtocolKinds(t *testing.T) {
	tests := []struct {
		in   protocol.IntentKind
		want IntentKind
	}{
		{protocol.IntentSubmit, IntentSubmit},
		{protocol.IntentSubmitLocal, IntentSubmitLocal},
		{protocol.IntentAllowTool, IntentAllowTool},
		{protocol.IntentAllowToolForSession, IntentAllowToolForSession},
		{protocol.IntentDenyTool, IntentDenyTool},
		{protocol.IntentCancelToolApproval, IntentCancelToolApproval},
		{protocol.IntentSubmitUserInput, IntentSubmitUserInput},
		{protocol.IntentCancelUserInput, IntentCancelUserInput},
		{protocol.IntentSelectSession, IntentSelectSession},
		{protocol.IntentRequestSessions, IntentRequestSessions},
		{protocol.IntentRequestExit, IntentRequestExit},
		{protocol.IntentShutdown, IntentShutdown},
		{protocol.IntentSetModelAndEffort, IntentSetModelAndEffort},
		{protocol.IntentSetApprovalMode, IntentSetApprovalMode},
		{protocol.IntentSetViewMode, IntentSetViewMode},
		{protocol.IntentToggleMode, IntentToggleMode},
		{protocol.IntentImplementPlan, IntentImplementPlan},
		{protocol.IntentDeclinePlan, IntentDeclinePlan},
		{protocol.IntentRequestSkillsManage, IntentRequestSkillsManage},
		{protocol.IntentSetSkillEnabled, IntentSetSkillEnabled},
		{protocol.IntentSetPluginEnabled, IntentSetPluginEnabled},
		{protocol.IntentWorktreeExitChoice, IntentWorktreeExitChoice},
	}
	for _, tt := range tests {
		if got := serviceIntentKind(tt.in); got != tt.want {
			t.Fatalf("serviceIntentKind(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCoreUserInputResponseMapsProtocolDTO(t *testing.T) {
	got := coreUserInputResponse(&protocol.UserInputResponse{Answers: []protocol.UserInputAnswer{{
		ID:      "target",
		Label:   "Other",
		Value:   "custom",
		IsOther: true,
	}}})
	if got == nil || len(got.Answers) != 1 {
		t.Fatalf("unexpected response: %+v", got)
	}
	answer := got.Answers[0]
	if answer.ID != "target" || answer.Label != "Other" || answer.Value != "custom" || !answer.IsOther {
		t.Fatalf("unexpected answer: %+v", answer)
	}
}

func TestAppSkillBindingMapsProtocolDTO(t *testing.T) {
	got := appSkillBinding(&protocol.SkillBinding{Name: "review", SkillFilePath: "/tmp/SKILL.md"})
	if got == nil || got.Name != "review" || got.SkillFilePath != "/tmp/SKILL.md" {
		t.Fatalf("unexpected binding: %+v", got)
	}
}
