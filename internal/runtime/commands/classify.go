package commands

import (
	"strconv"
	"strings"
)

type SubmitClass int

const (
	SubmitText SubmitClass = iota
	SubmitLocalReadOnly
	SubmitLocalUI
	SubmitLocalMode
	SubmitLocalMutating
	SubmitExit
	SubmitTurnStarting
	SubmitUsageError
)

type SubmitClassification struct {
	Line  string
	Class SubmitClass
}

func (c SubmitClassification) LocalNoTurn() bool {
	switch c.Class {
	case SubmitLocalReadOnly, SubmitLocalUI, SubmitLocalMode, SubmitLocalMutating, SubmitExit, SubmitUsageError:
		return true
	default:
		return false
	}
}

func (c SubmitClassification) BusyImmediate() bool {
	return c.Class == SubmitLocalReadOnly || c.Class == SubmitExit || c.Line == "/focus" || strings.HasPrefix(c.Line, "/btw ")
}

func (c SubmitClassification) SubmitBarrier() bool {
	switch c.Class {
	case SubmitLocalUI, SubmitLocalMode, SubmitLocalMutating, SubmitUsageError:
		return true
	default:
		return false
	}
}

func ClassifySubmit(line, help string, localCommands ...string) SubmitClassification {
	line = ExpandUniqueSlashPrefix(strings.TrimSpace(line), help, localCommands...)
	if !LooksLikeSlashCommand(line) {
		return SubmitClassification{Line: line, Class: SubmitText}
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return SubmitClassification{Line: line, Class: SubmitText}
	}
	head := fields[0]
	class := classifySlashFields(head, fields, line)
	return SubmitClassification{Line: line, Class: class}
}

func classifySlashFields(head string, fields []string, line string) SubmitClass {
	switch head {
	case "/status", "/mcp", "/feedback", "/help", "/diff":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		return SubmitUsageError
	case "/copy":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		if len(fields) == 2 && positiveInt(fields[1]) {
			return SubmitLocalReadOnly
		}
		return SubmitUsageError
	case "/memory":
		return classifyMemoryFields(fields)
	case "/stats":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		if len(fields) == 2 && validStatsView(fields[1]) {
			return SubmitLocalReadOnly
		}
		return SubmitUsageError
	case "/doctor":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		return SubmitUsageError
	case "/workflows":
		return classifyWorkflowsFields(fields)
	case "/deep-research":
		return classifyDeepResearchFields(fields)
	case "/model", "/permissions", "/skills", "/plugins", "/resume":
		if len(fields) == 1 {
			return SubmitLocalUI
		}
		return SubmitUsageError
	case "/hooks":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		if len(fields) >= 3 && fields[1] == "trust" {
			return SubmitLocalMutating
		}
		return SubmitUsageError
	case "/review":
		if len(fields) == 1 {
			return SubmitLocalUI
		}
		return SubmitTurnStarting
	case "/btw":
		if strings.TrimSpace(strings.TrimPrefix(line, "/btw")) == "" {
			return SubmitUsageError
		}
		return SubmitLocalReadOnly
	case "/focus":
		if len(fields) == 1 {
			return SubmitLocalUI
		}
		return SubmitUsageError
	case "/open":
		return SubmitLocalUI
	case "/agent":
		if len(fields) == 1 {
			return SubmitLocalMode
		}
		return SubmitUsageError
	case "/ask":
		if len(fields) == 1 {
			return SubmitLocalMode
		}
		return SubmitTurnStarting
	case "/plan":
		if len(fields) == 1 {
			return SubmitLocalMode
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "/plan"))
		if payload == "" || payload == "show" || payload == "on" || payload == "off" {
			return SubmitUsageError
		}
		return SubmitTurnStarting
	case "/goal":
		return classifyGoalFields(fields)
	case "/new":
		if len(fields) <= 2 {
			return SubmitLocalMutating
		}
		return SubmitUsageError
	case "/fork":
		if len(fields) <= 2 {
			return SubmitLocalMutating
		}
		return SubmitUsageError
	case "/rewind", "/checkpoint":
		if len(fields) == 1 {
			return SubmitLocalUI
		}
		return SubmitUsageError
	case "/clear":
		if len(fields) == 1 {
			return SubmitLocalMutating
		}
		return SubmitUsageError
	case "/exit":
		if len(fields) == 1 {
			return SubmitExit
		}
		return SubmitUsageError
	case "/init":
		if len(fields) == 1 {
			return SubmitTurnStarting
		}
		return SubmitUsageError
	case "/compact":
		if len(fields) == 1 {
			return SubmitTurnStarting
		}
		return SubmitUsageError
	default:
		return SubmitUsageError
	}
}

func positiveInt(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != "0"
}

func classifyDeepResearchFields(fields []string) SubmitClass {
	if len(fields) < 2 {
		return SubmitUsageError
	}
	for i := 1; i < len(fields); i++ {
		field := fields[i]
		switch {
		case field == "--resume":
			i++
			if i >= len(fields) || strings.TrimSpace(fields[i]) == "" || strings.HasPrefix(fields[i], "--") {
				return SubmitUsageError
			}
		case strings.HasPrefix(field, "--resume="):
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
				return SubmitUsageError
			}
		case strings.HasPrefix(field, "--"):
			return SubmitUsageError
		default:
			return SubmitLocalMutating
		}
	}
	return SubmitUsageError
}

func classifyGoalFields(fields []string) SubmitClass {
	if len(fields) == 1 {
		return SubmitLocalReadOnly
	}
	if len(fields) == 2 {
		switch fields[1] {
		case "status":
			return SubmitLocalReadOnly
		case "pause", "clear":
			return SubmitLocalMutating
		case "resume":
			return SubmitTurnStarting
		}
	}
	if fields[1] == "status" || fields[1] == "pause" || fields[1] == "resume" || fields[1] == "clear" {
		return SubmitUsageError
	}
	objectiveWords := 0
	for i := 1; i < len(fields); i++ {
		field := fields[i]
		if objectiveWords > 0 {
			objectiveWords += len(fields) - i
			break
		}
		switch {
		case field == "--tokens":
			i++
			if i >= len(fields) || !validGoalTokenBudget(fields[i]) {
				return SubmitUsageError
			}
		case strings.HasPrefix(field, "--tokens="):
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 || !validGoalTokenBudget(parts[1]) {
				return SubmitUsageError
			}
		case strings.HasPrefix(field, "--"):
			return SubmitUsageError
		default:
			objectiveWords++
		}
	}
	if objectiveWords == 0 {
		return SubmitUsageError
	}
	return SubmitTurnStarting
}

func validGoalTokenBudget(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "--") {
		return false
	}
	multiplier := 1
	last := raw[len(raw)-1]
	if last == 'k' || last == 'K' || last == 'm' || last == 'M' {
		raw = raw[:len(raw)-1]
		if raw == "" {
			return false
		}
		if last == 'm' || last == 'M' {
			multiplier = 1_000_000
		} else {
			multiplier = 1_000
		}
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return false
	}
	return int(value*float64(multiplier)+0.5) > 0
}

func classifyWorkflowsFields(fields []string) SubmitClass {
	if len(fields) == 1 {
		return SubmitLocalReadOnly
	}
	return SubmitUsageError
}

func validStatsView(view string) bool {
	switch view {
	case "usage", "tools", "repair", "recent", "profile", "all":
		return true
	default:
		return false
	}
}

func classifyMemoryFields(fields []string) SubmitClass {
	if len(fields) == 1 {
		return SubmitLocalReadOnly
	}
	if len(fields) == 2 && (fields[1] == "list" || fields[1] == "path") {
		return SubmitLocalReadOnly
	}
	if len(fields) == 3 && fields[1] == "show" {
		return SubmitLocalReadOnly
	}
	if len(fields) == 3 && fields[1] == "forget" {
		return SubmitLocalMutating
	}
	return SubmitUsageError
}
