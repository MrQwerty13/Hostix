package doctor

import (
	"context"
	"errors"
	"fmt"
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

func (f fakeProbe) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	return f.outputs[name], f.errors[name]
}

func TestInspectDarwinPrefersTart(t *testing.T) {
	probe := fakeProbe{
		paths:   map[string]string{"docker": "/bin/docker", "tart": "/bin/tart"},
		outputs: map[string][]byte{"/bin/docker": []byte("Docker 28.0\n"), "/bin/tart": []byte("2.28.1\n")},
		errors:  map[string]error{},
	}

	report := Inspect(context.Background(), probe, "darwin", "arm64")
	if !report.Healthy {
		t.Fatal("expected report to be healthy")
	}
	if got, want := report.Recommendation, "Ready: Tart will be the default runtime on macOS."; got != want {
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
	if got, want := report.Recommendation, "Ready: Docker will be used as the macOS fallback runtime."; got != want {
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
		paths:   map[string]string{"docker": "/bin/docker"},
		outputs: map[string][]byte{"/bin/docker": []byte("permission denied")},
		errors:  map[string]error{"/bin/docker": fmt.Errorf("exit status 1")},
	}

	report := Inspect(context.Background(), probe, "linux", "arm64")
	if !report.Healthy {
		t.Fatal("installed Docker should count as available even if version probing fails")
	}
	if report.Tools[0].Error == "" {
		t.Fatal("expected version error to be recorded")
	}
}
