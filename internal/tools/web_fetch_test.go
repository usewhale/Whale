package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestWebFetchExtractsTitleAndText(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`
<html>
<head><title> Example Page </title></head>
<body><nav>skip me</nav><main><h1>Hello</h1><p>World</p></main></body>
</html>`)),
			Header:  http.Header{"Content-Type": []string{"text/html"}},
			Request: req,
		}, nil
	})}
	res, err := ts.webFetch(context.Background(), core.ToolCall{ID: "1", Name: "web_fetch", Input: `{"url":"https://example.com","prompt":"main content"}`})
	if err != nil || res.IsError() {
		t.Fatalf("web_fetch failed err=%v res=%+v", err, res)
	}

	var out struct {
		Data struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.ModelText), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Data.Title != "Example Page" {
		t.Fatalf("bad title: %q", out.Data.Title)
	}
	if !strings.Contains(out.Data.Content, "Hello") || strings.Contains(out.Data.Content, "skip me") {
		t.Fatalf("unexpected extracted content: %q", out.Data.Content)
	}
}

func TestWebFetchDecodesHTMLMetaCharset(t *testing.T) {
	raw := []byte(`<html><head><meta charset="shift_jis"><title>`)
	raw = append(raw, []byte{0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea}...)
	raw = append(raw, []byte(`</title></head><body><p>`)...)
	raw = append(raw, []byte{0x93, 0xfa, 0x96, 0x7b, 0x8c, 0xea}...)
	raw = append(raw, []byte(`</p></body></html>`)...)

	ts, _ := NewToolset(t.TempDir())
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(raw)),
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Request:    req,
		}, nil
	})}
	res, err := ts.webFetch(context.Background(), core.ToolCall{ID: "1", Name: "web_fetch", Input: `{"url":"https://example.com","prompt":"read page"}`})
	if err != nil || res.IsError() {
		t.Fatalf("web_fetch failed err=%v res=%+v", err, res)
	}

	var out struct {
		Data struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.ModelText), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Data.Title != "日本語" || !strings.Contains(out.Data.Content, "日本語") {
		t.Fatalf("expected decoded Shift_JIS content, got title=%q content=%q", out.Data.Title, out.Data.Content)
	}
}

func TestWebFetchMarksLowContentSPAShell(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`
<html>
<head><title>Google Antigravity</title><script type="module" src="/main.js"></script></head>
<body><app-root></app-root></body>
</html>`)),
			Header:  http.Header{"Content-Type": []string{"text/html"}},
			Request: req,
		}, nil
	})}
	res, err := ts.webFetch(context.Background(), core.ToolCall{ID: "1", Name: "web_fetch", Input: `{"url":"https://antigravity.google/docs/hooks","prompt":"extract hooks docs"}`})
	if err != nil || res.IsError() {
		t.Fatalf("web_fetch failed err=%v res=%+v", err, res)
	}

	var out struct {
		Data struct {
			LowContent bool   `json:"low_content"`
			Reason     string `json:"low_content_reason"`
			NextSteps  string `json:"next_steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.ModelText), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Data.LowContent {
		t.Fatalf("expected low content diagnosis, got: %s", res.ModelText)
	}
	if out.Data.Reason == "" || !strings.Contains(out.Data.NextSteps, "web_search") {
		t.Fatalf("expected actionable diagnosis, got reason=%q next_steps=%q", out.Data.Reason, out.Data.NextSteps)
	}
}

func TestWebFetchRegistryIncludesTool(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	found := false
	for _, td := range ts.Tools() {
		if td.Name() == "web_fetch" {
			found = true
			if !core.DescribeTool(td).ReadOnly {
				t.Fatal("web_fetch should be readOnly")
			}
			break
		}
	}
	if !found {
		t.Fatal("web_fetch not registered")
	}
}
