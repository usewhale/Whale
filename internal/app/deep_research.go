package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/usewhale/whale/internal/workflow"
)

const deepResearchUsage = "usage: /deep-research [--resume runId] question"

type deepResearchOptions struct {
	Question        string
	ResumeFromRunID string
	Confirmed       bool
	Remember        bool
}

func parseDeepResearchOptions(payload string) (deepResearchOptions, error) {
	fields := strings.Fields(strings.TrimSpace(payload))
	if len(fields) == 0 {
		return deepResearchOptions{}, errors.New(deepResearchUsage)
	}
	opts := deepResearchOptions{}
	questionStart := -1
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		switch {
		case field == "--resume":
			i++
			if i >= len(fields) || strings.TrimSpace(fields[i]) == "" || strings.HasPrefix(fields[i], "--") {
				return deepResearchOptions{}, errors.New(deepResearchUsage)
			}
			opts.ResumeFromRunID = strings.TrimSpace(fields[i])
		case strings.HasPrefix(field, "--resume="):
			value := strings.TrimSpace(strings.TrimPrefix(field, "--resume="))
			if value == "" {
				return deepResearchOptions{}, errors.New(deepResearchUsage)
			}
			opts.ResumeFromRunID = value
		case strings.HasPrefix(field, "--"):
			return deepResearchOptions{}, fmt.Errorf("%s: unknown option %s", deepResearchUsage, field)
		default:
			questionStart = i
			i = len(fields)
		}
	}
	if questionStart >= 0 {
		opts.Question = strings.TrimSpace(strings.Join(fields[questionStart:], " "))
	}
	if opts.Question == "" {
		return deepResearchOptions{}, errors.New(deepResearchUsage)
	}
	return opts, nil
}

func (a *App) startDeepResearchWorkflow(opts deepResearchOptions) (*LocalResult, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		return nil, errors.New(deepResearchUsage)
	}
	if a == nil || a.workflowRunner == nil {
		return nil, errors.New("workflow runner is unavailable")
	}
	resolved, err := a.workflowResolvedScript(workflow.BuiltinDeepResearchName)
	if err != nil {
		return nil, err
	}
	def := resolved.Definition
	if def.Name == "" {
		def = workflow.Definition{Name: workflow.BuiltinDeepResearchName, Description: "Multi-angle web research with independent source verification and cited synthesis"}
	}
	if opts.Remember {
		if _, err := a.trustWorkflow(workflow.BuiltinDeepResearchName); err != nil {
			return nil, err
		}
	}
	out, err := a.workflowRunner.StartWorkflow(context.Background(), a.sessionID, workflow.WorkflowInput{
		Name:            workflow.BuiltinDeepResearchName,
		Args:            question,
		ResumeFromRunID: opts.ResumeFromRunID,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.Error) != "" {
		return nil, errors.New(out.Error)
	}
	lines := []string{
		"Workflow(dynamic workflow: " + def.Name + ")",
		"/workflows to view dynamic workflow runs",
		"",
		"The " + def.Name + " workflow is now running in the background.",
	}
	if len(def.Phases) > 0 {
		lines = append(lines, "It will:", "")
		for i, phase := range def.Phases {
			title := strings.TrimSpace(phase.Title)
			if title == "" {
				title = fmt.Sprintf("Phase %d", i+1)
			}
			line := fmt.Sprintf("%d. %s", i+1, title)
			if detail := strings.TrimSpace(phase.Detail); detail != "" {
				line += " — " + detail
			}
			lines = append(lines, line)
		}
	} else if description := strings.TrimSpace(def.Description); description != "" {
		lines = append(lines, description)
	}
	if opts.ResumeFromRunID != "" {
		lines = append(lines, "", "Resumed from: "+opts.ResumeFromRunID)
	}
	lines = append(lines,
		"",
		"You can watch live progress with /workflows. I'll report back when it completes.",
		"",
		"✻ Waiting for 1 dynamic workflow to finish")
	fields := []LocalResultField{
		{Label: "Status", Value: out.Status, Tone: "info"},
		{Label: "Run", Value: string(out.RunID)},
		{Label: "Workflow", Value: def.Name},
		{Label: "Args", Value: question},
	}
	if opts.ResumeFromRunID != "" {
		fields = append(fields, LocalResultField{Label: "Resume", Value: opts.ResumeFromRunID})
	}
	if out.ScriptPath != "" {
		fields = append(fields, LocalResultField{Label: "Script", Value: out.ScriptPath})
	}
	return &LocalResult{
		Kind:      "workflow-run",
		Title:     def.Name + " is running in background",
		Fields:    fields,
		PlainText: strings.Join(lines, "\n"),
	}, nil
}

func (a *App) StartWorkflowFromConfirmation(name, args, resumeFromRunID string, trust bool) (*LocalResult, error) {
	name = strings.TrimSpace(name)
	switch name {
	case "", workflow.BuiltinDeepResearchName:
		return a.startDeepResearchWorkflow(deepResearchOptions{
			Question:        strings.TrimSpace(args),
			ResumeFromRunID: strings.TrimSpace(resumeFromRunID),
			Confirmed:       true,
			Remember:        trust,
		})
	default:
		return nil, fmt.Errorf("unknown workflow %q", name)
	}
}

func (a *App) buildWorkflowLaunchConfirmation(name string, opts deepResearchOptions) (*LocalResult, error) {
	def, err := a.workflowDefinition(name)
	if err != nil {
		return nil, err
	}
	if def.Name == "" {
		def = workflow.Definition{Name: name, Description: "Dynamic workflow"}
	}
	lines := []string{
		"Workflow(dynamic workflow: " + def.Name + ")",
		"",
		"Run a dynamic workflow?",
		"",
		def.Description,
	}
	if len(def.Phases) > 0 {
		lines = append(lines, "", "phases:")
		for i, phase := range def.Phases {
			detail := strings.TrimSpace(phase.Detail)
			line := fmt.Sprintf("  %d. %s", i+1, strings.TrimSpace(phase.Title))
			if detail != "" {
				line += " - " + detail
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "", "args: "+opts.Question)
	if note := strings.TrimSpace(def.RiskNote); note != "" {
		lines = append(lines, "", note)
	}
	lines = append(lines, "",
		"Use Enter to run, or Esc to cancel.")

	fields := []LocalResultField{
		{Label: "Workflow", Value: def.Name, Tone: "info"},
		{Label: "Args", Value: opts.Question},
	}
	if opts.ResumeFromRunID != "" {
		fields = append(fields, LocalResultField{Label: "Resume", Value: opts.ResumeFromRunID})
	}
	if def.EstimatedAgents > 0 {
		fields = append(fields, LocalResultField{Label: "Estimated agents", Value: strconv.Itoa(def.EstimatedAgents), Tone: "warn"})
	}
	if def.DefaultBudgetTokens > 0 {
		fields = append(fields, LocalResultField{Label: "Default budget", Value: strconv.Itoa(def.DefaultBudgetTokens) + " completion tokens"})
	}
	if note := strings.TrimSpace(def.RiskNote); note != "" {
		fields = append(fields, LocalResultField{Label: "Risk", Value: note, Tone: "warn"})
	}
	return &LocalResult{
		Kind:     "workflow-launch",
		Title:    "Run a dynamic workflow?",
		Fields:   fields,
		Sections: []LocalResultSection{workflowPhaseSection(def.Phases)},
		Actions: []LocalResultAction{
			{Label: "Yes, run it", WorkflowName: def.Name, WorkflowArgs: opts.Question, WorkflowResume: opts.ResumeFromRunID},
			{Label: "Yes, and don't ask again for " + def.Name + " in this workspace", WorkflowName: def.Name, WorkflowArgs: opts.Question, WorkflowResume: opts.ResumeFromRunID, WorkflowTrust: true},
			{Label: "No", Description: "Cancel this workflow launch", Tone: "muted"},
		},
		PlainText: strings.Join(lines, "\n"),
	}, nil
}

func (a *App) workflowDefinition(name string) (workflow.Definition, error) {
	if a == nil || a.workflowRunner == nil || a.workflowRunner.Library == nil {
		return workflow.Definition{}, nil
	}
	defs, err := a.workflowRunner.Library.List(context.Background())
	if err != nil {
		return workflow.Definition{}, err
	}
	for _, def := range defs {
		if def.Name == name && def.Status == workflow.DefinitionReady {
			return def, nil
		}
	}
	return workflow.Definition{}, nil
}

func (a *App) workflowResolvedScript(name string) (workflow.ResolvedScript, error) {
	if a == nil || a.workflowRunner == nil || a.workflowRunner.Library == nil {
		return workflow.ResolvedScript{}, nil
	}
	return a.workflowRunner.Library.Resolve(context.Background(), strings.TrimSpace(name))
}

func (a *App) workflowTrustKey(name string) (string, error) {
	resolved, err := a.workflowResolvedScript(name)
	if err != nil {
		return "", err
	}
	def := resolved.Definition
	if strings.TrimSpace(def.Name) == "" {
		def.Name = strings.TrimSpace(name)
	}
	return workflowTrustKeyForResolved(def, resolved.Script), nil
}

func workflowTrustKeyForResolved(def workflow.Definition, script string) string {
	name := strings.ToLower(strings.TrimSpace(def.Name))
	source := strings.ToLower(strings.TrimSpace(def.Source))
	if source == "" {
		source = "workflow"
	}
	path := strings.TrimSpace(def.Path)
	if path != "" {
		path = filepath.Clean(path)
	}
	sum := sha256.Sum256([]byte(script))
	return strings.Join([]string{name, source, path, "sha256:" + hex.EncodeToString(sum[:])}, "|")
}

func (a *App) workflowTrusted(name string) (bool, error) {
	key, err := a.workflowTrustKey(name)
	if err != nil {
		return false, err
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return false, nil
	}
	for _, trusted := range a.cfg.TrustedWorkflows {
		if strings.ToLower(strings.TrimSpace(trusted)) == key {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) trustWorkflow(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("workflow name is required")
	}
	key, err := a.workflowTrustKey(name)
	if err != nil {
		return "", err
	}
	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return "", err
	}
	set := disabledNameSet(cfg.Workflows.Trusted)
	set[strings.ToLower(key)] = key
	cfg.Workflows.Trusted = sortedSkillNames(set)
	if err := SaveConfigFile(path, cfg); err != nil {
		return "", err
	}
	a.cfg.TrustedWorkflows = mergeNames(a.cfg.TrustedWorkflows, []string{key})
	return path, nil
}

func workflowPhaseSection(phases []workflow.ScriptPhase) LocalResultSection {
	fields := make([]LocalResultField, 0, len(phases))
	for i, phase := range phases {
		title := strings.TrimSpace(phase.Title)
		if title == "" {
			continue
		}
		value := strings.TrimSpace(phase.Detail)
		if value == "" {
			value = title
		}
		fields = append(fields, LocalResultField{Label: strconv.Itoa(i+1) + ". " + title, Value: value})
	}
	if len(fields) == 0 {
		return LocalResultSection{}
	}
	return LocalResultSection{Title: "Phases", Fields: fields}
}

func workflowResultDisplayFields(result any) []LocalResultField {
	obj, ok := result.(map[string]any)
	if !ok {
		if value := workflowResultDisplayValue(result); value != "" {
			return []LocalResultField{{Label: "Result", Value: value}}
		}
		return nil
	}
	fields := []LocalResultField{}
	used := map[string]bool{}
	if answer := strings.TrimSpace(workflowLocalString(obj["answer"])); answer != "" {
		fields = append(fields, LocalResultField{Label: "Answer", Value: answer})
		used["answer"] = true
	} else if summary := strings.TrimSpace(workflowLocalString(obj["summary"])); summary != "" {
		fields = append(fields, LocalResultField{Label: "Summary", Value: summary})
		used["summary"] = true
	} else if decision := strings.TrimSpace(workflowLocalString(obj["decision"])); decision != "" {
		fields = append(fields, LocalResultField{Label: "Decision", Value: decision})
		used["decision"] = true
	} else if verdict := strings.TrimSpace(workflowLocalString(obj["verdict"])); verdict != "" {
		fields = append(fields, LocalResultField{Label: "Verdict", Value: verdict})
		used["verdict"] = true
	}
	if sources := workflowStringList(obj["sources"]); len(sources) > 0 {
		fields = append(fields, LocalResultField{Label: "Sources", Value: strings.Join(sources, "\n")})
		used["sources"] = true
	}
	if caveats := workflowStringList(obj["caveats"]); len(caveats) > 0 {
		fields = append(fields, LocalResultField{Label: "Caveats", Value: strings.Join(caveats, "\n")})
		used["caveats"] = true
	} else if caveats := strings.TrimSpace(workflowLocalString(obj["caveats"])); caveats != "" {
		fields = append(fields, LocalResultField{Label: "Caveats", Value: caveats})
		used["caveats"] = true
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		if used[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := workflowResultDisplayValue(obj[key])
		if value == "" {
			continue
		}
		fields = append(fields, LocalResultField{Label: key, Value: value})
	}
	return fields
}

func workflowResultDisplayValue(value any) string {
	if value == nil {
		return "null"
	}
	if s := strings.TrimSpace(workflowLocalString(value)); s != "" {
		return s
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("<unrenderable workflow result: %T>", value)
	}
	return strings.TrimSpace(string(data))
}

func workflowStringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := strings.TrimSpace(workflowLocalString(item)); s != "" {
			out = append(out, s)
		}
	}
	return out
}
