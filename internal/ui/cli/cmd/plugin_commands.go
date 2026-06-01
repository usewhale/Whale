package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/plugins"
)

func newPluginCmd(opts *cliOptions) *cobra.Command {
	c := &cobra.Command{
		Use:   "plugin",
		Short: "Manage Whale plugins",
		Args:  cobra.NoArgs,
	}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preparePluginCLI(cmd, opts); err != nil {
				return err
			}
			return runPluginList(cmd.OutOrStdout(), opts.cfg)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "install <path>",
		Short: "Install a local plugin directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preparePluginCLI(cmd, opts); err != nil {
				return err
			}
			return runPluginInstall(cmd.OutOrStdout(), opts.cfg, args[0])
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "uninstall <id>",
		Short: "Uninstall a local plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preparePluginCLI(cmd, opts); err != nil {
				return err
			}
			return runPluginUninstall(cmd.OutOrStdout(), opts.cfg, args[0])
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "enable <id>",
		Short: "Enable a plugin for this project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preparePluginCLI(cmd, opts); err != nil {
				return err
			}
			return runPluginSetEnabled(cmd.OutOrStdout(), opts.cfg, args[0], true)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "disable <id>",
		Short: "Disable a plugin for this project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preparePluginCLI(cmd, opts); err != nil {
				return err
			}
			return runPluginSetEnabled(cmd.OutOrStdout(), opts.cfg, args[0], false)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "inspect <id>",
		Short: "Inspect an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preparePluginCLI(cmd, opts); err != nil {
				return err
			}
			return runPluginInspect(cmd.OutOrStdout(), opts.cfg, args[0])
		},
	})
	return c
}

func preparePluginCLI(cmd *cobra.Command, opts *cliOptions) error {
	if err := rejectWorktreeFlag(cmd); err != nil {
		return err
	}
	return prepareCLIConfig(cmd, opts)
}

func runPluginList(out io.Writer, cfg app.Config) error {
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	mgr := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspace}, cfg.Plugins)
	statuses := mgr.Statuses()
	if len(statuses) == 0 {
		fmt.Fprintln(out, "No plugins installed.")
		return nil
	}
	fmt.Fprintln(out, "Installed plugins:")
	for _, st := range statuses {
		state := "disabled"
		if st.Enabled {
			state = "enabled"
		}
		components := pluginComponentSummary(st.Manifest)
		if components == "" {
			components = "-"
		}
		fmt.Fprintf(out, "  %-24s %-10s %-8s %s\n", st.Manifest.ID, state, st.Manifest.Version, components)
	}
	return nil
}

func runPluginInstall(out io.Writer, cfg app.Config, path string) error {
	res, err := plugins.InstallLocal(cfg.DataDir, path)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "installed plugin: %s@%s\n", res.Record.ID, res.Record.Version)
	fmt.Fprintf(out, "source: %s\n", res.Record.SourcePath)
	fmt.Fprintf(out, "cache: %s\n", res.Record.InstallPath)
	if len(res.Record.Diagnostics) > 0 {
		fmt.Fprintln(out, "diagnostics:")
		for _, diag := range res.Record.Diagnostics {
			fmt.Fprintf(out, "  %s: %s\n", diag.Label, diag.Detail)
		}
	}
	return nil
}

func runPluginUninstall(out io.Writer, cfg app.Config, id string) error {
	removed, err := plugins.UninstallLocal(cfg.DataDir, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "uninstalled plugin: %s\n", removed.ID)
	return nil
}

func runPluginSetEnabled(out io.Writer, cfg app.Config, id string, enabled bool) error {
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	_, msg, err := app.SetPluginEnabledConfig(cfg, workspace, id, enabled)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, msg)
	return nil
}

func runPluginInspect(out io.Writer, cfg app.Config, id string) error {
	id = plugins.NormalizePluginID(id)
	if id == "" {
		return fmt.Errorf("plugin id must not be empty")
	}
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	mgr := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspace}, cfg.Plugins)
	status, ok := mgr.Status(id)
	if !ok {
		return fmt.Errorf("plugin not installed: %s", id)
	}
	if record, found, err := plugins.FindInstalled(cfg.DataDir, id); err != nil {
		return err
	} else if found {
		printInstalledRecord(out, record, status.Enabled, status.Diagnostics)
		return nil
	}
	printPluginStatus(out, status)
	return nil
}

func printInstalledRecord(out io.Writer, record plugins.InstalledRecord, enabled bool, diagnostics []plugins.Diagnostic) {
	manifest := record.Manifest()
	fmt.Fprintf(out, "%s\n", manifest.Name)
	fmt.Fprintf(out, "id: %s\n", manifest.ID)
	fmt.Fprintf(out, "version: %s\n", manifest.Version)
	if manifest.Description != "" {
		fmt.Fprintf(out, "description: %s\n", manifest.Description)
	}
	if enabled {
		fmt.Fprintln(out, "status: enabled")
	} else {
		fmt.Fprintln(out, "status: disabled")
	}
	if len(manifest.Authors) > 0 {
		fmt.Fprintf(out, "authors: %s\n", strings.Join(manifest.Authors, ", "))
	}
	if manifest.License != "" {
		fmt.Fprintf(out, "license: %s\n", manifest.License)
	}
	if manifest.Homepage != "" {
		fmt.Fprintf(out, "homepage: %s\n", manifest.Homepage)
	}
	if manifest.Repository != "" {
		fmt.Fprintf(out, "repository: %s\n", manifest.Repository)
	}
	if len(manifest.Keywords) > 0 {
		fmt.Fprintf(out, "keywords: %s\n", strings.Join(manifest.Keywords, ", "))
	}
	fmt.Fprintf(out, "source: %s\n", record.SourcePath)
	fmt.Fprintf(out, "cache: %s\n", record.InstallPath)
	printComponents(out, manifest)
	printDiagnostics(out, diagnostics)
}

func printPluginStatus(out io.Writer, status plugins.PluginStatus) {
	manifest := status.Manifest
	fmt.Fprintf(out, "%s\n", manifest.Name)
	fmt.Fprintf(out, "id: %s\n", manifest.ID)
	fmt.Fprintf(out, "version: %s\n", manifest.Version)
	if manifest.Description != "" {
		fmt.Fprintf(out, "description: %s\n", manifest.Description)
	}
	if manifest.Official {
		fmt.Fprintln(out, "source: built-in")
	}
	if status.Enabled {
		fmt.Fprintln(out, "status: enabled")
	} else {
		fmt.Fprintln(out, "status: disabled")
	}
	printComponents(out, manifest)
	if len(status.Tools) > 0 {
		fmt.Fprintf(out, "tools: %s\n", strings.Join(status.Tools, ", "))
	}
	if len(status.Skills) > 0 {
		fmt.Fprintf(out, "skills: %s\n", strings.Join(status.Skills, ", "))
	}
	if len(status.Agents) > 0 {
		fmt.Fprintf(out, "agents: %s\n", strings.Join(status.Agents, ", "))
	}
	if len(status.Rules) > 0 {
		fmt.Fprintf(out, "rules: %s\n", strings.Join(status.Rules, ", "))
	}
	printDiagnostics(out, status.Diagnostics)
}

func printComponents(out io.Writer, manifest plugins.Manifest) {
	components := componentPairs(manifest)
	if len(components) == 0 {
		return
	}
	fmt.Fprintln(out, "components:")
	for _, pair := range components {
		fmt.Fprintf(out, "  %s: %s\n", pair[0], pair[1])
	}
}

func printDiagnostics(out io.Writer, diags []plugins.Diagnostic) {
	if len(diags) == 0 {
		return
	}
	fmt.Fprintln(out, "diagnostics:")
	for _, diag := range diags {
		level := string(diag.Level)
		if level == "" {
			level = "warn"
		}
		fmt.Fprintf(out, "  %s %s: %s\n", level, diag.Label, diag.Detail)
	}
}

func pluginComponentSummary(manifest plugins.Manifest) string {
	pairs := componentPairs(manifest)
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair[0])
	}
	return strings.Join(parts, ",")
}

func componentPairs(manifest plugins.Manifest) [][2]string {
	pairs := [][2]string{
		{"skills", manifest.Components.Skills},
		{"commands", manifest.Components.Commands},
		{"hooks", manifest.Components.Hooks},
		{"mcp", manifest.Components.MCP},
		{"rules", manifest.Components.Rules},
		{"agents", manifest.Components.Agents},
	}
	out := pairs[:0]
	for _, pair := range pairs {
		if strings.TrimSpace(pair[1]) != "" {
			out = append(out, pair)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
