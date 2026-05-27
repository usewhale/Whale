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

func TestWebSearchDuckDuckGoSuccess(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`
<html><body>
<a class="result__a" href="https://example.com/a">Alpha &amp; One</a>
<a class="result__snippet">alpha snippet</a>
</body></html>`)),
				Header:  make(http.Header),
				Request: req,
			}, nil
		}
		t.Fatalf("bing should not be called on ddg success")
		return nil, nil
	})}

	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"alpha","max_results":3}`,
	})
	if err != nil || res.IsError {
		t.Fatalf("web search failed err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"source":"duckduckgo"`) {
		t.Fatalf("expected duckduckgo source, got %s", res.Content)
	}
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Results []struct {
				Title string `json:"title"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Data.Results) == 0 || out.Data.Results[0].Title != "Alpha & One" {
		t.Fatalf("missing parsed title: %s", res.Content)
	}
}

func TestWebSearchFallsBackToBing(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body>Unfortunately, bots use DuckDuckGo too</body></html>`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`
<html><body>
<li class="b_algo"><h2><a href="https://www.bing.com/ck/a?u=a1https%3A%2F%2Fexample.com%2Fpath%3Fq%3D1">Example</a></h2><div class="b_caption"><p>bing snippet</p></div></li>
</body></html>`)),
			Header:  make(http.Header),
			Request: req,
		}, nil
	})}

	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"beta"}`,
	})
	if err != nil || res.IsError {
		t.Fatalf("web search failed err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"source":"bing"`) {
		t.Fatalf("expected bing source, got %s", res.Content)
	}
	if !strings.Contains(res.Content, "example.com/path?q=1") {
		t.Fatalf("expected normalized bing url, got %s", res.Content)
	}
}

func TestWebSearchBingNoResultsUsesBingSource(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body>Unfortunately, bots use DuckDuckGo too</body></html>`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><body><main>No results found</main></body></html>`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"nope"}`,
	})
	if err != nil || res.IsError {
		t.Fatalf("web search failed err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"source":"bing"`) {
		t.Fatalf("expected bing source for bing no-results page, got %s", res.Content)
	}
	if !strings.Contains(res.Content, `"count":0`) {
		t.Fatalf("expected zero count, got %s", res.Content)
	}
}

func TestWebSearchDuckDuckGoNoResultsWinsOverUnparseableBing(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body><main>No results found</main></body></html>`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><body><main>unexpected search layout</main></body></html>`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"primary-empty"}`,
	})
	if err != nil || res.IsError {
		t.Fatalf("web search should preserve duckduckgo no-results err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"source":"duckduckgo"`) {
		t.Fatalf("expected duckduckgo source, got %s", res.Content)
	}
	if !strings.Contains(res.Content, `"count":0`) {
		t.Fatalf("expected zero count, got %s", res.Content)
	}
}

func TestWebSearchBingUnparseableFallbackFails(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body>Unfortunately, bots use DuckDuckGo too</body></html>`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><body><main>unexpected search layout</main></body></html>`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"layout-change"}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	msg := toolErrorMessage(t, res)
	for _, want := range []string{
		"duckduckgo returned a bot challenge",
		"bing fallback returned no parseable results",
		"unexpected search layout",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got %q", want, msg)
		}
	}
}

func TestWebSearchBingChallengeFallbackFails(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body>Unfortunately, bots use DuckDuckGo too</body></html>`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><body>Verify you are human before continuing</body></html>`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"blocked"}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	msg := toolErrorMessage(t, res)
	for _, want := range []string{
		"duckduckgo returned a bot challenge",
		"bing fallback returned a bot challenge",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got %q", want, msg)
		}
	}
}

func TestWebSearchForceBingEnv(t *testing.T) {
	t.Setenv("WHALE_WEBSEARCH_FORCE_BING", "1")
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "ddg.test" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<html><body><a class="result__a" href="https://example.com/ddg">DDG Result</a></body></html>`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<html><body><li class="b_algo"><h2><a href="https://example.com/bing">Bing Result</a></h2></li></body></html>`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{"query":"force-bing"}`,
	})
	if err != nil || res.IsError {
		t.Fatalf("web search failed err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"source":"bing"`) {
		t.Fatalf("expected forced bing source, got %s", res.Content)
	}
}

func TestWebSearchMissingQuery(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.webSearch(context.Background(), core.ToolCall{
		ID:    "1",
		Name:  "web_search",
		Input: `{}`,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "query is required") {
		t.Fatalf("expected query required error, got %s", res.Content)
	}
}

func TestWebSearchRegistryIncludesTool(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	found := false
	for _, tdef := range ts.Tools() {
		if tdef.Name() == "web_search" {
			found = true
			if !core.DescribeTool(tdef).ReadOnly {
				t.Fatal("web_search should be readOnly")
			}
			break
		}
	}
	if !found {
		t.Fatal("web_search not registered")
	}
}

func TestWebSearchCompatSearchQueryArray(t *testing.T) {
	ts, err := NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.ddgSearchURL = "https://ddg.test/search?q=%s"
	ts.bingSearchURL = "https://bing.test/search?q=%s"
	ts.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`<a class="result__a" href="https://example.com">Example</a>`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	in := map[string]any{
		"search_query": []map[string]any{
			{"q": "x", "max_results": 2},
		},
	}
	b, _ := json.Marshal(in)
	res, err := ts.webSearch(context.Background(), core.ToolCall{ID: "1", Name: "web_search", Input: string(b)})
	if err != nil || res.IsError {
		t.Fatalf("compat search query failed err=%v res=%+v", err, res)
	}
}
