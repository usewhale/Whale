package webfetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	DefaultTimeoutMS = 15000
	MaxTimeoutMS     = 60000
	MaxURLLength     = 2000
	MaxFetchBytes    = 10 * 1024 * 1024
	MaxReplayChars   = 100000
	DefaultTTL       = 15 * time.Minute
	DefaultCacheSize = 50 * 1024 * 1024
)

type Extractor interface {
	Extract(ctx context.Context, prompt, content string) (string, error)
}

type Request struct {
	URL       string
	Prompt    string
	TimeoutMS int
}

type Result struct {
	URL              string        `json:"url"`
	FinalURL         string        `json:"final_url,omitempty"`
	StatusCode       int           `json:"status_code,omitempty"`
	CodeText         string        `json:"code_text,omitempty"`
	ContentType      string        `json:"content_type,omitempty"`
	Title            string        `json:"title,omitempty"`
	Content          string        `json:"content,omitempty"`
	ContentChars     int           `json:"content_chars"`
	Bytes            int           `json:"bytes"`
	DurationMS       int64         `json:"duration_ms"`
	FromCache        bool          `json:"from_cache"`
	Truncated        bool          `json:"truncated"`
	LowContent       bool          `json:"low_content,omitempty"`
	LowContentReason string        `json:"low_content_reason,omitempty"`
	NextSteps        string        `json:"next_steps,omitempty"`
	RedirectBlocked  bool          `json:"redirect_blocked,omitempty"`
	RedirectLocation string        `json:"redirect_location,omitempty"`
	Recovery         *RecoveryHint `json:"recovery,omitempty"`
	ExtractError     string        `json:"extract_error,omitempty"`
}

type RecoveryHint struct {
	Code              string   `json:"code"`
	Retryable         bool     `json:"retryable"`
	RecommendedAction string   `json:"recommended_action"`
	NextSteps         []string `json:"next_steps,omitempty"`
}

type Error struct {
	Code    string
	Message string
	Result  Result
}

func (e *Error) Error() string { return strings.TrimSpace(e.Message) }

type Client struct {
	httpClient *http.Client
	extractor  Extractor
	cache      *cache
}

type Options struct {
	HTTPClient *http.Client
	Extractor  Extractor
	TTL        time.Duration
	MaxCache   int
}

func NewClient(opts Options) *Client {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	maxCache := opts.MaxCache
	if maxCache <= 0 {
		maxCache = DefaultCacheSize
	}
	return &Client{
		httpClient: httpClient,
		extractor:  opts.Extractor,
		cache:      newCache(ttl, maxCache),
	}
}

func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.httpClient = httpClient
	}
}

func (c *Client) SetExtractor(extractor Extractor) {
	c.extractor = extractor
}

func (c *Client) Fetch(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	u, err := normalizeURL(req.URL)
	if err != nil {
		return Result{}, &Error{Code: "invalid_args", Message: err.Error()}
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Result{}, &Error{Code: "invalid_args", Message: "prompt is required"}
	}
	timeoutMS := req.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = DefaultTimeoutMS
	}
	if timeoutMS > MaxTimeoutMS {
		timeoutMS = MaxTimeoutMS
	}
	key := cacheKey(u.String(), prompt)
	if item, ok := c.cache.get(key); ok {
		item.FromCache = true
		item.DurationMS = time.Since(start).Milliseconds()
		return item, nil
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	result, err := c.fetchWithRedirects(cctx, u, prompt)
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		return result, err
	}
	if result.ExtractError == "" {
		c.cache.set(key, result)
	}
	return result, nil
}

func (c *Client) fetchWithRedirects(ctx context.Context, u *url.URL, prompt string) (Result, error) {
	current := cloneURL(u)
	for redirects := 0; redirects <= 10; redirects++ {
		resp, raw, err := c.do(ctx, current)
		if err != nil {
			return Result{URL: u.String(), FinalURL: current.String(), Recovery: classifyFetchError(current, err)}, &Error{
				Code:    "fetch_failed",
				Message: err.Error(),
				Result:  Result{URL: u.String(), FinalURL: current.String(), Recovery: classifyFetchError(current, err)},
			}
		}
		if isRedirect(resp.StatusCode) {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			next, safe, reason := resolveRedirect(current, location)
			if !safe {
				res := Result{
					URL:              u.String(),
					FinalURL:         current.String(),
					StatusCode:       resp.StatusCode,
					CodeText:         http.StatusText(resp.StatusCode),
					RedirectBlocked:  true,
					RedirectLocation: location,
					Content:          fmt.Sprintf("Fetch stopped at redirect to %s: %s", location, reason),
					Recovery: &RecoveryHint{
						Code:              "redirect_requires_review",
						Retryable:         false,
						RecommendedAction: "Open or fetch the redirected URL only if the host change is expected.",
						NextSteps:         []string{"Use the official canonical URL.", "Avoid following cross-host redirects that may be login, tracking, or bot-challenge pages."},
					},
				}
				return res, nil
			}
			current = next
			continue
		}
		return c.processResponse(ctx, u.String(), current.String(), prompt, resp, raw)
	}
	res := Result{URL: u.String(), FinalURL: current.String(), Recovery: &RecoveryHint{
		Code:              "too_many_redirects",
		Retryable:         false,
		RecommendedAction: "Use the final canonical URL directly.",
	}}
	return res, &Error{Code: "fetch_failed", Message: "too many redirects", Result: res}
}

func (c *Client) do(ctx context.Context, u *url.URL) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; WhaleFetch/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	client := *c.httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		return resp, nil, err
	}
	return resp, raw, nil
}

func (c *Client) processResponse(ctx context.Context, requestedURL, finalURL, prompt string, resp *http.Response, raw []byte) (Result, error) {
	truncated := len(raw) > MaxFetchBytes
	if truncated {
		raw = raw[:MaxFetchBytes]
	}
	contentType := resp.Header.Get("Content-Type")
	base := Result{
		URL:         requestedURL,
		FinalURL:    finalURL,
		StatusCode:  resp.StatusCode,
		CodeText:    http.StatusText(resp.StatusCode),
		ContentType: contentType,
		Bytes:       len(raw),
		Truncated:   truncated,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		base.Recovery = classifyHTTPStatus(finalURL, resp.StatusCode)
		return base, &Error{Code: "fetch_failed", Message: fmt.Sprintf("http %d", resp.StatusCode), Result: base}
	}

	body := decodeBody(raw, contentType)
	title := extractTitle(body)
	readable := htmlToText(body)
	if !looksHTML(contentType, body) {
		readable = body
	}
	if truncated {
		readable += "\n\n[truncated]"
	}
	base.Title = title
	diag := detectLowContent(finalURL, title, body, readable)
	base.LowContent = diag.LowContent
	base.LowContentReason = diag.Reason
	base.NextSteps = diag.NextSteps
	if IsBotChallengeContent(body) || IsBotChallengeContent(readable) {
		base.LowContent = true
		base.LowContentReason = "fetch returned a bot challenge or human verification page"
		base.NextSteps = "Do not repeat the same fetch URL. Use an official canonical URL, a raw content URL, or web_search with site: and exact-title queries to find an accessible source."
		base.Recovery = &RecoveryHint{
			Code:              "bot_challenge",
			Retryable:         false,
			RecommendedAction: "Stop retrying the same URL and switch to an official/raw URL or search fallback.",
			NextSteps:         []string{"Try an official documentation URL.", "For GitHub files, use raw.githubusercontent.com.", "Use web_search with site: and exact title keywords."},
		}
	}

	replayContent := readable
	if len([]rune(replayContent)) > MaxReplayChars {
		replayContent = string([]rune(replayContent)[:MaxReplayChars]) + "\n\n[truncated]"
		base.Truncated = true
	}

	extracted := replayContent
	if c.extractor != nil {
		out, err := c.extractor.Extract(ctx, prompt, replayContent)
		if err != nil {
			base.ExtractError = err.Error()
			base.Recovery = &RecoveryHint{
				Code:              "extract_failed",
				Retryable:         true,
				RecommendedAction: "Use the returned readable content directly or retry extraction with a narrower prompt.",
				NextSteps:         []string{"Ask for a smaller extraction target.", "Fetch the official/raw URL if this is documentation or source code."},
			}
		} else if strings.TrimSpace(out) != "" {
			extracted = strings.TrimSpace(out)
		}
	}
	base.Content = extracted
	base.ContentChars = len([]rune(base.Content))
	return base, nil
}

func normalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("valid url is required")
	}
	if len(raw) > MaxURLLength {
		return nil, fmt.Errorf("url exceeds %d characters", MaxURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("valid url is required")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("url scheme must be http or https")
	}
	if u.User != nil {
		return nil, errors.New("url must not include username or password")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return nil, errors.New("valid host is required")
	}
	if !strings.Contains(host, ".") && host != "localhost" && net.ParseIP(host) == nil {
		return nil, errors.New("host must be a fully-qualified domain, localhost, or IP address")
	}
	if u.Scheme == "http" && host != "localhost" && net.ParseIP(host) == nil {
		u.Scheme = "https"
	}
	u.Fragment = ""
	return u, nil
}

func resolveRedirect(current *url.URL, location string) (*url.URL, bool, string) {
	if location == "" {
		return nil, false, "missing Location header"
	}
	next, err := current.Parse(location)
	if err != nil {
		return nil, false, "invalid Location header"
	}
	if next.User != nil {
		return nil, false, "redirect includes credentials"
	}
	if next.Scheme != current.Scheme {
		return nil, false, "redirect changes scheme"
	}
	if effectivePort(next) != effectivePort(current) {
		return nil, false, "redirect changes port"
	}
	if stripWWW(next.Hostname()) != stripWWW(current.Hostname()) {
		return nil, false, "redirect changes host"
	}
	next.Fragment = ""
	return next, true, ""
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func stripWWW(host string) string {
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

func cloneURL(u *url.URL) *url.URL {
	c := *u
	return &c
}

func cacheKey(u, prompt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return u + "#" + hex.EncodeToString(sum[:8])
}

func classifyHTTPStatus(rawURL string, status int) *RecoveryHint {
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		hint := &RecoveryHint{
			Code:              "auth_or_blocked",
			Retryable:         false,
			RecommendedAction: "Use an official public URL, provide credentials through an approved tool, or stop retrying the same blocked URL.",
			NextSteps:         []string{"Search for a public docs page for the same content.", "If this is GitHub, try raw.githubusercontent.com or the gh CLI with the user's credentials."},
		}
		if strings.Contains(host, "github.com") || strings.Contains(host, "api.github.com") {
			hint.Code = "github_auth_or_api_blocked"
			hint.RecommendedAction = "For GitHub content, prefer raw.githubusercontent.com for files or gh CLI/API with an authenticated token."
		}
		return hint
	case http.StatusTooManyRequests:
		return &RecoveryHint{Code: "rate_limited", Retryable: true, RecommendedAction: "Wait before retrying or use an authenticated official API when available."}
	default:
		if status >= 500 {
			return &RecoveryHint{Code: "server_error", Retryable: true, RecommendedAction: "Retry later or use an official mirror/raw URL."}
		}
		return &RecoveryHint{Code: "http_error", Retryable: false, RecommendedAction: "Verify the URL and try an official canonical URL."}
	}
}

func classifyFetchError(u *url.URL, err error) *RecoveryHint {
	msg := strings.ToLower(err.Error())
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return &RecoveryHint{
			Code:              "timeout",
			Retryable:         true,
			RecommendedAction: "Retry once with a narrower URL or use an official raw/content URL.",
			NextSteps:         []string{"For GitHub files, use raw.githubusercontent.com.", "Avoid repeating the same URL if it keeps timing out."},
		}
	}
	if u != nil && strings.Contains(strings.ToLower(u.Hostname()), "github.com") {
		return &RecoveryHint{
			Code:              "github_fetch_failed",
			Retryable:         true,
			RecommendedAction: "Try raw.githubusercontent.com for file content or gh CLI for authenticated GitHub resources.",
		}
	}
	return &RecoveryHint{Code: "network_error", Retryable: true, RecommendedAction: "Retry once, then switch to search or an official alternate URL if the same host keeps failing."}
}

func looksHTML(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "html") || strings.Contains(strings.ToLower(body[:min(len(body), 200)]), "<html")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func decodeBody(raw []byte, contentType string) string {
	enc, _, _ := charset.DetermineEncoding(raw, contentType)
	if enc == nil {
		return string(raw)
	}
	decoded, err := enc.NewDecoder().Bytes(raw)
	if err != nil {
		return string(raw)
	}
	return string(decoded)
}

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
	reBlockTags = regexp.MustCompile(`(?is)</?(p|div|br|h[1-6]|li|tr|section|article|main)\b[^>]*>`)
	reTitle     = regexp.MustCompile(`(?is)<title[^>]*>([\s\S]*?)</title>`)
)

func decodeHTMLBasic(s string) string {
	return html.UnescapeString(s)
}

func htmlToText(rawHTML string) string {
	s := rawHTML
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

func extractTitle(rawHTML string) string {
	m := reTitle.FindStringSubmatch(rawHTML)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Join(strings.Fields(decodeHTMLBasic(m[1])), " "))
}

type lowContentDiagnostic struct {
	LowContent bool
	Reason     string
	NextSteps  string
}

func detectLowContent(rawURL, title, rawHTML, text string) lowContentDiagnostic {
	trimmedText := strings.TrimSpace(text)
	normalizedText := strings.ToLower(strings.Join(strings.Fields(trimmedText), " "))
	normalizedTitle := strings.ToLower(strings.Join(strings.Fields(title), " "))
	lowerHTML := strings.ToLower(rawHTML)
	scriptCount := len(reScriptTag.FindAllStringIndex(rawHTML, -1))

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
		return lowContentResult(rawURL, "page looks like a JavaScript-rendered shell with little readable text")
	case titleOnly && (hasSPARoot || scriptHeavy):
		return lowContentResult(rawURL, "fetch returned mostly the page title")
	case emptyReadable && scriptCount > 0:
		return lowContentResult(rawURL, "fetch returned no readable text from a script-driven page")
	case scriptHeavy && veryShort:
		return lowContentResult(rawURL, "page is script-heavy and returned very little readable text")
	default:
		return lowContentDiagnostic{}
	}
}

func lowContentResult(rawURL, reason string) lowContentDiagnostic {
	return lowContentDiagnostic{
		LowContent: true,
		Reason:     reason,
		NextSteps:  "The URL may be JavaScript-rendered, authenticated, or only visible through search snippets. Use web_search with the exact URL, a site: query for the host/path, and related official keywords before concluding the page is unavailable. URL: " + rawURL,
	}
}

type cacheItem struct {
	key       string
	value     Result
	size      int
	expiresAt time.Time
	createdAt time.Time
}

type cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxBytes int
	bytes    int
	items    map[string]cacheItem
}

func newCache(ttl time.Duration, maxBytes int) *cache {
	return &cache{ttl: ttl, maxBytes: maxBytes, items: map[string]cacheItem{}}
}

func (c *cache) get(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return Result{}, false
	}
	if time.Now().After(item.expiresAt) {
		delete(c.items, key)
		c.bytes -= item.size
		return Result{}, false
	}
	return item.value, true
}

func (c *cache) set(key string, value Result) {
	size := len(value.Content)
	if size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.items[key]; ok {
		c.bytes -= old.size
	}
	now := time.Now()
	c.items[key] = cacheItem{key: key, value: value, size: size, expiresAt: now.Add(c.ttl), createdAt: now}
	c.bytes += size
	c.evict()
}

func (c *cache) evict() {
	if c.bytes <= c.maxBytes {
		return
	}
	items := make([]cacheItem, 0, len(c.items))
	for _, item := range c.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].createdAt.Before(items[j].createdAt) })
	for _, item := range items {
		if c.bytes <= c.maxBytes {
			return
		}
		delete(c.items, item.key)
		c.bytes -= item.size
	}
}

func IsBotChallengeContent(content string) bool {
	lower := strings.ToLower(content)
	needles := []string{"bot challenge", "captcha", "verify you are human", "unusual traffic", "cf-chl", "cloudflare"}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func JoinPromptAndContent(prompt, content string) string {
	var b bytes.Buffer
	b.WriteString("User extraction prompt:\n")
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\nFetched content:\n")
	b.WriteString(strings.TrimSpace(content))
	return b.String()
}
