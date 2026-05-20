package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	CacheFilename    = "version.json"
	LatestReleaseURL = "https://api.github.com/repos/usewhale/DeepSeek-Code-Whale/releases/latest"
	ReleaseNotesURL  = "https://github.com/usewhale/DeepSeek-Code-Whale/releases/latest"
	CheckInterval    = 20 * time.Hour
)

type Info struct {
	LatestVersion    string    `json:"latest_version"`
	LastCheckedAt    time.Time `json:"last_checked_at"`
	DismissedVersion string    `json:"dismissed_version,omitempty"`
}

type Checker struct {
	DataDir        string
	CurrentVersion string
	Enabled        bool
	Now            func() time.Time
	Client         *http.Client
	Goos           string
	ExecutablePath string
}

type Result struct {
	LatestVersion   string
	CurrentVersion  string
	ReleaseNotesURL string
	UpdateAction    Action
}

type Action struct {
	Name       string
	Cmd        string
	Args       []string
	ManualOnly bool
}

type latestRelease struct {
	TagName string `json:"tag_name"`
}

var (
	cacheMu        sync.Mutex
	versionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)
)

func (c Checker) UpgradeVersion(ctx context.Context) (Result, bool) {
	if !c.Enabled {
		return Result{}, false
	}
	current, ok := parseVersion(c.CurrentVersion)
	if !ok {
		return Result{}, false
	}
	info, _ := ReadInfo(c.cachePath())
	if info == nil || c.stale(info.LastCheckedAt) {
		if ctx == nil {
			ctx = context.Background()
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := c.Refresh(refreshCtx); err == nil {
			info, _ = ReadInfo(c.cachePath())
		} else {
			_ = c.RecordCheckAttempt()
			info, _ = ReadInfo(c.cachePath())
		}
	}
	return c.upgradeFromInfo(info, current)
}

// CachedUpgradeVersion returns an upgrade prompt from the on-disk cache only,
// never contacting the network. Callers should pair this with
// RefreshIfStaleAsync so the cache is refreshed in the background for the next
// startup, keeping the UI startup path fail-open and non-blocking.
func (c Checker) CachedUpgradeVersion() (Result, bool) {
	if !c.Enabled {
		return Result{}, false
	}
	current, ok := parseVersion(c.CurrentVersion)
	if !ok {
		return Result{}, false
	}
	info, _ := ReadInfo(c.cachePath())
	return c.upgradeFromInfo(info, current)
}

// RefreshIfStaleAsync kicks off a background refresh when the cache is stale.
// Returns immediately; the caller should not wait on the returned channel
// except in tests that need deterministic completion.
func (c Checker) RefreshIfStaleAsync() <-chan struct{} {
	done := make(chan struct{})
	if !c.Enabled {
		close(done)
		return done
	}
	info, _ := ReadInfo(c.cachePath())
	if info != nil && !c.stale(info.LastCheckedAt) {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := c.Refresh(ctx); err != nil {
			_ = c.RecordCheckAttempt()
		}
	}()
	return done
}

func (c Checker) upgradeFromInfo(info *Info, current semver) (Result, bool) {
	if info == nil {
		return Result{}, false
	}
	latest, ok := parseVersion(info.LatestVersion)
	if !ok || !latest.after(current) {
		return Result{}, false
	}
	if strings.TrimSpace(info.DismissedVersion) == strings.TrimSpace(info.LatestVersion) {
		return Result{}, false
	}
	action := DetectAction(c.Goos, c.ExecutablePath)
	return Result{
		LatestVersion:   info.LatestVersion,
		CurrentVersion:  c.CurrentVersion,
		ReleaseNotesURL: ReleaseNotesURL,
		UpdateAction:    action,
	}, true
}

func (c Checker) Refresh(ctx context.Context) error {
	latest, err := c.FetchLatestVersion(ctx)
	if err != nil {
		return err
	}
	path := c.cachePath()
	cacheMu.Lock()
	defer cacheMu.Unlock()
	prev, _ := ReadInfo(path)
	info := Info{
		LatestVersion: latest,
		LastCheckedAt: c.now(),
	}
	if prev != nil {
		info.DismissedVersion = prev.DismissedVersion
	}
	return WriteInfo(path, info)
}

func (c Checker) RecordCheckAttempt() error {
	path := c.cachePath()
	cacheMu.Lock()
	defer cacheMu.Unlock()
	info, _ := ReadInfo(path)
	if info == nil {
		info = &Info{}
	}
	info.LastCheckedAt = c.now()
	return WriteInfo(path, *info)
}

func (c Checker) FetchLatestVersion(ctx context.Context) (string, error) {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "whale-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("latest release request failed: %s", resp.Status)
	}
	var release latestRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(release.TagName)
	if _, ok := parseVersion(tag); !ok {
		return "", fmt.Errorf("latest release has invalid tag: %q", tag)
	}
	return tag, nil
}

func (c Checker) Dismiss(version string) error {
	path := c.cachePath()
	cacheMu.Lock()
	defer cacheMu.Unlock()
	info, err := ReadInfo(path)
	if err != nil || info == nil {
		return nil
	}
	info.DismissedVersion = strings.TrimSpace(version)
	return WriteInfo(path, *info)
}

func ReadInfo(path string) (*Info, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func WriteInfo(path string, info Info) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func DetectAction(goos, executablePath string) Action {
	if strings.TrimSpace(goos) == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "windows":
		return Action{
			Name:       "Windows installer",
			Cmd:        "powershell",
			Args:       []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "iwr https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 -UseB | iex"},
			ManualOnly: true,
		}
	case "darwin":
		if isHomebrewPath(executablePath) {
			return Action{Name: "Homebrew", Cmd: "brew", Args: []string{"upgrade", "usewhale/tap/whale"}}
		}
		return Action{Name: "install script", Cmd: "sh", Args: []string{"-c", "curl -fsSL https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.sh | sh"}}
	case "linux":
		return Action{Name: "install script", Cmd: "sh", Args: []string{"-c", "curl -fsSL https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.sh | sh"}}
	default:
		return Action{Name: "install script", Cmd: "sh", Args: []string{"-c", "curl -fsSL https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.sh | sh"}}
	}
}

func (a Action) String() string {
	parts := []string{quoteCommandPart(a.Cmd)}
	for _, arg := range a.Args {
		parts = append(parts, quoteCommandPart(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandPart(part string) string {
	if part == "" || strings.ContainsAny(part, " \t\r\n|&;<>(){}[]$`'\"\\*?") {
		return strconv.Quote(part)
	}
	return part
}

func (c Checker) cachePath() string {
	return filepath.Join(c.DataDir, CacheFilename)
}

func (c Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Checker) stale(lastChecked time.Time) bool {
	return lastChecked.IsZero() || lastChecked.Before(c.now().Add(-CheckInterval))
}

func isHomebrewPath(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	if isHomebrewPathShape(clean) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return false
	}
	return isHomebrewPathShape(filepath.Clean(resolved))
}

func isHomebrewPathShape(path string) bool {
	return strings.HasPrefix(path, string(filepath.Separator)+"opt"+string(filepath.Separator)+"homebrew"+string(filepath.Separator)) ||
		strings.Contains(path, string(filepath.Separator)+"Cellar"+string(filepath.Separator)+"whale"+string(filepath.Separator))
}

type semver struct {
	major int
	minor int
	patch int
}

func parseVersion(v string) (semver, bool) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(v))
	if match == nil {
		return semver{}, false
	}
	major, err1 := strconv.Atoi(match[1])
	minor, err2 := strconv.Atoi(match[2])
	patch, err3 := strconv.Atoi(match[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return semver{}, false
	}
	return semver{major: major, minor: minor, patch: patch}, true
}

func (v semver) after(other semver) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	return v.patch > other.patch
}
