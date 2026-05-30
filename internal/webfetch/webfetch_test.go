package webfetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type extractorFunc func(context.Context, string, string) (string, error)

func (f extractorFunc) Extract(ctx context.Context, prompt, content string) (string, error) {
	return f(ctx, prompt, content)
}

func TestFetchFollowsSafeRedirect(t *testing.T) {
	calls := 0
	client := NewClient(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path == "" || req.URL.Path == "/" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/docs"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html><body><h1>Docs</h1></body></html>")),
			Request:    req,
		}, nil
	})}})
	res, err := client.Fetch(context.Background(), Request{URL: "https://example.com", Prompt: "read"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 2 || res.FinalURL != "https://example.com/docs" || !strings.Contains(res.Content, "Docs") {
		t.Fatalf("unexpected redirect result calls=%d res=%+v", calls, res)
	}
}

func TestFetchBlocksCrossHostRedirect(t *testing.T) {
	client := NewClient(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://login.example.net/auth"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}})
	res, err := client.Fetch(context.Background(), Request{URL: "https://example.com/private", Prompt: "read"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !res.RedirectBlocked || res.Recovery == nil || res.Recovery.Code != "redirect_requires_review" {
		t.Fatalf("expected blocked redirect recovery, got %+v", res)
	}
}

func TestFetchCachesResult(t *testing.T) {
	calls := 0
	client := NewClient(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("cached body")),
			Request:    req,
		}, nil
	})}})
	first, err := client.Fetch(context.Background(), Request{URL: "https://example.com/a", Prompt: "read"})
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	second, err := client.Fetch(context.Background(), Request{URL: "https://example.com/a", Prompt: "read"})
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if calls != 1 || first.FromCache || !second.FromCache {
		t.Fatalf("unexpected cache state calls=%d first=%v second=%v", calls, first.FromCache, second.FromCache)
	}
}

func TestFetchDoesNotCacheExtractorFailure(t *testing.T) {
	httpCalls := 0
	extractCalls := 0
	client := NewClient(Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			httpCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("retryable body")),
				Request:    req,
			}, nil
		})},
		Extractor: extractorFunc(func(ctx context.Context, prompt, content string) (string, error) {
			extractCalls++
			if extractCalls == 1 {
				return "", errors.New("rate limited")
			}
			return "extracted after retry", nil
		}),
	})

	first, err := client.Fetch(context.Background(), Request{URL: "https://example.com/extract", Prompt: "summarize"})
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if first.ExtractError == "" || first.Recovery == nil || first.Recovery.Code != "extract_failed" {
		t.Fatalf("expected extraction failure result, got %+v", first)
	}

	second, err := client.Fetch(context.Background(), Request{URL: "https://example.com/extract", Prompt: "summarize"})
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if second.FromCache || second.ExtractError != "" || second.Content != "extracted after retry" {
		t.Fatalf("expected uncached extraction retry success, got %+v", second)
	}
	if httpCalls != 2 || extractCalls != 2 {
		t.Fatalf("expected retry through fetch and extractor, httpCalls=%d extractCalls=%d", httpCalls, extractCalls)
	}
}

func TestFetchTruncatesLargeTextWithoutArtifact(t *testing.T) {
	body := strings.Repeat("x", MaxReplayChars+100)
	client := NewClient(Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		})},
	})
	res, err := client.Fetch(context.Background(), Request{URL: "https://example.com/large", Prompt: "read"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("expected truncation, got %+v", res)
	}
	if !strings.HasSuffix(res.Content, "\n\n[truncated]") {
		t.Fatalf("expected truncation marker, got suffix %q", res.Content[len(res.Content)-20:])
	}
	if strings.Contains(res.Content, "artifact") {
		t.Fatalf("ordinary text fetch should not mention artifact, got %q", res.Content[len(res.Content)-80:])
	}
}

func TestFetchClassifiesGitHubUnauthorized(t *testing.T) {
	client := NewClient(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}})
	res, err := client.Fetch(context.Background(), Request{URL: "https://api.github.com/repos/usewhale/whale/contents/README.md", Prompt: "read"})
	if err == nil {
		t.Fatal("expected error")
	}
	if res.Recovery == nil || res.Recovery.Code != "github_auth_or_api_blocked" || !strings.Contains(res.Recovery.RecommendedAction, "raw.githubusercontent.com") {
		t.Fatalf("unexpected recovery: %+v", res.Recovery)
	}
}

func TestFetchDetectsBotChallengeContent(t *testing.T) {
	client := NewClient(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html><body>Verify you are human before continuing</body></html>")),
			Request:    req,
		}, nil
	})}})
	res, err := client.Fetch(context.Background(), Request{URL: "https://example.com/protected", Prompt: "read"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Recovery == nil || res.Recovery.Code != "bot_challenge" || !res.LowContent {
		t.Fatalf("expected bot challenge recovery, got %+v", res)
	}
}
