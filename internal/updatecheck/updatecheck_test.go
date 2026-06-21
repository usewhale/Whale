package updatecheck

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeVersionUsesCachedLatest(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion: "v0.1.16",
		LastCheckedAt: now,
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}

	got, ok := Checker{
		DataDir:        dir,
		CurrentVersion: "v0.1.15",
		Enabled:        true,
		Now:            func() time.Time { return now },
		Goos:           "linux",
	}.UpgradeVersion(context.Background())
	if !ok {
		t.Fatal("expected upgrade")
	}
	if got.LatestVersion != "v0.1.16" || got.CurrentVersion != "v0.1.15" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestUpgradeVersionRefreshesStaleCacheBeforeDeciding(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion: "v0.1.15",
		LastCheckedAt: now.Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	checker := Checker{
		DataDir:        dir,
		CurrentVersion: "v0.1.15",
		Enabled:        true,
		Now:            func() time.Time { return now },
		Client:         latestVersionClient("v0.1.16"),
		Goos:           "linux",
	}
	got, ok := checker.UpgradeVersion(context.Background())
	if !ok {
		t.Fatal("expected upgrade after refresh")
	}
	if got.LatestVersion != "v0.1.16" {
		t.Fatalf("latest=%q want v0.1.16", got.LatestVersion)
	}
}

func TestUpgradeVersionRefreshesMissingCacheBeforeDeciding(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	checker := Checker{
		DataDir:        dir,
		CurrentVersion: "v0.1.15",
		Enabled:        true,
		Now:            func() time.Time { return now },
		Client:         latestVersionClient("v0.1.16"),
		Goos:           "linux",
	}
	got, ok := checker.UpgradeVersion(context.Background())
	if !ok {
		t.Fatal("expected upgrade after refresh")
	}
	if got.LatestVersion != "v0.1.16" {
		t.Fatalf("latest=%q want v0.1.16", got.LatestVersion)
	}
}

func TestUpgradeVersionRecordsFailedRefreshBackoff(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("offline")
		}),
	}
	checker := Checker{
		DataDir:        dir,
		CurrentVersion: "v0.1.15",
		Enabled:        true,
		Now:            func() time.Time { return now },
		Client:         client,
		Goos:           "linux",
	}
	if _, ok := checker.UpgradeVersion(context.Background()); ok {
		t.Fatal("did not expect upgrade")
	}
	if calls != 1 {
		t.Fatalf("calls=%d want 1", calls)
	}
	info, err := ReadInfo(filepath.Join(dir, CacheFilename))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if !info.LastCheckedAt.Equal(now) {
		t.Fatalf("last_checked_at=%s want %s", info.LastCheckedAt, now)
	}
	if _, ok := checker.UpgradeVersion(context.Background()); ok {
		t.Fatal("did not expect upgrade on backoff")
	}
	if calls != 1 {
		t.Fatalf("calls=%d want 1 after backoff", calls)
	}
}

func TestUpgradeVersionSkipsUnreleasedOrDismissedVersions(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		current   string
		latest    string
		dismissed string
	}{
		{name: "same version", current: "v0.1.16", latest: "v0.1.16"},
		{name: "older latest", current: "v0.1.17", latest: "v0.1.16"},
		{name: "dev current", current: "dev-abcdef0", latest: "v0.1.16"},
		{name: "prerelease latest", current: "v0.1.15", latest: "v0.1.16-rc.1"},
		{name: "dismissed latest", current: "v0.1.15", latest: "v0.1.16", dismissed: "v0.1.16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
				LatestVersion:    tt.latest,
				LastCheckedAt:    now,
				DismissedVersion: tt.dismissed,
			}); err != nil {
				t.Fatalf("WriteInfo: %v", err)
			}
			_, ok := Checker{
				DataDir:        dir,
				CurrentVersion: tt.current,
				Enabled:        true,
				Now:            func() time.Time { return now },
			}.UpgradeVersion(context.Background())
			if ok {
				t.Fatal("did not expect upgrade")
			}
		})
	}
}

func TestCachedUpgradeVersionDoesNotTouchNetwork(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion: "v0.1.16",
		LastCheckedAt: now.Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	calls := 0
	checker := Checker{
		DataDir:        dir,
		CurrentVersion: "v0.1.15",
		Enabled:        true,
		Now:            func() time.Time { return now },
		Goos:           "linux",
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("must not call network")
		})},
	}
	got, ok := checker.CachedUpgradeVersion()
	if !ok {
		t.Fatal("expected upgrade from cache")
	}
	if got.LatestVersion != "v0.1.16" {
		t.Fatalf("latest=%q", got.LatestVersion)
	}
	if calls != 0 {
		t.Fatalf("network called %d times", calls)
	}
}

func TestRefreshIfStaleAsyncUpdatesCache(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion: "v0.1.15",
		LastCheckedAt: now.Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	checker := Checker{
		DataDir:        dir,
		CurrentVersion: "v0.1.15",
		Enabled:        true,
		Now:            func() time.Time { return now },
		Goos:           "linux",
		Client:         latestVersionClient("v0.1.16"),
	}
	<-checker.RefreshIfStaleAsync()
	info, err := ReadInfo(filepath.Join(dir, CacheFilename))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.LatestVersion != "v0.1.16" {
		t.Fatalf("latest=%q want v0.1.16", info.LatestVersion)
	}
}

func TestRefreshIfStaleAsyncSkipsWhenFresh(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion: "v0.1.16",
		LastCheckedAt: now,
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	calls := 0
	checker := Checker{
		DataDir: dir,
		Enabled: true,
		Now:     func() time.Time { return now },
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("must not call network")
		})},
	}
	<-checker.RefreshIfStaleAsync()
	if calls != 0 {
		t.Fatalf("network called %d times when cache fresh", calls)
	}
}

func TestDismissPersistsVersion(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion:    "v0.1.16",
		LastCheckedAt:    now.Add(-24 * time.Hour),
		DismissedVersion: "v0.1.16",
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	checker := Checker{
		DataDir: dir,
		Now:     func() time.Time { return now },
	}
	if err := checker.Dismiss("v0.1.17"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	info, err := ReadInfo(filepath.Join(dir, CacheFilename))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.DismissedVersion != "v0.1.17" {
		t.Fatalf("dismissed=%q", info.DismissedVersion)
	}
}

func TestRefreshPreservesDismissalWrittenWhileFetchIsInFlight(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := WriteInfo(filepath.Join(dir, CacheFilename), Info{
		LatestVersion: "v0.1.16",
		LastCheckedAt: now.Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			close(started)
			<-release
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v0.1.16"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	checker := Checker{
		DataDir: dir,
		Now:     func() time.Time { return now },
		Client:  client,
	}
	done := make(chan error, 1)
	go func() {
		done <- checker.Refresh(context.Background())
	}()
	<-started
	if err := checker.Dismiss("v0.1.16"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	info, err := ReadInfo(filepath.Join(dir, CacheFilename))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.DismissedVersion != "v0.1.16" {
		t.Fatalf("dismissed=%q", info.DismissedVersion)
	}
}

func TestDetectAction(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		exe        string
		cmd        string
		arg0       string
		manualOnly bool
	}{
		{name: "homebrew apple silicon", goos: "darwin", exe: "/opt/homebrew/bin/whale", cmd: "brew", arg0: "upgrade"},
		{name: "homebrew cellar", goos: "darwin", exe: "/usr/local/Cellar/whale/0.1.16/bin/whale", cmd: "brew", arg0: "upgrade"},
		{name: "macos usr local install script", goos: "darwin", exe: "/usr/local/bin/whale", cmd: "sh", arg0: "-c"},
		{name: "linux fallback", goos: "linux", exe: "/home/me/.local/bin/whale", cmd: "sh", arg0: "-c"},
		{name: "linux usr local install script", goos: "linux", exe: "/usr/local/bin/whale", cmd: "sh", arg0: "-c"},
		{name: "windows", goos: "windows", exe: `C:\Users\me\AppData\Local\Programs\Whale\bin\whale.exe`, cmd: "powershell", arg0: "-NoProfile", manualOnly: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := DetectAction(tt.goos, tt.exe)
			if action.Cmd != tt.cmd {
				t.Fatalf("cmd=%q want %q", action.Cmd, tt.cmd)
			}
			if len(action.Args) == 0 || action.Args[0] != tt.arg0 {
				t.Fatalf("args=%v want first %q", action.Args, tt.arg0)
			}
			if action.ManualOnly != tt.manualOnly {
				t.Fatalf("manualOnly=%v want %v", action.ManualOnly, tt.manualOnly)
			}
		})
	}
}

func TestDetectActionResolvesHomebrewSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "usr", "local", "Cellar", "whale", "0.1.16", "bin", "whale")
	link := filepath.Join(dir, "usr", "local", "bin", "whale")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll link: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	action := DetectAction("darwin", link)
	if action.Cmd != "brew" {
		t.Fatalf("cmd=%q want brew", action.Cmd)
	}
}

func TestActionStringQuotesShellSensitiveArguments(t *testing.T) {
	action := DetectAction("windows", `C:\Users\me\AppData\Local\Programs\Whale\bin\whale.exe`)
	got := action.String()
	want := `powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr https://raw.githubusercontent.com/usewhale/Whale/main/scripts/install.ps1 -UseB | iex"`
	if got != want {
		t.Fatalf("String()=%q want %q", got, want)
	}
}

func latestVersionClient(version string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"tag_name":"` + version + `"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
