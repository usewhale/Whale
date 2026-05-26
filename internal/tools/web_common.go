package tools

import (
	"regexp"
	"strings"
)

var (
	reTag       = regexp.MustCompile(`(?is)<[^>]+>`)
	reScript    = regexp.MustCompile(`(?is)<script[\s\S]*?</script>`)
	reScriptTag = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	reStyle     = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`)
	reNoScript  = regexp.MustCompile(`(?is)<noscript[\s\S]*?</noscript>`)
	reNav       = regexp.MustCompile(`(?is)<nav[\s\S]*?</nav>`)
	reFooter    = regexp.MustCompile(`(?is)<footer[\s\S]*?</footer>`)
	reAside     = regexp.MustCompile(`(?is)<aside[\s\S]*?</aside>`)
	reSvg       = regexp.MustCompile(`(?is)<svg[\s\S]*?</svg>`)
	reBlockTags = regexp.MustCompile(`(?is)</?(p|div|br|h[1-6]|li|tr|section|article)\b[^>]*>`)
	reTitle     = regexp.MustCompile(`(?is)<title[^>]*>([\s\S]*?)</title>`)
)

type webFetchFormat string

const (
	webFetchFormatText     webFetchFormat = "text"
	webFetchFormatMarkdown webFetchFormat = "markdown"
	webFetchFormatHTML     webFetchFormat = "html"
)

type lowContentDiagnostic struct {
	LowContent bool
	Reason     string
	NextSteps  string
}

func parseWebFetchFormat(raw string, defaultFormat webFetchFormat) (webFetchFormat, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return defaultFormat, true
	case "text", "txt", "plain":
		return webFetchFormatText, true
	case "markdown", "md":
		return webFetchFormatMarkdown, true
	case "html", "raw", "bytes":
		return webFetchFormatHTML, true
	default:
		return "", false
	}
}

func decodeHTMLBasic(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", `'`)
	return s
}

func normalizeHTMLText(s string) string {
	r := reTag.ReplaceAllString(s, "")
	r = decodeHTMLBasic(r)
	r = strings.Join(strings.Fields(r), " ")
	return strings.TrimSpace(r)
}

func htmlToText(html string) string {
	s := html
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reNoScript.ReplaceAllString(s, "")
	s = reNav.ReplaceAllString(s, "")
	s = reFooter.ReplaceAllString(s, "")
	s = reAside.ReplaceAllString(s, "")
	s = reSvg.ReplaceAllString(s, "")
	s = reBlockTags.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = decodeHTMLBasic(s)
	s = regexp.MustCompile(`[ \t]+`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`\n[ \t]+`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func extractHTMLTitle(html string) string {
	m := reTitle.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Join(strings.Fields(decodeHTMLBasic(m[1])), " "))
}

func detectLowWebContent(url, title, html, text string) lowContentDiagnostic {
	trimmedText := strings.TrimSpace(text)
	normalizedText := strings.ToLower(strings.Join(strings.Fields(trimmedText), " "))
	normalizedTitle := strings.ToLower(strings.Join(strings.Fields(title), " "))
	lowerHTML := strings.ToLower(html)
	scriptCount := len(reScriptTag.FindAllStringIndex(html, -1))

	hasSPARoot := strings.Contains(lowerHTML, "<app-root") ||
		strings.Contains(lowerHTML, `id="root"`) ||
		strings.Contains(lowerHTML, `id='root'`) ||
		strings.Contains(lowerHTML, `id="app"`) ||
		strings.Contains(lowerHTML, `id='app'`) ||
		strings.Contains(lowerHTML, "__next_data__") ||
		strings.Contains(lowerHTML, "type=\"module\"")

	titleOnly := normalizedTitle != "" &&
		(normalizedText == normalizedTitle || normalizedText == "title "+normalizedTitle)
	veryShort := len(trimmedText) > 0 && len(trimmedText) < 160
	emptyReadable := trimmedText == ""
	scriptHeavy := scriptCount >= 3 && len(trimmedText) < 400

	switch {
	case hasSPARoot && (veryShort || emptyReadable):
		return lowWebContentResult(url, "page looks like a JavaScript-rendered shell with little readable text")
	case titleOnly && (hasSPARoot || scriptHeavy):
		return lowWebContentResult(url, "fetch returned mostly the page title")
	case emptyReadable && scriptCount > 0:
		return lowWebContentResult(url, "fetch returned no readable text from a script-driven page")
	case scriptHeavy && veryShort:
		return lowWebContentResult(url, "page is script-heavy and returned very little readable text")
	default:
		return lowContentDiagnostic{}
	}
}

func lowWebContentResult(url, reason string) lowContentDiagnostic {
	return lowContentDiagnostic{
		LowContent: true,
		Reason:     reason,
		NextSteps:  "The URL may be JavaScript-rendered, authenticated, or only visible through search snippets. Use web_search with the exact URL, a site: query for the host/path, and related official keywords before concluding the page is unavailable. URL: " + url,
	}
}
