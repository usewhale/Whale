package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/securefs"
	"github.com/usewhale/whale/internal/store"
)

type DoctorLevel string

const (
	DoctorOK   DoctorLevel = "ok"
	DoctorWarn DoctorLevel = "warn"
	DoctorFail DoctorLevel = "fail"
)

type DoctorCheck struct {
	Label  string
	Level  DoctorLevel
	Detail string
}

type DoctorReport struct {
	Workspace string
	DataDir   string
	Checks    []DoctorCheck
}

type apiKeySource string

const (
	apiKeySourceMissing     apiKeySource = "missing"
	apiKeySourceEnv         apiKeySource = "env"
	apiKeySourceCredentials apiKeySource = "credentials"
)

type fileState struct {
	Path    string
	Present bool
	Err     error
}

func RunDoctor(ctx context.Context, cfg Config, workspaceRoot string) (DoctorReport, error) {
	dataDir := strings.TrimSpace(cfg.DataDir)
	if dataDir == "" {
		dataDir = store.DefaultDataDir()
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if resolved, err := LoadAndApplyConfig(cfg, workspaceRoot); err == nil {
		cfg = resolved
		dataDir = cfg.DataDir
	}
	order := parseCSVList(cfg.MemoryFileOrder)
	if len(order) == 0 {
		order = defaults.DefaultMemoryFileOrder()
	}

	apiKeyCheck, source, key := doctorCheckAPIKey(dataDir)
	credsCheck := doctorCheckCredentials(dataDir)
	loadedConfig, configErr := LoadConfigFiles(dataDir, workspaceRoot)
	configCheck := doctorCheckConfig(loadedConfig, configErr)
	legacyCheck := doctorCheckLegacyConfig(dataDir, workspaceRoot, len(ConfigSources(loadedConfig)) > 0)
	dataDirCheck := doctorCheckDataDir(dataDir)
	dataDirOverrideCheck := doctorCheckDataDirOverride(runtime.GOOS, os.Getenv, dataDir)
	dataDirACLCheck := doctorCheckDataDirACL(runtime.GOOS, dataDir)
	apiReachCheck := doctorCheckAPIReach(ctx, key)
	memoryCheck := doctorCheckMemory(workspaceRoot, order, cfg.MemoryMaxChars)
	hooksCheck := doctorCheckHooks(dataDir, workspaceRoot)
	pluginChecks := doctorCheckPlugins(ctx, cfg, workspaceRoot)

	_ = source

	checks := []DoctorCheck{
		apiKeyCheck,
		credsCheck,
		configCheck,
		legacyCheck,
		dataDirCheck,
	}
	if dataDirOverrideCheck.Level != "" {
		checks = append(checks, dataDirOverrideCheck)
	}
	if dataDirACLCheck.Level != "" {
		checks = append(checks, dataDirACLCheck)
	}
	checks = append(checks, apiReachCheck, memoryCheck)
	if hooksCheck.Level != "" {
		checks = append(checks, hooksCheck)
	}
	checks = append(checks, pluginChecks...)

	return DoctorReport{
		Workspace: workspaceRoot,
		DataDir:   dataDir,
		Checks:    checks,
	}, nil
}

func doctorCheckPlugins(ctx context.Context, cfg Config, workspaceRoot string) []DoctorCheck {
	mgr := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspaceRoot}, cfg.PluginsDisabled)
	var checks []DoctorCheck
	statuses := mgr.Statuses()
	enabled := 0
	for _, st := range statuses {
		if st.Enabled {
			enabled++
		}
	}
	checks = append(checks, DoctorCheck{
		Label:  "plugins",
		Level:  DoctorOK,
		Detail: fmt.Sprintf("%d enabled, %d disabled", enabled, len(statuses)-enabled),
	})
	for _, diag := range mgr.Diagnostics(ctx) {
		level := DoctorOK
		switch diag.Level {
		case plugins.DiagnosticWarn:
			level = DoctorWarn
		case plugins.DiagnosticFail:
			level = DoctorFail
		}
		label := "plugin " + diag.PluginID
		if diag.Label != "" {
			label += " " + diag.Label
		}
		checks = append(checks, DoctorCheck{Label: label, Level: level, Detail: diag.Detail})
	}
	return checks
}

func (r DoctorReport) Summary() (ok, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Level {
		case DoctorOK:
			ok++
		case DoctorWarn:
			warn++
		case DoctorFail:
			fail++
		}
	}
	return ok, warn, fail
}

func (r DoctorReport) HasFailures() bool {
	_, _, fail := r.Summary()
	return fail > 0
}

func doctorCheckAPIKey(dataDir string) (DoctorCheck, apiKeySource, string) {
	key, source, err := resolveDeepSeekAPIKey(dataDir)
	if err != nil {
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorFail,
			Detail: err.Error(),
		}, apiKeySourceMissing, ""
	}
	if strings.TrimSpace(key) == "" {
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorFail,
			Detail: "not configured — run `whale setup` or set `DEEPSEEK_API_KEY`",
		}, apiKeySourceMissing, ""
	}
	switch source {
	case apiKeySourceEnv:
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorOK,
			Detail: fmt.Sprintf("set via env DEEPSEEK_API_KEY (%s)", tailKey(key)),
		}, source, key
	case apiKeySourceCredentials:
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorOK,
			Detail: fmt.Sprintf("from %s (%s)", credentialsPath(dataDir), tailKey(key)),
		}, source, key
	default:
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorFail,
			Detail: "not configured — run `whale setup` or set `DEEPSEEK_API_KEY`",
		}, apiKeySourceMissing, ""
	}
}

func doctorCheckCredentials(dataDir string) DoctorCheck {
	st := readCredentialsState(dataDir)
	switch {
	case st.Err != nil:
		return DoctorCheck{
			Label:  "credentials",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s unreadable — %v", st.Path, st.Err),
		}
	case !st.Present:
		return DoctorCheck{
			Label:  "credentials",
			Level:  DoctorWarn,
			Detail: fmt.Sprintf("%s missing — `whale setup` writes one", st.Path),
		}
	default:
		return DoctorCheck{
			Label:  "credentials",
			Level:  DoctorOK,
			Detail: st.Path,
		}
	}
}

func doctorCheckDataDir(dataDir string) DoctorCheck {
	sessionsDir := store.DefaultSessionsDir(dataDir)
	if err := securefs.MkdirPrivate(dataDir); err != nil {
		return DoctorCheck{
			Label:  "data dir",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s create failed — %v", dataDir, err),
		}
	}
	if err := securefs.MkdirPrivate(sessionsDir); err != nil {
		return DoctorCheck{
			Label:  "data dir",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s create failed — %v", sessionsDir, err),
		}
	}
	probe, err := os.CreateTemp(dataDir, ".doctor-probe-*")
	if err != nil {
		return DoctorCheck{
			Label:  "data dir",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s not writable — %v", dataDir, err),
		}
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)
	return DoctorCheck{
		Label:  "data dir",
		Level:  DoctorOK,
		Detail: fmt.Sprintf("%s writable · sessions %s", dataDir, sessionsDir),
	}
}

func doctorCheckDataDirOverride(goos string, getenv func(string) string, dataDir string) DoctorCheck {
	whaleHome := strings.TrimSpace(getenv(store.DataDirEnv))
	if whaleHome != "" {
		detail := fmt.Sprintf("using %s=%s", store.DataDirEnv, whaleHome)
		if strings.TrimSpace(dataDir) != "" && filepath.Clean(whaleHome) != filepath.Clean(dataDir) {
			detail = fmt.Sprintf("%s is set; current data dir is %s", store.DataDirEnv, dataDir)
		}
		return DoctorCheck{
			Label:  "data dir override",
			Level:  DoctorOK,
			Detail: detail,
		}
	}
	if goos == "windows" {
		return DoctorCheck{
			Label:  "data dir override",
			Level:  DoctorOK,
			Detail: fmt.Sprintf("set %s to use a custom Whale data directory", store.DataDirEnv),
		}
	}
	return DoctorCheck{}
}

func doctorCheckDataDirACL(goos, dataDir string) DoctorCheck {
	if goos != "windows" {
		return DoctorCheck{}
	}
	status := securefs.CheckPrivatePath(dataDir)
	level := DoctorOK
	if !status.Protected {
		level = DoctorWarn
	}
	return DoctorCheck{
		Label:  "data dir acl",
		Level:  level,
		Detail: status.Detail,
	}
}

func doctorCheckAPIReach(ctx context.Context, key string) DoctorCheck {
	if strings.TrimSpace(key) == "" {
		return DoctorCheck{
			Label:  "api reach",
			Level:  DoctorWarn,
			Detail: "skipped — no API key configured",
		}
	}
	msg, err := CheckDeepSeekAPIReachability(ctx, key)
	if err != nil {
		level := DoctorFail
		if errors.Is(err, errDoctorAuth) {
			level = DoctorFail
		}
		return DoctorCheck{
			Label:  "api reach",
			Level:  level,
			Detail: msg,
		}
	}
	return DoctorCheck{
		Label:  "api reach",
		Level:  DoctorOK,
		Detail: msg,
	}
}

func doctorCheckMemory(workspaceRoot string, fileOrder []string, maxChars int) DoctorCheck {
	pm, ok := memory.ReadProjectMemory(workspaceRoot, fileOrder, maxChars)
	if !ok {
		return DoctorCheck{
			Label:  "project doc",
			Level:  DoctorWarn,
			Detail: fmt.Sprintf("no project doc file found (%s)", strings.Join(fileOrder, ", ")),
		}
	}
	detail := pm.Path
	if pm.Truncated {
		detail += " (truncated)"
	}
	return DoctorCheck{
		Label:  "project doc",
		Level:  DoctorOK,
		Detail: detail,
	}
}

func doctorCheckConfig(loaded LoadedConfig, err error) DoctorCheck {
	if err != nil {
		return DoctorCheck{
			Label:  "config",
			Level:  DoctorFail,
			Detail: err.Error(),
		}
	}
	sources := ConfigSources(loaded)
	if len(sources) == 0 {
		return DoctorCheck{
			Label:  "config",
			Level:  DoctorOK,
			Detail: "no config.toml or config.local.toml found — defaults will be used",
		}
	}
	return DoctorCheck{
		Label:  "config",
		Level:  DoctorOK,
		Detail: strings.Join(sources, ", "),
	}
}

func doctorCheckLegacyConfig(dataDir, workspaceRoot string, hasActiveConfig bool) DoctorCheck {
	paths := []string{
		preferencesPath(dataDir),
		filepath.Join(dataDir, "settings.json"),
		filepath.Join(workspaceRoot, ".whale", "settings.json"),
	}
	found := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		} else if err != nil && !os.IsNotExist(err) {
			return DoctorCheck{
				Label:  "legacy config",
				Level:  DoctorFail,
				Detail: fmt.Sprintf("%s unreadable — %v", path, err),
			}
		}
	}
	if len(found) == 0 {
		return DoctorCheck{
			Label:  "legacy config",
			Level:  DoctorOK,
			Detail: "no obsolete config files found",
		}
	}
	if hasActiveConfig {
		return DoctorCheck{
			Label:  "legacy config",
			Level:  DoctorWarn,
			Detail: fmt.Sprintf("%d obsolete Whale v0.1.8-or-earlier file(s) ignored — config.toml is active; no migration needed", len(found)),
		}
	}
	return DoctorCheck{
		Label:  "legacy config",
		Level:  DoctorWarn,
		Detail: fmt.Sprintf("%d obsolete Whale v0.1.8-or-earlier file(s) found — run `whale migrate-config` if you used those versions", len(found)),
	}
}

func doctorCheckHooks(dataDir, workspaceRoot string) DoctorCheck {
	loaded, err := LoadConfigFiles(dataDir, workspaceRoot)
	if err != nil {
		return DoctorCheck{
			Label:  "hooks",
			Level:  DoctorFail,
			Detail: err.Error(),
		}
	}
	totalHooks := 0
	loadedFiles := 0
	if loaded.ProjectLoaded {
		totalHooks += countFileConfigHooks(loaded.Project)
		loadedFiles++
	}
	if loaded.ProjectLocalLoaded {
		totalHooks += countFileConfigHooks(loaded.ProjectLocal)
		loadedFiles++
	}
	if loaded.GlobalLoaded {
		totalHooks += countFileConfigHooks(loaded.Global)
		loadedFiles++
	}
	if totalHooks == 0 {
		return DoctorCheck{}
	}
	return DoctorCheck{
		Label:  "hooks",
		Level:  DoctorOK,
		Detail: fmt.Sprintf("%d hook(s) from %d file(s)", totalHooks, loadedFiles),
	}
}

func countHooks(st agent.HookSettings) int {
	n := 0
	for _, hooks := range st.Hooks {
		for _, hook := range hooks {
			if strings.TrimSpace(hook.Command) != "" {
				n++
			}
		}
	}
	return n
}

func resolveDeepSeekAPIKey(dataDir string) (string, apiKeySource, error) {
	if v := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")); v != "" {
		return v, apiKeySourceEnv, nil
	}
	creds, err := LoadCredentials(dataDir)
	if err != nil {
		return "", apiKeySourceMissing, err
	}
	if v := strings.TrimSpace(creds.DeepSeekAPIKey); v != "" {
		return v, apiKeySourceCredentials, nil
	}
	return "", apiKeySourceMissing, nil
}

func readCredentialsState(dataDir string) fileState {
	path := credentialsPath(dataDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileState{Path: path}
		}
		return fileState{Path: path, Present: true, Err: err}
	}
	var creds Credentials
	if err := json.Unmarshal(b, &creds); err != nil {
		return fileState{Path: path, Present: true, Err: fmt.Errorf("unmarshal credentials: %w", err)}
	}
	return fileState{Path: path, Present: true}
}

func tailKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if len(trimmed) <= 4 {
		return trimmed
	}
	return "…" + trimmed[len(trimmed)-4:]
}

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
