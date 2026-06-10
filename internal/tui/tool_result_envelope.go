package tui

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func parseToolEnvelope(raw string) toolResultEnvelope {
	env, _ := parseToolEnvelopeOK(raw)
	return env
}

// toolEnvelopeFromStructured builds the TUI's envelope view from the
// channel-separated fields carried on protocol events. The payload map is
// the canonical core payload: envelope data plus the reserved keys
// message/summary/truncated/metadata. Returns false when the event predates
// the structured fields (callers fall back to parsing the text).
func toolEnvelopeFromStructured(outcome, code string, payload map[string]any) (toolResultEnvelope, bool) {
	if strings.TrimSpace(outcome) == "" || payload == nil {
		return toolResultEnvelope{}, false
	}
	success := outcome == string(core.OutcomeSuccess) || outcome == string(core.OutcomeNoResult)
	metrics, _ := payload["metrics"].(map[string]any)
	innerPayload, _ := payload["payload"].(map[string]any)
	diagnosis, _ := payload["diagnosis"].(map[string]any)
	metadata, _ := payload["metadata"].(map[string]any)
	status := strings.TrimSpace(core.AsString(payload["status"]))
	if status == "" {
		status = "ok"
	}
	message, _ := payload["message"].(string)
	summary, _ := payload["summary"].(string)
	return toolResultEnvelope{
		success:    success,
		hasSuccess: true,
		ok:         success,
		hasOK:      true,
		code:       strings.TrimSpace(code),
		message:    message,
		summary:    strings.TrimSpace(summary),
		status:     status,
		data:       payload,
		metrics:    metrics,
		payload:    innerPayload,
		diagnosis:  diagnosis,
		metadata:   metadata,
	}, true
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
	status := strings.TrimSpace(core.AsString(data["status"]))
	if status == "" {
		status = "ok"
	}
	return toolResultEnvelope{
		success:    body.Success,
		hasSuccess: strings.Contains(raw, `"success"`),
		ok:         body.OK,
		hasOK:      strings.Contains(raw, `"ok"`),
		code:       strings.TrimSpace(body.Code),
		message:    core.FirstNonEmpty(body.Message, body.Error),
		summary:    strings.TrimSpace(body.Summary),
		status:     status,
		data:       data,
		metrics:    metrics,
		payload:    payload,
		diagnosis:  diagnosis,
		metadata:   body.Metadata,
	}, true
}
