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
