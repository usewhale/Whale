package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryListDiscoversProjectAndUserWorkflows(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	userRoot := filepath.Join(t.TempDir(), "user")
	writeWorkflowFile(t, filepath.Join(projectRoot, "review", "project-review.js"), `export const meta = {
  name: 'project-review',
  description: 'Review project code',
  whenToUse: 'when code changed',
}
log('project')
`)
	writeWorkflowFile(t, filepath.Join(userRoot, "user-review.js"), `export const meta = {
  name: 'user-review',
  description: 'Review user code',
}
log('user')
`)
	writeWorkflowFile(t, filepath.Join(projectRoot, "_fixtures", "hidden-review.js"), `export const meta = { name: 'hidden-review', description: 'hidden' }
log('hidden')
`)
	if err := os.WriteFile(filepath.Join(projectRoot, "ignore.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	lib := NewLibraryWithRoots([]LibraryRoot{
		{Path: projectRoot, Source: "project", Rank: 0},
		{Path: userRoot, Source: "user", Rank: 1},
	})
	defs, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := workflowDefinitionNamesWithoutBuiltin(defs); strings.Join(got, ",") != "project-review,user-review" {
		t.Fatalf("definitions = %v", got)
	}
	project := findDefinition(t, defs, "project-review")
	if project.Source != "project" || project.Description != "Review project code" || project.WhenToUse != "when code changed" {
		t.Fatalf("project definition = %+v", project)
	}
	user := findDefinition(t, defs, "user-review")
	if user.Source != "user" || user.Status != DefinitionReady {
		t.Fatalf("user definition = %+v", user)
	}
}

func TestLibraryProjectOverridesUserWorkflow(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	userRoot := filepath.Join(t.TempDir(), "user")
	writeWorkflowFile(t, filepath.Join(userRoot, "review-code.js"), `export const meta = { name: 'review-code', description: 'user version' }
log('user')
`)
	writeWorkflowFile(t, filepath.Join(projectRoot, "review-code.js"), `export const meta = { name: 'review-code', description: 'project version' }
log('project')
`)

	lib := NewLibraryWithRoots([]LibraryRoot{
		{Path: projectRoot, Source: "project", Rank: 0},
		{Path: userRoot, Source: "user", Rank: 1},
	})
	defs, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	userDefs := nonBuiltinDefinitions(defs)
	if len(userDefs) != 1 {
		t.Fatalf("definitions = %+v", defs)
	}
	def := userDefs[0]
	if def.Source != "project" || def.Description != "project version" || def.Status != DefinitionReady {
		t.Fatalf("definition = %+v", def)
	}
}

func TestLibraryFlagsProblemDefinitions(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "BadName.js"), `export const meta = { name: 'bad-name', description: 'bad file' }
log('bad')
`)
	writeWorkflowFile(t, filepath.Join(root, "wrong-file.js"), `export const meta = { name: 'right-name', description: 'mismatch' }
log('mismatch')
`)
	writeWorkflowFile(t, filepath.Join(root, "bad-syntax.js"), `export const meta = { name: 'bad-syntax', description: 'syntax' }
const =
`)

	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	defs, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	userDefs := nonBuiltinDefinitions(defs)
	if len(userDefs) != 3 {
		t.Fatalf("definitions = %+v", defs)
	}
	for _, def := range userDefs {
		if def.Status != DefinitionProblem || def.Error == "" {
			t.Fatalf("expected problem definition, got %+v", def)
		}
	}
}

func TestLibraryResolveReadsNamedWorkflow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "named-review.js")
	writeWorkflowFile(t, path, `export const meta = { name: 'named-review', description: 'named' }
log('named')
`)
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	resolved, err := lib.Resolve(context.Background(), "named-review")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Definition.Path != path || !strings.Contains(resolved.Script, "log('named')") {
		t.Fatalf("resolved = %+v", resolved)
	}
	if _, err := lib.Resolve(context.Background(), "BadName"); err == nil || !strings.Contains(err.Error(), "kebab-case") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
	if _, err := lib.Resolve(context.Background(), "missing-review"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing workflow error, got %v", err)
	}
}

func TestLibrarySaveGeneratedWritesProjectWorkflow(t *testing.T) {
	root := t.TempDir()
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	script := `export const meta = { name: 'generated-review', description: 'generated review' }
log('generated')
`

	resolved, err := lib.SaveGenerated(context.Background(), script, "generated-review")
	if err != nil {
		t.Fatalf("SaveGenerated: %v", err)
	}
	wantPath := filepath.Join(root, "generated-review.js")
	if resolved.Definition.Path != wantPath || resolved.Definition.Source != "project" {
		t.Fatalf("resolved = %+v", resolved)
	}
	b, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read saved workflow: %v", err)
	}
	if !strings.Contains(string(b), "export const meta") || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("saved script = %q", string(b))
	}
}

func TestLibrarySaveGeneratedRejectsOverwriteAndMismatchedName(t *testing.T) {
	root := t.TempDir()
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	script := `export const meta = { name: 'generated-review', description: 'generated review' }
log('generated')
`
	if _, err := lib.SaveGenerated(context.Background(), script, "other-name"); err == nil || !strings.Contains(err.Error(), "must match meta.name") {
		t.Fatalf("expected mismatched saveAs error, got %v", err)
	}
	if _, err := lib.SaveGenerated(context.Background(), script, "generated-review"); err != nil {
		t.Fatalf("SaveGenerated: %v", err)
	}
	if _, err := lib.SaveGenerated(context.Background(), script, "generated-review"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite error, got %v", err)
	}
}

func TestLibrarySaveGeneratedRejectsCommonAuthoringMistakes(t *testing.T) {
	root := t.TempDir()
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	for _, tt := range []struct {
		name    string
		script  string
		wantErr string
	}{
		{
			name: "phase wrapper",
			script: `export const meta = { name: 'bad-phase', description: 'bad phase' }
await phase("Review", async () => agent("review", { label: "review" }))
`,
			wantErr: "phase() is not a callback",
		},
		{
			name: "structured opts",
			script: `export const meta = { name: 'bad-structured', description: 'bad structured' }
await agent("review", { label: "review", structured: { type: "object" } })
`,
			wantErr: "use schema, not structured",
		},
		{
			name: "bare async assignment",
			script: `export const meta = { name: 'bad-await', description: 'bad await' }
const review = agent("review", { label: "review" })
return review
`,
			wantErr: "must await async workflow primitives",
		},
		{
			name: "claude model",
			script: `export const meta = { name: 'bad-model', description: 'bad model' }
await agent("review", { label: "review", model: "claude-sonnet-4-20250514" })
`,
			wantErr: "must not hard-code Claude model names",
		},
		{
			name: "quoted claude model key",
			script: `export const meta = { name: 'bad-quoted-model', description: 'bad model' }
await agent("review", { label: "review", "model": "haiku" })
`,
			wantErr: "must not hard-code Claude model names",
		},
		{
			name: "assigned claude model",
			script: `export const meta = { name: 'bad-assigned-model', description: 'bad model' }
const opts = { label: "review" }
opts.model = "opus"
await agent("review", opts)
`,
			wantErr: "must not hard-code Claude model names",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := lib.SaveGenerated(context.Background(), tt.script, ""); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected %q error, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLibrarySaveGeneratedAllowsClaudeReferencesOutsideModelFields(t *testing.T) {
	root := t.TempDir()
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	script := `export const meta = {
  name: 'compare-claude-code',
  description: 'Compare Whale with Claude Code, Sonnet, Opus, and Haiku workflows.'
}
await agent("Review prompts that mention Claude Code and Sonnet migration notes.", { label: "review" })
const instructions = "Do not copy this prompt text into options: model: \"claude-sonnet-4-20250514\""
return { summary: "Claude references in ordinary text are allowed", instructions }
`
	if _, err := lib.SaveGenerated(context.Background(), script, ""); err != nil {
		t.Fatalf("SaveGenerated: %v", err)
	}
}

func TestLibrarySaveGeneratedAllowsStructuredReferencesOutsideOptionFields(t *testing.T) {
	root := t.TempDir()
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	script := `export const meta = {
  name: 'structured-reference',
  description: 'Mention structured option migration.'
}
// This workflow explains why old generated code used structured: and why schema is preferred.
await agent("Explain the old structured: option in prose only.", { label: "review", schema: { type: "object", required: ["ok"], properties: { ok: { type: "boolean" } } } })
return { ok: true }
`
	if _, err := lib.SaveGenerated(context.Background(), script, ""); err != nil {
		t.Fatalf("SaveGenerated: %v", err)
	}
}

func TestLibraryIncludesBuiltinDeepResearch(t *testing.T) {
	lib := NewLibraryWithRoots(nil)
	defs, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	def := findDefinition(t, defs, BuiltinDeepResearchName)
	if def.Source != "builtin" || def.Status != DefinitionReady || def.Description == "" {
		t.Fatalf("builtin definition = %+v", def)
	}
	if len(def.Phases) == 0 {
		t.Fatalf("builtin metadata incomplete = %+v", def)
	}
	resolved, err := lib.Resolve(context.Background(), BuiltinDeepResearchName)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Definition.Source != "builtin" || !strings.Contains(resolved.Script, `phase("Scope")`) {
		t.Fatalf("resolved builtin = %+v", resolved)
	}
}

func TestLibraryProjectOverridesBuiltinWorkflow(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "deep-research.js"), `export const meta = {
  name: 'deep-research',
  description: 'project deep research',
}
log('project')
`)
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	defs, err := lib.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	def := findDefinition(t, defs, BuiltinDeepResearchName)
	if def.Source != "project" || def.Description != "project deep research" {
		t.Fatalf("definition = %+v", def)
	}
}

func writeWorkflowFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func workflowDefinitionNames(defs []Definition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func workflowDefinitionNamesWithoutBuiltin(defs []Definition) []string {
	return workflowDefinitionNames(nonBuiltinDefinitions(defs))
}

func nonBuiltinDefinitions(defs []Definition) []Definition {
	out := make([]Definition, 0, len(defs))
	for _, def := range defs {
		if def.Source != "builtin" {
			out = append(out, def)
		}
	}
	return out
}

func findDefinition(t *testing.T, defs []Definition, name string) Definition {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("definition %q not found in %+v", name, defs)
	return Definition{}
}
