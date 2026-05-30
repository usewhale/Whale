package service

import (
	"strings"
)

func localSubmitResultEvent(status, text string) Event {
	return Event{Kind: EventLocalSubmitResult, Text: text, Status: status, Metadata: map[string]any{EventMetadataLocalSubmit: true}}
}

func localSubmitDoneEvent() Event {
	return Event{Kind: EventLocalSubmitDone, Metadata: map[string]any{EventMetadataLocalSubmit: true}}
}

func btwQuestionFromLine(line string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || fields[0] != "/btw" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "/btw")), true
}

func autoAcceptMessage(enabled bool) string {
	if enabled {
		return "Session auto-accept enabled"
	}
	return "Session auto-accept disabled"
}
