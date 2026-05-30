package policy

import (
	"regexp"
	"strings"
	"sync"
)

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
