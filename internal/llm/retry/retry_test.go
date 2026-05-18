package retry

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestBackoffRespectsRetryAfterHeaderAndCapsDelay(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxDelay = 2 * time.Second
	err := &HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"10"}},
	}

	if got := Backoff(policy, 1, err); got != 2*time.Second {
		t.Fatalf("delay: want capped 2s, got %s", got)
	}
}

func TestBackoffParsesRetryAfterBody(t *testing.T) {
	policy := DefaultPolicy()
	policy.Jitter = 0
	err := &HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Body:       `{"error":"rate limit","retry_after_ms":1500}`,
	}

	if got := Backoff(policy, 1, err); got != 1500*time.Millisecond {
		t.Fatalf("delay: want 1500ms, got %s", got)
	}
}

func TestShouldRetryDefaultStatusCodes(t *testing.T) {
	policy := DefaultPolicy()

	if !ShouldRetry(policy, &HTTPError{StatusCode: http.StatusTooManyRequests}) {
		t.Fatal("expected 429 to retry")
	}
	if ShouldRetry(policy, &HTTPError{StatusCode: http.StatusBadRequest}) {
		t.Fatal("expected 400 not to retry")
	}
}

func TestNormalizePolicyPreservesDefaultBehaviorForPartialOverrides(t *testing.T) {
	policy := NormalizePolicy(Policy{MaxAttempts: 2})
	if policy.MaxAttempts != 2 {
		t.Fatalf("max attempts: want override 2, got %d", policy.MaxAttempts)
	}
	if !policy.RespectRetryAfter {
		t.Fatal("expected partial policy to preserve Retry-After support")
	}
	if !policy.RetryNetwork {
		t.Fatal("expected partial policy to preserve network retries")
	}

	err := &HTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"3"}},
	}
	if got := Backoff(Policy{MaxAttempts: 2}, 1, err); got != 3*time.Second {
		t.Fatalf("partial policy should respect Retry-After, got %s", got)
	}
	if !ShouldRetry(Policy{MaxAttempts: 2}, errors.New("temporary network failure")) {
		t.Fatal("partial policy should retry network failures")
	}
}
