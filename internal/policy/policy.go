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
	"github.com/usewhale/whale/internal/policy/shellrisk"
)

type PermissionAction string

const (
	PermissionAllow PermissionAction = "allow"
	PermissionAsk   PermissionAction = "ask"
	PermissionDeny  PermissionAction = "deny"
)

type ApprovalMode string

const (
	ApprovalModeOnRequest ApprovalMode = "on-request"
	ApprovalModeNever     ApprovalMode = "never"
)

func ParseApprovalMode(v string) (ApprovalMode, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "on-request", "on_request":
		return ApprovalModeOnRequest, nil
	case "never", "never-ask", "never_ask":
		return ApprovalModeNever, nil
	default:
		return "", fmt.Errorf("invalid approval mode: %s", v)
	}
}

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

type RulePolicy struct {
	Default       PermissionAction
	Rules         []PermissionRule
	WorkspaceRoot string
}

type DefaultToolPolicy struct {
	Default       PermissionAction
	Rules         []PermissionRule
	WorkspaceRoot string

	Mode          ApprovalMode
	AllowPrefixes []string
	DenyPrefixes  []string
}

type PolicyDecision struct {
	Allow            bool
	RequiresApproval bool
	Reason           string
	Code             string
	Phase            string
	MatchedRule      string
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
	if spec.Name == "shell_run" && decision.Code == "scoped_allow_prefix" && !decision.RequiresApproval {
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
	return []PermissionRule{
		{Permission: "read", Pattern: "*", Action: PermissionAllow},
		{Permission: "read", Pattern: "*.env", Action: PermissionAsk},
		{Permission: "read", Pattern: "*.env.*", Action: PermissionAsk},
		{Permission: "read", Pattern: "*.env.example", Action: PermissionAllow},
		{Permission: "edit", Pattern: "*", Action: PermissionAsk},
		{Permission: "shell", Pattern: "*", Action: PermissionAllow},
		{Permission: "shell", Pattern: "curl *", Action: PermissionAsk},
		{Permission: "shell", Pattern: "wget *", Action: PermissionAsk},
		{Permission: "shell", Pattern: "npm install*", Action: PermissionAsk},
		{Permission: "shell", Pattern: "pnpm install*", Action: PermissionAsk},
		{Permission: "shell", Pattern: "yarn add*", Action: PermissionAsk},
		{Permission: "shell", Pattern: "git push*", Action: PermissionAsk},
		{Permission: "shell", Pattern: "gh pr merge*", Action: PermissionAsk},
		{Permission: "shell", Pattern: "rm -rf*", Action: PermissionDeny},
		{Permission: "external_directory", Pattern: "*", Action: PermissionAsk},
		{Permission: "mcp", Pattern: "*", Action: PermissionAsk},
		{Permission: "memory", Pattern: "*", Action: PermissionAsk},
	}
}

func (p RulePolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	requests := p.requestsFor(spec, call)
	if len(requests) == 0 {
		requests = []permissionRequest{{Kind: permissionKind(spec.Name), Pattern: permissionTarget(call)}}
	}
	var ask *PermissionRule
	var pending *PolicyDecision
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
			}
		case PermissionAsk:
			copy := rule
			ask = &copy
		case PermissionAllow:
			if req.Kind == "shell" && shellWildcardAllow(rule) {
				decision := shellrisk.Classify(req.Pattern)
				if decision.Allow && decision.Level == shellrisk.LevelSafeRead {
					continue
				}
				// Defer the risk-classifier decision instead of returning
				// immediately so later requests (e.g. external_directory)
				// can still surface a deny that must take precedence.
				code := "approval_required"
				if decision.Level == shellrisk.LevelBoundedWrite {
					code = shellrisk.CodeBoundedWrite
				}
				pending = &PolicyDecision{Allow: true, RequiresApproval: true, Reason: decision.Reason, Code: code, Phase: "needs_approval", MatchedRule: ruleLabel(rule)}
			}
		}
	}
	if pending != nil {
		return *pending
	}
	if ask != nil {
		return PolicyDecision{
			Allow:            true,
			RequiresApproval: true,
			Reason:           "permission rule requires approval",
			Code:             "permission_required",
			Phase:            "needs_approval",
			MatchedRule:      ruleLabel(*ask),
		}
	}
	return PolicyDecision{Allow: true, Code: "permission_allow", Phase: "allowed"}
}

func shellWildcardAllow(rule PermissionRule) bool {
	return strings.TrimSpace(rule.Pattern) == "*"
}

func (p DefaultToolPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	if p.Mode != "" || (p.Default == "" && len(p.Rules) == 0 && strings.TrimSpace(p.WorkspaceRoot) == "") {
		return p.legacyDecide(spec, call)
	}
	rules := append([]PermissionRule{}, DefaultRules()...)
	rules = append(rules, p.Rules...)
	for _, prefix := range p.AllowPrefixes {
		if strings.TrimSpace(prefix) != "" {
			rules = append(rules, PermissionRule{Permission: "shell", Pattern: strings.TrimSpace(prefix) + "*", Action: PermissionAllow})
		}
	}
	for _, prefix := range p.DenyPrefixes {
		if strings.TrimSpace(prefix) != "" {
			rules = append(rules, PermissionRule{Permission: "shell", Pattern: strings.TrimSpace(prefix) + "*", Action: PermissionDeny})
		}
	}
	def := p.Default
	if def == "" {
		def = PermissionAllow
	}
	if p.Mode == ApprovalModeNever {
		def = PermissionAllow
	}
	return RulePolicy{Default: def, Rules: rules, WorkspaceRoot: p.WorkspaceRoot}.Decide(spec, call)
}

func (p DefaultToolPolicy) legacyDecide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	if p.Mode == "" {
		p.Mode = ApprovalModeOnRequest
	}
	if spec.Name == "shell_run" {
		cmd := shellCommandFromInput(call.Input)
		for _, deny := range p.DenyPrefixes {
			if hasDenyCommandPrefix(cmd, deny) {
				return PolicyDecision{Allow: false, Reason: "command blocked by deny prefix", Code: "policy_denied", Phase: "denied", MatchedRule: deny}
			}
		}
		for _, allow := range p.AllowPrefixes {
			if hasAllowCommandPrefix(cmd, allow) {
				return PolicyDecision{Allow: true, RequiresApproval: false, Code: "allow_prefix", Phase: "allowed", MatchedRule: allow}
			}
		}
	}
	if p.Mode == ApprovalModeNever {
		return PolicyDecision{Allow: true, Code: "auto_allow", Phase: "allowed"}
	}
	if core.IsReadOnlyToolCall(spec, call) {
		return PolicyDecision{Allow: true, Code: "read_only", Phase: "allowed"}
	}
	if spec.Name == "shell_run" {
		decision := shellrisk.Classify(shellCommandFromInput(call.Input))
		if decision.Allow {
			return PolicyDecision{Allow: true, Code: "shell_auto_allow", Phase: "allowed"}
		}
		if decision.Level == shellrisk.LevelBoundedWrite {
			return PolicyDecision{Allow: true, RequiresApproval: true, Reason: decision.Reason, Code: shellrisk.CodeBoundedWrite, Phase: "needs_approval"}
		}
	}
	if hasCapability(spec, "mutates_state") {
		return PolicyDecision{Allow: true, RequiresApproval: true, Reason: "tool mutates persistent state", Code: "approval_required", Phase: "needs_approval"}
	}
	switch spec.Name {
	case "edit", "write", "apply_patch", "shell_run":
	default:
		if strings.HasPrefix(spec.Name, "mcp__") {
			return PolicyDecision{Allow: true, RequiresApproval: true, Reason: "MCP tool requires approval", Code: "approval_required", Phase: "needs_approval"}
		}
		return PolicyDecision{Allow: true, Code: "non_mutating_default", Phase: "allowed"}
	}
	return PolicyDecision{Allow: true, RequiresApproval: true, Reason: "tool requires approval", Code: "approval_required", Phase: "needs_approval"}
}

type permissionRequest struct {
	Kind    string
	Pattern string
}

func (p RulePolicy) requestsFor(spec core.ToolSpec, call core.ToolCall) []permissionRequest {
	kind := permissionKind(spec.Name)
	target := permissionTarget(call)
	requests := []permissionRequest{{Kind: kind, Pattern: target}}
	switch spec.Name {
	case "shell_run":
		cmd := shellCommandFromInput(call.Input)
		requests[0].Pattern = cmd
		for _, dir := range p.externalDirs(cmd) {
			requests = append(requests, permissionRequest{Kind: "external_directory", Pattern: dir})
		}
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
	normalizeShell := permission == "shell"
	var shellSegments []string
	if normalizeShell {
		// Shell commands are matched line by line. Newlines are kept as
		// segment boundaries rather than collapsed into spaces so a benign
		// prefix on the first line cannot carry an allow rule across a
		// newline onto a second command, and a deny rule on a later line is
		// still caught. Intra-line whitespace is still collapsed so legacy
		// prefixes match regardless of spacing ("rm   -rf x" -> "rm -rf x").
		shellSegments = normalizeShellSegments(pattern)
	}
	for i := len(p.Rules) - 1; i >= 0; i-- {
		rule := p.Rules[i]
		if rule.Action == "" {
			continue
		}
		if !wildcardMatch(rule.Permission, permission) {
			continue
		}
		if normalizeShell {
			if shellRuleMatches(rule, shellSegments) {
				return rule
			}
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return rule
		}
	}
	return fallback
}

// normalizeShellWhitespace collapses runs of intra-line whitespace to single
// spaces, matching the legacy command-prefix normalization.
func normalizeShellWhitespace(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// normalizeShellSegments splits a shell command on newline boundaries and
// returns one whitespace-normalized segment per non-empty line. Splitting
// before normalizing keeps newlines from being folded into spaces, so rule
// matching cannot span two commands written on separate lines. An empty or
// whitespace-only command yields a single empty segment.
func normalizeShellSegments(command string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if seg := normalizeShellWhitespace(line); seg != "" {
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// shellRuleMatches reports whether a shell rule applies to a command already
// split into normalized newline segments. The "*" rule matches any command. A
// non-"*" allow rule never matches a multi-line command, mirroring the legacy
// allow_shell_prefixes behavior that rejected commands containing newlines;
// ask and deny rules match when any single line matches, so a dangerous
// command smuggled onto a later line is still caught.
func shellRuleMatches(rule PermissionRule, segments []string) bool {
	pattern := normalizeShellWhitespace(rule.Pattern)
	if pattern == "*" {
		return true
	}
	if rule.Action == PermissionAllow && len(segments) > 1 {
		return false
	}
	for _, seg := range segments {
		if wildcardMatch(pattern, seg) {
			return true
		}
	}
	return false
}

func (p RulePolicy) externalDirs(command string) []string {
	root := cleanAbs(p.WorkspaceRoot)
	if command == "" || root == "" {
		return nil
	}
	var out []string
	for _, token := range strings.Fields(command) {
		token = strings.Trim(token, `"'`)
		// Split on shell redirection operators so a path glued to a
		// redirection without a separating space (e.g. ">/etc/out",
		// "2>>/var/log/x", "<~/secret", "cat foo>/etc/x") is still scanned
		// instead of being mistaken for a workspace-relative token.
		for _, frag := range strings.FieldsFunc(token, isRedirectionRune) {
			frag = strings.Trim(frag, `"'`)
			if frag == "" {
				continue
			}
			// A flag token still carries a path in its value (e.g.
			// "-coverprofile=/etc/out", "--output=/etc/out"); extract the
			// value before discarding the option itself.
			if strings.HasPrefix(frag, "-") {
				idx := strings.IndexByte(frag, '=')
				if idx < 0 {
					continue
				}
				frag = strings.Trim(frag[idx+1:], `"'`)
			}
			if frag == "" || strings.HasPrefix(frag, "-") || strings.Contains(frag, "://") {
				continue
			}
			clean := p.resolveShellPathToken(root, frag)
			if clean == "" || pathInside(clean, root) || strings.HasPrefix(clean, "/tmp/") || strings.HasPrefix(clean, "/private/tmp/") {
				continue
			}
			out = append(out, externalDirForToken(clean))
		}
	}
	return uniqueStrings(out)
}

// isRedirectionRune reports whether r is a shell redirection operator
// character. In unquoted command text these characters always introduce a
// redirection (or process substitution), so splitting on them isolates the
// redirected path even when no whitespace separates it from the operator.
func isRedirectionRune(r rune) bool {
	return r == '<' || r == '>'
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

func hasDenyCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(rule, "\n\r") {
		return false
	}
	for _, segment := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if hasSingleLineCommandPrefix(segment, rule) {
			return true
		}
	}
	return false
}

func hasSingleLineCommandPrefix(command, rule string) bool {
	command = normalizeCommandPrefix(command)
	rule = normalizeCommandPrefix(rule)
	if command == "" || rule == "" {
		return false
	}
	return command == rule || strings.HasPrefix(command, rule+" ")
}

func hasCapability(spec core.ToolSpec, capability string) bool {
	want := strings.TrimSpace(strings.ToLower(capability))
	if want == "" {
		return false
	}
	for _, got := range spec.Capabilities {
		if strings.TrimSpace(strings.ToLower(got)) == want {
			return true
		}
	}
	return false
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
