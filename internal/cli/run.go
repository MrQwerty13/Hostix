package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MrQwerty13/Hostix/internal/app"
	"github.com/spf13/cobra"
)

type runService interface {
	Run(context.Context, app.RunRequest) (app.RunResult, error)
	Close() error
}

type runServiceFactory func(io.Writer) (runService, error)

func defaultRunServiceFactory(progress io.Writer) (runService, error) {
	return app.NewDockerRunService(progress)
}

func newRunCommand(factory runServiceFactory) *cobra.Command {
	var stack string
	var runtimeName string
	var name string
	var portValues []string
	var environmentValues []string

	cmd := &cobra.Command{
		Use:   "run <project> [-- command [args...]]",
		Short: "Build and run a project in an isolated environment",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("project path is required")
			}
			if len(args) > 1 && cmd.ArgsLenAtDash() != 1 {
				return errors.New("put the start command after --, for example: hostix run . -- python main.py")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ports, err := parsePortBindings(portValues)
			if err != nil {
				return err
			}
			environment, err := parseEnvironment(environmentValues)
			if err != nil {
				return err
			}

			service, err := factory(cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("initialize Docker runtime: %w", err)
			}

			result, runErr := service.Run(cmd.Context(), app.RunRequest{
				ProjectDir:  args[0],
				Stack:       stack,
				Runtime:     runtimeName,
				Name:        name,
				Command:     append([]string(nil), args[1:]...),
				Environment: environment,
				Ports:       ports,
			})
			closeErr := service.Close()
			if err := errors.Join(runErr, closeErr); err != nil {
				return err
			}

			if result.Replaced {
				fmt.Fprintln(cmd.OutOrStdout(), "Replaced the previous Hostix container.")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Container %s is running.\n", result.Instance.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "ID: %s\n", shortIdentifier(result.Instance.ID))
			fmt.Fprintf(cmd.OutOrStdout(), "Image: %s\n", result.ImageRef)
			return nil
		},
	}

	cmd.Flags().StringVar(&stack, "stack", "auto", "project stack (auto or python)")
	cmd.Flags().StringVar(&runtimeName, "runtime", "auto", "runtime backend (auto or docker)")
	cmd.Flags().StringVar(&name, "name", "", "override the deterministic container name")
	cmd.Flags().StringSliceVarP(&portValues, "port", "p", nil, "publish a port as HOST:CONTAINER[/PROTOCOL]")
	cmd.Flags().StringArrayVarP(&environmentValues, "env", "e", nil, "set an environment variable as NAME=VALUE")
	return cmd
}

func shortIdentifier(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
