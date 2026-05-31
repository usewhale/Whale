package workflow

import (
	"context"
	"fmt"
	"strings"
)

const DefaultPromptCatalogLimit = 12

func RenderPromptCatalog(ctx context.Context, library *Library, limit int) string {
	if library == nil {
		return ""
	}
	defs, err := library.List(ctx)
	if err != nil {
		return "Available workflows: unavailable (" + err.Error() + ")."
	}
	return RenderPromptCatalogDefinitions(defs, limit)
}

func RenderPromptCatalogDefinitions(defs []Definition, limit int) string {
	if limit <= 0 {
		limit = DefaultPromptCatalogLimit
	}
	ready := make([]Definition, 0, len(defs))
	problems := 0
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == "" {
			continue
		}
		if def.Status != DefinitionReady {
			problems++
			continue
		}
		ready = append(ready, def)
	}
	totalReady := len(ready)
	if totalReady == 0 {
		return ""
	}
	if len(ready) > limit {
		ready = ready[:limit]
	}

	var b strings.Builder
	b.WriteString("Available workflows.\n\n")
	b.WriteString("Use workflow when the user asks for a workflow, fan-out, multi-agent orchestration, or names/describes one below. If one fits, call workflow with name; args are optional unless clearly required. Do not preflight by reading/searching files unless asked; workflows report missing inputs. Use ordinary tools for single quick reads/edits/answers.\n\n")
	b.WriteString("After launch, say only that /workflows opens the panel; do not list run-id commands.\n\n")
	for _, def := range ready {
		b.WriteString("- ")
		b.WriteString(def.Name)
		if source := strings.TrimSpace(def.Source); source != "" {
			b.WriteString(" [")
			b.WriteString(source)
			b.WriteString("]")
		}
		if desc := compactWorkflowCatalogText(def.Description, 180); desc != "" {
			b.WriteString(": ")
			b.WriteString(desc)
		}
		if when := compactWorkflowCatalogText(def.WhenToUse, 220); when != "" {
			b.WriteString(" when: ")
			b.WriteString(when)
		}
		if len(def.Phases) > 0 {
			b.WriteString(" phases: ")
			b.WriteString(compactWorkflowPhases(def.Phases, 5))
		}
		if def.DefaultBudgetTokens > 0 {
			b.WriteString(" defaultBudgetTokens: ")
			b.WriteString(fmt.Sprintf("%d", def.DefaultBudgetTokens))
		}
		b.WriteString("\n")
	}
	if totalReady > len(ready) {
		b.WriteString("- ")
		b.WriteString(fmt.Sprintf("%d", totalReady-len(ready)))
		b.WriteString(" additional workflow(s) omitted from this prompt catalog.\n")
	}
	if problems > 0 {
		b.WriteString("- ")
		b.WriteString(fmt.Sprintf("%d", problems))
		b.WriteString(" workflow definition(s) have parse or validation problems and are not callable by name.\n")
	}
	return strings.TrimSpace(b.String())
}

func compactWorkflowPhases(phases []ScriptPhase, limit int) string {
	if limit <= 0 || limit > len(phases) {
		limit = len(phases)
	}
	names := make([]string, 0, limit)
	for _, phase := range phases[:limit] {
		if title := compactWorkflowCatalogText(phase.Title, 48); title != "" {
			names = append(names, title)
		}
	}
	out := strings.Join(names, " -> ")
	if len(phases) > limit {
		out += " -> ..."
	}
	return out
}

func compactWorkflowCatalogText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" || max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-3]) + "..."
}
