package mcp

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const (
	toolPrefix        = "mcp__"
	toolDelimiter     = "__"
	maxToolNameLength = 64
)

func QualifyToolName(server, tool string) string {
	raw := fmt.Sprintf("%s%s%s%s", toolPrefix, sanitizeComponent(server, "server"), toolDelimiter, sanitizeComponent(tool, "tool"))
	if len(raw) <= maxToolNameLength {
		return raw
	}
	sum := shortHash(raw)
	prefixLen := maxToolNameLength - len(sum) - 1
	if prefixLen < len(toolPrefix)+1 {
		prefixLen = len(toolPrefix) + 1
	}
	return strings.TrimRight(raw[:prefixLen], "_") + "_" + sum
}

func ParseQualifiedToolName(name string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(name, toolPrefix)
	if !found {
		return "", "", false
	}
	server, tool, found = strings.Cut(rest, toolDelimiter)
	if !found || server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

func NormalizeServerNameForToolName(server string) string {
	return sanitizeComponent(server, "server")
}

func UniqueToolName(base string, seen map[string]bool) string {
	if !seen[base] {
		seen[base] = true
		return base
	}
	sum := shortHash(base)
	candidate := base
	if len(candidate)+1+len(sum) > maxToolNameLength {
		candidate = strings.TrimRight(candidate[:maxToolNameLength-len(sum)-1], "_")
	}
	candidate = candidate + "_" + sum
	for i := 2; seen[candidate]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		limit := maxToolNameLength - len(suffix)
		if limit < 1 {
			limit = maxToolNameLength
		}
		candidate = strings.TrimRight(base[:min(len(base), limit)], "_") + suffix
	}
	seen[candidate] = true
	return candidate
}

func sanitizeComponent(value, fallback string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	prevUnderscore := false
	for _, r := range value {
		valid := r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
		if !valid {
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
			continue
		}
		b.WriteRune(r)
		prevUnderscore = false
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return fallback
	}
	return out
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:10]
}
