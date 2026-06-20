package session

import (
	"strings"
	"testing"
)

func TestPlanModeInstructionMatchesCodexStyleFinalizationRules(t *testing.T) {
	instructions := PlanModeInstruction()
	for _, want := range []string{
		"Plan Mode is a collaboration mode",
		"Ground the plan in the actual environment",
		"Finalization rule",
		"output exactly one <proposed_plan> block",
		"Put the opening tag on its own line",
		"The UI owns the implementation confirmation",
		"Produce at most one <proposed_plan> block per turn",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("plan mode instruction missing %q:\n%s", want, instructions)
		}
	}
}
