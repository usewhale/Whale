package service

import (
	"strings"
)

func buildImplementPlanPrompt(_ string) string {
	return strings.TrimSpace(`Implement the plan.

Before editing, initialize and maintain an update_plan checklist for the implementation work. Keep exactly one item in_progress while working and mark items completed as soon as they are done.`)
}
