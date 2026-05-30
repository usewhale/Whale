package tools

import (
	"html"
	"regexp"
	"strings"
)

var reHTMLTag = regexp.MustCompile(`(?is)<[^>]+>`)

func normalizeHTMLText(s string) string {
	r := reHTMLTag.ReplaceAllString(s, "")
	r = html.UnescapeString(r)
	r = strings.Join(strings.Fields(r), " ")
	return strings.TrimSpace(r)
}
