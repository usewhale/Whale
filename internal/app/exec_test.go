package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRunExecReturnsFinalOutput(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-1234567890abcdef1234")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	t.Setenv("DEEPSEEK_BASE_URL", srv.URL)

	res, err := RunExec(context.Background(), DefaultConfig(), StartOptions{NewSession: true}, "hi")
	if err != nil {
		t.Fatalf("RunExec: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("status = %q", res.Status)
	}
	if res.Output != "hello world" {
		t.Fatalf("output = %q", res.Output)
	}
	if res.SessionID == "" {
		t.Fatal("expected session id")
	}
	if res.Model == "" {
		t.Fatal("expected model")
	}
}

func TestSummarizeExecTextPreservesUTF8(t *testing.T) {
	input := strings.Repeat("中文🙂", 100)
	got := summarizeExecText(input)
	if !utf8.ValidString(got) {
		t.Fatalf("summary must be valid UTF-8: %q", got)
	}
	if strings.Contains(got, "�") {
		t.Fatalf("summary must not contain replacement characters: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated marker, got %q", got)
	}
}
