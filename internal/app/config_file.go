package app

import (
	"bytes"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/securefs"
	"github.com/usewhale/whale/internal/store"
	"os"
	"path/filepath"
	"strings"
)

const (
	ConfigFileName      = "config.toml"
	LocalConfigFileName = "config.local.toml"
)

type FileConfig struct {
	Model           string `toml:"model,omitempty"`
	ReasoningEffort string `toml:"reasoning_effort,omitempty"`
	ThinkingEnabled *bool  `toml:"thinking_enabled,omitempty"`

	Permissions FilePermissionsConfig         `toml:"permissions,omitempty"`
	UI          FileUIConfig                  `toml:"ui,omitempty"`
	API         FileAPIConfig                 `toml:"api,omitempty"`
	Retry       FileRetryConfig               `toml:"retry,omitempty"`
	Tasks       FileTasksConfig               `toml:"tasks,omitempty"`
	Budget      FileBudgetConfig              `toml:"budget,omitempty"`
	MCP         FileMCPConfig                 `toml:"mcp,omitempty"`
	Context     FileContextConfig             `toml:"context,omitempty"`
	ProjectDoc  FileProjectDocConfig          `toml:"project_doc,omitempty"`
	Skills      FileSkillsConfig              `toml:"skills,omitempty"`
	Plugins     FilePluginsConfig             `toml:"plugins,omitempty"`
	Workflows   FileWorkflowsConfig           `toml:"workflows,omitempty"`
	Hooks       map[string][]agent.HookConfig `toml:"hooks,omitempty"`
}

type FileUIConfig struct {
	ViewMode                string `toml:"view_mode,omitempty"`
	ShowReasoning           *bool  `toml:"show_reasoning,omitempty"`
	CheckForUpdateOnStartup *bool  `toml:"check_for_update_on_startup,omitempty"`
}

type FilePermissionsConfig struct {
	Default           string            `toml:"default,omitempty"`
	AutoAccept        *bool             `toml:"auto_accept,omitempty"`
	Read              map[string]string `toml:"read,omitempty"`
	Edit              map[string]string `toml:"edit,omitempty"`
	Shell             map[string]string `toml:"shell,omitempty"`
	ExternalDirectory map[string]string `toml:"external_directory,omitempty"`
	MCP               map[string]string `toml:"mcp,omitempty"`
	Memory            map[string]string `toml:"memory,omitempty"`
	Task              map[string]string `toml:"task,omitempty"`
	MutatingTool      map[string]string `toml:"mutating_tool,omitempty"`
}

type FileAPIConfig struct {
	BaseURL string `toml:"base_url,omitempty"`
}

type FileRetryConfig struct {
	MaxAttempts       *int   `toml:"max_attempts,omitempty"`
	StreamMaxAttempts *int   `toml:"stream_max_attempts,omitempty"`
	StreamIdleTimeout string `toml:"stream_idle_timeout,omitempty"`
	MaxDelay          string `toml:"max_delay,omitempty"`
}

type FileTasksConfig struct {
	MaxParallelSubagents *int `toml:"max_parallel_subagents,omitempty"`
}

type FileBudgetConfig struct {
	SessionLimitUSD *float64 `toml:"session_limit_usd,omitempty"`
}

type FileMCPConfig struct {
	ConfigPath string `toml:"config_path,omitempty"`
}

type FileContextConfig struct {
	AutoCompact      *bool    `toml:"auto_compact,omitempty"`
	CompactThreshold *float64 `toml:"compact_threshold,omitempty"`
}

type FileProjectDocConfig struct {
	Enabled           *bool    `toml:"enabled,omitempty"`
	MaxBytes          *int     `toml:"max_bytes,omitempty"`
	FallbackFilenames []string `toml:"fallback_filenames,omitempty"`
}

type FileSkillsConfig struct {
	Enabled  []string `toml:"enabled,omitempty"`
	Disabled []string `toml:"disabled,omitempty"`
}

type FilePluginsConfig struct {
	Enabled  []string `toml:"enabled,omitempty"`
	Disabled []string `toml:"disabled,omitempty"`
}

type FileWorkflowsConfig struct {
	Trusted []string `toml:"trusted,omitempty"`
}

type LoadedConfig struct {
	Global             FileConfig
	GlobalLoaded       bool
	GlobalPath         string
	Project            FileConfig
	ProjectLoaded      bool
	ProjectPath        string
	ProjectLocal       FileConfig
	ProjectLocalLoaded bool
	ProjectLocalPath   string
}

func GlobalConfigPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = store.DefaultDataDir()
	}
	return filepath.Join(dataDir, ConfigFileName)
}

func ProjectConfigPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".whale", ConfigFileName)
}

func ProjectLocalConfigPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".whale", LocalConfigFileName)
}

func LoadConfigFiles(dataDir, workspaceRoot string) (LoadedConfig, error) {
	globalPath := GlobalConfigPath(dataDir)
	global, globalLoaded, err := LoadConfigFile(globalPath)
	if err != nil {
		return LoadedConfig{}, err
	}
	projectPath := ProjectConfigPath(workspaceRoot)
	project, projectLoaded, err := LoadConfigFile(projectPath)
	if err != nil {
		return LoadedConfig{}, err
	}
	projectLocalPath := ProjectLocalConfigPath(workspaceRoot)
	projectLocal, projectLocalLoaded, err := LoadConfigFile(projectLocalPath)
	if err != nil {
		return LoadedConfig{}, err
	}
	return LoadedConfig{
		Global:             global,
		GlobalLoaded:       globalLoaded,
		GlobalPath:         globalPath,
		Project:            project,
		ProjectLoaded:      projectLoaded,
		ProjectPath:        projectPath,
		ProjectLocal:       projectLocal,
		ProjectLocalLoaded: projectLocalLoaded,
		ProjectLocalPath:   projectLocalPath,
	}, nil
}

func LoadConfigFile(path string) (FileConfig, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileConfig{}, false, nil
		}
		return FileConfig{}, false, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	meta, err := toml.Decode(string(b), &cfg)
	if err != nil {
		return FileConfig{}, false, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := checkRemovedConfigKeys(path, meta); err != nil {
		return FileConfig{}, false, err
	}
	return cfg, true, nil
}

// removedConfigKeys maps pre-v0.1.9 permission keys, which no longer have a
// migration, to the modern setting that replaces them. Both the [permissions]
// table form and the original top-level form are listed.
var removedConfigKeys = map[string]string{
	"permissions.mode":                 "set permissions.default and the per-tool [permissions.*] tables instead",
	"permissions.allow_shell_prefixes": "add [permissions.shell] entries with an \"allow\" action instead",
	"permissions.deny_shell_prefixes":  "add [permissions.shell] entries with a \"deny\" action instead",
	"allow_shell_prefixes":             "add [permissions.shell] entries with an \"allow\" action instead",
	"deny_shell_prefixes":              "add [permissions.shell] entries with a \"deny\" action instead",
}

// checkRemovedConfigKeys rejects configs that still carry legacy permission
// keys. They decode without error but are silently dropped, so a user relying
// on a legacy deny would lose that protection without warning; failing loudly
// forces an explicit migration instead.
func checkRemovedConfigKeys(path string, meta toml.MetaData) error {
	for _, key := range meta.Undecoded() {
		if hint, ok := removedConfigKeys[key.String()]; ok {
			return fmt.Errorf("config %s uses removed permission key %q; %s", path, key.String(), hint)
		}
	}
	return nil
}

func SaveConfigFile(path string, cfg FileConfig) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := securefs.WritePrivateFile(path, buf.Bytes()); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
