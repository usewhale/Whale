package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/core"
)

type PermissionAction string

const (
	PermissionAllow PermissionAction = "allow"
	PermissionAsk   PermissionAction = "ask"
	PermissionDeny  PermissionAction = "deny"
)

func ParsePermissionAction(v string) (PermissionAction, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "allow":
		return PermissionAllow, nil
	case "ask":
		return PermissionAsk, nil
	case "deny":
		return PermissionDeny, nil
	default:
		return "", fmt.Errorf("invalid permission action: %s", v)
	}
}

type PermissionRule struct {
	Permission string
	Pattern    string
	Action     PermissionAction
}

type PermissionConfig struct {
	Read              map[string]string
	Edit              map[string]string
	Shell             map[string]string
	ExternalDirectory map[string]string
	MCP               map[string]string
	Memory            map[string]string
	Task              map[string]string
	MutatingTool      map[string]string
}

type RulePolicy struct {
	Default       PermissionAction
	Rules         []PermissionRule
	WorkspaceRoot string
}

type DefaultToolPolicy struct {
	Default       PermissionAction
	Rules         []PermissionRule
	WorkspaceRoot string
}

type PolicyDecision struct {
	Allow            bool
	RequiresApproval bool
	Reason           string
	Code             string
	Phase            string
	MatchedRule      string
	Permission       string
	Pattern          string
}

type ToolPolicy interface {
	Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision
}

type ScopedAllowPolicy struct {
	Base               ToolPolicy
	ShellAllowPrefixes []string
}

func (p ScopedAllowPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	base := p.Base
	if base == nil {
		base = DefaultToolPolicy{}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow || spec.Name != "shell_run" {
		return decision
	}
	cmd := shellCommandFromInput(call.Input)
	if !shellCommandEligibleForScopedAllow(cmd) {
		return decision
	}
	for _, allow := range p.ShellAllowPrefixes {
		if hasAllowCommandPrefix(cmd, allow) && scopedAllowCommandAllowed(cmd, allow) {
			return PolicyDecision{
				Allow:            true,
				RequiresApproval: false,
				Code:             "scoped_allow_prefix",
				Phase:            "allowed",
				MatchedRule:      allow,
			}
		}
	}
	return decision
}

type ReadOnlyTurnPolicy struct {
	Base ToolPolicy
}

func (p ReadOnlyTurnPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	base := p.Base
	if base == nil {
		base = DefaultToolPolicy{}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow {
		return decision
	}
	if core.IsReadOnlyToolCall(spec, call) {
		return decision
	}
	return PolicyDecision{
		Allow:  false,
		Reason: "review turns are read-only",
		Code:   "read_only_turn_denied",
		Phase:  "denied",
	}
}

func DefaultRules() []PermissionRule {
	rules, err := RulesFromConfig(DefaultPermissionConfig())
	if err != nil {
		panic(fmt.Sprintf("invalid default permission config: %v", err))
	}
	return rules
}

func DefaultPermissionConfig() PermissionConfig {
	return PermissionConfig{
		Read: map[string]string{
			"*":             "allow",
			"*.env":         "ask",
			"*.env.*":       "ask",
			"*.env.example": "allow",
		},
		Edit: map[string]string{
			"*": "ask",
		},
		Shell: map[string]string{
			"*":                       "allow",
			"rm *":                    "ask",
			"rm -r*":                  "deny",
			"rm -R*":                  "deny",
			"rm -f -r*":               "deny",
			"rm -r -f*":               "deny",
			"rm -fr*":                 "deny",
			"rm -rf*":                 "deny",
			"rm --recursive*":         "deny",
			"rm --force -r*":          "deny",
			"rm --force -R*":          "deny",
			"rm --force --recursive*": "deny",
			"rm --recursive --force*": "deny",
			"curl *":                  "ask",
			"wget *":                  "ask",
			"npm install*":            "ask",
			"pnpm install*":           "ask",
			"yarn add*":               "ask",
			"git reset*":              "ask",
			"git restore*":            "ask",
			"git rm*":                 "ask",
			"git clean*":              "ask",
			"git push*":               "ask",
			"gh pr merge*":            "ask",
			"sudo *":                  "ask",
			"dd *":                    "ask",
			"mkfs*":                   "deny",
			"diskutil erase*":         "deny",
		},
		ExternalDirectory: map[string]string{
			"*": "ask",
		},
		MCP: map[string]string{
			"*": "ask",
		},
		Memory: map[string]string{
			"*": "ask",
		},
		MutatingTool: map[string]string{
			"*": "ask",
		},
	}
}

func (p RulePolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	if denied := p.mcpAllowedDirsDenied(spec, call); denied != "" {
		return PolicyDecision{
			Allow:       false,
			Reason:      denied,
			Code:        "mcp_allowed_dirs_denied",
			Phase:       "denied",
			MatchedRule: "mcp_allowed_dirs",
			Permission:  "mcp",
		}
	}
	requests := p.requestsFor(spec, call)
	if len(requests) == 0 {
		requests = []permissionRequest{{Kind: permissionKind(spec.Name), Pattern: permissionTarget(call)}}
	}
	var ask *PermissionRule
	var askReq permissionRequest
	for _, req := range requests {
		rule := p.evaluate(req.Kind, req.Pattern)
		switch rule.Action {
		case PermissionDeny:
			return PolicyDecision{
				Allow:       false,
				Reason:      fmt.Sprintf("%s denied by permission rule", req.Kind),
				Code:        "permission_denied",
				Phase:       "denied",
				MatchedRule: ruleLabel(rule),
				Permission:  req.Kind,
				Pattern:     req.Pattern,
			}
		case PermissionAsk:
			copy := rule
			ask = &copy
			askReq = req
		}
	}
	if ask != nil {
		return PolicyDecision{
			Allow:            true,
			RequiresApproval: true,
			Reason:           "permission rule requires approval",
			Code:             "permission_required",
			Phase:            "needs_approval",
			MatchedRule:      ruleLabel(*ask),
			Permission:       askReq.Kind,
			Pattern:          askReq.Pattern,
		}
	}
	return PolicyDecision{Allow: true, Code: "permission_allow", Phase: "allowed"}
}

func (p DefaultToolPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	rules := append([]PermissionRule{}, DefaultRules()...)
	rules = append(rules, p.Rules...)
	def := p.Default
	if def == "" {
		def = PermissionAllow
	}
	return RulePolicy{Default: def, Rules: rules, WorkspaceRoot: p.WorkspaceRoot}.Decide(spec, call)
}

type permissionRequest struct {
	Kind    string
	Pattern string
}

func (p RulePolicy) requestsFor(spec core.ToolSpec, call core.ToolCall) []permissionRequest {
	kind := permissionKind(spec.Name)
	target := permissionTarget(call)
	// A custom or plugin tool that advertises a state-mutating capability but
	// whose name resolves to no built-in permission category is governed by the
	// mutating_tool permission kind. Do not also evaluate the raw tool name, or
	// restrictive global defaults would make [permissions.mutating_tool] allow
	// rules ineffective.
	if !isMappedPermissionKind(kind) && hasCapability(spec, "mutates_state") {
		return []permissionRequest{{Kind: "mutating_tool", Pattern: spec.Name}}
	}
	requests := []permissionRequest{{Kind: kind, Pattern: target}}
	switch spec.Name {
	case "shell_run":
		cmd := shellCommandFromInput(call.Input)
		requests[0].Pattern = cmd
		for _, dir := range p.externalDirs(cmd) {
			requests = append(requests, permissionRequest{Kind: "external_directory", Pattern: dir})
		}
	default:
		if strings.HasPrefix(spec.Name, "mcp__") && mcpFilesystemTool(spec) {
			for _, dir := range p.externalDirsFromMCPInput(call.Input) {
				requests = append(requests, permissionRequest{Kind: "external_directory", Pattern: dir})
			}
		}
	}
	switch spec.Name {
	case "grep", "search_files":
		// These read-scoped tools carry a search regex/glob in their non-path
		// fields. Evaluate read rules against the directory being searched,
		// not the query text, so that searching for ".env" is not mistaken
		// for reading a .env file.
		requests[0].Pattern = readScopeTarget(call)
	case "apply_patch":
		// Evaluate edit rules against each file the patch touches so that
		// path-based [permissions.edit] rules apply, rather than matching a
		// hash of the whole patch.
		if paths := patchEditPaths(call.Input); len(paths) > 0 {
			requests = requests[:0]
			for _, path := range paths {
				requests = append(requests, permissionRequest{Kind: kind, Pattern: path})
			}
		}
	}
	return requests
}

// isMappedPermissionKind reports whether kind is one of the built-in permission
// categories permissionKind resolves known tools to, as opposed to a raw,
// unmapped custom or plugin tool name.
func isMappedPermissionKind(kind string) bool {
	switch kind {
	case "read", "edit", "shell", "memory", "task", "mcp":
		return true
	default:
		return false
	}
}

// hasCapability reports whether spec advertises the given capability,
// case-insensitively.
func hasCapability(spec core.ToolSpec, capability string) bool {
	want := strings.TrimSpace(strings.ToLower(capability))
	for _, got := range spec.Capabilities {
		if strings.TrimSpace(strings.ToLower(got)) == want {
			return true
		}
	}
	return false
}

// patchEditPaths extracts the file paths an apply_patch call modifies from its
// "*** Update/Add/Delete File:" headers. Rename targets ("*** Move to:") are
// included as well, so a rule cannot be bypassed by moving an allowed file
// onto a denied path.
func patchEditPaths(input string) []string {
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return nil
	}
	patch, _ := body["patch"].(string)
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, prefix) {
				if path := strings.TrimSpace(strings.TrimPrefix(line, prefix)); path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return uniqueStrings(paths)
}

func (p RulePolicy) evaluate(permission, pattern string) PermissionRule {
	fallback := PermissionRule{Permission: permission, Pattern: "*", Action: p.Default}
	if fallback.Action == "" {
		fallback.Action = PermissionAllow
	}
	if permission == "shell" {
		return p.evaluateShell(pattern, fallback)
	}
	for i := len(p.Rules) - 1; i >= 0; i-- {
		rule := p.Rules[i]
		if rule.Action == "" {
			continue
		}
		if !wildcardMatch(rule.Permission, permission) {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return rule
		}
	}
	return fallback
}

func (p RulePolicy) evaluateShell(command string, fallback PermissionRule) PermissionRule {
	// Each shell segment keeps normal last-match-wins rule evaluation. The
	// command-level result then preserves deny precedence across segments so an
	// approval prompt cannot mask a separate denied command.
	segments := normalizeShellSegments(command)
	var ask *PermissionRule
	var allow *PermissionRule
	for _, segment := range segments {
		rule, matched := p.evaluateShellSegment(segment)
		if !matched {
			rule = fallback
		}
		switch rule.Action {
		case PermissionDeny:
			return rule
		case PermissionAsk:
			copy := rule
			ask = &copy
		case PermissionAllow:
			copy := rule
			allow = &copy
		}
	}
	if ask != nil {
		return *ask
	}
	if allow != nil {
		return *allow
	}
	return fallback
}

func (p RulePolicy) evaluateShellSegment(segment string) (PermissionRule, bool) {
	for i := len(p.Rules) - 1; i >= 0; i-- {
		rule := p.Rules[i]
		if rule.Action == "" {
			continue
		}
		if !wildcardMatch(rule.Permission, "shell") {
			continue
		}
		if shellSegmentRuleMatches(rule, segment) {
			return rule, true
		}
	}
	return PermissionRule{}, false
}

// normalizeShellWhitespace collapses runs of intra-line whitespace to single
// spaces, matching the legacy command-prefix normalization.
func normalizeShellWhitespace(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// normalizeShellSegments splits a shell command on common shell control
// boundaries and returns one whitespace-normalized segment per non-empty part.
// Splitting before normalizing keeps separators from being folded into spaces,
// so rule matching cannot span two commands. An empty or whitespace-only
// command yields a single empty segment.
func normalizeShellSegments(command string) []string {
	var out []string
	for _, part := range expandShellRuleSegments(command) {
		if seg := normalizeShellSegmentForRule(part); seg != "" {
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func normalizeShellSegmentForRule(segment string) string {
	var fields []string
	var current strings.Builder
	var quote rune
	escaped := false
	runes := []rune(segment)

	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}

	for _, r := range runes {
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if escaped {
			escaped = false
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			if quote == '"' || quote == 0 {
				escaped = true
				continue
			}
			current.WriteRune(r)
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case '\'':
			if quote == 0 {
				quote = '\''
			} else {
				current.WriteRune(r)
			}
		case ' ', '\t':
			if quote == 0 {
				flush()
			} else {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return strings.Join(fields, " ")
}

func expandShellRuleSegments(command string) []string {
	var out []string
	for _, part := range splitShellRuleSegments(command) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitShellRuleSegments(command string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	runes := []rune(command)

	flush := func() {
		part := strings.TrimSpace(current.String())
		current.Reset()
		if part != "" {
			parts = append(parts, part)
		}
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}
		if escaped {
			escaped = false
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			if quote == '"' {
				escaped = true
			}
			current.WriteRune(r)
		case '"':
			if quote == 0 {
				quote = '"'
			} else if quote == '"' {
				quote = 0
			}
			current.WriteRune(r)
		case '\'':
			if quote == 0 {
				quote = '\''
			}
			current.WriteRune(r)
		case '\n', '\r', ';', '|':
			if quote != 0 {
				current.WriteRune(r)
				continue
			}
			flush()
			if r == '|' && i+1 < len(runes) && runes[i+1] == r {
				i++
			}
		case '&':
			if quote != 0 {
				current.WriteRune(r)
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			if previousNonSpaceRune(runes, i) == '>' || previousNonSpaceRune(runes, i) == '<' {
				current.WriteRune(r)
				continue
			}
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return parts
}

func previousNonSpaceRune(runes []rune, before int) rune {
	for i := before - 1; i >= 0; i-- {
		if runes[i] != ' ' && runes[i] != '\t' {
			return runes[i]
		}
	}
	return 0
}

func shellSegmentRuleMatches(rule PermissionRule, segment string) bool {
	pattern := normalizeShellWhitespace(rule.Pattern)
	if pattern == "*" {
		return true
	}
	return wildcardMatch(pattern, segment)
}

func (p RulePolicy) externalDirs(command string) []string {
	root := cleanAbs(p.WorkspaceRoot)
	if command == "" || root == "" {
		return nil
	}
	var out []string
	for _, segment := range expandShellRuleSegments(command) {
		argv := strings.Fields(segment)
		if len(argv) == 0 || !shellFileCommand(argv[0]) {
			continue
		}
		for _, arg := range argv[1:] {
			arg = strings.Trim(arg, `"'`)
			arg = shellPathArgBeforeRedirection(arg)
			if !shellFileCommandPathArg(arg) {
				continue
			}
			clean := p.resolveShellPathToken(root, arg)
			if clean == "" || pathInside(clean, root) || pathInsideTrustedTemp(clean) {
				continue
			}
			out = append(out, externalDirForToken(clean))
		}
	}
	return uniqueStrings(out)
}

func (p RulePolicy) externalDirsFromMCPInput(input string) []string {
	root := cleanAbs(p.WorkspaceRoot)
	if root == "" || strings.TrimSpace(input) == "" {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return nil
	}
	var out []string
	for _, token := range mcpPathTokens(body) {
		clean := p.resolveShellPathToken(root, token)
		if clean == "" || pathInside(clean, root) || pathInsideTrustedTemp(clean) {
			continue
		}
		out = append(out, externalDirForToken(clean))
	}
	return uniqueStrings(out)
}

func mcpPathTokens(body map[string]any) []string {
	keys := []string{"path", "file_path", "root", "directory", "source", "destination"}
	out := []string{}
	for _, key := range keys {
		if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	if xs, ok := body["paths"].([]any); ok {
		for _, x := range xs {
			if v, ok := x.(string); ok && strings.TrimSpace(v) != "" {
				out = append(out, strings.TrimSpace(v))
			}
		}
	}
	return out
}

func (p RulePolicy) mcpAllowedDirsDenied(spec core.ToolSpec, call core.ToolCall) string {
	if !strings.HasPrefix(spec.Name, "mcp__") {
		return ""
	}
	allowedDirs := mcpFilesystemAllowedDirs(spec)
	if len(allowedDirs) == 0 {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return ""
	}
	for _, token := range mcpPathTokens(body) {
		token = expandHome(token)
		if !filepath.IsAbs(token) {
			continue
		}
		clean := cleanAbs(token)
		if !pathInsideAnyCanonical(clean, allowedDirs) {
			return fmt.Sprintf("MCP filesystem server cannot access %s; allowed directories: %s. Use Whale built-in file tools for this path, or add the directory to the MCP server configuration.", clean, strings.Join(allowedDirs, ", "))
		}
	}
	return ""
}

func mcpFilesystemAllowedDirs(spec core.ToolSpec) []string {
	const prefix = "mcp_filesystem_allowed_dir:"
	var out []string
	for _, cap := range spec.Capabilities {
		if dir, ok := strings.CutPrefix(strings.TrimSpace(cap), prefix); ok {
			if dir = cleanAbs(dir); dir != "" {
				out = append(out, dir)
			}
		}
	}
	return uniqueStrings(out)
}

func mcpFilesystemTool(spec core.ToolSpec) bool {
	return hasCapability(spec, "mcp_filesystem") || len(mcpFilesystemAllowedDirs(spec)) > 0
}

func pathInsideAnyCanonical(path string, roots []string) bool {
	path = canonicalAccessPath(path)
	for _, root := range roots {
		root = canonicalAccessPath(root)
		if pathInside(path, root) {
			return true
		}
	}
	return false
}

func canonicalAccessPath(path string) string {
	clean := cleanAbs(path)
	if clean == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(real)
	}
	cur := clean
	var suffix []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		if real, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{filepath.Clean(real)}, suffix...)
			return filepath.Clean(filepath.Join(parts...))
		}
		cur = parent
	}
}

func shellFileCommand(command string) bool {
	// Normalize with filepath.Base so a tool invoked by path, e.g. /bin/cat,
	// still matches and its outside-workspace operands are checked against the
	// external_directory rules.
	switch strings.ToLower(filepath.Base(strings.TrimSpace(command))) {
	case "cat", "ls", "cp", "mv", "rm", "mkdir", "rmdir", "touch", "chmod", "chown", "readlink", "realpath", "stat", "du", "head", "tail", "wc":
		return true
	default:
		return false
	}
}

func shellFileCommandPathArg(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") || strings.Contains(arg, "://") {
		return false
	}
	if strings.ContainsAny(arg, "<>$`") || strings.Contains(arg, "$(") || strings.Contains(arg, "${") {
		return false
	}
	return true
}

func shellPathArgBeforeRedirection(arg string) string {
	idx := strings.IndexAny(arg, "<>")
	if idx < 0 {
		return arg
	}
	return arg[:idx]
}

func pathInsideTrustedTemp(clean string) bool {
	if clean == "" {
		return false
	}
	if temp := cleanAbs(os.TempDir()); temp != "" && pathInside(clean, temp) {
		return true
	}
	return clean == "/tmp" || strings.HasPrefix(clean, "/tmp/") ||
		clean == "/private/tmp" || strings.HasPrefix(clean, "/private/tmp/")
}

// externalDirForToken returns the directory a shell path token should be
// evaluated against. A token that is itself a directory is matched as-is so
// explicit rules for that directory apply; otherwise its parent directory is
// used (the token names a file, existing or yet to be created).
func externalDirForToken(clean string) string {
	if info, err := os.Stat(clean); err == nil && info.IsDir() {
		return clean
	}
	return filepath.Dir(clean)
}

func (p RulePolicy) resolveShellPathToken(root, token string) string {
	token = expandHome(token)
	if filepath.IsAbs(token) {
		return cleanAbs(token)
	}
	if root == "" {
		return ""
	}
	return cleanAbs(filepath.Join(root, token))
}

func permissionKind(toolName string) string {
	switch toolName {
	case "read_file", "grep", "search_files", "list_dir", "load_skill":
		return "read"
	case "edit", "write", "apply_patch":
		return "edit"
	case "shell_run":
		return "shell"
	case "remember", "remember_update", "forget":
		return "memory"
	case "spawn_subagent":
		return "task"
	default:
		if strings.HasPrefix(toolName, "mcp__") {
			return "mcp"
		}
		return toolName
	}
}

// readScopeTarget returns the directory a read-scoped search tool operates on,
// ignoring regex/glob query fields. It falls back to "*" when no search root
// is given (the tool searches the whole workspace).
func readScopeTarget(call core.ToolCall) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
		for _, key := range []string{"file_path", "path", "dir", "directory"} {
			if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return "*"
}

func permissionTarget(call core.ToolCall) string {
	if strings.HasPrefix(call.Name, "mcp__") {
		return call.Name
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
		for _, key := range []string{"file_path", "path", "pattern", "url", "query", "name"} {
			if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		if call.Name == "apply_patch" {
			if v, ok := body["patch"].(string); ok && strings.TrimSpace(v) != "" {
				return "apply_patch:" + hashString(v)
			}
		}
	}
	if strings.TrimSpace(call.Input) != "" {
		return strings.TrimSpace(call.Input)
	}
	return "*"
}

func RulesFromMap(permission string, rules map[string]string) ([]PermissionRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	// Rules are evaluated last-match-wins, so order keys least-specific first.
	// Specificity is approximated by literal (non-wildcard) character count, so
	// a narrow pattern like "git push --force*" outranks a broad "git push*"
	// regardless of how the unordered TOML map was written. Ties fall back to
	// lexical order for determinism.
	sort.SliceStable(keys, func(i, j int) bool {
		li, lj := literalLen(keys[i]), literalLen(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	out := make([]PermissionRule, 0, len(keys))
	for _, pattern := range keys {
		action, err := ParsePermissionAction(rules[pattern])
		if err != nil {
			return nil, err
		}
		out = append(out, PermissionRule{Permission: permission, Pattern: expandHome(pattern), Action: action})
	}
	return out, nil
}

func RulesFromConfig(config PermissionConfig) ([]PermissionRule, error) {
	tables := []struct {
		name  string
		rules map[string]string
	}{
		{name: "read", rules: config.Read},
		{name: "edit", rules: config.Edit},
		{name: "shell", rules: config.Shell},
		{name: "external_directory", rules: config.ExternalDirectory},
		{name: "mcp", rules: config.MCP},
		{name: "memory", rules: config.Memory},
		{name: "task", rules: config.Task},
		{name: "mutating_tool", rules: config.MutatingTool},
	}
	var out []PermissionRule
	for _, table := range tables {
		rules, err := RulesFromMap(table.name, table.rules)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", table.name, err)
		}
		out = append(out, rules...)
	}
	return out, nil
}

// literalLen counts the characters of a glob pattern that are not wildcards,
// used as a specificity heuristic when ordering rules.
func literalLen(pattern string) int {
	n := 0
	for _, r := range pattern {
		if r != '*' && r != '?' {
			n++
		}
	}
	return n
}

// wildcardReCache memoizes compiled glob patterns. wildcardMatch is called once
// per rule per tool call, so caching avoids recompiling the same patterns.
var wildcardReCache sync.Map // string -> *regexp.Regexp (nil for invalid patterns)

func wildcardMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == "" {
		return value == ""
	}
	if pattern == "*" {
		return true
	}
	re := compileWildcard(pattern)
	if re == nil {
		return false
	}
	return re.MatchString(value)
}

func compileWildcard(pattern string) *regexp.Regexp {
	if cached, ok := wildcardReCache.Load(pattern); ok {
		return cached.(*regexp.Regexp)
	}
	expr := regexp.QuoteMeta(pattern)
	expr = strings.ReplaceAll(expr, `\*`, ".*")
	expr = strings.ReplaceAll(expr, `\?`, ".")
	re, err := regexp.Compile("(?i)^" + expr + "$")
	if err != nil {
		re = nil
	}
	wildcardReCache.Store(pattern, re)
	return re
}

func ruleLabel(rule PermissionRule) string {
	return strings.TrimSpace(rule.Permission + ":" + rule.Pattern + "=" + string(rule.Action))
}

func shellCommandFromInput(input string) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return ""
	}
	cmd, _ := body["command"].(string)
	return strings.TrimSpace(cmd)
}

func shellCommandEligibleForScopedAllow(command string) bool {
	if command == "" {
		return false
	}
	return !strings.ContainsAny(command, "\n\r;&|<>$`(){}#")
}

func scopedAllowCommandAllowed(command, rule string) bool {
	rule = normalizeCommandPrefix(rule)
	if !strings.HasPrefix(rule, "gh pr ") {
		return true
	}
	return ghPRScopedAllowCommandAllowed(command, rule)
}

func ghPRScopedAllowCommandAllowed(command, rule string) bool {
	argv := strings.Fields(command)
	if len(argv) < 3 || strings.ToLower(argv[0]) != "gh" || strings.ToLower(argv[1]) != "pr" {
		return false
	}
	for _, arg := range argv[3:] {
		if arg == "--web" || arg == "-w" {
			return false
		}
	}
	switch rule {
	case "gh pr view":
		return ghPRViewScopedAllowArgs(argv[3:])
	case "gh pr list":
		return ghPRListScopedAllowArgs(argv[3:])
	case "gh pr diff":
		return ghPRDiffScopedAllowArgs(argv[3:])
	default:
		return false
	}
}

func ghPRViewScopedAllowArgs(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--comments":
			continue
		case "--json":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || strings.TrimSpace(args[i+1]) == "" {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}

func ghPRListScopedAllowArgs(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "--json", "--state", "--author", "--base", "--head", "--label", "--search":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || strings.TrimSpace(args[i+1]) == "" {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}

func ghPRDiffScopedAllowArgs(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for _, arg := range args[1:] {
		switch arg {
		case "--patch", "--name-only":
			continue
		default:
			if strings.HasPrefix(arg, "--color=") {
				continue
			}
			return false
		}
	}
	return true
}

func hasAllowCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(command, "\n\r") || strings.ContainsAny(rule, "\n\r") {
		return false
	}
	return hasSingleLineCommandPrefix(command, rule)
}

func hasSingleLineCommandPrefix(command, rule string) bool {
	command = normalizeCommandPrefix(command)
	rule = normalizeCommandPrefix(rule)
	if command == "" || rule == "" {
		return false
	}
	return command == rule || strings.HasPrefix(command, rule+" ")
}

func normalizeCommandPrefix(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(v))), " ")
}

func hashString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%x", sum[:8])
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	if strings.HasPrefix(path, "$HOME/") {
		return filepath.Join(home, strings.TrimPrefix(path, "$HOME/"))
	}
	if path == "$HOME" {
		return home
	}
	return path
}

func cleanAbs(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func pathInside(path, root string) bool {
	path = cleanAbs(path)
	root = cleanAbs(root)
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
