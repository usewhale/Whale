package app

import (
	"context"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/store"
	"os"
	"runtime"
	"strings"
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

// DoctorOptions controls which checks RunDoctor performs.
type DoctorOptions struct {
	// SkipNetworkChecks skips network-dependent checks such as API reachability,
	// which can block the caller for several seconds on timeout.
	SkipNetworkChecks bool
}

func RunDoctor(ctx context.Context, cfg Config, workspaceRoot string, opts ...DoctorOptions) (DoctorReport, error) {
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
	var skipNet bool
	for _, o := range opts {
		if o.SkipNetworkChecks {
			skipNet = true
		}
	}
	var apiReachCheck DoctorCheck
	if !skipNet {
		apiReachCheck = doctorCheckAPIReach(ctx, key)
	}
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
	if !skipNet {
		checks = append(checks, apiReachCheck)
	}
	checks = append(checks, memoryCheck)
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
