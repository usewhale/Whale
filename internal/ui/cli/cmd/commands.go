package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/usewhale/whale/internal/app"
	whaleworktree "github.com/usewhale/whale/internal/worktree"
)

func newExecCmd(opts *cliOptions) *cobra.Command {
	var jsonOutput bool
	var timeoutSec int
	c := &cobra.Command{
		Use:   "exec [prompt]",
		Short: "Run a single prompt non-interactively",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := prepareWorktree(cmd, opts); err != nil {
				return err
			}
			if err := prepareCLIConfig(cmd, opts); err != nil {
				return err
			}
			return runExec(cmd.OutOrStdout(), cmd.ErrOrStderr(), cmd.InOrStdin(), opts, args, jsonOutput, timeoutSec)
		},
	}
	c.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON output")
	c.Flags().IntVar(&timeoutSec, "timeout-sec", 0, "Optional timeout in seconds for this exec run")
	return c
}

func newResumeCmd(opts *cliOptions) *cobra.Command {
	var last bool
	c := &cobra.Command{
		Use:   "resume [id]",
		Short: "Resume a session (open picker, use --last, or pass an id)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			if err := prepareResumeWorktree(args, last, opts); err != nil {
				return err
			}
			if err := prepareCLIConfig(cmd, opts); err != nil {
				return err
			}
			start, err := resumeStartOptions(args, last)
			if err != nil {
				return err
			}
			return runLoop(opts, start)
		},
	}
	c.Flags().BoolVar(&last, "last", false, "Resume the most recent session without opening the picker")
	return c
}

func newWorktreeCmd(opts *cliOptions) *cobra.Command {
	c := &cobra.Command{
		Use:   "worktree",
		Short: "Manage Whale worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			return runWorktreeList(cmd.OutOrStdout())
		},
	}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List Whale worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			return runWorktreeList(cmd.OutOrStdout())
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "status [name]",
		Short: "Show Whale worktree status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			return runWorktreeStatus(cmd.OutOrStdout(), args)
		},
	})
	var force bool
	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a Whale worktree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			return runWorktreeRemove(cmd.OutOrStdout(), args[0], force)
		},
	}
	remove.Flags().BoolVar(&force, "force", false, "Discard changes in the worktree")
	c.AddCommand(remove)
	return c
}

func resumeStartOptions(args []string, last bool) (app.StartOptions, error) {
	if last && len(args) > 0 {
		return app.StartOptions{}, fmt.Errorf("usage: whale resume [--last] [id]")
	}
	if last {
		return app.StartOptions{}, nil
	}
	if len(args) == 1 {
		id := strings.TrimSpace(args[0])
		if id == "" {
			return app.StartOptions{}, fmt.Errorf("usage: whale resume [--last] [id]")
		}
		return app.StartOptions{SessionID: id}, nil
	}
	return app.StartOptions{ResumeMenu: true}, nil
}

func prepareResumeWorktree(args []string, last bool, opts *cliOptions) error {
	if len(args) == 0 && !last {
		return nil
	}
	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	start, err := resumeStartOptions(args, last)
	if err != nil {
		return err
	}
	sess, err := app.ResolveResumeWorktree(opts.cfg, start, workspaceRoot)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sess.Path) == "" {
		return nil
	}
	targetWorkspace := sess.Path
	if workspace := strings.TrimSpace(sess.Workspace); workspace != "" && pathInside(workspace, sess.Path) {
		targetWorkspace = workspace
	}
	if err := os.Chdir(targetWorkspace); err != nil {
		return fmt.Errorf("enter resume worktree: %w", err)
	}
	opts.worktreeSession = sess
	return nil
}

func pathInside(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func newDoctorCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run Whale health checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			return runDoctor(cmd.OutOrStdout(), opts.cfg)
		},
	}
}

func newSetupCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Save your DeepSeek API key for future Whale sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			return runSetup(cmd.OutOrStdout(), cmd.InOrStdin(), opts.cfg.DataDir)
		},
	}
}

func newMigrateConfigCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-config",
		Short: "Migrate Whale v0.1.8-or-earlier config files to config.toml",
		Long: strings.TrimSpace(`Migrate legacy Whale config files to config.toml.

This is only needed if you used Whale v0.1.8 or earlier and have legacy
preferences.json or settings.json files. If you started with Whale v0.1.9 or
newer, you do not need to run this command.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectWorktreeFlag(cmd); err != nil {
				return err
			}
			workspaceRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get workspace: %w", err)
			}
			report, err := app.MigrateConfig(opts.cfg.DataDir, workspaceRoot)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "migrate-config is only needed for Whale v0.1.8 or earlier legacy config files.")
			if len(report.Written) == 0 {
				if len(report.Skipped) == 0 {
					fmt.Fprintln(out, "no legacy config to migrate")
				} else {
					fmt.Fprintln(out, "no config.toml changes needed")
				}
			} else {
				fmt.Fprintln(out, "migrated config:")
				for _, path := range report.Written {
					fmt.Fprintf(out, "  %s\n", path)
				}
			}
			if len(report.Skipped) > 0 {
				fmt.Fprintln(out, "obsolete Whale v0.1.8-or-earlier files are no longer read:")
				for _, path := range report.Skipped {
					fmt.Fprintf(out, "  %s\n", path)
				}
			}
			return nil
		},
	}
}

func runSetup(out io.Writer, in io.Reader, dataDir string) error {
	reader := bufio.NewReader(in)
	envKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	fmt.Fprintln(out, "Whale setup")
	if envKey != "" {
		fmt.Fprintln(out, "DEEPSEEK_API_KEY is set in the current environment.")
		fmt.Fprint(out, "DeepSeek API key (press enter to reuse current env value): ")
	} else {
		fmt.Fprint(out, "DeepSeek API key: ")
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read api key: %w", err)
	}
	key := strings.TrimSpace(line)
	if key == "" {
		key = envKey
	}
	if err := app.ValidateDeepSeekAPIKey(key); err != nil {
		return err
	}
	if err := app.SaveCredentials(dataDir, app.Credentials{DeepSeekAPIKey: key}); err != nil {
		return err
	}
	fmt.Fprintf(out, "saved DeepSeek API key to %s\n", filepath.Join(dataDir, "credentials.json"))
	fmt.Fprintln(out, "Run `whale` to start a session.")
	return nil
}

func runDoctor(out io.Writer, cfg app.Config) error {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	report, err := app.RunDoctor(context.Background(), cfg, workspaceRoot)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "whale doctor")
	fmt.Fprintf(out, "  workspace: %s\n", report.Workspace)
	fmt.Fprintf(out, "  data dir: %s\n", report.DataDir)
	fmt.Fprintln(out)
	for _, check := range report.Checks {
		fmt.Fprintf(out, "  %s  %-12s %s\n", doctorBadge(check.Level), check.Label, check.Detail)
	}
	fmt.Fprintln(out)
	ok, warn, fail := report.Summary()
	fmt.Fprintf(out, "%d ok · %d warn · %d fail\n", ok, warn, fail)
	if fail > 0 {
		return ExitError{Code: 1}
	}
	return nil
}

func runWorktreeList(out io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	items, err := whaleworktree.List(cwd)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "no Whale worktrees")
		return nil
	}
	fmt.Fprintln(out, "NAME\tBRANCH\tHEAD\tSTATUS\tPATH")
	for _, item := range items {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", item.Name, item.Branch, valueOrDash(item.Head), worktreeStatus(item), item.Path)
	}
	return nil
}

func runWorktreeStatus(out io.Writer, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	if len(args) == 0 {
		items, err := whaleworktree.List(cwd)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(out, "no Whale worktrees")
			return nil
		}
		for _, item := range items {
			fmt.Fprintf(out, "%s: %s (%s)\n", item.Name, worktreeStatus(item), item.Path)
		}
		return nil
	}
	item, err := whaleworktree.Status(cwd, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "name: %s\nbranch: %s\nhead: %s\nstatus: %s\npath: %s\n", item.Name, item.Branch, valueOrDash(item.Head), worktreeStatus(item), item.Path)
	return nil
}

func runWorktreeRemove(out io.Writer, name string, force bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	res, err := whaleworktree.Remove(cwd, name, force)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "removed worktree: %s\npath: %s\n", res.Entry.Name, res.Entry.Path)
	if res.BranchDeleted {
		fmt.Fprintf(out, "deleted branch: %s\n", whaleworktree.BranchName(name))
	} else if strings.TrimSpace(res.BranchWarning) != "" {
		fmt.Fprintf(out, "branch warning: %s\n", res.BranchWarning)
	}
	return nil
}

func worktreeStatus(item whaleworktree.Entry) string {
	if item.Missing {
		return "missing"
	}
	if item.Dirty {
		return "dirty"
	}
	return "clean"
}

func valueOrDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return strings.TrimSpace(v)
}

func doctorBadge(level app.DoctorLevel) string {
	switch level {
	case app.DoctorOK:
		return "ok"
	case app.DoctorWarn:
		return "warn"
	default:
		return "fail"
	}
}

func runExec(out io.Writer, errOut io.Writer, in io.Reader, opts *cliOptions, args []string, jsonOutput bool, timeoutSec int) error {
	prompt, err := readExecPrompt(in, args)
	if err != nil {
		return err
	}
	start := app.StartOptions{NewSession: true, Worktree: opts.worktreeSession}

	ctx := context.Background()
	if timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}

	res, execErr := app.RunExec(ctx, opts.cfg, start, prompt)
	if jsonOutput {
		if err := writeExecJSON(out, res); err != nil {
			return err
		}
		if execErr != nil {
			return ExitError{Code: 1}
		}
		return nil
	}
	if txt := res.TextOutput(); txt != "" {
		if _, err := io.WriteString(out, txt); err != nil {
			return err
		}
		if !strings.HasSuffix(txt, "\n") {
			if _, err := io.WriteString(out, "\n"); err != nil {
				return err
			}
		}
	}
	if execErr != nil {
		if strings.TrimSpace(res.Error) != "" {
			if _, err := fmt.Fprintln(errOut, res.Error); err != nil {
				return err
			}
		}
		return ExitError{Code: 1}
	}
	return nil
}

func readExecPrompt(in io.Reader, args []string) (string, error) {
	if len(args) == 1 {
		prompt := strings.TrimSpace(args[0])
		if prompt == "" {
			return "", fmt.Errorf("prompt is empty")
		}
		return prompt, nil
	}
	if f, ok := in.(*os.File); ok {
		if info, err := f.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
			return "", fmt.Errorf("prompt is required")
		}
	}
	b, err := io.ReadAll(in)
	if err != nil {
		return "", fmt.Errorf("read prompt from stdin: %w", err)
	}
	prompt := strings.TrimSpace(string(b))
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	return prompt, nil
}

func writeExecJSON(out io.Writer, res app.ExecResult) error {
	if err := res.Validate(); err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}
