package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"
	dockerruntime "github.com/MrQwerty13/Hostix/internal/runtime/docker"
)

type fakeDockerBackend struct {
	status          hostruntime.Status
	statusErr       error
	buildErr        error
	createErr       error
	startErr        error
	removeErr       error
	buildTag        string
	buildDockerfile string
	buildBytes      int64
	createRequest   hostruntime.CreateRequest
	removedIDs      []string
	startedID       string
	closed          bool
}

func (f *fakeDockerBackend) BuildImage(_ context.Context, request dockerruntime.BuildRequest) error {
	f.buildTag = request.Tag
	f.buildDockerfile = request.Dockerfile
	if request.Context != nil {
		count, _ := io.Copy(io.Discard, request.Context)
		f.buildBytes = count
	}
	return f.buildErr
}

func (f *fakeDockerBackend) Status(context.Context, string) (hostruntime.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeDockerBackend) Remove(_ context.Context, id string, _ hostruntime.RemoveOptions) error {
	f.removedIDs = append(f.removedIDs, id)
	return f.removeErr
}

func (f *fakeDockerBackend) Create(_ context.Context, request hostruntime.CreateRequest) (hostruntime.Instance, error) {
	f.createRequest = request
	if f.createErr != nil {
		return hostruntime.Instance{}, f.createErr
	}
	return hostruntime.Instance{ID: "container-id", Name: request.Name}, nil
}

func (f *fakeDockerBackend) Start(_ context.Context, id string) error {
	f.startedID = id
	return f.startErr
}

func (f *fakeDockerBackend) Close() error {
	f.closed = true
	return nil
}

func TestDockerRunServiceRunsDetectedFastAPIProject(t *testing.T) {
	projectDir := fastAPIProject(t)
	backend := &fakeDockerBackend{statusErr: hostruntime.ErrNotFound}
	var progress strings.Builder
	service := newDockerRunService(backend, &progress)

	result, err := service.Run(context.Background(), RunRequest{ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.buildBytes == 0 || backend.buildDockerfile != ".hostix.Dockerfile" {
		t.Fatalf("build request: bytes=%d Dockerfile=%q", backend.buildBytes, backend.buildDockerfile)
	}
	if backend.startedID != "container-id" {
		t.Fatalf("started ID = %q", backend.startedID)
	}
	if got, want := backend.createRequest.Command[2], "uvicorn"; got != want {
		t.Fatalf("command = %#v", backend.createRequest.Command)
	}
	if got, want := len(backend.createRequest.Ports), 1; got != want {
		t.Fatalf("port count = %d, want %d", got, want)
	}
	if backend.createRequest.Ports[0].HostPort != defaultWebPort {
		t.Fatalf("port = %#v", backend.createRequest.Ports[0])
	}
	if result.Framework != "fastapi" || result.ImageRef != backend.buildTag {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(progress.String(), "Building image") {
		t.Fatalf("progress = %q", progress.String())
	}
}

func TestDockerRunServiceReplacesOnlyManagedContainer(t *testing.T) {
	projectDir := fastAPIProject(t)
	backend := &fakeDockerBackend{status: hostruntime.Status{ID: "old", Managed: true}}
	service := newDockerRunService(backend, io.Discard)

	result, err := service.Run(context.Background(), RunRequest{ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Replaced || len(backend.removedIDs) != 1 || backend.removedIDs[0] != "old" {
		t.Fatalf("replacement result=%v removed=%v", result.Replaced, backend.removedIDs)
	}
}

func TestDockerRunServiceRefusesForeignContainer(t *testing.T) {
	backend := &fakeDockerBackend{status: hostruntime.Status{ID: "foreign", Managed: false}}
	service := newDockerRunService(backend, io.Discard)

	_, err := service.Run(context.Background(), RunRequest{ProjectDir: fastAPIProject(t)})
	if !errors.Is(err, hostruntime.ErrAlreadyExists) {
		t.Fatalf("Run() error = %v, want ErrAlreadyExists", err)
	}
	if len(backend.removedIDs) != 0 {
		t.Fatalf("foreign container was removed: %v", backend.removedIDs)
	}
}

func TestDockerRunServiceAcceptsExplicitCommand(t *testing.T) {
	projectDir := t.TempDir()
	writeTestFile(t, filepath.Join(projectDir, "requirements.txt"), "requests==2.32.0\n")
	backend := &fakeDockerBackend{statusErr: hostruntime.ErrNotFound}
	service := newDockerRunService(backend, io.Discard)

	_, err := service.Run(context.Background(), RunRequest{
		ProjectDir: projectDir,
		Command:    []string{"python", "worker.py"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := backend.createRequest.Command[1], "worker.py"; got != want {
		t.Fatalf("command = %#v", backend.createRequest.Command)
	}
}

func TestDockerRunServiceExplicitCommandResolvesAmbiguousFrameworks(t *testing.T) {
	projectDir := t.TempDir()
	writeTestFile(t, filepath.Join(projectDir, "requirements.txt"), "fastapi\nflask\n")
	backend := &fakeDockerBackend{statusErr: hostruntime.ErrNotFound}
	service := newDockerRunService(backend, io.Discard)

	_, err := service.Run(context.Background(), RunRequest{
		ProjectDir: projectDir,
		Command:    []string{"python", "server.py"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := backend.createRequest.Command[1], "server.py"; got != want {
		t.Fatalf("command = %#v", backend.createRequest.Command)
	}
}

func TestDockerRunServiceRequiresSafeCommandBeforeBuilding(t *testing.T) {
	projectDir := t.TempDir()
	writeTestFile(t, filepath.Join(projectDir, "requirements.txt"), "requests\n")
	backend := &fakeDockerBackend{}
	service := newDockerRunService(backend, io.Discard)

	_, err := service.Run(context.Background(), RunRequest{ProjectDir: projectDir})
	if err == nil || !strings.Contains(err.Error(), "pass one after --") {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.buildTag != "" {
		t.Fatal("image was built without a start command")
	}
}

func TestDockerRunServicePreservesExistingContainerWhenBuildFails(t *testing.T) {
	backend := &fakeDockerBackend{
		status:   hostruntime.Status{ID: "old", Managed: true},
		buildErr: errors.New("build failed"),
	}
	service := newDockerRunService(backend, io.Discard)

	_, err := service.Run(context.Background(), RunRequest{ProjectDir: fastAPIProject(t)})
	if err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	if len(backend.removedIDs) != 0 {
		t.Fatalf("existing container removed after build failure: %v", backend.removedIDs)
	}
}

func TestDockerRunServiceCleansUpWhenStartFails(t *testing.T) {
	backend := &fakeDockerBackend{
		statusErr: hostruntime.ErrNotFound,
		startErr:  errors.New("start failed"),
	}
	service := newDockerRunService(backend, io.Discard)

	_, err := service.Run(context.Background(), RunRequest{ProjectDir: fastAPIProject(t)})
	if err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	if got, want := backend.removedIDs, []string{"container-id"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("removed IDs = %v, want %v", got, want)
	}
}

func fastAPIProject(t *testing.T) string {
	t.Helper()
	projectDir := t.TempDir()
	writeTestFile(t, filepath.Join(projectDir, "requirements.txt"), "fastapi==0.116.0\nuvicorn==0.35.0\n")
	writeTestFile(t, filepath.Join(projectDir, "main.py"), "from fastapi import FastAPI\napp = FastAPI()\n")
	return projectDir
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
