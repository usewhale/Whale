package effects

import (
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type Kind string

const (
	ExternalDirectory Kind = "external_directory"
	ReadPath          Kind = "read_path"
	WritePath         Kind = "write_path"
	ShellExec         Kind = "shell_exec"
)

type Risk string

const (
	RiskSafeRead     Risk = "safe_read"
	RiskBoundedWrite Risk = "bounded_write"
	RiskUnknown      Risk = "unknown"
)

type Effect struct {
	Kind     Kind
	Scope    string
	Risk     Risk
	Metadata map[string]string
}

type Plan struct {
	Effects []Effect
	Risk    Risk
}

type Grant struct {
	Kind    Kind
	Pattern string
	Action  string
}

func ExternalDirectoryEffect(path string) Effect {
	return Effect{Kind: ExternalDirectory, Scope: CleanScope(path), Risk: RiskSafeRead}
}

func ReadPathEffect(path string) Effect {
	return Effect{Kind: ReadPath, Scope: CleanScope(path), Risk: RiskSafeRead}
}

func WritePathEffect(path string) Effect {
	return Effect{Kind: WritePath, Scope: CleanScope(path), Risk: RiskBoundedWrite}
}

func ShellExecEffect(command string) Effect {
	return Effect{Kind: ShellExec, Scope: strings.TrimSpace(command), Risk: RiskUnknown}
}

func GrantKey(kind Kind, pattern string) string {
	pattern = CleanScope(pattern)
	if pattern == "" {
		return ""
	}
	return "grant:" + string(kind) + ":" + filepath.ToSlash(pattern)
}

func ParseGrantKey(key string) (Grant, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(key), "grant:")
	if !ok {
		return Grant{}, false
	}
	kind, pattern, ok := strings.Cut(rest, ":")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(pattern) == "" {
		return Grant{}, false
	}
	return Grant{
		Kind:    Kind(kind),
		Pattern: CleanScope(filepath.FromSlash(pattern)),
		Action:  "allow",
	}, true
}

func GrantAllowsKey(grantedKey, requestedKey string) bool {
	if strings.TrimSpace(grantedKey) == strings.TrimSpace(requestedKey) {
		return true
	}
	granted, ok := ParseGrantKey(grantedKey)
	if !ok {
		return false
	}
	requested, ok := ParseGrantKey(requestedKey)
	if !ok || granted.Kind != requested.Kind {
		return false
	}
	switch granted.Kind {
	case ExternalDirectory:
		return pathInsideOrEqual(requested.Pattern, granted.Pattern)
	default:
		return granted.Pattern == requested.Pattern
	}
}

func CleanScope(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(path))
}

func pathInsideOrEqual(path, root string) bool {
	path = CleanScope(path)
	root = CleanScope(root)
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	ok, err := core.PathInside(path, root)
	return err == nil && ok
}
