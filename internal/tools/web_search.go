package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
)

const (
	defaultWebSearchMaxResults = 5
	maxWebSearchResults        = 10
	maxWebSearchTimeoutMS      = 60000
	defaultWebSearchTimeoutMS  = 15000
	webSearchUserAgent         = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
)

type webSearchEntry struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func (b *Toolset) webSearch(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in webSearchInput
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	query := webSearchQuery(in)
	if query == "" {
		return marshalToolError(call, "invalid_args", "query is required"), nil
	}
	maxResults := normalizeWebSearchMaxResults(in)
	timeoutMS := normalizeWebSearchTimeoutMS(in.TimeoutMS)
	results, source, note, err := b.searchWithFallback(ctx, query, maxResults, timeoutMS)
	if err != nil {
		return marshalToolError(call, "web_search_failed", err.Error()), nil
	}
	return marshalToolResult(call, buildWebSearchResult(query, source, note, results))
}

type webSearchInput struct {
	Query       string `json:"query"`
	Q           string `json:"q"`
	MaxResults  int    `json:"max_results"`
	TimeoutMS   int    `json:"timeout_ms"`
	SearchQuery []struct {
		Q          string `json:"q"`
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	} `json:"search_query"`
}

func webSearchQuery(in webSearchInput) string {
	if query := strings.TrimSpace(in.Query); query != "" {
		return query
	}
	if query := strings.TrimSpace(in.Q); query != "" {
		return query
	}
	for _, sq := range in.SearchQuery {
		if query := strings.TrimSpace(sq.Q); query != "" {
			return query
		}
		if query := strings.TrimSpace(sq.Query); query != "" {
			return query
		}
	}
	return ""
}

func normalizeWebSearchMaxResults(in webSearchInput) int {
	maxResults := in.MaxResults
	if maxResults <= 0 && len(in.SearchQuery) > 0 && in.SearchQuery[0].MaxResults > 0 {
		maxResults = in.SearchQuery[0].MaxResults
	}
	if maxResults <= 0 {
		maxResults = defaultWebSearchMaxResults
	}
	if maxResults > maxWebSearchResults {
		maxResults = maxWebSearchResults
	}
	return maxResults
}

func normalizeWebSearchTimeoutMS(timeoutMS int) int {
	if timeoutMS <= 0 {
		timeoutMS = defaultWebSearchTimeoutMS
	}
	if timeoutMS > maxWebSearchTimeoutMS {
		timeoutMS = maxWebSearchTimeoutMS
	}
	return timeoutMS
}

func buildWebSearchResult(query, source, note string, results []webSearchEntry) map[string]any {
	message := fmt.Sprintf("Found %d result(s)", len(results))
	if len(results) == 0 {
		message = "No results found"
	}
	if note != "" {
		message = message + ". " + note
	}

	data := map[string]any{
		"query":   query,
		"source":  source,
		"count":   len(results),
		"message": message,
		"results": results,
	}
	return data
}

func (b *Toolset) searchWithFallback(ctx context.Context, query string, maxResults int, timeoutMS int) ([]webSearchEntry, string, string, error) {
	ddgHTML, ddgErr := b.fetchSearchHTML(ctx, fmt.Sprintf(b.ddgSearchURL, url.QueryEscape(query)), timeoutMS)
	ddgResults := parseDuckDuckGoResults(ddgHTML, maxResults)
	if os.Getenv("WHALE_WEBSEARCH_FORCE_BING") == "1" {
		ddgResults = nil
		ddgHTML = "anomaly-modal"
	}
	if len(ddgResults) > 0 {
		return ddgResults, "duckduckgo", "", nil
	}
	ddgBlocked := isDuckDuckGoChallenge(ddgHTML)
	ddgNoResults := ddgErr == nil && isDuckDuckGoNoResults(ddgHTML)

	bingHTML, bingErr := b.fetchSearchHTML(ctx, fmt.Sprintf(b.bingSearchURL, url.QueryEscape(query)), timeoutMS)
	if bingErr == nil {
		bingResults := parseBingResults(bingHTML, maxResults)
		if len(bingResults) > 0 {
			note := "DuckDuckGo returned no parseable results; used Bing fallback"
			if ddgBlocked {
				note = "DuckDuckGo returned a bot challenge; used Bing fallback"
			}
			return bingResults, "bing", note, nil
		}
	}

	if ddgBlocked && bingErr != nil {
		return nil, "", "", fmt.Errorf("duckduckgo challenge and bing fallback failed: %v", bingErr)
	}
	if ddgErr != nil && bingErr != nil {
		return nil, "", "", fmt.Errorf("duckduckgo failed: %v; bing failed: %v", ddgErr, bingErr)
	}
	if ddgNoResults {
		return []webSearchEntry{}, "duckduckgo", "", nil
	}
	if bingErr == nil {
		note := "DuckDuckGo returned no parseable results; Bing fallback returned no results"
		if ddgBlocked {
			note = "DuckDuckGo returned a bot challenge; Bing fallback returned no results"
		}
		if isBingNoResults(bingHTML) {
			return []webSearchEntry{}, "bing", note, nil
		}
		if isBingChallenge(bingHTML) {
			return nil, "", "", fmt.Errorf("%s; bing fallback returned a bot challenge", duckDuckGoFallbackFailure(ddgErr, ddgBlocked, ddgHTML))
		}
		return nil, "", "", fmt.Errorf("%s; bing fallback returned no parseable results (%s)", duckDuckGoFallbackFailure(ddgErr, ddgBlocked, ddgHTML), webSearchHTMLPreview(bingHTML))
	}
	return nil, "", "", fmt.Errorf("%s; bing fallback failed: %v", duckDuckGoFallbackFailure(ddgErr, ddgBlocked, ddgHTML), bingErr)
}

func (b *Toolset) fetchSearchHTML(ctx context.Context, fullURL string, timeoutMS int) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeDurationMS(timeoutMS))
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", webSearchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parseDuckDuckGoResults(html string, maxResults int) []webSearchEntry {
	titleRe := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>(.*?)</a>|<div[^>]*class="result__snippet"[^>]*>(.*?)</div>`)

	snippets := snippetRe.FindAllStringSubmatch(html, -1)
	out := make([]webSearchEntry, 0, maxResults)
	matches := titleRe.FindAllStringSubmatch(html, -1)
	for i, m := range matches {
		if len(out) >= maxResults {
			break
		}
		title := normalizeHTMLText(m[2])
		if title == "" {
			continue
		}
		rawURL := strings.TrimSpace(m[1])
		entry := webSearchEntry{
			Title: title,
			URL:   normalizeDuckDuckGoURL(rawURL),
		}
		if i < len(snippets) {
			s := snippets[i]
			snippet := ""
			if len(s) > 1 {
				snippet = s[1]
			}
			if snippet == "" && len(s) > 2 {
				snippet = s[2]
			}
			entry.Snippet = normalizeHTMLText(snippet)
		}
		out = append(out, entry)
	}
	return out
}

func parseBingResults(html string, maxResults int) []webSearchEntry {
	resultRe := regexp.MustCompile(`(?is)<li[^>]*class="[^"]*\bb_algo\b[^"]*"[^>]*>(.*?)</li>`)
	titleRe := regexp.MustCompile(`(?is)<h2[^>]*>.*?<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`(?is)<div[^>]*class="[^"]*\bb_caption\b[^"]*"[^>]*>.*?<p[^>]*>(.*?)</p>`)
	blocks := resultRe.FindAllStringSubmatch(html, -1)
	out := make([]webSearchEntry, 0, maxResults)
	for _, b := range blocks {
		if len(out) >= maxResults {
			break
		}
		block := b[1]
		tm := titleRe.FindStringSubmatch(block)
		if len(tm) < 3 {
			continue
		}
		title := normalizeHTMLText(tm[2])
		if title == "" {
			continue
		}
		entry := webSearchEntry{
			Title: title,
			URL:   normalizeBingURL(strings.TrimSpace(tm[1])),
		}
		sm := snippetRe.FindStringSubmatch(block)
		if len(sm) >= 2 {
			entry.Snippet = normalizeHTMLText(sm[1])
		}
		out = append(out, entry)
	}
	return out
}

func normalizeDuckDuckGoURL(raw string) string {
	if strings.HasPrefix(raw, "//duckduckgo.com/l/?uddg=") {
		if u, err := url.Parse("https:" + raw); err == nil {
			enc := u.Query().Get("uddg")
			if dec, err := url.QueryUnescape(enc); err == nil && dec != "" {
				return dec
			}
		}
	}
	return raw
}

func normalizeBingURL(raw string) string {
	if strings.Contains(raw, "bing.com/ck/a?") {
		if u, err := url.Parse(raw); err == nil {
			enc := u.Query().Get("u")
			if strings.HasPrefix(enc, "a1") {
				enc = enc[2:]
			}
			if dec, err := url.QueryUnescape(enc); err == nil && dec != "" {
				if parsed, perr := url.Parse(dec); perr == nil && parsed.Scheme != "" {
					return dec
				}
			}
		}
	}
	return raw
}

func isDuckDuckGoChallenge(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "anomaly-modal") || strings.Contains(lower, "bots use duckduckgo too")
}

func isDuckDuckGoNoResults(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "no results found") ||
		strings.Contains(lower, "no results for") ||
		strings.Contains(lower, "not find any results")
}

func isBingChallenge(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "captcha") ||
		strings.Contains(lower, "verify you are human") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "forbidden")
}

func isBingNoResults(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "no results found") ||
		strings.Contains(lower, "did not match any documents") ||
		strings.Contains(lower, "there are no results for")
}

func duckDuckGoFallbackFailure(err error, blocked bool, html string) string {
	if blocked {
		return "duckduckgo returned a bot challenge"
	}
	if err != nil {
		return fmt.Sprintf("duckduckgo failed: %v", err)
	}
	return fmt.Sprintf("duckduckgo returned no parseable results (%s)", webSearchHTMLPreview(html))
}

func webSearchHTMLPreview(html string) string {
	preview := strings.Join(strings.Fields(html), " ")
	if len(preview) > 120 {
		preview = preview[:120]
	}
	return fmt.Sprintf("%d chars, first 120: %q", len(html), preview)
}

func timeDurationMS(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
