package cli

import (
	"fmt"
	"runtime"

	"github.com/MrQwerty13/Hostix/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check whether Hostix runtimes are installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := doctor.Inspect(cmd.Context(), doctor.NewSystemProbe(), runtime.GOOS, runtime.GOARCH)
			printDoctorReport(cmd, report)
			if !report.Healthy {
				return doctor.ErrNoRuntime
			}
			return nil
		},
	}
}

func printDoctorReport(cmd *cobra.Command, report doctor.Report) {
	fmt.Fprintf(cmd.OutOrStdout(), "Hostix environment (%s/%s)\n", report.OS, report.Arch)
	for _, tool := range report.Tools {
		status := "missing"
		detail := tool.Error
		if tool.Available {
			status = "ok"
			detail = tool.Version
		}
		if detail != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", status, tool.Name, detail)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", status, tool.Name)
	}
	fmt.Fprintln(cmd.OutOrStdout(), report.Recommendation)
}
