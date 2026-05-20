package commands

import (
	"strings"

	"github.com/usewhale/whale/internal/plugins"
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
	case "/status", "/mcp", "/feedback", "/help":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		return SubmitUsageError
	case "/worktree":
		if len(fields) == 1 || (len(fields) == 2 && fields[1] == "list") || (len(fields) == 2 && fields[1] == "status") || (len(fields) == 3 && fields[1] == "status") {
			return SubmitLocalReadOnly
		}
		if len(fields) >= 3 && fields[1] == "remove" {
			if len(fields) == 3 || (len(fields) == 4 && fields[3] == "--force") {
				return SubmitLocalMutating
			}
		}
		return SubmitUsageError
	case "/memory":
		if class, ok := plugins.BuiltinSlashCommandClass(line); ok {
			return submitClassFromPluginCommandClass(class)
		}
		return SubmitUsageError
	case "/stats":
		if len(fields) == 1 {
			return SubmitLocalReadOnly
		}
		if len(fields) == 2 && validStatsView(fields[1]) {
			return SubmitLocalReadOnly
		}
		return SubmitUsageError
	case "/model", "/permissions", "/skills", "/plugins", "/resume":
		if len(fields) == 1 {
			return SubmitLocalUI
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

func submitClassFromPluginCommandClass(class plugins.CommandClass) SubmitClass {
	switch class {
	case plugins.CommandReadOnly:
		return SubmitLocalReadOnly
	case plugins.CommandMutating:
		return SubmitLocalMutating
	case plugins.CommandUI:
		return SubmitLocalUI
	case plugins.CommandTurnStarting:
		return SubmitTurnStarting
	default:
		return SubmitUsageError
	}
}

func validStatsView(view string) bool {
	switch view {
	case "usage", "tools", "repair", "recent", "all":
		return true
	default:
		return false
	}
}
