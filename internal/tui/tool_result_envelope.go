package tui

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func parseToolEnvelope(raw string) toolResultEnvelope {
	env, _ := parseToolEnvelopeOK(raw)
	return env
}

func parseToolEnvelopeOK(raw string) (toolResultEnvelope, bool) {
	body, ok := core.ParseToolEnvelope(raw)
	if !ok {
		return toolResultEnvelope{}, false
	}
	data := body.Data
	metrics, _ := data["metrics"].(map[string]any)
	payload, _ := data["payload"].(map[string]any)
	diagnosis, _ := data["diagnosis"].(map[string]any)
	status := strings.TrimSpace(asString(data["status"]))
	if status == "" {
		status = "ok"
	}
	return toolResultEnvelope{
		success:    body.Success,
		hasSuccess: strings.Contains(raw, `"success"`),
		ok:         body.OK,
		hasOK:      strings.Contains(raw, `"ok"`),
		code:       strings.TrimSpace(body.Code),
		message:    firstNonEmpty(body.Message, body.Error),
		summary:    strings.TrimSpace(body.Summary),
		status:     status,
		data:       data,
		metrics:    metrics,
		payload:    payload,
		diagnosis:  diagnosis,
		metadata:   body.Metadata,
	}, true
}
