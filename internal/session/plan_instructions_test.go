package session

import (
	"strings"
	"testing"
)

func TestPlanModeInstructionDescribesPlanAsReply(t *testing.T) {
	instructions := PlanModeInstruction()
	for _, want := range []string{
		"Plan Mode is a collaboration mode",
		"Ground the plan in the actual environment",
		"How to present the plan",
		"write the plan as your final reply",
		"taken as your proposed plan",
		"the user is then asked to approve it",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("plan mode instruction missing %q:\n%s", want, instructions)
		}
	}
	// The plan-as-reply model must not reintroduce the fragile sentinel contract.
	if strings.Contains(instructions, "<proposed_plan>") {
		t.Fatalf("plan mode instruction should not reference the <proposed_plan> sentinel:\n%s", instructions)
	}
}
