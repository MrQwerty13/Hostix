package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeProbe struct {
	paths   map[string]string
	outputs map[string][]byte
	errors  map[string]error
}

func (f fakeProbe) LookPath(file string) (string, error) {
	path, ok := f.paths[file]
	if !ok {
		return "", errors.New("not found")
	}
	return path, nil
}

func (f fakeProbe) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	output, ok := f.outputs[key]
	if !ok {
		output = f.outputs[name]
	}
	err, ok := f.errors[key]
	if !ok {
		err = f.errors[name]
	}
	return output, err
}

func TestInspectReportsUnavailableDockerDaemon(t *testing.T) {
	probe := fakeProbe{
		paths: map[string]string{"docker": "/bin/docker", "tart": "/bin/tart"},
		outputs: map[string][]byte{
			"/bin/docker --version":                        []byte("Docker version 29.5.2"),
			"/bin/docker info --format {{.ServerVersion}}": []byte("cannot connect to Docker daemon"),
			"/bin/tart": []byte("2.32.1"),
		},
		errors: map[string]error{
			"/bin/docker info --format {{.ServerVersion}}": errors.New("exit status 1"),
		},
	}

	report := Inspect(context.Background(), probe, "darwin", "arm64")
	if report.Healthy {
		t.Fatal("unavailable daemon must make the current Docker runtime unhealthy")
	}
	if report.Tools[0].Operational || !strings.Contains(report.Tools[0].Error, "cannot connect") {
		t.Fatalf("Docker result = %#v", report.Tools[0])
	}
}

func TestInspectDarwinReportsTartWhileUsingCurrentDockerBackend(t *testing.T) {
	probe := fakeProbe{
		paths:   map[string]string{"docker": "/bin/docker", "tart": "/bin/tart"},
		outputs: map[string][]byte{"/bin/docker": []byte("Docker 28.0\n"), "/bin/tart": []byte("2.28.1\n")},
		errors:  map[string]error{},
	}

	report := Inspect(context.Background(), probe, "darwin", "arm64")
	if !report.Healthy {
		t.Fatal("expected report to be healthy")
	}
	if got, want := report.Recommendation, "Ready: Docker is available for current runs; Tart is detected for the planned macOS backend."; got != want {
		t.Fatalf("recommendation = %q, want %q", got, want)
	}
	if got, want := report.Tools[1].Version, "2.28.1"; got != want {
		t.Fatalf("Tart version = %q, want %q", got, want)
	}
}

func TestInspectDarwinFallsBackToDocker(t *testing.T) {
	probe := fakeProbe{
		paths:   map[string]string{"docker": "/bin/docker"},
		outputs: map[string][]byte{"/bin/docker": []byte("Docker version 28.0")},
		errors:  map[string]error{},
	}

	report := Inspect(context.Background(), probe, "darwin", "amd64")
	if !report.Healthy {
		t.Fatal("expected Docker fallback to be healthy")
	}
	if report.Tools[1].Available {
		t.Fatal("expected Tart to be unavailable")
	}
	if got, want := report.Recommendation, "Ready: Docker is available for current runs."; got != want {
		t.Fatalf("recommendation = %q, want %q", got, want)
	}
}

func TestInspectDarwinDoesNotClaimTartBackendIsReadyYet(t *testing.T) {
	probe := fakeProbe{
		paths:   map[string]string{"tart": "/bin/tart"},
		outputs: map[string][]byte{"/bin/tart": []byte("2.28.1")},
		errors:  map[string]error{},
	}

	report := Inspect(context.Background(), probe, "darwin", "arm64")
	if report.Healthy {
		t.Fatal("Tart-only host should not be ready before TartRuntime is implemented")
	}
	if got, want := report.Recommendation, "Tart is installed, but current run support requires Docker."; got != want {
		t.Fatalf("recommendation = %q, want %q", got, want)
	}
}

func TestInspectLinuxRequiresDocker(t *testing.T) {
	report := Inspect(context.Background(), fakeProbe{paths: map[string]string{}, outputs: map[string][]byte{}, errors: map[string]error{}}, "linux", "amd64")
	if report.Healthy {
		t.Fatal("expected report without Docker to be unhealthy")
	}
	if got, want := len(report.Tools), 1; got != want {
		t.Fatalf("tool count = %d, want %d", got, want)
	}
}

func TestInspectKeepsInstalledToolAvailableWhenVersionFails(t *testing.T) {
	probe := fakeProbe{
		paths: map[string]string{"docker": "/bin/docker"},
		outputs: map[string][]byte{
			"/bin/docker --version":                        []byte("permission denied"),
			"/bin/docker info --format {{.ServerVersion}}": []byte("29.5.2"),
		},
		errors: map[string]error{"/bin/docker --version": fmt.Errorf("exit status 1")},
	}

	report := Inspect(context.Background(), probe, "linux", "arm64")
	if !report.Healthy {
		t.Fatal("installed Docker should count as available even if version probing fails")
	}
	if report.Tools[0].Error == "" {
		t.Fatal("expected version error to be recorded")
	}
}
