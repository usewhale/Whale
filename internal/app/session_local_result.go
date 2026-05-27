package app

import (
	"fmt"

	"github.com/usewhale/whale/internal/agent"
)

func buildCompactLocalResult(info agent.CompactInfo, text string) *LocalResult {
	fields := []LocalResultField{
		{Label: "Result", Value: compactResultValue(info), Tone: compactResultTone(info)},
		{Label: "Messages", Value: fmt.Sprintf("%d -> %d", info.MessagesBefore, info.MessagesAfter)},
		{Label: "Tokens", Value: fmt.Sprintf("~%d -> ~%d", info.BeforeEstimate, info.AfterEstimate)},
	}
	if !info.Compacted {
		fields = []LocalResultField{{Label: "Result", Value: "nothing to compact", Tone: "muted"}}
	}
	return &LocalResult{Kind: "compact", Title: "Compact", Fields: fields, PlainText: text}
}

func compactResultValue(info agent.CompactInfo) string {
	if !info.Compacted {
		return "nothing to compact"
	}
	return "compacted conversation"
}

func compactResultTone(info agent.CompactInfo) string {
	if info.Compacted {
		return "info"
	}
	return "muted"
}

func buildNewSessionLocalResult(sessionID, previousID, mode string, dropped int, text string) *LocalResult {
	fields := []LocalResultField{
		{Label: "Session", Value: sessionID, Tone: "info"},
		{Label: "Previous", Value: previousID},
		{Label: "Resume previous", Value: "whale resume " + previousID},
		{Label: "Mode", Value: mode},
	}
	if dropped > 0 {
		fields = append(fields, LocalResultField{Label: "Dropped messages", Value: fmt.Sprintf("%d", dropped), Tone: "warn"})
	}
	return &LocalResult{Kind: "new_session", Title: "New session", Fields: fields, PlainText: text}
}

func buildForkLocalResult(title, sourceID, nextID, resume string, text string) *LocalResult {
	return &LocalResult{
		Kind:  "fork",
		Title: "Forked conversation",
		Fields: []LocalResultField{
			{Label: "Title", Value: title, Tone: "info"},
			{Label: "Session", Value: nextID, Tone: "info"},
			{Label: "Original", Value: sourceID},
			{Label: "Resume original", Value: resume},
		},
		PlainText: text,
	}
}
