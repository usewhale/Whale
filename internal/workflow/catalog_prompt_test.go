package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPromptCatalogIncludesReadyNamedWorkflows(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "dead-code-scan.js"), `export const meta = {
  name: 'dead-code-scan',
  description: 'Scan for dead code across a repository.',
  whenToUse: 'When the user asks to find unused code, delete dead branches, or run a repository cleanup workflow.',
  phases: [{ title: 'Scan', detail: 'Find candidates' }, { title: 'Verify', detail: 'Check references' }],
}
log('ok')
`)
	writeWorkflowFile(t, filepath.Join(root, "bad.js"), `// no meta`)
	lib := NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	catalog := RenderPromptCatalog(context.Background(), lib, 8)

	for _, want := range []string{
		"Available workflows.",
		"Use workflow when the user asks for a workflow",
		"Do not preflight by reading/searching files",
		"args are optional unless clearly required",
		"- dead-code-scan [project]: Scan for dead code across a repository.",
		"when: When the user asks to find unused code",
		"phases: Scan -> Verify",
		"deep-research [builtin]",
		"workflow definition(s) have parse or validation problems",
	} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("catalog missing %q:\n%s", want, catalog)
		}
	}
}

func TestRenderPromptCatalogOmitsWhenNoReadyDefinitions(t *testing.T) {
	got := RenderPromptCatalogDefinitions([]Definition{{
		Name:   "bad",
		Status: DefinitionProblem,
		Error:  "broken",
	}}, 4)
	if got != "" {
		t.Fatalf("expected empty catalog, got:\n%s", got)
	}
}

func TestRenderPromptCatalogTruncatesLongFields(t *testing.T) {
	long := strings.Repeat("word ", 80)
	got := RenderPromptCatalogDefinitions([]Definition{{
		Name:        "long-workflow",
		Description: long,
		WhenToUse:   long,
		Status:      DefinitionReady,
	}}, 4)
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncation marker:\n%s", got)
	}
	if len(got) > 900 {
		t.Fatalf("catalog should stay compact, len=%d:\n%s", len(got), got)
	}
}
