package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
)

func (p RulePolicy) externalDirs(command string) []string {
	root := cleanAbs(p.WorkspaceRoot)
	if command == "" || root == "" {
		return nil
	}
	projectRoots := p.projectRoots()
	var out []string
	for _, segment := range expandShellRuleSegments(command) {
		for _, token := range shellRedirectionPathTokens(segment) {
			if token == dynamicShellRedirectionTarget {
				out = append(out, dynamicShellRedirectionTarget)
				continue
			}
			clean := p.resolveShellPathToken(root, token)
			if clean == "" || pathInsideAny(clean, projectRoots) || pathInsideTrustedShellPath(clean) {
				continue
			}
			out = append(out, externalDirForToken(clean))
		}
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
			if clean == "" || pathInsideAny(clean, projectRoots) || pathInsideTrustedShellPath(clean) {
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
	projectRoots := p.projectRoots()
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return nil
	}
	var out []string
	for _, token := range mcpPathTokens(body) {
		clean := p.resolveShellPathToken(root, token)
		if clean == "" || pathInsideAny(clean, projectRoots) || pathInsideTrustedTemp(clean) {
			continue
		}
		out = append(out, externalDirForToken(clean))
	}
	return uniqueStrings(out)
}
func (p RulePolicy) externalDirsFromReadInput(call core.ToolCall) []string {
	root := cleanAbs(p.WorkspaceRoot)
	if root == "" {
		return nil
	}
	target := readScopeTarget(call)
	if target == "*" {
		return nil
	}
	clean := p.resolveShellPathToken(root, target)
	if clean == "" || pathInsideAny(clean, p.projectRoots()) || p.isDiscoveredSkillReadPath(clean) {
		return nil
	}
	return []string{externalDirForToken(clean)}
}
func (p RulePolicy) isDiscoveredSkillReadPath(target string) bool {
	root := cleanAbs(p.WorkspaceRoot)
	if root == "" || strings.TrimSpace(target) == "" {
		return false
	}
	targetReal := canonicalAccessPath(target)
	if targetReal == "" {
		return false
	}
	for _, skill := range skills.Discover(skills.DefaultRoots(root)) {
		if skill == nil || strings.TrimSpace(skill.Path) == "" {
			continue
		}
		dirReal := canonicalAccessPath(skill.Path)
		if dirReal != "" && pathInsideOrFalse(targetReal, dirReal) {
			return true
		}
	}
	return false
}
func (p RulePolicy) projectRoots() []string {
	roots := []string{}
	if root := cleanAbs(p.WorkspaceRoot); root != "" {
		roots = append(roots, root)
	}
	if root := cleanAbs(p.WorktreeRoot); root != "" {
		roots = append(roots, root)
	}
	return uniqueStrings(roots)
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
		if pathInsideOrFalse(path, root) {
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
func shellRedirectionPathTokens(segment string) []string {
	var out []string
	runes := []rune(segment)
	var quote rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '\\' {
			i++
			continue
		}
		if r != '>' && r != '<' && !(r == '&' && i+1 < len(runes) && runes[i+1] == '>') {
			continue
		}
		if r == '<' && i+1 < len(runes) && runes[i+1] == '<' {
			for i+1 < len(runes) && runes[i+1] == '<' {
				i++
			}
			continue
		}
		if token, next := shellRedirectionTargetAfter(runes, i); token != "" {
			out = append(out, token)
			i = next
		}
	}
	return uniqueStrings(out)
}
func shellRedirectionTargetAfter(runes []rune, op int) (string, int) {
	i := op
	if runes[i] == '&' && i+1 < len(runes) && runes[i+1] == '>' {
		i += 2
	} else {
		i++
		for i < len(runes) && (runes[i] == '>' || runes[i] == '|' || runes[i] == '&') {
			i++
		}
	}
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	var b strings.Builder
	var quote rune
	for ; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == ';' || r == '|' || r == '&' {
			break
		}
		b.WriteRune(r)
	}
	return cleanShellRedirectionPathToken(b.String()), i
}
func shellRedirectionOperatorNeedsPath(field string) bool {
	if strings.HasPrefix(field, "<<") || strings.HasSuffix(field, "<<") {
		return false
	}
	switch field {
	case ">", ">>", "<", ">|", "&>", "&>>":
		return true
	}
	if len(field) >= 2 && allDigits(field[:len(field)-1]) && (strings.HasSuffix(field, ">") || strings.HasSuffix(field, "<")) {
		return true
	}
	if len(field) >= 3 && allDigits(field[:len(field)-2]) && strings.HasSuffix(field, ">>") {
		return true
	}
	return false
}
func attachedShellRedirectionPath(field string) string {
	if strings.Contains(field, "<<") {
		return ""
	}
	for i, r := range field {
		if r != '>' && r != '<' {
			continue
		}
		j := i + 1
		for j < len(field) && (field[j] == '>' || field[j] == '|' || field[j] == '&') {
			j++
		}
		return cleanShellRedirectionPathToken(field[j:])
	}
	return ""
}
func cleanShellRedirectionPathToken(token string) string {
	token = strings.Trim(strings.TrimSpace(token), `"'`)
	if token == "" || strings.HasPrefix(token, "&") || strings.Contains(token, "://") {
		return ""
	}
	if shellPathTokenIsDynamic(token) {
		return dynamicShellRedirectionTarget
	}
	return token
}
func shellPathTokenIsDynamic(token string) bool {
	return strings.ContainsAny(token, "<>$`") || strings.Contains(token, "$(") || strings.Contains(token, "${") || strings.HasPrefix(token, "~")
}
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func pathInsideTrustedTemp(clean string) bool {
	if clean == "" {
		return false
	}
	if temp := cleanAbs(os.TempDir()); temp != "" && pathInsideOrFalse(clean, temp) {
		return true
	}
	return clean == "/tmp" || strings.HasPrefix(clean, "/tmp/") ||
		clean == "/private/tmp" || strings.HasPrefix(clean, "/private/tmp/")
}
func pathInsideTrustedShellPath(clean string) bool {
	return filepath.Clean(clean) == "/dev/null"
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
func pathInsideOrFalse(path, root string) bool {
	ok, err := core.PathInside(path, root)
	if err != nil {
		return false
	}
	return ok
}
func pathInsideAny(path string, roots []string) bool {
	for _, root := range roots {
		if pathInsideOrFalse(path, root) {
			return true
		}
	}
	return false
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
