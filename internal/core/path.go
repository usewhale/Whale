package core

import (
	"os"
	"path/filepath"
	"strings"
)

// PathInside reports whether path is inside parent (or equal to parent),
// resolving symlinks through the nearest existing ancestor for accurate
// boundary checking even when the final path does not exist yet.
func PathInside(path, parent string) (bool, error) {
	path = strings.TrimSpace(path)
	parent = strings.TrimSpace(parent)
	if path == "" || parent == "" {
		return false, nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return false, err
	}
	absPath = canonicalAccessPath(absPath)
	absParent = canonicalAccessPath(absParent)
	rel, err := filepath.Rel(absParent, absPath)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))), nil
}

func canonicalAccessPath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}
	current := clean
	var suffix []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return clean
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{filepath.Clean(resolved)}, suffix...)
			return filepath.Clean(filepath.Join(parts...))
		}
		current = parent
	}
}
