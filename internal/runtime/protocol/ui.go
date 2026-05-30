package protocol

import "strings"

const (
	ViewModeDefault = "default"
	ViewModeFocus   = "focus"
)

const (
	SkillAvailabilityReady      = "ready"
	SkillAvailabilityNeedsSetup = "needs_setup"
	SkillAvailabilityDisabled   = "disabled"
	SkillAvailabilityProblem    = "problem"
)

func ViewModeToggleMessage(mode string) string {
	if strings.TrimSpace(mode) == ViewModeFocus {
		return "Focus view enabled"
	}
	return "Focus view disabled"
}

func OpenCommandSuccessText(path string) string {
	return "Opened " + path
}
