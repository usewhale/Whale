package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/store"
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
	Budget      FileBudgetConfig              `toml:"budget,omitempty"`
	MCP         FileMCPConfig                 `toml:"mcp,omitempty"`
	Context     FileContextConfig             `toml:"context,omitempty"`
	ProjectDoc  FileProjectDocConfig          `toml:"project_doc,omitempty"`
	Skills      FileSkillsConfig              `toml:"skills,omitempty"`
	Plugins     FilePluginsConfig             `toml:"plugins,omitempty"`
	Hooks       map[string][]agent.HookConfig `toml:"hooks,omitempty"`
}

type FileUIConfig struct {
	ViewMode                string `toml:"view_mode,omitempty"`
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
	MaxDelay          string `toml:"max_delay,omitempty"`
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func ApplyLoadedConfig(cfg *Config, loaded LoadedConfig) error {
	if err := ApplyFileConfig(cfg, loaded.Global); err != nil {
		return err
	}
	if err := ApplyFileConfig(cfg, loaded.Project); err != nil {
		return err
	}
	if err := ApplyFileConfig(cfg, loaded.ProjectLocal); err != nil {
		return err
	}
	return nil
}

func ApplyFileConfig(cfg *Config, file FileConfig) error {
	if strings.TrimSpace(file.Model) != "" {
		cfg.Model = strings.TrimSpace(file.Model)
	}
	if strings.TrimSpace(file.ReasoningEffort) != "" {
		cfg.ReasoningEffort = strings.TrimSpace(file.ReasoningEffort)
	}
	if file.ThinkingEnabled != nil {
		cfg.ThinkingEnabled = *file.ThinkingEnabled
	}
	if strings.TrimSpace(file.UI.ViewMode) != "" {
		mode, err := NormalizeViewMode(file.UI.ViewMode)
		if err != nil {
			return err
		}
		cfg.ViewMode = mode
	}
	if file.UI.CheckForUpdateOnStartup != nil {
		cfg.CheckForUpdateOnStartup = *file.UI.CheckForUpdateOnStartup
	}
	if err := applyPermissionsConfig(cfg, file.Permissions); err != nil {
		return err
	}
	if file.Budget.SessionLimitUSD != nil {
		cfg.BudgetWarningUSD = *file.Budget.SessionLimitUSD
	}
	if strings.TrimSpace(file.MCP.ConfigPath) != "" {
		cfg.MCPConfigPath = expandUserPath(file.MCP.ConfigPath)
	}
	if strings.TrimSpace(file.API.BaseURL) != "" {
		cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(file.API.BaseURL), "/")
	}
	if file.Retry.MaxAttempts != nil {
		if *file.Retry.MaxAttempts <= 0 {
			return fmt.Errorf("invalid retry.max_attempts: must be greater than 0")
		}
		cfg.RetryMaxAttempts = *file.Retry.MaxAttempts
	}
	if file.Retry.StreamMaxAttempts != nil {
		if *file.Retry.StreamMaxAttempts <= 0 {
			return fmt.Errorf("invalid retry.stream_max_attempts: must be greater than 0")
		}
		cfg.RetryStreamMaxAttempts = *file.Retry.StreamMaxAttempts
	}
	if strings.TrimSpace(file.Retry.MaxDelay) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(file.Retry.MaxDelay))
		if err != nil {
			return fmt.Errorf("invalid retry.max_delay: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("invalid retry.max_delay: must be greater than 0")
		}
		cfg.RetryMaxDelay = d
	}
	if file.Context.AutoCompact != nil {
		cfg.AutoCompact = *file.Context.AutoCompact
	}
	if file.Context.CompactThreshold != nil {
		cfg.AutoCompactThreshold = *file.Context.CompactThreshold
	}
	if file.ProjectDoc.Enabled != nil {
		cfg.MemoryEnabled = *file.ProjectDoc.Enabled
	}
	if file.ProjectDoc.MaxBytes != nil {
		cfg.MemoryMaxChars = *file.ProjectDoc.MaxBytes
	}
	if len(file.ProjectDoc.FallbackFilenames) > 0 {
		cfg.MemoryFileOrder = strings.Join(trimList(file.ProjectDoc.FallbackFilenames), ",")
	}
	if len(file.Skills.Disabled) > 0 {
		cfg.SkillsDisabled = mergeNames(cfg.SkillsDisabled, file.Skills.Disabled)
	}
	if len(file.Skills.Enabled) > 0 {
		cfg.SkillsDisabled = removeNames(cfg.SkillsDisabled, file.Skills.Enabled)
	}
	if len(file.Plugins.Disabled) > 0 {
		cfg.PluginsDisabled = mergeNames(cfg.PluginsDisabled, file.Plugins.Disabled)
	}
	if len(file.Plugins.Enabled) > 0 {
		cfg.PluginsDisabled = removeNames(cfg.PluginsDisabled, file.Plugins.Enabled)
	}
	return nil
}

func LoadAndApplyConfig(cfg Config, workspaceRoot string) (Config, error) {
	base := DefaultConfig()
	base.DataDir = firstNonEmpty(strings.TrimSpace(cfg.DataDir), store.DefaultDataDir())

	loaded, err := LoadConfigFiles(base.DataDir, workspaceRoot)
	if err != nil {
		return Config{}, err
	}
	if err := ApplyLoadedConfig(&base, loaded); err != nil {
		return Config{}, err
	}
	overlayExplicitConfig(&base, cfg)
	base.ConfigLoaded = true
	return base, nil
}

func overlayExplicitConfig(dst *Config, src Config) {
	def := DefaultConfig()
	dst.DataDir = firstNonEmpty(strings.TrimSpace(src.DataDir), dst.DataDir)
	if src.ModelExplicit || (strings.TrimSpace(src.Model) != "" && src.Model != def.Model) {
		dst.Model = src.Model
		dst.ModelExplicit = src.ModelExplicit
	}
	if src.PermissionDefault != "" && src.PermissionDefault != def.PermissionDefault {
		dst.PermissionDefault = src.PermissionDefault
	}
	if len(src.PermissionRules) > 0 && !permissionRulesEqual(src.PermissionRules, def.PermissionRules) {
		dst.PermissionRules = append([]policy.PermissionRule{}, src.PermissionRules...)
	}
	if src.AutoAcceptPermissions != def.AutoAcceptPermissions {
		dst.AutoAcceptPermissions = src.AutoAcceptPermissions
	}
	if src.AutoCompact != def.AutoCompact {
		dst.AutoCompact = src.AutoCompact
	}
	if src.AutoCompactThreshold != def.AutoCompactThreshold {
		dst.AutoCompactThreshold = src.AutoCompactThreshold
	}
	if src.MemoryEnabled != def.MemoryEnabled {
		dst.MemoryEnabled = src.MemoryEnabled
	}
	if src.MemoryMaxChars != 0 && src.MemoryMaxChars != def.MemoryMaxChars {
		dst.MemoryMaxChars = src.MemoryMaxChars
	}
	if strings.TrimSpace(src.MemoryFileOrder) != "" && src.MemoryFileOrder != def.MemoryFileOrder {
		dst.MemoryFileOrder = src.MemoryFileOrder
	}
	if src.BudgetWarningUSD != def.BudgetWarningUSD {
		dst.BudgetWarningUSD = src.BudgetWarningUSD
	}
	if strings.TrimSpace(src.ReasoningEffort) != "" && src.ReasoningEffort != def.ReasoningEffort {
		dst.ReasoningEffort = src.ReasoningEffort
	}
	if src.ThinkingEnabled != def.ThinkingEnabled {
		dst.ThinkingEnabled = src.ThinkingEnabled
	}
	if src.CheckForUpdateOnStartup != def.CheckForUpdateOnStartup {
		dst.CheckForUpdateOnStartup = src.CheckForUpdateOnStartup
	}
	if strings.TrimSpace(src.ViewMode) != "" && src.ViewMode != def.ViewMode {
		dst.ViewMode = src.ViewMode
	}
	if src.RetryMaxAttempts != 0 && src.RetryMaxAttempts != def.RetryMaxAttempts {
		dst.RetryMaxAttempts = src.RetryMaxAttempts
	}
	if src.RetryStreamMaxAttempts != 0 && src.RetryStreamMaxAttempts != def.RetryStreamMaxAttempts {
		dst.RetryStreamMaxAttempts = src.RetryStreamMaxAttempts
	}
	if src.RetryMaxDelay != 0 && src.RetryMaxDelay != def.RetryMaxDelay {
		dst.RetryMaxDelay = src.RetryMaxDelay
	}
	if strings.TrimSpace(src.MCPConfigPath) != "" {
		dst.MCPConfigPath = src.MCPConfigPath
	}
	if strings.TrimSpace(src.APIBaseURL) != "" {
		dst.APIBaseURL = strings.TrimRight(strings.TrimSpace(src.APIBaseURL), "/")
	}
	if len(src.SkillsDisabled) > 0 {
		dst.SkillsDisabled = trimList(src.SkillsDisabled)
	}
	if len(src.PluginsDisabled) > 0 {
		dst.PluginsDisabled = trimList(src.PluginsDisabled)
	}
}

func permissionRulesEqual(a, b []policy.PermissionRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func applyPermissionsConfig(cfg *Config, file FilePermissionsConfig) error {
	if strings.TrimSpace(file.Default) != "" {
		action, err := policy.ParsePermissionAction(file.Default)
		if err != nil {
			return fmt.Errorf("invalid permissions.default: %w", err)
		}
		cfg.PermissionDefault = action
	}
	if file.AutoAccept != nil {
		cfg.AutoAcceptPermissions = *file.AutoAccept
	}
	rules, err := policy.RulesFromConfig(policy.PermissionConfig{
		Read:              file.Read,
		Edit:              file.Edit,
		Shell:             file.Shell,
		ExternalDirectory: file.ExternalDirectory,
		MCP:               file.MCP,
		Memory:            file.Memory,
		Task:              file.Task,
		MutatingTool:      file.MutatingTool,
	})
	if err != nil {
		return fmt.Errorf("invalid permissions: %w", err)
	}
	cfg.PermissionRules = append(cfg.PermissionRules, rules...)
	return nil
}

func SaveGlobalPreferences(dataDir, model, effort string, thinking bool) error {
	path := GlobalConfigPath(dataDir)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return err
	}
	cfg.Model = strings.TrimSpace(model)
	cfg.ReasoningEffort = strings.TrimSpace(effort)
	cfg.ThinkingEnabled = &thinking
	return SaveConfigFile(path, cfg)
}

func SaveGlobalViewMode(dataDir, mode string) error {
	mode, err := NormalizeViewMode(mode)
	if err != nil {
		return err
	}
	path := GlobalConfigPath(dataDir)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return err
	}
	cfg.UI.ViewMode = mode
	return SaveConfigFile(path, cfg)
}

func NormalizeViewMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ViewModeDefault:
		return ViewModeDefault, nil
	case ViewModeFocus:
		return ViewModeFocus, nil
	default:
		return "", fmt.Errorf("invalid ui.view_mode: %s (want %q or %q)", mode, ViewModeDefault, ViewModeFocus)
	}
}

func ConfigSources(loaded LoadedConfig) []string {
	out := make([]string, 0, 3)
	if loaded.ProjectLocalLoaded {
		out = append(out, loaded.ProjectLocalPath)
	}
	if loaded.ProjectLoaded {
		out = append(out, loaded.ProjectPath)
	}
	if loaded.GlobalLoaded {
		out = append(out, loaded.GlobalPath)
	}
	return out
}

func hooksFromFileConfig(cfg FileConfig) agent.HookSettings {
	out := agent.HookSettings{Hooks: map[agent.HookEvent][]agent.HookConfig{}}
	for raw, hooks := range cfg.Hooks {
		ev := agent.HookEvent(strings.TrimSpace(raw))
		switch ev {
		case agent.HookEventPreToolUse, agent.HookEventPostToolUse, agent.HookEventUserPromptSubmit, agent.HookEventStop:
			out.Hooks[ev] = append(out.Hooks[ev], hooks...)
		}
	}
	return out
}

func countFileConfigHooks(cfg FileConfig) int {
	return countHooks(hooksFromFileConfig(cfg))
}

func trimList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func expandUserPath(path string) string {
	path = strings.TrimSpace(path)
	if runtime.GOOS == "windows" {
		path = expandWindowsPercentEnv(path)
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func expandWindowsPercentEnv(path string) string {
	var out strings.Builder
	for i := 0; i < len(path); {
		if path[i] != '%' {
			out.WriteByte(path[i])
			i++
			continue
		}
		end := strings.IndexByte(path[i+1:], '%')
		if end < 0 {
			out.WriteByte(path[i])
			i++
			continue
		}
		end += i + 1
		name := path[i+1 : end]
		if name == "" {
			out.WriteString("%%")
			i = end + 1
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			out.WriteString(value)
		} else {
			out.WriteString(path[i : end+1])
		}
		i = end + 1
	}
	return out.String()
}
