package workflow

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const WorkflowFileExt = ".js"

var workflowNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var workflowPhaseWrapperPattern = regexp.MustCompile("\\bphase\\s*\\(\\s*['\"`][^'\"`]*['\"`]\\s*,")
var workflowBareAsyncAssignmentPattern = regexp.MustCompile(`(?m)^\s*(?:const|let|var)\s+[A-Za-z_$][A-Za-z0-9_$]*\s*=\s*(?:agent|parallel|pipeline|workflow)\s*\(`)

type Library struct {
	Roots []LibraryRoot
}

type LibraryRoot struct {
	Path   string
	Source string
	Rank   int
}

type DefinitionStatus string

const (
	DefinitionReady   DefinitionStatus = "ready"
	DefinitionProblem DefinitionStatus = "problem"
)

type Definition struct {
	Name                string
	Description         string
	WhenToUse           string
	RiskNote            string
	EstimatedAgents     int
	DefaultBudgetTokens int
	Phases              []ScriptPhase
	Path                string
	Root                string
	Source              string
	Status              DefinitionStatus
	Error               string
}

type ResolvedScript struct {
	Definition Definition
	Script     string
}

func NewLibrary(workspaceRoot string) *Library {
	roots := []LibraryRoot{}
	if root := strings.TrimSpace(workspaceRoot); root != "" {
		roots = append(roots, LibraryRoot{
			Path:   filepath.Join(root, ".whale", "workflows"),
			Source: "project",
			Rank:   0,
		})
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, LibraryRoot{
			Path:   filepath.Join(home, ".whale", "workflows"),
			Source: "user",
			Rank:   1,
		})
	}
	return NewLibraryWithRoots(roots)
}

func NewLibraryWithRoots(roots []LibraryRoot) *Library {
	out := make([]LibraryRoot, 0, len(roots))
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
			source = "workflow"
		}
		out = append(out, LibraryRoot{Path: clean, Source: source, Rank: root.Rank})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].Path < out[j].Path
	})
	return &Library{Roots: out}
}

func ValidWorkflowName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && workflowNamePattern.MatchString(name)
}

func (l *Library) List(ctx context.Context) ([]Definition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, nil
	}
	byName := map[string]Definition{}
	nameRank := map[string]int{}
	for _, root := range l.Roots {
		defs, err := scanWorkflowRoot(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			rank, exists := nameRank[def.Name]
			if !exists {
				byName[def.Name] = def
				nameRank[def.Name] = root.Rank
				continue
			}
			if root.Rank > rank {
				continue
			}
			if root.Rank < rank {
				byName[def.Name] = def
				nameRank[def.Name] = root.Rank
				continue
			}
			prev := byName[def.Name]
			prev.Status = DefinitionProblem
			prev.Error = fmt.Sprintf("duplicate workflow name %q in %s and %s", def.Name, prev.Path, def.Path)
			byName[def.Name] = prev
		}
	}
	for _, def := range builtinWorkflowDefinitions() {
		if _, exists := byName[def.Name]; exists {
			continue
		}
		byName[def.Name] = def
	}
	out := make([]Definition, 0, len(byName))
	for _, def := range byName {
		out = append(out, def)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == DefinitionReady
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (l *Library) Resolve(ctx context.Context, name string) (ResolvedScript, error) {
	name = strings.TrimSpace(name)
	if !ValidWorkflowName(name) {
		return ResolvedScript{}, fmt.Errorf("invalid workflow name %q: must be kebab-case", name)
	}
	defs, err := l.List(ctx)
	if err != nil {
		return ResolvedScript{}, err
	}
	for _, def := range defs {
		if def.Name != name {
			continue
		}
		if def.Status != DefinitionReady {
			return ResolvedScript{}, errors.New(def.Error)
		}
		if def.Source == "builtin" {
			script, ok := BuiltinWorkflowScript(name)
			if !ok {
				return ResolvedScript{}, fmt.Errorf("builtin workflow not found: %s", name)
			}
			return ResolvedScript{Definition: def, Script: script}, nil
		}
		b, err := os.ReadFile(def.Path)
		if err != nil {
			return ResolvedScript{}, fmt.Errorf("read workflow %q: %w", name, err)
		}
		return ResolvedScript{Definition: def, Script: string(b)}, nil
	}
	return ResolvedScript{}, fmt.Errorf("workflow not found: %s", name)
}

func ResolveScriptPath(ctx context.Context, path string) (ResolvedScript, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedScript{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ResolvedScript{}, errors.New("workflow scriptPath is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ResolvedScript{}, fmt.Errorf("read workflow script: %w", err)
	}
	script := string(b)
	parsed, err := parseWorkflowScript(script)
	if err != nil {
		return ResolvedScript{}, err
	}
	if err := validateWorkflowCompile(parsed.Executable); err != nil {
		return ResolvedScript{}, err
	}
	def := Definition{
		Name:                parsed.Meta.Name,
		Description:         parsed.Meta.Description,
		WhenToUse:           parsed.Meta.WhenToUse,
		RiskNote:            parsed.Meta.RiskNote,
		EstimatedAgents:     parsed.Meta.EstimatedAgents,
		DefaultBudgetTokens: parsed.Meta.DefaultBudgetTokens,
		Phases:              append([]ScriptPhase(nil), parsed.Meta.Phases...),
		Path:                path,
		Source:              "file",
		Status:              DefinitionReady,
	}
	if !ValidWorkflowName(def.Name) {
		return ResolvedScript{}, fmt.Errorf("invalid meta.name %q: must be kebab-case", def.Name)
	}
	return ResolvedScript{Definition: def, Script: script}, nil
}

func (l *Library) PrepareGenerated(ctx context.Context, script, saveAs string) (ResolvedScript, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedScript{}, err
	}
	if l == nil {
		return ResolvedScript{}, errors.New("workflow library is not configured")
	}
	parsed, err := parseWorkflowScript(script)
	if err != nil {
		return ResolvedScript{}, err
	}
	if err := validateWorkflowCompile(parsed.Executable); err != nil {
		return ResolvedScript{}, err
	}
	if err := validateGeneratedWorkflowScript(parsed.Executable); err != nil {
		return ResolvedScript{}, err
	}
	name := strings.TrimSpace(saveAs)
	if name == "" {
		name = strings.TrimSpace(parsed.Meta.Name)
	}
	if !ValidWorkflowName(name) {
		return ResolvedScript{}, fmt.Errorf("invalid workflow name %q: must be kebab-case", name)
	}
	if parsed.Meta.Name != name {
		return ResolvedScript{}, fmt.Errorf("saveAs %q must match meta.name %q", name, parsed.Meta.Name)
	}
	root, ok := l.projectRoot()
	if !ok {
		return ResolvedScript{}, errors.New("project workflow root is not configured")
	}
	path := filepath.Join(root.Path, name+WorkflowFileExt)
	if _, err := os.Stat(path); err == nil {
		return ResolvedScript{}, fmt.Errorf("workflow already exists: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return ResolvedScript{}, fmt.Errorf("stat workflow %s: %w", path, err)
	}
	normalized := strings.TrimSpace(script) + "\n"
	def := inspectWorkflowScript(root.Source, path, normalized)
	if def.Status != DefinitionReady {
		return ResolvedScript{}, errors.New(def.Error)
	}
	return ResolvedScript{Definition: def, Script: normalized}, nil
}

func (l *Library) SaveGenerated(ctx context.Context, script, saveAs string) (ResolvedScript, error) {
	prepared, err := l.PrepareGenerated(ctx, script, saveAs)
	if err != nil {
		return ResolvedScript{}, err
	}
	path := prepared.Definition.Path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ResolvedScript{}, fmt.Errorf("create workflow root %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(prepared.Script), 0o644); err != nil {
		return ResolvedScript{}, fmt.Errorf("write workflow %s: %w", path, err)
	}
	return l.Resolve(ctx, prepared.Definition.Name)
}

func validateGeneratedWorkflowScript(code string) error {
	if workflowPhaseWrapperPattern.MatchString(code) {
		return errors.New("generated workflow must call phase('Name') as a statement; phase() is not a callback/wrapper and must not receive a second argument")
	}
	if workflowBareAsyncAssignmentPattern.MatchString(code) {
		return errors.New("generated workflow must await async workflow primitives before reading their result: use const result = await agent(...), await parallel(...), await pipeline(...), or await workflow(...)")
	}
	if generatedWorkflowHasStructuredOption(code) {
		return errors.New("generated workflow agent opts must use schema, not structured")
	}
	if generatedWorkflowHasClaudeModel(code) {
		return errors.New("generated workflow must not hard-code Claude model names; omit the model field unless the user explicitly requests a provider-supported model")
	}
	return nil
}

func generatedWorkflowHasStructuredOption(code string) bool {
	for i := 0; i < len(code); {
		i = skipWorkflowIgnored(code, i)
		if i >= len(code) {
			break
		}
		switch code[i] {
		case '\'', '"':
			value, next, ok := readWorkflowStringLiteral(code, i)
			if !ok {
				i++
				continue
			}
			if value == "structured" {
				if workflowFieldSeparatorExists(code, next, ':') {
					return true
				}
			}
			i = next
		case '`':
			i = skipWorkflowTemplateLiteral(code, i)
		default:
			if !isWorkflowIdentStart(code[i]) {
				i++
				continue
			}
			start := i
			i++
			for i < len(code) && isWorkflowIdentPart(code[i]) {
				i++
			}
			if code[start:i] != "structured" {
				continue
			}
			if workflowFieldSeparatorExists(code, i, ':') {
				return true
			}
			if prevNonSpaceByte(code, start) == '.' {
				if workflowFieldSeparatorExists(code, i, '=') {
					return true
				}
			}
		}
	}
	return false
}

func workflowFieldSeparatorExists(code string, pos int, sep byte) bool {
	pos = skipWorkflowIgnored(code, pos)
	return pos < len(code) && code[pos] == sep
}

func generatedWorkflowHasClaudeModel(code string) bool {
	for _, value := range generatedWorkflowModelValues(code) {
		if containsClaudeModelName(value) {
			return true
		}
	}
	return false
}

func generatedWorkflowModelValues(code string) []string {
	var values []string
	for i := 0; i < len(code); {
		i = skipWorkflowIgnored(code, i)
		if i >= len(code) {
			break
		}
		switch code[i] {
		case '\'', '"':
			value, next, ok := readWorkflowStringLiteral(code, i)
			if !ok {
				i++
				continue
			}
			if value == "model" {
				if fieldValue, ok := workflowModelFieldValue(code, next, ':'); ok {
					values = append(values, fieldValue)
				}
			}
			i = next
		case '`':
			i = skipWorkflowTemplateLiteral(code, i)
		default:
			if !isWorkflowIdentStart(code[i]) {
				i++
				continue
			}
			start := i
			i++
			for i < len(code) && isWorkflowIdentPart(code[i]) {
				i++
			}
			if code[start:i] != "model" {
				continue
			}
			if fieldValue, ok := workflowModelFieldValue(code, i, ':'); ok {
				values = append(values, fieldValue)
				continue
			}
			if prevNonSpaceByte(code, start) == '.' {
				if fieldValue, ok := workflowModelFieldValue(code, i, '='); ok {
					values = append(values, fieldValue)
				}
			}
		}
	}
	return values
}

func workflowModelFieldValue(code string, pos int, sep byte) (string, bool) {
	pos = skipWorkflowIgnored(code, pos)
	if pos >= len(code) || code[pos] != sep {
		return "", false
	}
	pos = skipWorkflowIgnored(code, pos+1)
	if pos >= len(code) || (code[pos] != '\'' && code[pos] != '"') {
		return "", false
	}
	value, _, ok := readWorkflowStringLiteral(code, pos)
	return value, ok
}

func skipWorkflowIgnored(code string, pos int) int {
	for pos < len(code) {
		switch code[pos] {
		case ' ', '\t', '\r', '\n':
			pos++
			continue
		case '/':
			if pos+1 >= len(code) {
				return pos
			}
			switch code[pos+1] {
			case '/':
				pos += 2
				for pos < len(code) && code[pos] != '\n' {
					pos++
				}
				continue
			case '*':
				pos += 2
				for pos+1 < len(code) && !(code[pos] == '*' && code[pos+1] == '/') {
					pos++
				}
				if pos+1 < len(code) {
					pos += 2
				}
				continue
			}
		}
		return pos
	}
	return pos
}

func readWorkflowStringLiteral(code string, pos int) (string, int, bool) {
	if pos >= len(code) || (code[pos] != '\'' && code[pos] != '"') {
		return "", pos, false
	}
	quote := code[pos]
	var b strings.Builder
	pos++
	for pos < len(code) {
		ch := code[pos]
		if ch == quote {
			return b.String(), pos + 1, true
		}
		if ch == '\\' {
			if pos+1 >= len(code) {
				return "", pos, false
			}
			b.WriteByte(code[pos+1])
			pos += 2
			continue
		}
		b.WriteByte(ch)
		pos++
	}
	return "", pos, false
}

func skipWorkflowTemplateLiteral(code string, pos int) int {
	pos++
	for pos < len(code) {
		if code[pos] == '\\' {
			pos += 2
			continue
		}
		if code[pos] == '`' {
			return pos + 1
		}
		pos++
	}
	return pos
}

func prevNonSpaceByte(code string, pos int) byte {
	for pos > 0 {
		pos--
		switch code[pos] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return code[pos]
		}
	}
	return 0
}

func isWorkflowIdentStart(ch byte) bool {
	return ch == '_' || ch == '$' || ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z')
}

func isWorkflowIdentPart(ch byte) bool {
	return isWorkflowIdentStart(ch) || ('0' <= ch && ch <= '9')
}

func containsClaudeModelName(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "claude") ||
		strings.Contains(value, "sonnet") ||
		strings.Contains(value, "opus") ||
		strings.Contains(value, "haiku")
}

func (l *Library) projectRoot() (LibraryRoot, bool) {
	for _, root := range l.Roots {
		if root.Source == "project" && strings.TrimSpace(root.Path) != "" {
			return root, true
		}
	}
	return LibraryRoot{}, false
}

func scanWorkflowRoot(ctx context.Context, root LibraryRoot) ([]Definition, error) {
	if strings.TrimSpace(root.Path) == "" {
		return nil, nil
	}
	if _, err := os.Stat(root.Path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat workflow root %s: %w", root.Path, err)
	}
	defs := []Definition{}
	err := filepath.WalkDir(root.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root.Path && strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(d.Name()) != WorkflowFileExt {
			return nil
		}
		defs = append(defs, inspectWorkflowFile(root, path))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan workflow root %s: %w", root.Path, err)
	}
	return defs, nil
}

func inspectWorkflowFile(root LibraryRoot, path string) Definition {
	base := strings.TrimSuffix(filepath.Base(path), WorkflowFileExt)
	def := Definition{
		Name:   base,
		Path:   path,
		Root:   root.Path,
		Source: root.Source,
		Status: DefinitionReady,
	}
	if !ValidWorkflowName(base) {
		def.Status = DefinitionProblem
		def.Error = fmt.Sprintf("invalid workflow filename %q: filename must be kebab-case and end with .js", filepath.Base(path))
		return def
	}
	b, err := os.ReadFile(path)
	if err != nil {
		def.Status = DefinitionProblem
		def.Error = fmt.Sprintf("read workflow: %v", err)
		return def
	}
	inspected := inspectWorkflowScript(root.Source, path, string(b))
	inspected.Root = root.Path
	return inspected
}

func inspectWorkflowScript(source, path, script string) Definition {
	base := strings.TrimSuffix(filepath.Base(path), WorkflowFileExt)
	def := Definition{
		Name:   base,
		Path:   path,
		Source: source,
		Status: DefinitionReady,
	}
	if !ValidWorkflowName(base) {
		def.Status = DefinitionProblem
		def.Error = fmt.Sprintf("invalid workflow filename %q: filename must be kebab-case and end with .js", filepath.Base(path))
		return def
	}
	parsed, err := parseWorkflowScript(script)
	if err != nil {
		def.Status = DefinitionProblem
		def.Error = err.Error()
		return def
	}
	if err := validateWorkflowCompile(parsed.Executable); err != nil {
		def.Status = DefinitionProblem
		def.Error = err.Error()
		return def
	}
	def.Name = parsed.Meta.Name
	def.Description = parsed.Meta.Description
	def.WhenToUse = parsed.Meta.WhenToUse
	def.RiskNote = parsed.Meta.RiskNote
	def.EstimatedAgents = parsed.Meta.EstimatedAgents
	def.DefaultBudgetTokens = parsed.Meta.DefaultBudgetTokens
	def.Phases = append([]ScriptPhase(nil), parsed.Meta.Phases...)
	if !ValidWorkflowName(def.Name) {
		def.Status = DefinitionProblem
		def.Error = fmt.Sprintf("invalid meta.name %q: must be kebab-case", def.Name)
		return def
	}
	if def.Name != base {
		def.Status = DefinitionProblem
		def.Error = fmt.Sprintf("workflow filename %q must match meta.name %q", filepath.Base(path), def.Name)
	}
	return def
}
