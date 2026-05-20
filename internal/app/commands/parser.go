package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/usewhale/whale/internal/session"
)

type Result struct {
	Handled            bool
	ShouldExit         bool
	ClearScreen        bool
	SessionID          string
	Output             string
	ShowStatus         bool
	Mode               string
	AskPrompt          string
	PlanPrompt         string
	InitMemory         bool
	ShowSkills         bool
	ReviewPrompt       string
	AllowShellPrefixes []string
	ForkName           string
	BtwQuestion        string
}

func NewSessionID(now time.Time) string {
	u, err := uuid.NewV7()
	if err != nil {
		return now.Format("20060102-150405")
	}
	return u.String()
}

func Parse(line, currentSessionID string, now time.Time) (Result, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return Result{}, nil
	}
	if trimmed == "/exit" {
		return Result{Handled: true, ShouldExit: true, SessionID: currentSessionID}, nil
	}
	if trimmed == "/status" {
		return Result{Handled: true, SessionID: currentSessionID, ShowStatus: true}, nil
	}
	fields := strings.Fields(trimmed)
	head := ""
	if len(fields) > 0 {
		head = fields[0]
	}
	if head == "/resume" && len(fields) > 1 {
		return Result{}, fmt.Errorf("usage: /resume")
	}
	if head == "/new" {
		next := ""
		if len(fields) > 2 {
			return Result{}, fmt.Errorf("usage: /new [id]")
		}
		if len(fields) == 2 {
			next = strings.TrimSpace(fields[1])
		}
		if next == "" {
			next = NewSessionID(now)
		}
		return Result{Handled: true, SessionID: next, Output: fmt.Sprintf("new session: %s", next)}, nil
	}
	if head == "/fork" {
		if len(fields) > 2 {
			return Result{}, fmt.Errorf("usage: /fork [name]")
		}
		name := ""
		if len(fields) == 2 {
			name = strings.TrimSpace(fields[1])
		}
		return Result{Handled: true, SessionID: currentSessionID, ForkName: name}, nil
	}
	if trimmed == "/clear" {
		return Result{Handled: true, SessionID: currentSessionID, ClearScreen: true}, nil
	}
	if trimmed == "/agent" {
		return Result{Handled: true, SessionID: currentSessionID, Mode: string(session.ModeAgent)}, nil
	}
	if trimmed == "/ask" {
		return Result{Handled: true, SessionID: currentSessionID, Mode: string(session.ModeAsk)}, nil
	}
	if strings.HasPrefix(trimmed, "/ask ") {
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "/ask"))
		return Result{Handled: true, SessionID: currentSessionID, Mode: string(session.ModeAsk), AskPrompt: payload}, nil
	}
	if trimmed == "/plan" {
		return Result{Handled: true, SessionID: currentSessionID, Mode: string(session.ModePlan)}, nil
	}
	if strings.HasPrefix(trimmed, "/plan ") {
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "/plan"))
		if payload == "" || payload == "show" || payload == "on" || payload == "off" {
			return Result{}, fmt.Errorf("usage: /plan [prompt]")
		}
		return Result{Handled: true, SessionID: currentSessionID, Mode: string(session.ModePlan), PlanPrompt: payload}, nil
	}
	if trimmed == "/init" {
		return Result{Handled: true, SessionID: currentSessionID, InitMemory: true}, nil
	}
	if trimmed == "/skills" || strings.HasPrefix(trimmed, "/skills ") {
		fields := strings.Fields(trimmed)
		if len(fields) == 1 && fields[0] == "/skills" {
			return Result{Handled: true, SessionID: currentSessionID, ShowSkills: true}, nil
		}
		return Result{}, fmt.Errorf("usage: /skills")
	}
	if head == "/review" {
		args := strings.TrimSpace(strings.TrimPrefix(trimmed, "/review"))
		prompt, err := ReviewPromptFromArgs(args)
		if err != nil {
			return Result{}, err
		}
		allowPrefixes, err := ReviewShellAllowPrefixesFromArgs(args)
		if err != nil {
			return Result{}, err
		}
		return Result{Handled: true, SessionID: currentSessionID, ReviewPrompt: prompt, AllowShellPrefixes: allowPrefixes}, nil
	}
	if head == "/btw" {
		question := strings.TrimSpace(strings.TrimPrefix(trimmed, "/btw"))
		if question == "" {
			return Result{}, fmt.Errorf("Usage: /btw <your question>")
		}
		return Result{Handled: true, SessionID: currentSessionID, BtwQuestion: question}, nil
	}
	return Result{}, nil
}

func PlanPromptFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/plan ") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "/plan"))
	if payload == "" || payload == "show" || payload == "on" || payload == "off" {
		return "", false
	}
	return payload, true
}

func AskPromptFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/ask ") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "/ask"))
	if payload == "" {
		return "", false
	}
	return payload, true
}

func LooksLikeSlashCommand(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false
	}
	head := line
	if idx := strings.IndexAny(head, " \t"); idx >= 0 {
		head = head[:idx]
	}
	if len(head) <= 1 {
		return true
	}
	return !strings.Contains(head[1:], "/")
}

func ExpandUniqueSlashPrefix(line, help string, localCommands ...string) string {
	line = strings.TrimSpace(line)
	if !LooksLikeSlashCommand(line) || strings.Contains(line, " ") {
		return line
	}
	commands := append(ParseSlashCommands(help), localCommands...)
	matches := make([]string, 0, 1)
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, line) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) != 1 {
		return line
	}
	return matches[0]
}

func ParseSlashCommands(help string) []string {
	parts := strings.Split(help, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		field := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(field, "/") || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}
