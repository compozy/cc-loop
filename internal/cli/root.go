package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/compozy/cc-loop/internal/installer"
	"github.com/compozy/cc-loop/internal/loop"
	"github.com/compozy/cc-loop/internal/updater"
	"github.com/compozy/cc-loop/internal/version"
)

type rootOptions struct {
	claudeConfigDir string
}

func Execute(ctx context.Context, args []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	cmd := NewRootCommand(ctx)
	cmd.SetArgs(args)
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	return cmd.Execute()
}

func NewRootCommand(ctx context.Context) *cobra.Command {
	opts := &rootOptions{}
	cmd := &cobra.Command{
		Use:           "cc-loop",
		Short:         "Claude Code lifecycle loop hooks for long-running agent work",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetContext(ctx)
	cmd.PersistentFlags().StringVar(&opts.claudeConfigDir, "claude-config-dir", "", "override Claude Code config directory (defaults to CLAUDE_CONFIG_DIR or ~/.claude)")

	cmd.AddCommand(newInstallCommand(opts))
	cmd.AddCommand(newUpgradeCommand(opts))
	cmd.AddCommand(newUninstallCommand(opts))
	cmd.AddCommand(newStatusCommand(opts))
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newHookCommand(opts))
	return cmd
}

func newInstallCommand(opts *rootOptions) *cobra.Command {
	var sourceBinary string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or refresh the local cc-loop runtime",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := pathsFromOptions(opts)
			if err != nil {
				return err
			}
			messages, err := installer.Install(paths, installer.Options{SourceBinary: sourceBinary})
			if err != nil {
				return err
			}
			for _, message := range messages {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
					return fmt.Errorf("write install output: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceBinary, "source-binary", "", "source binary to install into the Claude Code runtime")
	_ = cmd.Flags().MarkHidden("source-binary")
	return cmd
}

func newUpgradeCommand(opts *rootOptions) *cobra.Command {
	var version string
	var targetBinary string
	var apiBaseURL string
	var skipMarketplace bool
	var skipSelfUpdate bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Download and install the latest cc-loop release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := pathsFromOptions(opts)
			if err != nil {
				return err
			}
			messages, err := updater.Upgrade(cmd.Context(), paths, updater.Options{
				Version:         version,
				TargetBinary:    targetBinary,
				APIBaseURL:      apiBaseURL,
				SkipMarketplace: skipMarketplace,
				SkipSelfUpdate:  skipSelfUpdate,
			})
			if err != nil {
				return err
			}
			for _, message := range messages {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
					return fmt.Errorf("write upgrade output: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "latest", "release version to install, such as latest or v0.1.1")
	cmd.Flags().StringVar(&targetBinary, "target-binary", "", "CLI binary path to replace (defaults to the running cc-loop executable)")
	cmd.Flags().BoolVar(&skipMarketplace, "skip-marketplace", false, "skip Claude Code plugin marketplace refresh")
	cmd.Flags().BoolVar(&skipSelfUpdate, "skip-self-update", false, "skip replacing the running CLI binary and only refresh the managed runtime")
	cmd.Flags().StringVar(&apiBaseURL, "api-base-url", "", "GitHub API base URL")
	_ = cmd.Flags().MarkHidden("api-base-url")
	return cmd
}

func newUninstallCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the managed cc-loop runtime",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := pathsFromOptions(opts)
			if err != nil {
				return err
			}
			messages, err := installer.Uninstall(paths)
			if err != nil {
				return err
			}
			for _, message := range messages {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
					return fmt.Errorf("write uninstall output: %w", err)
				}
			}
			return nil
		},
	}
}

func newStatusCommand(opts *rootOptions) *cobra.Command {
	var filter loop.StatusFilter
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print cc-loop state as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := pathsFromOptions(opts)
			if err != nil {
				return err
			}
			records, err := loop.ListStatusRecords(paths, filter)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(records); err != nil {
				return fmt.Errorf("encode status JSON: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&filter.All, "all", false, "include non-active loop records")
	cmd.Flags().StringVar(&filter.SessionID, "session-id", "", "only print loop records for one session")
	cmd.Flags().StringVar(&filter.WorkspaceRoot, "workspace-root", "", "only print loop records for one workspace root")
	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print cc-loop version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}

func newHookCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Run Claude Code lifecycle hook handlers",
		Hidden: true,
	}
	cmd.AddCommand(newUserPromptSubmitHookCommand(opts))
	cmd.AddCommand(newStopHookCommand(opts))
	return cmd
}

func newUserPromptSubmitHookCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:    "user-prompt-submit",
		Short:  "Handle Claude Code UserPromptSubmit events",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var payload loop.UserPromptPayload
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&payload); err != nil {
				return fmt.Errorf("invalid JSON input: %w", err)
			}
			paths, err := pathsFromOptions(opts)
			if err != nil {
				return err
			}
			result, err := loop.HandleUserPromptSubmit(paths, payload, zeroTime())
			if err != nil {
				return err
			}
			return writeHookResult(cmd.OutOrStdout(), result)
		},
	}
}

func newStopHookCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:    "stop",
		Short:  "Handle Claude Code Stop events",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var payload loop.StopPayload
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&payload); err != nil {
				return writeHookResult(cmd.OutOrStdout(), loop.StopWarning(fmt.Sprintf("Claude Code loop stop hook received invalid JSON: %v", err)))
			}
			paths, err := pathsFromOptions(opts)
			if err != nil {
				return err
			}
			result, err := loop.HandleStop(cmd.Context(), paths, payload, zeroTime())
			if err != nil {
				return err
			}
			return writeHookResult(cmd.OutOrStdout(), result)
		},
	}
}

func writeHookResult(out io.Writer, result loop.HookResult) error {
	if result == nil {
		return nil
	}
	encoder := json.NewEncoder(out)
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode hook result: %w", err)
	}
	return nil
}

func pathsFromOptions(opts *rootOptions) (loop.Paths, error) {
	if opts.claudeConfigDir != "" {
		return loop.NewPaths(opts.claudeConfigDir)
	}
	return loop.DefaultPaths()
}
