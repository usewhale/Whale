package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type localShellCommandsFile struct {
	Commands []localShellCommand `toml:"commands"`
}

type localShellCommand struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Command     string `toml:"command"`
	CWD         string `toml:"cwd"`
	TimeoutMS   int    `toml:"timeout_ms"`
	Class       string `toml:"class"`
}

func (p installedPlugin) loadCommands(diags []Diagnostic) ([]SlashCommand, []Diagnostic) {
	path := componentPath(p.record.InstallPath, p.record.Components.Commands)
	if path == "" {
		if strings.TrimSpace(p.record.Components.Commands) != "" {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: "path not found: " + p.record.Components.Commands})
		}
		return nil, diags
	}
	var out []SlashCommand
	info, err := os.Stat(path)
	if err != nil {
		diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: err.Error()})
		return nil, diags
	}
	if info.IsDir() {
		md, mdDiags := p.loadPromptCommands(path)
		out = append(out, md...)
		diags = append(diags, mdDiags...)
		tomlPath := filepath.Join(path, "commands.toml")
		if fileExists(tomlPath) {
			shell, shellDiags := p.loadShellCommands(tomlPath)
			out = append(out, shell...)
			diags = append(diags, shellDiags...)
		}
		return out, diags
	}
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		shell, shellDiags := p.loadShellCommands(path)
		out = append(out, shell...)
		diags = append(diags, shellDiags...)
		return out, diags
	}
	diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: "commands component must be a directory or TOML file"})
	return nil, diags
}

func (p installedPlugin) loadPromptCommands(root string) ([]SlashCommand, []Diagnostic) {
	var out []SlashCommand
	var diags []Diagnostic
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: err.Error()})
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		cmd, err := p.promptCommandFromMarkdown(root, path)
		if err != nil {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: err.Error()})
			return nil
		}
		out = append(out, cmd)
		return nil
	})
	if err != nil {
		diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: err.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, diags
}

func (p installedPlugin) promptCommandFromMarkdown(root, path string) (SlashCommand, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SlashCommand{}, err
	}
	meta, body := splitMarkdownFrontmatter(string(b))
	name := namespacedPluginName(p.record.ID, componentNameFromPath(root, path))
	if explicit := strings.TrimSpace(meta["name"]); explicit != "" {
		name = namespacedPluginName(p.record.ID, explicit)
	}
	if name == "/"+p.record.ID {
		return SlashCommand{}, fmt.Errorf("%s: command name must not be empty", path)
	}
	desc := strings.TrimSpace(firstNonEmpty(meta["description"], meta["when_to_use"]))
	class := parseCommandClass(meta["class"], CommandTurnStarting)
	hidden := parseBool(meta["hidden"])
	readOnly := parseBool(meta["read_only"]) || class == CommandReadOnly
	skipHooks := parseBool(meta["skip_user_prompt_hooks"])
	skipSkills := parseBool(meta["skip_skill_injection"])
	body = strings.TrimSpace(body)
	return SlashCommand{
		Name:        name,
		Usage:       strings.TrimSpace(meta["argument_hint"]),
		Description: desc,
		Class:       class,
		StartsTurn:  true,
		Run: func(ctx context.Context, pluginCtx Context, line string) (CommandResult, error) {
			_ = ctx
			_ = pluginCtx
			args := commandArgs(line, name)
			input := renderPromptCommand(body, args)
			return CommandResult{Turn: &CommandTurn{
				Input:               input,
				Hidden:              hidden,
				ReadOnly:            readOnly,
				SkipUserPromptHooks: skipHooks,
				SkipSkillInjection:  skipSkills,
			}}, nil
		},
	}, nil
}

func (p installedPlugin) loadShellCommands(path string) ([]SlashCommand, []Diagnostic) {
	var file localShellCommandsFile
	if _, err := toml.DecodeFile(path, &file); err != nil {
		return nil, []Diagnostic{{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: err.Error()}}
	}
	var out []SlashCommand
	var diags []Diagnostic
	for _, spec := range file.Commands {
		name := namespacedPluginName(p.record.ID, spec.Name)
		if name == "/"+p.record.ID {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: "ignored shell command with empty name"})
			continue
		}
		command := strings.TrimSpace(spec.Command)
		if command == "" {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.commands", Detail: "ignored shell command " + name + " with empty command"})
			continue
		}
		class := parseCommandClass(spec.Class, CommandMutating)
		timeout := spec.TimeoutMS
		if timeout <= 0 {
			timeout = 30000
		}
		cwd := strings.TrimSpace(spec.CWD)
		description := strings.TrimSpace(spec.Description)
		out = append(out, SlashCommand{
			Name:        name,
			Description: description,
			Class:       class,
			StartsTurn:  true,
			Run: func(ctx context.Context, pluginCtx Context, line string) (CommandResult, error) {
				_ = ctx
				_ = pluginCtx
				prompt := shellCommandTurnPrompt(p.record.ID, name, command, cwd, timeout)
				return CommandResult{Turn: &CommandTurn{
					Input:               prompt,
					Hidden:              true,
					ReadOnly:            class == CommandReadOnly,
					SkipUserPromptHooks: true,
					SkipSkillInjection:  true,
				}}, nil
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, diags
}

func (p installedPlugin) loadAgents(diags []Diagnostic) ([]AgentDefinition, []Diagnostic) {
	path := componentPath(p.record.InstallPath, p.record.Components.Agents)
	if path == "" {
		if strings.TrimSpace(p.record.Components.Agents) != "" {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.agents", Detail: "path not found: " + p.record.Components.Agents})
		}
		return nil, diags
	}
	var out []AgentDefinition
	err := filepath.WalkDir(path, func(file string, d os.DirEntry, err error) error {
		if err != nil {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.agents", Detail: err.Error()})
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(file), ".md") {
			return nil
		}
		agent, err := p.agentFromMarkdown(path, file)
		if err != nil {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.agents", Detail: err.Error()})
			return nil
		}
		out = append(out, agent)
		return nil
	})
	if err != nil {
		diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.agents", Detail: err.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, diags
}

func (p installedPlugin) agentFromMarkdown(root, path string) (AgentDefinition, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return AgentDefinition{}, err
	}
	meta, body := splitMarkdownFrontmatter(string(b))
	name := namespacedPluginBareName(p.record.ID, componentNameFromPath(root, path))
	if explicit := strings.TrimSpace(meta["name"]); explicit != "" {
		name = namespacedPluginBareName(p.record.ID, explicit)
	}
	if name == p.record.ID {
		return AgentDefinition{}, fmt.Errorf("%s: agent name must not be empty", path)
	}
	return AgentDefinition{
		Name:            name,
		Description:     strings.TrimSpace(firstNonEmpty(meta["description"], meta["when_to_use"])),
		SystemPrompt:    strings.TrimSpace(body),
		Model:           strings.TrimSpace(meta["model"]),
		Effort:          strings.TrimSpace(meta["effort"]),
		MaxToolIters:    parseInt(meta["max_tool_iters"]),
		MaxToolCalls:    parseInt(meta["max_tool_calls"]),
		Capabilities:    parseList(meta["capabilities"]),
		AllowedTools:    parseList(meta["allowed_tools"]),
		DisallowedTools: parseList(meta["disallowed_tools"]),
		Source:          "plugin:" + p.record.ID,
		FilePath:        path,
	}, nil
}

func (p installedPlugin) loadRules(diags []Diagnostic) ([]RuleBlock, []Diagnostic) {
	path := componentPath(p.record.InstallPath, p.record.Components.Rules)
	if path == "" {
		if strings.TrimSpace(p.record.Components.Rules) != "" {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.rules", Detail: "path not found: " + p.record.Components.Rules})
		}
		return nil, diags
	}
	var out []RuleBlock
	err := filepath.WalkDir(path, func(file string, d os.DirEntry, err error) error {
		if err != nil {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.rules", Detail: err.Error()})
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(file), ".md") {
			return nil
		}
		b, err := os.ReadFile(file)
		if err != nil {
			diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.rules", Detail: err.Error()})
			return nil
		}
		meta, body := splitMarkdownFrontmatter(string(b))
		name := namespacedPluginBareName(p.record.ID, componentNameFromPath(path, file))
		if explicit := strings.TrimSpace(meta["name"]); explicit != "" {
			name = namespacedPluginBareName(p.record.ID, explicit)
		}
		out = append(out, RuleBlock{Name: name, Content: strings.TrimSpace(body), Source: "plugin:" + p.record.ID, FilePath: file})
		return nil
	})
	if err != nil {
		diags = append(diags, Diagnostic{PluginID: p.record.ID, Level: DiagnosticWarn, Label: "components.rules", Detail: err.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, diags
}

func splitMarkdownFrontmatter(raw string) (map[string]string, string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return map[string]string{}, raw
	}
	rest := strings.TrimPrefix(raw, "---\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return map[string]string{}, raw
	}
	head := rest[:idx]
	body := strings.TrimPrefix(rest[idx:], "\n---")
	body = strings.TrimPrefix(body, "\n")
	meta := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.ToLower(strings.TrimSpace(k))] = trimMetaValue(v)
	}
	return meta, body
}

func trimMetaValue(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"'`)
	return strings.TrimSpace(v)
}

func componentNameFromPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	parts := strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return strings.Join(cleanNameParts(parts), ":")
}

func namespacedPluginName(pluginID, name string) string {
	return "/" + namespacedPluginBareName(pluginID, name)
}

func namespacedPluginBareName(pluginID, name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == ':' || r == '/' || r == '\\'
	})
	cleaned := cleanNameParts(parts)
	if len(cleaned) == 0 {
		return cleanPluginID(pluginID)
	}
	return cleanPluginID(pluginID) + ":" + strings.Join(cleaned, ":")
}

func cleanNameParts(parts []string) []string {
	var out []string
	for _, part := range parts {
		if clean := cleanPluginComponentName(part); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}

func parseCommandClass(raw string, fallback CommandClass) CommandClass {
	switch CommandClass(strings.TrimSpace(strings.ToLower(raw))) {
	case CommandReadOnly, CommandMutating, CommandUI, CommandTurnStarting:
		return CommandClass(strings.TrimSpace(strings.ToLower(raw)))
	default:
		return fallback
	}
}

func parseBool(raw string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(strings.ToLower(raw)))
	return v
}

func parseInt(raw string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(raw))
	return v
}

func parseList(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "[]")
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	var out []string
	for _, field := range fields {
		if v := trimMetaValue(field); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func commandArgs(line, commandName string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), commandName))
}

func renderPromptCommand(body, args string) string {
	body = strings.ReplaceAll(body, "{{args}}", args)
	body = strings.ReplaceAll(body, "${args}", args)
	if strings.TrimSpace(args) == "" || strings.Contains(body, args) {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(body) + "\n\nUser arguments:\n" + args
}

func shellCommandTurnPrompt(pluginID, name, command, cwd string, timeoutMS int) string {
	var b strings.Builder
	b.WriteString("Run this plugin shell command exactly once using the shell_run tool. Do not rewrite, split, or broaden the command.\n\n")
	b.WriteString("Plugin: " + pluginID + "\n")
	b.WriteString("Command name: " + name + "\n")
	b.WriteString("shell_run input:\n")
	b.WriteString("{\n")
	b.WriteString(fmt.Sprintf("  \"command\": %q,\n", command))
	if strings.TrimSpace(cwd) != "" {
		b.WriteString(fmt.Sprintf("  \"cwd\": %q,\n", cwd))
	}
	b.WriteString(fmt.Sprintf("  \"timeout_ms\": %d\n", timeoutMS))
	b.WriteString("}\n")
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
