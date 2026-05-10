package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/build"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/tui"
)

type cliOptions struct {
	cfg     app.Config
	session string
	mode    string
	configs []string
}

func Execute() error {
	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	return root.Execute()
}

func bindPersistentFlags(c *cobra.Command, opts *cliOptions) {
	c.PersistentFlags().StringVar(&opts.cfg.DataDir, "data-dir", opts.cfg.DataDir, "Whale data directory")
	c.PersistentFlags().StringVar(&opts.cfg.ApprovalMode, "approval-mode", opts.cfg.ApprovalMode, "Tool approval mode: on-request|never-ask")
	c.PersistentFlags().StringVar(&opts.cfg.AllowPrefixes, "allow-prefixes", "", "Comma-separated shell command prefixes to auto-allow")
	c.PersistentFlags().StringVar(&opts.cfg.DenyPrefixes, "deny-prefixes", "", "Comma-separated shell command prefixes to deny")
	c.PersistentFlags().BoolVar(&opts.cfg.AutoCompact, "auto-compact", opts.cfg.AutoCompact, "Enable auto compact before request send")
	c.PersistentFlags().Float64Var(&opts.cfg.AutoCompactThreshold, "auto-compact-threshold", opts.cfg.AutoCompactThreshold, "Auto compact trigger threshold ratio")
	c.PersistentFlags().IntVar(&opts.cfg.ContextWindow, "model-context-window", opts.cfg.ContextWindow, "Model context window used by local estimator")
	c.PersistentFlags().BoolVar(&opts.cfg.MemoryEnabled, "memory-enabled", opts.cfg.MemoryEnabled, "Enable project memory file injection")
	c.PersistentFlags().IntVar(&opts.cfg.MemoryMaxChars, "memory-max-chars", opts.cfg.MemoryMaxChars, "Max chars loaded from project memory file")
	c.PersistentFlags().StringVar(&opts.cfg.MemoryFileOrder, "memory-file-order", opts.cfg.MemoryFileOrder, "Comma-separated project memory file priority")
	c.PersistentFlags().StringVar(&opts.cfg.MCPConfigPath, "mcp-config", opts.cfg.MCPConfigPath, "Path to Whale MCP config file")
	c.PersistentFlags().Float64Var(&opts.cfg.BudgetWarningUSD, "budget-warning-usd", 0, "Warn at >=80% and >=100% of cumulative session token cost estimate; 0 disables")
	c.PersistentFlags().StringVarP(&opts.cfg.Model, "model", "m", opts.cfg.Model, "Model to use ("+strings.Join(defaults.SupportedModels(), "|")+")")
	c.PersistentFlags().StringArrayVar(&opts.configs, "config", nil, "Config overrides (supports: model_reasoning_effort=...)")
	c.PersistentFlags().StringVar(&opts.session, "session", "", "Force startup session id")
	c.PersistentFlags().StringVar(&opts.mode, "mode", "", "Force startup mode: plan|agent")
	c.Flags().BoolP("version", "V", false, "Print version")
	setFlagDefaultForHelp(c, "data-dir", "~/.whale")

	hideRootFlag(c, "data-dir")
	hideRootFlag(c, "approval-mode")
	hideRootFlag(c, "allow-prefixes")
	hideRootFlag(c, "deny-prefixes")
	hideRootFlag(c, "auto-compact")
	hideRootFlag(c, "auto-compact-threshold")
	hideRootFlag(c, "budget-warning-usd")
	hideRootFlag(c, "config")
	hideRootFlag(c, "memory-enabled")
	hideRootFlag(c, "memory-file-order")
	hideRootFlag(c, "memory-max-chars")
	hideRootFlag(c, "mcp-config")
	hideRootFlag(c, "mode")
	hideRootFlag(c, "model-context-window")
	hideRootFlag(c, "session")
}

func hideRootFlag(c *cobra.Command, name string) {
	_ = c.PersistentFlags().MarkHidden(name)
}

func setFlagDefaultForHelp(c *cobra.Command, name string, value string) {
	if f := c.PersistentFlags().Lookup(name); f != nil {
		f.DefValue = value
	}
}

func runLoop(opts *cliOptions, start app.StartOptions) error {
	if strings.TrimSpace(start.SessionID) == "" {
		start.SessionID = opts.session
	}
	if strings.TrimSpace(start.ModeOverride) == "" {
		start.ModeOverride = opts.mode
	}
	return tui.Run(opts.cfg, start)
}

func prepareCLIConfig(cmd *cobra.Command, opts *cliOptions) error {
	opts.cfg.ModelExplicit = false
	if flag := cmd.Flags().Lookup("model"); flag != nil && flag.Changed {
		opts.cfg.ModelExplicit = true
	}
	if err := applyCLIConfigs(&opts.cfg, opts.configs); err != nil {
		return err
	}
	return validateModel(opts.cfg.Model)
}

func applyCLIConfigs(cfg *app.Config, entries []string) error {
	for _, raw := range entries {
		pair := strings.TrimSpace(raw)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("invalid --config: %s", raw)
		}
		key := strings.TrimSpace(k)
		val := strings.Trim(strings.TrimSpace(v), "\"")
		switch key {
		case "model_reasoning_effort":
			mapped, err := normalizeEffort(val)
			if err != nil {
				return err
			}
			cfg.ReasoningEffort = mapped
		default:
			return fmt.Errorf("unsupported --config key: %s", key)
		}
	}
	return nil
}

func validateModel(v string) error {
	if !defaults.IsSupportedModel(v) {
		return fmt.Errorf("unsupported model: %s", v)
	}
	return nil
}

func validateEffort(v string) error {
	_, err := normalizeEffort(v)
	return err
}

func normalizeEffort(v string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(v))
	switch e {
	case "high", "max":
		return e, nil
	case "low", "medium":
		return "high", nil
	case "xhigh":
		return "max", nil
	default:
		return "", fmt.Errorf("unsupported model_reasoning_effort: %s", v)
	}
}

func newRootCmd(opts *cliOptions) *cobra.Command {
	root := &cobra.Command{
		Use:     "whale",
		Short:   "Whale: DeepSeek-native coding agent for the terminal.",
		Version: build.CurrentVersion(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command: %s", args[0])
			}
			if err := prepareCLIConfig(cmd, opts); err != nil {
				return err
			}
			return runLoop(opts, app.StartOptions{NewSession: true})
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	bindPersistentFlags(root, opts)
	root.AddCommand(newExecCmd(opts))
	root.AddCommand(newDoctorCmd(opts))
	root.AddCommand(newSetupCmd(opts))
	root.AddCommand(newResumeCmd(opts))
	return root
}
