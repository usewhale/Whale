package app

import "strings"

func shortFingerprint(v string) string {
	const n = 12
	if len(v) <= n {
		return v
	}
	return v[:n]
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

func SupportedReasoningEfforts() []string {
	return []string{"high", "max"}
}

func normalizeEffort(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "high", "max":
		return strings.ToLower(strings.TrimSpace(v))
	case "low", "medium":
		return "high"
	case "xhigh":
		return "max"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func NormalizeEffort(v string) string {
	return normalizeEffort(v)
}

func OnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
