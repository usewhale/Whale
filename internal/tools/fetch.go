package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

const (
	defaultFetchTimeoutMS = 15000
	maxFetchTimeoutMS     = 60000
	maxFetchBytes         = 2 * 1024 * 1024
)

func (b *Toolset) fetch(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		URL       string `json:"url"`
		Format    string `json:"format"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return marshalToolError(call, "invalid_args", "valid url is required"), nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return marshalToolError(call, "invalid_args", "url scheme must be http or https"), nil
	}

	format, ok := parseWebFetchFormat(in.Format, webFetchFormatText)
	if !ok {
		return marshalToolError(call, "invalid_args", "format must be one of: text, markdown, html"), nil
	}

	timeoutMS := in.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = defaultFetchTimeoutMS
	}
	if timeoutMS > maxFetchTimeoutMS {
		timeoutMS = maxFetchTimeoutMS
	}

	cctx, cancel := context.WithTimeout(ctx, timeDurationMS(timeoutMS))
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return marshalToolError(call, "fetch_failed", err.Error()), nil
	}
	req.Header.Set("User-Agent", webSearchUserAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return marshalToolError(call, "fetch_failed", err.Error()), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return marshalToolError(call, "fetch_failed", fmt.Sprintf("http %d", resp.StatusCode)), nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return marshalToolError(call, "fetch_failed", err.Error()), nil
	}
	truncated := false
	if len(raw) > maxFetchBytes {
		truncated = true
		raw = raw[:maxFetchBytes]
	}
	body := decodeWebBody(raw, resp.Header.Get("Content-Type"))
	text := htmlToText(body)
	content := body
	switch format {
	case webFetchFormatText, webFetchFormatMarkdown:
		content = text
	case webFetchFormatHTML:
		content = body
	}
	if truncated {
		content += "\n\n[truncated]"
	}
	title := extractHTMLTitle(body)
	lowContent := detectLowWebContent(u.String(), title, body, text)

	return marshalToolResult(call, map[string]any{
		"url":                u.String(),
		"status_code":        resp.StatusCode,
		"content":            content,
		"format":             string(format),
		"truncated":          truncated,
		"low_content":        lowContent.LowContent,
		"low_content_reason": lowContent.Reason,
		"next_steps":         lowContent.NextSteps,
	})
}
