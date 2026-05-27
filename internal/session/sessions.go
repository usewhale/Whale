package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SessionSummary struct {
	ID           string
	ModTime      time.Time
	Size         int64
	Meta         SessionMeta
	Conversation string
}

const toolInputEventsSuffix = ".tool_input_events.jsonl"

func ListSessions(sessionsDir string, limit int) ([]SessionSummary, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]SessionSummary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !isSessionJSONLName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if id == "" {
			continue
		}
		if isSubagentSessionID(id) {
			continue
		}
		meta, err := LoadSessionMeta(sessionsDir, id)
		if err == nil && strings.TrimSpace(meta.Kind) == "subagent" {
			continue
		}
		out = append(out, SessionSummary{
			ID:      id,
			ModTime: info.ModTime(),
			Size:    info.Size(),
			Meta:    meta,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i].Conversation = SessionConversationTitle(sessionsDir, out[i].ID, out[i].Meta)
	}
	return out, nil
}

func SessionConversationTitle(sessionsDir, sessionID string, meta SessionMeta) string {
	if title := strings.TrimSpace(meta.Title); title != "" {
		return singleLine(title)
	}
	if title, err := FirstVisibleUserMessage(sessionsDir, sessionID); err == nil && title != "" {
		return title
	}
	return "(no message yet)"
}

func FirstVisibleUserMessage(sessionsDir, sessionID string) (string, error) {
	path := FindSessionPathByID(sessionsDir, sessionID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg struct {
			Role   string
			Text   string
			Hidden bool
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Role != "user" || msg.Hidden {
			continue
		}
		if text := strings.TrimSpace(msg.Text); text != "" {
			return singleLine(text), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func singleLine(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func FindSessionPathByID(sessionsDir, sessionID string) string {
	id := sanitizeSessionID(sessionID)
	return filepath.Join(sessionsDir, id+".jsonl")
}

func isSessionJSONLName(name string) bool {
	return strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, toolInputEventsSuffix)
}

func isSubagentSessionID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.Contains(id, "--subagent-") || strings.HasPrefix(id, "subagent-")
}

func sanitizeSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	v = strings.ReplaceAll(v, "/", "_")
	v = strings.ReplaceAll(v, "\\", "_")
	return v
}
