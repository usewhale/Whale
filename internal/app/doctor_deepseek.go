package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

var errDoctorAuth = errors.New("doctor auth error")

func CheckDeepSeekAPIReachability(ctx context.Context, key string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, baseURL, nil)
	if err != nil {
		return "request build failed", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))

	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return classifyDoctorHTTPError(err), err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "unauthorized — check your DeepSeek API key", fmt.Errorf("%w: 401", errDoctorAuth)
	case resp.StatusCode == http.StatusForbidden:
		return "forbidden — verify the key is active and allowed", fmt.Errorf("%w: 403", errDoctorAuth)
	case resp.StatusCode >= 200 && resp.StatusCode < 500:
		return fmt.Sprintf("reachable — %s responded %d", baseURL, resp.StatusCode), nil
	default:
		return fmt.Sprintf("HTTP %d from %s", resp.StatusCode, baseURL), fmt.Errorf("http %d", resp.StatusCode)
	}
}

func classifyDoctorHTTPError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout — check your network connection"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout — check your network connection"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS resolution failed — check your network connection"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return "connection refused — check firewall or base URL settings"
		}
		return "connection failed — check your network connection"
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "tls handshake timeout"):
		return "TLS handshake timed out — check your network connection"
	case strings.Contains(msg, "timeout"):
		return "timeout — check your network connection"
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "lookup "):
		return "DNS resolution failed — check your network connection"
	case strings.Contains(msg, "connect:"):
		return "connection failed — check your network connection"
	default:
		return err.Error()
	}
}
