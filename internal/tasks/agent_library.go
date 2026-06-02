package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const AgentDefinitionFileExt = ".md"

var agentDefinitionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

type AgentDefinitionLibrary struct {
	Roots       []AgentDefinitionRoot
	Definitions []AgentDefinition
}

type AgentDefinitionRoot struct {
	Path   string
	Source string
	Rank   int
}

func NewAgentDefinitionLibrary(workspaceRoot string) *AgentDefinitionLibrary {
	var roots []AgentDefinitionRoot
	if root := strings.TrimSpace(workspaceRoot); root != "" {
		roots = append(roots,
			AgentDefinitionRoot{Path: filepath.Join(root, ".whale", "agents"), Source: "project", Rank: 0},
		)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots,
			AgentDefinitionRoot{Path: filepath.Join(home, ".whale", "agents"), Source: "user", Rank: 1},
		)
	}
	return NewAgentDefinitionLibraryWithRoots(roots)
}

func NewAgentDefinitionLibraryWithDefinitions(workspaceRoot string, definitions []AgentDefinition) *AgentDefinitionLibrary {
	library := NewAgentDefinitionLibrary(workspaceRoot)
	if library == nil {
		library = &AgentDefinitionLibrary{}
	}
	library.Definitions = cloneAgentDefinitions(definitions)
	return library
}

func NewAgentDefinitionLibraryWithRoots(roots []AgentDefinitionRoot) *AgentDefinitionLibrary {
	out := make([]AgentDefinitionRoot, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		source := strings.TrimSpace(root.Source)
		if source == "" {
			source = "agent"
		}
		out = append(out, AgentDefinitionRoot{Path: clean, Source: source, Rank: root.Rank})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].Path < out[j].Path
	})
	return &AgentDefinitionLibrary{Roots: out}
}

func ValidAgentDefinitionName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && len(name) <= 64 && agentDefinitionNamePattern.MatchString(name)
}

func (l *AgentDefinitionLibrary) Resolve(name string) (AgentDefinition, bool, error) {
	name = strings.TrimSpace(name)
	if !ValidAgentDefinitionName(name) {
		return AgentDefinition{}, false, nil
	}
	if l == nil {
		return AgentDefinition{}, false, nil
	}
	var best AgentDefinition
	bestRank := 0
	found := false
	for _, root := range l.Roots {
		def, ok, err := resolveAgentDefinitionFromRoot(context.Background(), root, name)
		if err != nil {
			return AgentDefinition{}, false, err
		}
		if !ok {
			continue
		}
		if found && bestRank <= root.Rank {
			continue
		}
		best = def
		bestRank = root.Rank
		found = true
	}
	for _, def := range l.Definitions {
		if def.Name != name {
			continue
		}
		if found && bestRank <= 2 {
			continue
		}
		best = def
		bestRank = 2
		found = true
	}
	return best, found, nil
}

func (l *AgentDefinitionLibrary) List(ctx context.Context) ([]AgentDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, nil
	}
	byName := map[string]AgentDefinition{}
	nameRank := map[string]int{}
	for _, root := range l.Roots {
		defs, err := scanAgentDefinitionRoot(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			rank, exists := nameRank[def.Name]
			if exists && rank <= root.Rank {
				continue
			}
			byName[def.Name] = def
			nameRank[def.Name] = root.Rank
		}
	}
	for _, def := range l.Definitions {
		if strings.TrimSpace(def.Name) == "" {
			continue
		}
		rank, exists := nameRank[def.Name]
		if exists && rank <= 2 {
			continue
		}
		byName[def.Name] = def
		nameRank[def.Name] = 2
	}
	out := make([]AgentDefinition, 0, len(byName))
	for _, def := range byName {
		out = append(out, def)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func scanAgentDefinitionRoot(ctx context.Context, root AgentDefinitionRoot) ([]AgentDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(root.Path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var defs []AgentDefinition
	err := filepath.WalkDir(root.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root.Path && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".json" {
			return nil
		}
		def, ok, err := parseAgentDefinitionFile(path, root.Source)
		if err != nil {
			return nil
		}
		if ok {
			defs = append(defs, def)
		}
		return nil
	})
	return defs, err
}

func resolveAgentDefinitionFromRoot(ctx context.Context, root AgentDefinitionRoot, name string) (AgentDefinition, bool, error) {
	if err := ctx.Err(); err != nil {
		return AgentDefinition{}, false, err
	}
	if _, err := os.Stat(root.Path); err != nil {
		if os.IsNotExist(err) {
			return AgentDefinition{}, false, nil
		}
		return AgentDefinition{}, false, err
	}
	var matched AgentDefinition
	found := false
	var matchedErr error
	err := filepath.WalkDir(root.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root.Path && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".json" {
			return nil
		}
		def, ok, err := parseAgentDefinitionFile(path, root.Source)
		if err != nil {
			if strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) == name {
				matchedErr = fmt.Errorf("parse agent definition %s: %w", path, err)
				return matchedErr
			}
			return nil
		}
		if ok && def.Name == name {
			matched = def
			found = true
		}
		return nil
	})
	if matchedErr != nil {
		return AgentDefinition{}, false, matchedErr
	}
	if err != nil {
		return AgentDefinition{}, false, err
	}
	return matched, found, nil
}

func parseAgentDefinitionFile(path, source string) (AgentDefinition, bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return AgentDefinition{}, false, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var def AgentDefinition
		if err := json.Unmarshal(content, &def); err != nil {
			return AgentDefinition{}, false, err
		}
		if strings.TrimSpace(def.Name) == "" {
			def.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		return validateLoadedAgentDefinition(def)
	case ".md":
		return parseMarkdownAgentDefinition(string(content), filepath.Base(path), source)
	default:
		return AgentDefinition{}, false, nil
	}
}

func parseMarkdownAgentDefinition(content, filename, _ string) (AgentDefinition, bool, error) {
	frontmatter, body, ok, err := splitAgentFrontmatter(content)
	if err != nil || !ok {
		return AgentDefinition{}, false, err
	}
	values := parseAgentFrontmatter(frontmatter)
	name := stringFrontmatterValue(values, "name")
	if name == "" {
		name = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	desc := stringFrontmatterValue(values, "description")
	if desc == "" {
		return AgentDefinition{}, false, fmt.Errorf("description is required")
	}
	def := AgentDefinition{
		Name:            name,
		Description:     desc,
		WhenToUse:       stringFrontmatterValue(values, "whenToUse"),
		Prompt:          strings.TrimSpace(body),
		Tools:           stringListFrontmatterValue(values, "tools"),
		DisallowedTools: stringListFrontmatterValue(values, "disallowedTools"),
		Skills:          stringListFrontmatterValue(values, "skills"),
		MCPServers:      stringListFrontmatterValue(values, "mcpServers"),
		Model:           stringFrontmatterValue(values, "model"),
		Effort:          stringFrontmatterValue(values, "effort"),
		PermissionMode:  stringFrontmatterValue(values, "permissionMode"),
		MaxTurns:        intFrontmatterValue(values, "maxTurns"),
		InitialPrompt:   stringFrontmatterValue(values, "initialPrompt"),
		Memory:          stringFrontmatterValue(values, "memory"),
		Hooks:           hooksFrontmatterValue(frontmatter),
		Background:      boolFrontmatterValue(values, "background"),
		Isolation:       stringFrontmatterValue(values, "isolation"),
	}
	return validateLoadedAgentDefinition(def)
}

func validateLoadedAgentDefinition(def AgentDefinition) (AgentDefinition, bool, error) {
	def.Name = strings.TrimSpace(def.Name)
	if !ValidAgentDefinitionName(def.Name) {
		return AgentDefinition{}, false, fmt.Errorf("invalid agent name %q", def.Name)
	}
	if strings.TrimSpace(def.Description) == "" && strings.TrimSpace(def.WhenToUse) == "" {
		return AgentDefinition{}, false, fmt.Errorf("description is required")
	}
	if strings.TrimSpace(def.WhenToUse) == "" {
		def.WhenToUse = strings.TrimSpace(def.Description)
	}
	if strings.TrimSpace(def.Description) == "" {
		def.Description = strings.TrimSpace(def.WhenToUse)
	}
	return def, true, nil
}

func splitAgentFrontmatter(content string) (frontmatter, body string, ok bool, err error) {
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", "", false, nil
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	rest := strings.TrimPrefix(normalized, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false, fmt.Errorf("unclosed frontmatter")
	}
	frontmatter = rest[:end]
	body = strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return frontmatter, body, true, nil
}

func parseAgentFrontmatter(frontmatter string) map[string]any {
	values := map[string]any{}
	lines := strings.Split(frontmatter, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if raw == "" {
			var list []string
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" {
					continue
				}
				if !strings.HasPrefix(lines[j], " ") && !strings.HasPrefix(lines[j], "\t") {
					break
				}
				if strings.HasPrefix(next, "- ") {
					list = append(list, cleanScalar(next[2:]))
					i = j
				}
			}
			if list != nil {
				values[key] = list
			}
			continue
		}
		values[key] = parseAgentScalar(raw)
	}
	return values
}

func hooksFrontmatterValue(frontmatter string) any {
	lines := strings.Split(frontmatter, "\n")
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || isIndentedYAMLLine(line) {
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "hooks" {
			continue
		}
		if strings.TrimSpace(raw) != "" {
			return parseAgentScalar(raw)
		}
		start = i + 1
		break
	}
	if start < 0 {
		return nil
	}
	hooks := map[string]any{}
	var currentEvent string
	var current map[string]any
	var nestedKey string
	nestedIndent := 0
	for i := start; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := yamlIndent(line)
		if indent == 0 {
			break
		}
		if current != nil && nestedKey != "" && indent > nestedIndent {
			if strings.HasPrefix(trimmed, "- ") {
				appendNestedListValue(current, nestedKey, strings.TrimSpace(trimmed[2:]))
				continue
			}
			if key, raw, ok := strings.Cut(trimmed, ":"); ok {
				putNestedMapValue(current, nestedKey, strings.TrimSpace(key), strings.TrimSpace(raw))
				continue
			}
		}
		if current != nil && nestedKey != "" && indent <= nestedIndent {
			nestedKey = ""
			nestedIndent = 0
		}
		if indent <= 2 && !strings.HasPrefix(trimmed, "- ") && strings.HasSuffix(trimmed, ":") {
			event := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			if event == "" {
				continue
			}
			currentEvent = event
			if _, ok := hooks[currentEvent]; !ok {
				hooks[currentEvent] = []any{}
			}
			current = nil
			continue
		}
		if currentEvent == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			current = map[string]any{}
			hooks[currentEvent] = append(hooks[currentEvent].([]any), current)
			nestedKey = ""
			nestedIndent = 0
			item := strings.TrimSpace(trimmed[2:])
			if item == "" {
				continue
			}
			key, raw, ok := strings.Cut(item, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			raw = strings.TrimSpace(raw)
			if raw == "" {
				current[key] = map[string]any{}
				nestedKey = key
				nestedIndent = indent
			} else {
				current[key] = parseAgentScalar(raw)
			}
			continue
		}
		if current == nil {
			continue
		}
		key, raw, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			current[key] = map[string]any{}
			nestedKey = key
			nestedIndent = indent
			continue
		}
		current[key] = parseAgentScalar(raw)
	}
	if len(hooks) == 0 {
		return nil
	}
	return hooks
}

func isIndentedYAMLLine(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func yamlIndent(line string) int {
	indent := 0
	for _, ch := range line {
		switch ch {
		case ' ':
			indent++
		case '\t':
			indent += 2
		default:
			return indent
		}
	}
	return indent
}

func appendNestedListValue(target map[string]any, key, raw string) {
	value := parseAgentScalar(raw)
	switch existing := target[key].(type) {
	case []any:
		target[key] = append(existing, value)
	case []string:
		if s, ok := value.(string); ok {
			target[key] = append(existing, s)
			return
		}
		out := make([]any, 0, len(existing)+1)
		for _, item := range existing {
			out = append(out, item)
		}
		target[key] = append(out, value)
	default:
		target[key] = []any{value}
	}
}

func putNestedMapValue(target map[string]any, key, nestedKey, raw string) {
	if nestedKey == "" {
		return
	}
	existing, ok := target[key].(map[string]any)
	if !ok {
		existing = map[string]any{}
	}
	existing[nestedKey] = parseAgentScalar(raw)
	target[key] = existing
}

func parseAgentScalar(raw string) any {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
		if body == "" {
			return []string{}
		}
		parts := strings.Split(body, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := cleanScalar(part); value != "" {
				out = append(out, value)
			}
		}
		return out
	}
	switch strings.ToLower(raw) {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.Atoi(raw); err == nil {
		return i
	}
	return cleanScalar(raw)
}

func cleanScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	return strings.ReplaceAll(raw, `\n`, "\n")
}

func cloneAgentDefinitions(definitions []AgentDefinition) []AgentDefinition {
	if len(definitions) == 0 {
		return nil
	}
	out := make([]AgentDefinition, 0, len(definitions))
	for _, def := range definitions {
		def.Name = strings.TrimSpace(def.Name)
		if !ValidAgentDefinitionName(def.Name) {
			continue
		}
		def.Tools = cloneStrings(def.Tools)
		def.DisallowedTools = cloneStrings(def.DisallowedTools)
		def.Skills = cloneStrings(def.Skills)
		def.MCPServers = cloneStrings(def.MCPServers)
		out = append(out, def)
	}
	return out
}

func stringFrontmatterValue(values map[string]any, key string) string {
	if value, ok := values[key]; ok {
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case int:
			return strconv.Itoa(typed)
		}
	}
	return ""
}

func stringListFrontmatterValue(values map[string]any, key string) []string {
	value, ok := values[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return compactStrings(typed)
	case string:
		return compactStrings(strings.Split(typed, ","))
	default:
		return nil
	}
}

func intFrontmatterValue(values map[string]any, key string) int {
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(typed))
		return i
	default:
		return 0
	}
}

func boolFrontmatterValue(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func compactStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
