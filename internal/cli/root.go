package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"
)

// Execute runs the Hostix CLI with explicitly supplied streams so commands can
// be embedded and tested without mutating process-wide state.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, version string) error {
	cmd := NewRootCommand(version)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd.ExecuteContext(ctx)
}

// NewRootCommand constructs the root command and all currently supported
// subcommands.
func NewRootCommand(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hostix",
		Short:         "Run projects in Docker containers or Tart virtual machines",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       version,
	}

	cmd.AddCommand(newDoctorCommand())
	return cmd
}
