// Package doctor inspects the local machine for runtimes supported by Hostix.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNoRuntime indicates that none of the runtimes required by the current OS
// are available.
var ErrNoRuntime = errors.New("no supported runtime is installed")

// Probe abstracts executable discovery and invocation for deterministic tests.
type Probe interface {
	LookPath(file string) (string, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type systemProbe struct{}

// NewSystemProbe creates a probe backed by the local operating system.
func NewSystemProbe() Probe {
	return systemProbe{}
}

func (systemProbe) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (systemProbe) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ToolResult describes the availability and reported version of one runtime.
type ToolResult struct {
	Name      string
	Available bool
	Path      string
	Version   string
	Error     string
}

// Report is the complete diagnostic result for a host.
type Report struct {
	OS             string
	Arch           string
	Tools          []ToolResult
	Healthy        bool
	Recommendation string
}

// Inspect detects supported runtimes. macOS can use Tart or Docker; other
// systems currently require Docker.
func Inspect(ctx context.Context, probe Probe, goos, goarch string) Report {
	docker := inspectTool(ctx, probe, "Docker", "docker", "--version")
	report := Report{OS: goos, Arch: goarch, Tools: []ToolResult{docker}}

	if goos == "darwin" {
		tart := inspectTool(ctx, probe, "Tart", "tart", "--version")
		report.Tools = append(report.Tools, tart)
		report.Healthy = tart.Available || docker.Available
		switch {
		case tart.Available:
			report.Recommendation = "Ready: Tart will be the default runtime on macOS."
		case docker.Available:
			report.Recommendation = "Ready: Docker will be used as the macOS fallback runtime."
		default:
			report.Recommendation = "Install Tart or Docker to run projects with Hostix."
		}
		return report
	}

	report.Healthy = docker.Available
	if docker.Available {
		report.Recommendation = "Ready: Docker is available."
	} else {
		report.Recommendation = fmt.Sprintf("Install Docker to run projects with Hostix on %s.", goos)
	}
	return report
}

func inspectTool(ctx context.Context, probe Probe, displayName, executable string, args ...string) ToolResult {
	result := ToolResult{Name: displayName}
	path, err := probe.LookPath(executable)
	if err != nil {
		result.Error = "executable not found"
		return result
	}

	result.Available = true
	result.Path = path
	output, err := probe.Output(ctx, path, args...)
	result.Version = strings.TrimSpace(string(output))
	if err != nil {
		result.Error = fmt.Sprintf("version check failed: %v", err)
		if result.Version == "" {
			result.Version = "installed (version unavailable)"
		}
	}
	return result
}
