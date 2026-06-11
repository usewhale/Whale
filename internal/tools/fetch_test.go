package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type fakeWebFetchExtractor struct {
	gotPrompt  string
	gotContent string
	out        string
}

func (f *fakeWebFetchExtractor) Extract(ctx context.Context, prompt, content string) (string, error) {
	f.gotPrompt = prompt
	f.gotContent = content
	return f.out, nil
}

func TestFetchReturnsReadableContent(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><head><title>T</title></head><body><nav>skip</nav><h1>Hello</h1><p>World</p></body></html>`)),
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Request:    req,
		}, nil
	})}

	res, err := ts.fetch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "fetch",
		Input: `{"url":"https://example.com","prompt":"return the main text"}`,
	})
	if err != nil || res.IsError() {
		t.Fatalf("fetch failed err=%v res=%+v", err, res)
	}
	var out struct {
		Data struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.ModelText), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Data.URL != "https://example.com" || out.Data.Title != "T" {
		t.Fatalf("unexpected metadata: %+v", out.Data)
	}
	if !strings.Contains(out.Data.Content, "Hello") || strings.Contains(out.Data.Content, "skip") || strings.Contains(out.Data.Content, "<h1>") {
		t.Fatalf("expected extracted readable text, got: %q", out.Data.Content)
	}
}

func TestFetchUsesConfiguredExtractor(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	extractor := &fakeWebFetchExtractor{out: "answer from extractor"}
	ts.SetWebFetchExtractor(extractor)
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><body><h1>Deep detail</h1></body></html>`)),
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Request:    req,
		}, nil
	})}

	res, err := ts.fetch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "fetch",
		Input: `{"url":"https://example.com","prompt":"summarize"}`,
	})
	if err != nil || res.IsError() {
		t.Fatalf("fetch failed err=%v res=%+v", err, res)
	}
	var out struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.ModelText), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Data.Content != "answer from extractor" {
		t.Fatalf("expected extractor output, got %q", out.Data.Content)
	}
	if extractor.gotPrompt != "summarize" || !strings.Contains(extractor.gotContent, "Deep detail") {
		t.Fatalf("extractor received prompt=%q content=%q", extractor.gotPrompt, extractor.gotContent)
	}
}

func TestFetchRejectsMissingPromptAndOldFormatOnlyInput(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	res, err := ts.fetch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "fetch",
		Input: `{"url":"https://example.com","format":"raw"}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, "prompt is required") {
		t.Fatalf("expected prompt required error, got: %s", res.ModelText)
	}
}

func TestFetchFileURLIncludesRecoveryHint(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	res, err := ts.fetch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "fetch",
		Input: `{"url":"file:///tmp/result.txt","prompt":"read"}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected error, got: %+v", res)
	}
	for _, want := range []string{
		`"code":"invalid_args"`,
		`valid url is required`,
		"fetch only supports http/https URLs; use read_file for local file paths or tool result files.",
		`"recovery"`,
	} {
		if !strings.Contains(res.ModelText, want) {
			t.Fatalf("result missing %q:\n%s", want, res.ModelText)
		}
	}
}

func TestFetchHTTPErrorIncludesRecoveryHint(t *testing.T) {
	ts, _ := NewToolset(t.TempDir())
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader("auth required")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	res, err := ts.fetch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "fetch",
		Input: `{"url":"https://api.github.com/repos/usewhale/whale/contents/README.md","prompt":"read"}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected error, got: %+v", res)
	}
	var out struct {
		Data struct {
			Recovery struct {
				Code              string `json:"code"`
				RecommendedAction string `json:"recommended_action"`
			} `json:"recovery"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.ModelText), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Data.Recovery.Code != "github_auth_or_api_blocked" || !strings.Contains(out.Data.Recovery.RecommendedAction, "raw.githubusercontent.com") {
		t.Fatalf("expected GitHub recovery hint, got: %+v", out.Data.Recovery)
	}
}

func TestFetchRegistryIncludesTool(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	found := false
	for _, td := range ts.Tools() {
		if td.Name() == "fetch" {
			found = true
			desc := core.DescribeTool(td)
			if !desc.ReadOnly {
				t.Fatal("fetch should be readOnly")
			}
			if !strings.Contains(desc.Description, "recovery hints") {
				t.Fatalf("expected recovery hint in description: %q", desc.Description)
			}
			break
		}
	}
	if !found {
		t.Fatal("fetch not registered")
	}
}
