package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/build"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/tui"
)

type cliOptions struct {
	cfg                        app.Config
	dangerouslySkipPermissions bool
}

func Execute() error {
	opts := &cliOptions{cfg: app.DefaultConfig()}
	root := newRootCmd(opts)
	return root.Execute()
}

func bindPersistentFlags(c *cobra.Command, opts *cliOptions) {
	c.PersistentFlags().StringVarP(&opts.cfg.Model, "model", "m", opts.cfg.Model, "Model to use ("+strings.Join(defaults.SupportedModels(), "|")+")")
	c.PersistentFlags().BoolVar(&opts.cfg.ThinkingEnabled, "thinking", opts.cfg.ThinkingEnabled, "Override thinking for this run only")
	c.PersistentFlags().StringVar(&opts.cfg.ReasoningEffort, "effort", opts.cfg.ReasoningEffort, "Override reasoning effort for this run only (high|max)")
	c.PersistentFlags().BoolVar(&opts.dangerouslySkipPermissions, "dangerously-skip-permissions", false, "Skip tool approval prompts for this run; extremely dangerous")
	c.Flags().BoolP("version", "V", false, "Print version")
}

func runLoop(opts *cliOptions, start app.StartOptions) error {
	return tui.Run(opts.cfg, start)
}

func prepareCLIConfig(cmd *cobra.Command, opts *cliOptions) error {
	flagCfg := opts.cfg
	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	cfg, err := app.LoadAndApplyConfig(flagCfg, workspaceRoot)
	if err != nil {
		return err
	}
	if flagChanged(cmd, "model") {
		cfg.Model = flagCfg.Model
		cfg.ModelExplicit = true
	}
	if flagChanged(cmd, "thinking") {
		cfg.ThinkingEnabled = flagCfg.ThinkingEnabled
	}
	if flagChanged(cmd, "effort") {
		effort, err := validateEffort(flagCfg.ReasoningEffort)
		if err != nil {
			return err
		}
		cfg.ReasoningEffort = effort
	}
	if flagChanged(cmd, "dangerously-skip-permissions") && opts.dangerouslySkipPermissions {
		cfg.ApprovalMode = string(policy.ApprovalModeNever)
	}
	opts.cfg = cfg
	return validateModel(opts.cfg.Model)
}

func flagChanged(cmd *cobra.Command, name string) bool {
	for _, flags := range []*pflag.FlagSet{
		cmd.Flags(),
		cmd.PersistentFlags(),
		cmd.InheritedFlags(),
		cmd.Root().PersistentFlags(),
	} {
		if f := flags.Lookup(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}

func validateModel(v string) error {
	if !defaults.IsSupportedModel(v) {
		return fmt.Errorf("unsupported model: %s", v)
	}
	return nil
}

func validateEffort(v string) (string, error) {
	normalized := app.NormalizeEffort(v)
	for _, supported := range app.SupportedReasoningEfforts() {
		if strings.EqualFold(normalized, supported) && strings.EqualFold(strings.TrimSpace(v), normalized) {
			return normalized, nil
		}
	}
	return "", fmt.Errorf("unsupported effort: %s (supported: %s)", v, strings.Join(app.SupportedReasoningEfforts(), ", "))
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
	root.AddCommand(newMigrateConfigCmd(opts))
	root.AddCommand(newSetupCmd(opts))
	root.AddCommand(newResumeCmd(opts))
	return root
}
