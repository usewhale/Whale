package telemetry

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ApprovalEventsSuffix = ".approval_events.jsonl"

type ApprovalEvent struct {
	TS                 int64    `json:"ts"`
	Session            string   `json:"session"`
	Model              string   `json:"model,omitempty"`
	AssistantMessageID string   `json:"assistant_message_id,omitempty"`
	ToolCallID         string   `json:"tool_call_id,omitempty"`
	Tool               string   `json:"tool,omitempty"`
	Event              string   `json:"event"`
	Source             string   `json:"source,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	Code               string   `json:"code,omitempty"`
	Phase              string   `json:"phase,omitempty"`
	MatchedRule        string   `json:"matched_rule,omitempty"`
	Key                string   `json:"key,omitempty"`
	Keys               []string `json:"keys,omitempty"`
	Scope              string   `json:"scope,omitempty"`
}

func ApprovalEventsPath(sessionsDir, sessionID string) string {
	return filepath.Join(strings.TrimSpace(sessionsDir), sanitizeSessionID(sessionID)+ApprovalEventsSuffix)
}

func AppendApprovalEvent(sessionsDir string, rec ApprovalEvent, now time.Time) error {
	sessionsDir = strings.TrimSpace(sessionsDir)
	if sessionsDir == "" || strings.TrimSpace(rec.Session) == "" || strings.TrimSpace(rec.Event) == "" {
		return nil
	}
	rec = sanitizeApprovalEvent(rec)
	if rec.TS == 0 {
		rec.TS = now.UnixMilli()
	}
	path := ApprovalEventsPath(sessionsDir, rec.Session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func sanitizeApprovalEvent(rec ApprovalEvent) ApprovalEvent {
	rec.Key = sanitizeApprovalKey(rec.Key)
	if len(rec.Keys) > 0 {
		keys := make([]string, 0, len(rec.Keys))
		for _, key := range rec.Keys {
			if sanitized := sanitizeApprovalKey(key); sanitized != "" {
				keys = append(keys, sanitized)
			}
		}
		rec.Keys = keys
	}
	if rec.Tool == "shell_run" {
		rec.Scope = sanitizeShellApprovalScope(rec.Scope)
	}
	return rec
}

func sanitizeApprovalKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if command, ok := strings.CutPrefix(key, "shell_run|cmd:"); ok {
		return "shell_run|cmd_sha256:" + approvalHash(command)
	}
	if tool, raw, ok := strings.Cut(key, "|"); ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(tool) + "|input_sha256:" + approvalHash(raw)
	}
	return key
}

func sanitizeShellApprovalScope(scope string) string {
	scope = strings.TrimSpace(scope)
	switch scope {
	case "", "shell", "this shell command", "this bounded shell command family", "this safe shell command family":
		return scope
	}
	if strings.HasPrefix(scope, "shell_run|cmd:") {
		return sanitizeApprovalKey(scope)
	}
	return "shell_scope_sha256:" + approvalHash(scope)
}

func approvalHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return fmt.Sprintf("%x", sum[:])
}
