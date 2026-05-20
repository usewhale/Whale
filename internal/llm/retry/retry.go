package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Policy struct {
	MaxAttempts       int
	BaseDelay         time.Duration
	MaxDelay          time.Duration
	Jitter            float64
	RespectRetryAfter bool
	RetryNetwork      bool
	RetryStatusCodes  map[int]bool
}

type Info struct {
	Attempt     int
	MaxAttempts int
	Delay       time.Duration
	StatusCode  int
	Reason      string
	Stage       string
	StreamReset bool
}

type HTTPError struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("http %d", e.StatusCode)
	}
	return fmt.Sprintf("http %d: %s", e.StatusCode, body)
}

type Sleeper func(context.Context, time.Duration) error

func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts:       4,
		BaseDelay:         time.Second,
		MaxDelay:          60 * time.Second,
		Jitter:            0.1,
		RespectRetryAfter: true,
		RetryNetwork:      true,
		RetryStatusCodes: map[int]bool{
			http.StatusTooManyRequests:     true,
			http.StatusInternalServerError: true,
			http.StatusBadGateway:          true,
			http.StatusServiceUnavailable:  true,
			http.StatusGatewayTimeout:      true,
		},
	}
}

func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func NormalizePolicy(p Policy) Policy {
	def := DefaultPolicy()
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = def.MaxAttempts
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = def.BaseDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = def.MaxDelay
	}
	if p.Jitter < 0 {
		p.Jitter = 0
	}
	if !p.RespectRetryAfter {
		p.RespectRetryAfter = def.RespectRetryAfter
	}
	if !p.RetryNetwork {
		p.RetryNetwork = def.RetryNetwork
	}
	if p.RetryStatusCodes == nil {
		p.RetryStatusCodes = def.RetryStatusCodes
	}
	return p
}

func ShouldRetry(p Policy, err error) bool {
	p = NormalizePolicy(p)
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return p.RetryStatusCodes[httpErr.StatusCode]
	}
	return p.RetryNetwork
}

func Backoff(p Policy, attempt int, err error) time.Duration {
	p = NormalizePolicy(p)
	if p.RespectRetryAfter {
		if d, ok := retryAfterDelay(err); ok {
			return capDelay(d, p.MaxDelay)
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	multiplier := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(p.BaseDelay) * multiplier)
	delay = capDelay(delay, p.MaxDelay)
	if p.Jitter > 0 && delay > 0 {
		spread := float64(delay) * p.Jitter
		offset := (rand.Float64()*2 - 1) * spread
		delay = time.Duration(math.Max(0, float64(delay)+offset))
	}
	return capDelay(delay, p.MaxDelay)
}

func FormatInfo(info Info) string {
	retries := info.MaxAttempts - 1
	if retries < 1 {
		retries = 1
	}
	reason := strings.TrimSpace(info.Reason)
	if reason == "" {
		reason = "request failed"
	}
	return fmt.Sprintf("%s, retrying in %s (%d/%d)", reason, formatDuration(info.Delay), info.Attempt, retries)
}

func Reason(err error) string {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusTooManyRequests:
			return "API rate limited"
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return fmt.Sprintf("API temporarily unavailable (HTTP %d)", httpErr.StatusCode)
		default:
			return fmt.Sprintf("API request failed (HTTP %d)", httpErr.StatusCode)
		}
	}
	return "API request failed"
}

func retryAfterDelay(err error) (time.Duration, bool) {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return 0, false
	}
	if raw := strings.TrimSpace(httpErr.Header.Get("Retry-After")); raw != "" {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second, true
		}
		if when, parseErr := http.ParseTime(raw); parseErr == nil {
			if d := time.Until(when); d > 0 {
				return d, true
			}
			return 0, true
		}
	}
	return retryAfterFromBody(httpErr.Body)
}

var (
	bodyRetryAfterMS = regexp.MustCompile(`(?i)"retry_after_ms"\s*:\s*(\d+)`)
	bodyRetryAfterS  = regexp.MustCompile(`(?i)(?:try\s+again\s+in|retry(?:[_\-\s]?after)?[:\s]+)\s*(\d+(?:\.\d+)?)\s*(ms|s|sec|secs|second|seconds)?`)
)

func retryAfterFromBody(body string) (time.Duration, bool) {
	if match := bodyRetryAfterMS.FindStringSubmatch(body); len(match) == 2 {
		ms, err := strconv.Atoi(match[1])
		if err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond, true
		}
	}
	if match := bodyRetryAfterS.FindStringSubmatch(body); len(match) >= 2 {
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil || value < 0 {
			return 0, false
		}
		unit := "s"
		if len(match) >= 3 && strings.TrimSpace(match[2]) != "" {
			unit = strings.ToLower(strings.TrimSpace(match[2]))
		}
		switch unit {
		case "ms":
			return time.Duration(value * float64(time.Millisecond)), true
		default:
			return time.Duration(value * float64(time.Second)), true
		}
	}
	return 0, false
}

func capDelay(d, max time.Duration) time.Duration {
	if max > 0 && d > max {
		return max
	}
	return d
}

func formatDuration(d time.Duration) string {
	if d%time.Second == 0 {
		return d.String()
	}
	return d.Round(100 * time.Millisecond).String()
}
