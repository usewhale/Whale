package app

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var memoryEntryHeader = regexp.MustCompile(`^#\s+(.+)\s+\(([^/]+)/([^)]+)\)$`)

func buildMemoryLocalResult(line, text string, mutated bool) *LocalResult {
	fields := strings.Fields(strings.TrimSpace(line))
	action := "list"
	if len(fields) >= 2 {
		action = fields[1]
	}
	switch action {
	case "path":
		return buildMemoryPathLocalResult(text)
	case "show":
		return buildMemoryShowLocalResult(text)
	case "forget":
		return buildMemoryForgetLocalResult(text, mutated)
	default:
		return buildMemoryListLocalResult(text)
	}
}

func buildMemoryListLocalResult(text string) *LocalResult {
	result := &LocalResult{
		Kind:      "memory",
		Title:     "Memory",
		Fields:    []LocalResultField{{Label: "Action", Value: "list"}},
		PlainText: text,
	}
	for _, scope := range []string{"Global", "Project"} {
		if section := memoryListSection(text, scope); section.Title != "" {
			result.Sections = append(result.Sections, section)
		}
	}
	return result
}

func memoryListSection(text, scope string) LocalResultSection {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), scope+" (") {
			start = i
			break
		}
	}
	if start < 0 {
		return LocalResultSection{}
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if (strings.HasPrefix(trimmed, "Global (") || strings.HasPrefix(trimmed, "Project (")) && i > start {
			end = i
			break
		}
	}
	title := strings.TrimSpace(lines[start])
	section := LocalResultSection{Title: title}
	var index []string
	for _, line := range lines[start+1 : end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if value, ok := strings.CutPrefix(trimmed, "path: "); ok {
			section.Fields = append(section.Fields, LocalResultField{Label: "Path", Value: value})
			continue
		}
		if strings.HasPrefix(trimmed, "error: ") {
			section.Fields = append(section.Fields, LocalResultField{Label: "Error", Value: strings.TrimPrefix(trimmed, "error: "), Tone: "error"})
			continue
		}
		index = append(index, trimmed)
	}
	if len(index) > 0 {
		section.Fields = append(section.Fields, LocalResultField{Label: "Index", Value: strings.Join(index, "\n")})
	}
	return section
}

func buildMemoryPathLocalResult(text string) *LocalResult {
	fields := []LocalResultField{{Label: "Action", Value: "path"}}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			continue
		}
		fields = append(fields, LocalResultField{Label: strings.Title(key), Value: value})
	}
	return &LocalResult{Kind: "memory", Title: "Memory paths", Fields: fields, PlainText: text}
}

func buildMemoryShowLocalResult(text string) *LocalResult {
	lines := strings.Split(text, "\n")
	fields := []LocalResultField{{Label: "Action", Value: "show"}}
	contentStart := 0
	if len(lines) > 0 {
		if match := memoryEntryHeader.FindStringSubmatch(strings.TrimSpace(lines[0])); match != nil {
			fields = append(fields,
				LocalResultField{Label: "Name", Value: match[1], Tone: "info"},
				LocalResultField{Label: "Scope", Value: match[2]},
				LocalResultField{Label: "Type", Value: match[3]},
			)
			contentStart = 1
		}
	}
	i := skipBlankMemoryLines(lines, contentStart)
	if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "> ") {
		fields = append(fields, LocalResultField{Label: "Description", Value: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "> "))})
		i++
	}
	i = skipBlankMemoryLines(lines, i)
	if i < len(lines) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "path: "); ok {
			fields = append(fields, LocalResultField{Label: "Path", Value: filepath.ToSlash(value)})
			i++
		}
	}
	contentStart = skipBlankMemoryLines(lines, i)
	content := strings.TrimSpace(strings.Join(lines[contentStart:], "\n"))
	if content != "" {
		fields = append(fields, LocalResultField{Label: "Content", Value: content})
	}
	return &LocalResult{Kind: "memory", Title: "Memory entry", Fields: fields, PlainText: text}
}

func skipBlankMemoryLines(lines []string, start int) int {
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	return start
}

func buildMemoryForgetLocalResult(text string, mutated bool) *LocalResult {
	status := strings.TrimSpace(text)
	tone := "muted"
	if strings.HasPrefix(status, "forgot memory:") {
		tone = "info"
	}
	if strings.HasPrefix(status, "no such memory:") {
		tone = "warn"
	}
	fields := []LocalResultField{
		{Label: "Action", Value: "forget"},
		{Label: "Result", Value: status, Tone: tone},
		{Label: "Changed", Value: fmt.Sprintf("%t", mutated)},
	}
	return &LocalResult{Kind: "memory", Title: "Memory", Fields: fields, PlainText: text}
}
