// Package skills discovers and parses local Agent Skills.
package skills

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	SkillFileName        = "SKILL.md"
	MaxNameLength        = 64
	MaxDescriptionLength = 1024

	availableSkillsIndexMaxChars = 2000
	availableSkillLineMaxChars   = 180
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

// Skill represents a parsed SKILL.md file.
type Skill struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	When          string   `json:"when,omitempty"`
	Requires      Requires `json:"requires,omitempty"`
	Instructions  string   `json:"instructions"`
	Path          string   `json:"path"`
	SkillFilePath string   `json:"skill_file_path"`
}

// Requires describes optional setup that makes a skill fully ready.
type Requires struct {
	Commands []string `json:"commands,omitempty"`
	Env      []string `json:"env,omitempty"`
	MCP      []string `json:"mcp,omitempty"`
}

// DiscoveryState represents the outcome of discovering a single skill file.
type DiscoveryState int

const (
	// StateNormal indicates the skill was parsed and validated successfully.
	StateNormal DiscoveryState = iota
	// StateError indicates discovery encountered a scan/parse/validate error.
	StateError
)

// SkillState represents the latest discovery status of a skill file.
type SkillState struct {
	Name  string
	Path  string
	State DiscoveryState
	Err   error
}

// Availability is the user-facing availability bucket for a discovered skill.
type Availability string

const (
	AvailabilityReady      Availability = "ready"
	AvailabilityNeedsSetup Availability = "needs_setup"
	AvailabilityDisabled   Availability = "disabled"
	AvailabilityProblem    Availability = "problem"
)

// MissingRequirement describes one unmet requirement from a skill manifest.
type MissingRequirement struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// SkillView is a user-facing view of a skill and its current availability.
type SkillView struct {
	Name          string
	Description   string
	When          string
	Path          string
	SkillFilePath string
	Source        string
	Status        Availability
	Reason        string
	Missing       []MissingRequirement
}

// Report groups discovered skills for display and selection.
type Report struct {
	Roots      []string
	Ready      []SkillView
	NeedsSetup []SkillView
	Disabled   []SkillView
	Problems   []SkillView
}

// ReportOptions controls user-facing skill availability evaluation.
type ReportOptions struct {
	DisabledNames []string
	MCPConnected  map[string]bool
	WorkspaceRoot string
}

// DefaultRoots returns the skill discovery roots for a workspace.
func DefaultRoots(workspaceRoot string) []string {
	var roots []string
	if root := strings.TrimSpace(workspaceRoot); root != "" {
		roots = append(roots,
			filepath.Join(root, ".whale", "skills"),
			filepath.Join(root, ".agents", "skills"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots,
			filepath.Join(home, ".whale", "skills"),
			filepath.Join(home, ".agents", "skills"),
		)
	}
	return uniqueCleanPaths(roots)
}

// ValidName reports whether name is a valid skill name.
func ValidName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && len(name) <= MaxNameLength && namePattern.MatchString(name)
}

// Validate checks if the skill meets Whale's v1 skill requirements.
func (s *Skill) Validate() error {
	var errs []error
	if s.Name == "" {
		errs = append(errs, errors.New("name is required"))
	} else {
		if len(s.Name) > MaxNameLength {
			errs = append(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !namePattern.MatchString(s.Name) {
			errs = append(errs, errors.New("name must be alphanumeric with hyphens, no leading/trailing/consecutive hyphens"))
		}
		if s.Path != "" && !strings.EqualFold(filepath.Base(s.Path), s.Name) {
			errs = append(errs, fmt.Errorf("name %q must match directory %q", s.Name, filepath.Base(s.Path)))
		}
	}
	if s.Description == "" {
		errs = append(errs, errors.New("description is required"))
	} else if len(s.Description) > MaxDescriptionLength {
		errs = append(errs, fmt.Errorf("description exceeds %d characters", MaxDescriptionLength))
	}
	return errors.Join(errs...)
}

// Parse parses a SKILL.md file from disk.
func Parse(path string) (*Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	skill, err := ParseContent(content)
	if err != nil {
		return nil, err
	}
	skill.Path = filepath.Dir(path)
	skill.SkillFilePath = path
	return skill, nil
}

// ParseContent parses a SKILL.md from raw bytes.
func ParseContent(content []byte) (*Skill, error) {
	frontmatter, body, err := splitFrontmatter(string(content))
	if err != nil {
		return nil, err
	}
	values := parseFrontmatter(frontmatter)
	skill := &Skill{
		Name:         values.Name,
		Description:  values.Description,
		When:         values.When,
		Requires:     values.Requires,
		Instructions: strings.TrimSpace(body),
	}
	return skill, nil
}

// Discover finds all valid skills in the given roots. Earlier roots take
// precedence when multiple skills have the same name.
func Discover(roots []string) []*Skill {
	skills, _ := DiscoverWithStates(roots)
	return skills
}

// DiscoverWithStates finds all valid skills and returns parse/validation
// states for diagnostics.
func DiscoverWithStates(roots []string) ([]*Skill, []*SkillState) {
	var all []*Skill
	var states []*SkillState
	seenFiles := map[string]bool{}
	for _, root := range uniqueCleanPaths(roots) {
		if strings.TrimSpace(root) == "" {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			states = append(states, &SkillState{Path: root, State: StateError, Err: err})
			continue
		}
		walkRoot := root
		// WalkDir does not follow a symlinked root, but we still keep skill paths
		// anchored at the original root so picker bindings and display paths stay stable.
		if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				states = append(states, &SkillState{Path: root, State: StateError, Err: err})
				continue
			}
			walkRoot = resolved
		}
		err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				states = append(states, &SkillState{Path: path, State: StateError, Err: err})
				return nil
			}
			if d.IsDir() || d.Name() != SkillFileName {
				return nil
			}
			skillPath := path
			if walkRoot != root {
				if rel, err := filepath.Rel(walkRoot, path); err == nil {
					skillPath = filepath.Join(root, rel)
				}
			}
			abs, err := filepath.Abs(skillPath)
			if err != nil {
				states = append(states, &SkillState{Path: path, State: StateError, Err: err})
				return nil
			}
			if seenFiles[abs] {
				return nil
			}
			seenFiles[abs] = true
			skill, err := Parse(abs)
			if err != nil {
				states = append(states, &SkillState{Path: abs, State: StateError, Err: err})
				return nil
			}
			if err := skill.Validate(); err != nil {
				states = append(states, &SkillState{Name: skill.Name, Path: abs, State: StateError, Err: err})
				return nil
			}
			all = append(all, skill)
			states = append(states, &SkillState{Name: skill.Name, Path: abs, State: StateNormal})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			states = append(states, &SkillState{Path: root, State: StateError, Err: err})
		}
	}
	return Sort(Deduplicate(all)), states
}

// Find returns a skill by exact name.
func Find(roots []string, name string) (*Skill, []*SkillState, bool) {
	discovered, states := DiscoverWithStates(roots)
	for _, skill := range discovered {
		if skill.Name == name {
			return skill, states, true
		}
	}
	return nil, states, false
}

// FindByPath returns a discovered skill by exact SKILL.md path.
func FindByPath(roots []string, skillFilePath string) (*Skill, []*SkillState, bool) {
	discovered, states := DiscoverWithStates(roots)
	target, err := filepath.Abs(strings.TrimSpace(skillFilePath))
	if err != nil || target == "" {
		return nil, states, false
	}
	target = filepath.Clean(target)
	for _, skill := range discovered {
		path, err := filepath.Abs(strings.TrimSpace(skill.SkillFilePath))
		if err != nil || path == "" {
			continue
		}
		if filepath.Clean(path) == target {
			return skill, states, true
		}
	}
	return nil, states, false
}

// Sort returns a copy sorted by lowercase skill name.
func Sort(all []*Skill) []*Skill {
	out := append([]*Skill(nil), all...)
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i].Name)
		right := strings.ToLower(out[j].Name)
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left < right
	})
	return out
}

// Deduplicate removes duplicate skills by name. The first occurrence wins,
// so callers can pass roots in priority order.
func Deduplicate(all []*Skill) []*Skill {
	seen := map[string]bool{}
	out := make([]*Skill, 0, len(all))
	for _, skill := range all {
		if skill == nil || seen[skill.Name] {
			continue
		}
		seen[skill.Name] = true
		out = append(out, skill)
	}
	return out
}

// RenderAvailableSkills renders a compact system-prompt index.
func RenderAvailableSkills(all []*Skill) string {
	all = Sort(all)
	if len(all) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available skills:\n")
	omitted := 0
	for _, skill := range all {
		line := renderAvailableSkillLine(skill, true)
		if line == "" {
			continue
		}
		if b.Len()+len(line)+1 > availableSkillsIndexMaxChars {
			line = renderAvailableSkillLine(skill, false)
		}
		if b.Len()+len(line)+1 > availableSkillsIndexMaxChars {
			omitted++
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if omitted > 0 {
		b.WriteString(fmt.Sprintf("- ... %d more skills omitted from this compact index\n", omitted))
	}
	b.WriteString("\nUse load_skill only when the user names a skill with $skill-name or the task strongly matches a listed skill. The index is metadata only; load a skill before relying on its instructions. Prefer direct tools or delegation when clearly requested. Loaded skill results include the skill path for resolving references/, scripts/, templates/, and assets/.")
	return strings.TrimSpace(b.String())
}

func renderAvailableSkillLine(skill *Skill, includeDescription bool) string {
	if skill == nil {
		return ""
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return ""
	}
	line := "- " + name
	if !includeDescription {
		return line
	}
	description := compactInlineText(skill.Description)
	when := compactInlineText(skill.When)
	detail := description
	if when != "" {
		if detail != "" {
			detail += " When: "
		} else {
			detail = "When: "
		}
		detail += when
	}
	if detail == "" {
		return line
	}
	line += ": " + detail
	return clipString(line, availableSkillLineMaxChars)
}

func compactInlineText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func clipString(s string, max int) string {
	runes := []rune(s)
	if max <= 0 || len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}

// ApproxTokenCount returns a rough estimate using a common ~4 chars/token heuristic.
func ApproxTokenCount(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// Filter removes skills whose names appear in the disabled list.
func Filter(all []*Skill, disabled []string) []*Skill {
	if len(disabled) == 0 {
		return all
	}
	disabledSet := disabledNameSet(disabled)
	out := make([]*Skill, 0, len(all))
	for _, skill := range all {
		if skill != nil && !disabledSet[strings.ToLower(skill.Name)] {
			out = append(out, skill)
		}
	}
	return out
}

// BuildReport evaluates discovered skills for user-facing display.
func BuildReport(roots []string, opts ReportOptions) Report {
	roots = uniqueCleanPaths(roots)
	discovered, states := DiscoverWithStates(roots)
	disabled := disabledNameSet(opts.DisabledNames)
	report := Report{Roots: roots}
	for _, skill := range discovered {
		if skill == nil {
			continue
		}
		view := skillView(skill, opts.WorkspaceRoot)
		if disabled[strings.ToLower(skill.Name)] {
			view.Status = AvailabilityDisabled
			view.Reason = "Disabled in config"
			report.Disabled = append(report.Disabled, view)
			continue
		}
		missing := MissingRequirements(skill, opts)
		if len(missing) > 0 {
			view.Status = AvailabilityNeedsSetup
			view.Missing = missing
			view.Reason = FormatMissingRequirements(missing)
			report.NeedsSetup = append(report.NeedsSetup, view)
			continue
		}
		view.Status = AvailabilityReady
		report.Ready = append(report.Ready, view)
	}
	for _, st := range states {
		if st == nil || st.State != StateError {
			continue
		}
		name := strings.TrimSpace(st.Name)
		if name == "" && strings.TrimSpace(st.Path) != "" {
			name = filepath.Base(filepath.Dir(st.Path))
		}
		view := SkillView{
			Name:          name,
			Path:          filepath.Dir(st.Path),
			SkillFilePath: st.Path,
			Source:        SourceForPath(st.Path, opts.WorkspaceRoot),
			Status:        AvailabilityProblem,
			Reason:        "Invalid SKILL.md",
		}
		if st.Err != nil {
			view.Reason = st.Err.Error()
		}
		report.Problems = append(report.Problems, view)
	}
	sortViews(report.Ready)
	sortViews(report.NeedsSetup)
	sortViews(report.Disabled)
	sortViews(report.Problems)
	return report
}

// Selectable returns skills that should appear in the quick picker.
func (r Report) Selectable() []SkillView {
	out := make([]SkillView, 0, len(r.Ready)+len(r.NeedsSetup))
	out = append(out, r.Ready...)
	out = append(out, r.NeedsSetup...)
	sortViews(out)
	return out
}

// All returns every discovered skill view in display order.
func (r Report) All() []SkillView {
	out := make([]SkillView, 0, len(r.Ready)+len(r.NeedsSetup)+len(r.Disabled)+len(r.Problems))
	out = append(out, r.Ready...)
	out = append(out, r.NeedsSetup...)
	out = append(out, r.Disabled...)
	out = append(out, r.Problems...)
	sortViews(out)
	return out
}

// MissingRequirements returns unmet command, env, and MCP requirements.
func MissingRequirements(skill *Skill, opts ReportOptions) []MissingRequirement {
	if skill == nil {
		return nil
	}
	var missing []MissingRequirement
	for _, name := range uniqueTrimmed(skill.Requires.Commands) {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, MissingRequirement{Kind: "command", Name: name, Detail: "command not found"})
		}
	}
	for _, name := range uniqueTrimmed(skill.Requires.Env) {
		if _, ok := os.LookupEnv(name); !ok {
			missing = append(missing, MissingRequirement{Kind: "env", Name: name, Detail: "environment variable not set"})
		}
	}
	for _, name := range uniqueTrimmed(skill.Requires.MCP) {
		if !opts.MCPConnected[name] {
			missing = append(missing, MissingRequirement{Kind: "mcp", Name: name, Detail: "MCP server not connected"})
		}
	}
	return missing
}

// FormatMissingRequirements renders a compact user-facing setup reason.
func FormatMissingRequirements(missing []MissingRequirement) string {
	if len(missing) == 0 {
		return ""
	}
	parts := make([]string, 0, len(missing))
	for _, req := range missing {
		switch req.Kind {
		case "command":
			parts = append(parts, req.Name)
		case "env":
			parts = append(parts, req.Name)
		case "mcp":
			parts = append(parts, "MCP server "+req.Name)
		default:
			parts = append(parts, req.Name)
		}
	}
	return "Needs: " + strings.Join(parts, ", ")
}

// SourceForPath returns a compact source label for display.
func SourceForPath(path, workspaceRoot string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if workspaceRoot != "" {
		root, err := filepath.Abs(workspaceRoot)
		if err == nil {
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
				return "project"
			}
		}
	}
	return "user"
}

func splitFrontmatter(content string) (frontmatter, body string, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			start = i
			break
		}
	}
	if start == -1 || strings.TrimSpace(lines[start]) != "---" {
		return "", "", errors.New("no YAML frontmatter found")
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", "", errors.New("unclosed frontmatter")
	}
	return strings.Join(lines[start+1:end], "\n"), strings.Join(lines[end+1:], "\n"), nil
}

type parsedFrontmatter struct {
	Name        string
	Description string
	When        string
	Requires    Requires
}

func parseFrontmatter(frontmatter string) parsedFrontmatter {
	var values parsedFrontmatter
	section := ""
	listKey := ""
	lines := strings.Split(frontmatter, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		trimmed := strings.TrimSpace(line)
		if indent > 0 && section == "requires" {
			if strings.HasPrefix(trimmed, "- ") && listKey != "" {
				values.addRequirement(listKey, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
				continue
			}
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				continue
			}
			listKey = normalizeFrontmatterKey(key)
			for _, item := range parseScalarOrList(value) {
				values.addRequirement(listKey, item)
			}
			continue
		}

		section = ""
		listKey = ""
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = normalizeFrontmatterKey(key)
		if folded, ok := blockScalarStyle(value); ok {
			block, next := collectBlockScalar(lines, i+1, indent, folded)
			value = block
			i = next - 1
		}
		switch key {
		case "name":
			values.Name = firstScalar(value)
		case "description":
			values.Description = firstScalar(value)
		case "when":
			values.When = firstScalar(value)
		case "requires":
			section = "requires"
		}
	}
	return values
}

func blockScalarStyle(value string) (folded bool, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, false
	}
	switch value[0] {
	case '>':
		return true, true
	case '|':
		return false, true
	default:
		return false, false
	}
}

func collectBlockScalar(lines []string, start, parentIndent int, folded bool) (string, int) {
	block := make([]string, 0, 4)
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			block = append(block, "")
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= parentIndent {
			break
		}
		block = append(block, strings.TrimSpace(line))
	}
	return formatBlockScalar(block, folded), i
}

func formatBlockScalar(lines []string, folded bool) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if !folded {
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
				b.WriteString("\n\n")
			}
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

func (p *parsedFrontmatter) addRequirement(key, value string) {
	value = cleanScalar(value)
	if value == "" {
		return
	}
	switch key {
	case "commands", "command":
		p.Requires.Commands = append(p.Requires.Commands, value)
	case "env", "environment", "environment_variables":
		p.Requires.Env = append(p.Requires.Env, value)
	case "mcp", "mcp_servers", "mcpservers":
		p.Requires.MCP = append(p.Requires.MCP, value)
	}
}

func normalizeFrontmatterKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func firstScalar(value string) string {
	items := parseScalarOrList(value)
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func parseScalarOrList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
		if inner == "" {
			return nil
		}
		parts := strings.Split(inner, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if cleaned := cleanScalar(part); cleaned != "" {
				out = append(out, cleaned)
			}
		}
		return out
	}
	return []string{cleanScalar(value)}
}

func cleanScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

func disabledNameSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func uniqueTrimmed(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func skillView(skill *Skill, workspaceRoot string) SkillView {
	return SkillView{
		Name:          skill.Name,
		Description:   skill.Description,
		When:          skill.When,
		Path:          skill.Path,
		SkillFilePath: skill.SkillFilePath,
		Source:        SourceForPath(skill.SkillFilePath, workspaceRoot),
	}
}

func sortViews(views []SkillView) {
	sort.SliceStable(views, func(i, j int) bool {
		left := strings.ToLower(views[i].Name)
		right := strings.ToLower(views[j].Name)
		if left == right {
			return views[i].Name < views[j].Name
		}
		return left < right
	})
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		clean, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			clean = filepath.Clean(path)
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}
