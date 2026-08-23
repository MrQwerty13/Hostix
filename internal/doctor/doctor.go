// Package doctor inspects the local machine for runtimes supported by Hostix.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNoRuntime indicates that none of the currently implemented runtimes is
// ready on the host.
var ErrNoRuntime = errors.New("no supported runtime is ready")

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
	Name        string
	Available   bool
	Operational bool
	Path        string
	Version     string
	Error       string
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
	docker := inspectDocker(ctx, probe)
	report := Report{OS: goos, Arch: goarch, Tools: []ToolResult{docker}}

	if goos == "darwin" {
		tart := inspectTool(ctx, probe, "Tart", "tart", "--version")
		report.Tools = append(report.Tools, tart)
		// Tart is detected early, but the runnable backend is introduced in
		// Phase 3. Until then, current run support requires Docker.
		report.Healthy = docker.Operational
		switch {
		case tart.Available && docker.Operational:
			report.Recommendation = "Ready: Docker is available for current runs; Tart is detected for the planned macOS backend."
		case docker.Operational:
			report.Recommendation = "Ready: Docker is available for current runs."
		case docker.Available:
			report.Recommendation = "Docker CLI is installed, but its daemon is unavailable; start a Docker-compatible engine."
		case tart.Available:
			report.Recommendation = "Tart is installed, but current run support requires Docker."
		default:
			report.Recommendation = "Install Docker to run projects with the current Hostix build."
		}
		return report
	}

	report.Healthy = docker.Operational
	if docker.Operational {
		report.Recommendation = "Ready: Docker is available."
	} else if docker.Available {
		report.Recommendation = fmt.Sprintf("Docker CLI is installed, but its daemon is unavailable on %s.", goos)
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
	result.Operational = true
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

func inspectDocker(ctx context.Context, probe Probe) ToolResult {
	result := inspectTool(ctx, probe, "Docker", "docker", "--version")
	if !result.Available {
		return result
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := probe.Output(checkCtx, result.Path, "info", "--format", "{{.ServerVersion}}")
	if err == nil {
		result.Operational = true
		return result
	}

	result.Operational = false
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	result.Error = "daemon unavailable: " + detail
	return result
}
