package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type fakeAPI struct {
	containerCreate  func(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	containerStart   func(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	containerStop    func(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error)
	containerRemove  func(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	containerInspect func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	containerList    func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	containerLogs    func(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	execCreate       func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	execAttach       func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	execInspect      func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
	imageBuild       func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error)
	close            func() error
}

func (f *fakeAPI) ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	if f.containerCreate != nil {
		return f.containerCreate(ctx, options)
	}
	return client.ContainerCreateResult{}, nil
}

func (f *fakeAPI) ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error) {
	if f.containerStart != nil {
		return f.containerStart(ctx, id, options)
	}
	return client.ContainerStartResult{}, nil
}

func (f *fakeAPI) ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error) {
	if f.containerStop != nil {
		return f.containerStop(ctx, id, options)
	}
	return client.ContainerStopResult{}, nil
}

func (f *fakeAPI) ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	if f.containerRemove != nil {
		return f.containerRemove(ctx, id, options)
	}
	return client.ContainerRemoveResult{}, nil
}

func (f *fakeAPI) ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if f.containerInspect != nil {
		return f.containerInspect(ctx, id, options)
	}
	return client.ContainerInspectResult{}, nil
}

func (f *fakeAPI) ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	if f.containerList != nil {
		return f.containerList(ctx, options)
	}
	return client.ContainerListResult{}, nil
}

func (f *fakeAPI) ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	if f.containerLogs != nil {
		return f.containerLogs(ctx, id, options)
	}
	return nil, nil
}

func (f *fakeAPI) ExecCreate(ctx context.Context, id string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
	if f.execCreate != nil {
		return f.execCreate(ctx, id, options)
	}
	return client.ExecCreateResult{}, nil
}

func (f *fakeAPI) ExecAttach(ctx context.Context, id string, options client.ExecAttachOptions) (client.ExecAttachResult, error) {
	if f.execAttach != nil {
		return f.execAttach(ctx, id, options)
	}
	return client.ExecAttachResult{}, nil
}

func (f *fakeAPI) ExecInspect(ctx context.Context, id string, options client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if f.execInspect != nil {
		return f.execInspect(ctx, id, options)
	}
	return client.ExecInspectResult{}, nil
}

func (f *fakeAPI) ImageBuild(ctx context.Context, buildContext io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
	if f.imageBuild != nil {
		return f.imageBuild(ctx, buildContext, options)
	}
	return client.ImageBuildResult{}, nil
}

func (f *fakeAPI) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

func newTestRuntime(t *testing.T, api *fakeAPI) *Runtime {
	t.Helper()
	runtime, err := NewWithClient(api)
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return runtime
}

func TestCreateBuildsHostixContainerConfiguration(t *testing.T) {
	projectDir := t.TempDir()
	var captured client.ContainerCreateOptions
	api := &fakeAPI{containerCreate: func(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
		captured = options
		return client.ContainerCreateResult{ID: "container-id"}, nil
	}}
	runtime := newTestRuntime(t, api)

	instance, err := runtime.Create(context.Background(), hostruntime.CreateRequest{
		Name:          "python-app",
		Image:         "hostix/python-app:latest",
		Command:       []string{"python", "app.py"},
		ProjectDir:    projectDir,
		Environment:   map[string]string{"Z_LAST": "2", "A_FIRST": "1"},
		Ports:         []hostruntime.PortBinding{{HostPort: 8080, ContainerPort: 8000, Protocol: "TCP"}},
		CPUCount:      1.5,
		MemoryBytes:   512 * 1024 * 1024,
		RestartPolicy: "on-failure",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got, want := instance, (hostruntime.Instance{ID: "container-id", Name: "python-app"}); got != want {
		t.Fatalf("Create() instance = %#v, want %#v", got, want)
	}
	if captured.Name != "python-app" || captured.Image != "hostix/python-app:latest" {
		t.Fatalf("create identity = name %q, image %q", captured.Name, captured.Image)
	}
	if captured.Config == nil || captured.HostConfig == nil {
		t.Fatal("Create() must provide container and host configurations")
	}
	if got, want := captured.Config.Cmd, []string{"python", "app.py"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	if got, want := captured.Config.Env, []string{"A_FIRST=1", "Z_LAST=2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
	if got := captured.Config.Labels[LabelManaged]; got != managedLabelValue {
		t.Fatalf("managed label = %q", got)
	}
	if got := captured.Config.Labels[LabelProjectDir]; got != projectDir {
		t.Fatalf("project label = %q, want %q", got, projectDir)
	}
	if got := captured.Config.WorkingDir; got != workspaceTarget {
		t.Fatalf("working directory = %q, want %q", got, workspaceTarget)
	}
	if got, want := captured.HostConfig.NanoCPUs, int64(1_500_000_000); got != want {
		t.Fatalf("NanoCPUs = %d, want %d", got, want)
	}
	if got, want := captured.HostConfig.Memory, int64(512*1024*1024); got != want {
		t.Fatalf("memory = %d, want %d", got, want)
	}
	if got, want := captured.HostConfig.RestartPolicy.Name, container.RestartPolicyOnFailure; got != want {
		t.Fatalf("restart policy = %q, want %q", got, want)
	}
	if len(captured.HostConfig.Mounts) != 1 || captured.HostConfig.Mounts[0].Source != projectDir || captured.HostConfig.Mounts[0].Target != workspaceTarget {
		t.Fatalf("mounts = %#v", captured.HostConfig.Mounts)
	}

	port, err := networkPort("8000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := captured.Config.ExposedPorts[port]; !ok {
		t.Fatalf("port %s is not exposed", port)
	}
	bindings := captured.HostConfig.PortBindings[port]
	if len(bindings) != 1 || bindings[0].HostPort != "8080" {
		t.Fatalf("port bindings = %#v", bindings)
	}
}

func networkPort(value string) (network.Port, error) {
	return network.ParsePort(value)
}

func TestCreateMapsNameConflict(t *testing.T) {
	api := &fakeAPI{containerCreate: func(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
		return client.ContainerCreateResult{}, fmt.Errorf("name is in use: %w", cerrdefs.ErrConflict)
	}}
	runtime := newTestRuntime(t, api)

	_, err := runtime.Create(context.Background(), hostruntime.CreateRequest{Name: "taken", Image: "image"})
	if !errors.Is(err, hostruntime.ErrAlreadyExists) {
		t.Fatalf("Create() error = %v, want ErrAlreadyExists", err)
	}
	if !errors.Is(err, cerrdefs.ErrConflict) {
		t.Fatalf("Create() error does not preserve SDK cause: %v", err)
	}
}

func TestLifecycleMapsErrorsAndOptions(t *testing.T) {
	var removeForce bool
	api := &fakeAPI{
		containerStart: func(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error) {
			return client.ContainerStartResult{}, cerrdefs.ErrNotModified
		},
		containerStop: func(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error) {
			return client.ContainerStopResult{}, fmt.Errorf("missing: %w", cerrdefs.ErrNotFound)
		},
		containerRemove: func(_ context.Context, _ string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
			removeForce = options.Force
			return client.ContainerRemoveResult{}, nil
		},
	}
	runtime := newTestRuntime(t, api)

	if err := runtime.Start(context.Background(), "already-running"); err != nil {
		t.Fatalf("Start() idempotent error = %v", err)
	}
	if err := runtime.Stop(context.Background(), "missing"); !errors.Is(err, hostruntime.ErrNotFound) {
		t.Fatalf("Stop() error = %v, want ErrNotFound", err)
	}
	if err := runtime.Remove(context.Background(), "old", hostruntime.RemoveOptions{Force: true}); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !removeForce {
		t.Fatal("Remove() did not pass Force=true")
	}
}

func TestExecSeparatesStreamsAndReturnsExitCode(t *testing.T) {
	var createOptions client.ExecCreateOptions
	var attachOptions client.ExecAttachOptions
	api := &fakeAPI{
		execCreate: func(_ context.Context, id string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
			if id != "container-id" {
				t.Fatalf("ExecCreate() id = %q", id)
			}
			createOptions = options
			return client.ExecCreateResult{ID: "exec-id"}, nil
		},
		execAttach: func(_ context.Context, id string, options client.ExecAttachOptions) (client.ExecAttachResult, error) {
			if id != "exec-id" {
				t.Fatalf("ExecAttach() id = %q", id)
			}
			attachOptions = options
			clientConn, serverConn := net.Pipe()
			go func() {
				_, _ = serverConn.Write(append(dockerFrame(1, "hello\n"), dockerFrame(2, "warning\n")...))
				_ = serverConn.Close()
			}()
			return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(clientConn, "application/vnd.docker.multiplexed-stream")}, nil
		},
		execInspect: func(_ context.Context, id string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
			if id != "exec-id" {
				t.Fatalf("ExecInspect() id = %q", id)
			}
			return client.ExecInspectResult{ExitCode: 7}, nil
		},
	}
	runtime := newTestRuntime(t, api)

	result, err := runtime.Exec(context.Background(), "container-id", hostruntime.ExecRequest{
		Command:     []string{"sh", "-c", "test"},
		Environment: map[string]string{"B": "2", "A": "1"},
		WorkingDir:  "/workspace",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 7 || string(result.Stdout) != "hello\n" || string(result.Stderr) != "warning\n" {
		t.Fatalf("Exec() result = %#v", result)
	}
	if got, want := createOptions.Env, []string{"A=1", "B=2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exec environment = %#v, want %#v", got, want)
	}
	if !createOptions.AttachStdout || !createOptions.AttachStderr || createOptions.TTY || attachOptions.TTY {
		t.Fatalf("exec options = create %#v, attach %#v", createOptions, attachOptions)
	}
}

func TestLogsPassesOptionsDemultiplexesAndCloses(t *testing.T) {
	since := time.Unix(1_725_000_000, 0)
	source := &trackingReadCloser{Reader: bytes.NewReader(append(dockerFrame(1, "line one\n"), dockerFrame(2, "line two\n")...))}
	var captured client.ContainerLogsOptions
	api := &fakeAPI{
		containerInspect: func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: container.InspectResponse{Config: &container.Config{Tty: false}}}, nil
		},
		containerLogs: func(_ context.Context, _ string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
			captured = options
			return source, nil
		},
	}
	runtime := newTestRuntime(t, api)

	logs, err := runtime.Logs(context.Background(), "container-id", hostruntime.LogsOptions{Follow: true, Tail: 25, Since: since})
	if err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	content, err := io.ReadAll(logs)
	if err != nil {
		t.Fatalf("ReadAll(logs) error = %v", err)
	}
	if err := logs.Close(); err != nil {
		t.Fatalf("logs.Close() error = %v", err)
	}
	if got, want := string(content), "line one\nline two\n"; got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
	if !captured.ShowStdout || !captured.ShowStderr || !captured.Follow || captured.Tail != "25" || captured.Since != fmt.Sprint(since.Unix()) {
		t.Fatalf("log options = %#v", captured)
	}
	if got := source.closed.Load(); got != 1 {
		t.Fatalf("source close count = %d, want 1", got)
	}
}

func TestStatusMapsStateOwnershipAndNotFound(t *testing.T) {
	started := time.Date(2026, time.August, 23, 10, 11, 12, 123, time.UTC)
	api := &fakeAPI{containerInspect: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		if id == "missing" {
			return client.ContainerInspectResult{}, fmt.Errorf("gone: %w", cerrdefs.ErrNotFound)
		}
		return client.ContainerInspectResult{Container: container.InspectResponse{
			ID:   "container-id",
			Name: "/python-app",
			Config: &container.Config{Labels: map[string]string{
				LabelManaged: managedLabelValue,
			}},
			State: &container.State{Status: container.StatePaused, StartedAt: started.Format(time.RFC3339Nano)},
		}}, nil
	}}
	runtime := newTestRuntime(t, api)

	status, err := runtime.Status(context.Background(), "python-app")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.ID != "container-id" || status.Name != "python-app" || status.State != hostruntime.StateRunning || !status.Managed || !status.StartedAt.Equal(started) {
		t.Fatalf("Status() = %#v", status)
	}
	if _, err := runtime.Status(context.Background(), "missing"); !errors.Is(err, hostruntime.ErrNotFound) {
		t.Fatalf("Status(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListScopesToManagedContainersAndSorts(t *testing.T) {
	var captured client.ContainerListOptions
	api := &fakeAPI{containerList: func(_ context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
		captured = options
		return client.ContainerListResult{Items: []container.Summary{
			{ID: "2", Names: []string{"/z-app"}, State: container.StateExited, Labels: map[string]string{LabelManaged: managedLabelValue}},
			{ID: "ignored", Names: []string{"/foreign"}, State: container.StateRunning, Labels: map[string]string{}},
			{ID: "1", Names: []string{"/a-app"}, State: container.StateCreated, Labels: map[string]string{LabelManaged: managedLabelValue}},
		}}, nil
	}}
	runtime := newTestRuntime(t, api)

	statuses, err := runtime.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !captured.All || !captured.Filters["label"][LabelManaged+"="+managedLabelValue] {
		t.Fatalf("list options = %#v", captured)
	}
	if len(statuses) != 2 || statuses[0].Name != "a-app" || statuses[1].Name != "z-app" {
		t.Fatalf("List() = %#v", statuses)
	}
	if !statuses[0].Managed || !statuses[1].Managed || statuses[0].State != hostruntime.StateCreated || statuses[1].State != hostruntime.StateStopped {
		t.Fatalf("List() statuses = %#v", statuses)
	}
}

func TestBuildImageUsesRequestedDockerfileConsumesAndClosesStream(t *testing.T) {
	buildContext := bytes.NewBufferString("tar archive")
	body := &trackingReadCloser{Reader: strings.NewReader("{\"stream\":\"Step 1/1\\n\"}\n{\"aux\":{\"ID\":\"sha256:abc\"}}\n")}
	var captured client.ImageBuildOptions
	var capturedContext io.Reader
	api := &fakeAPI{imageBuild: func(_ context.Context, contextReader io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
		capturedContext = contextReader
		captured = options
		return client.ImageBuildResult{Body: body}, nil
	}}
	runtime := newTestRuntime(t, api)
	var output bytes.Buffer

	err := runtime.BuildImage(context.Background(), BuildRequest{
		Context:    buildContext,
		Dockerfile: ".hostix.Dockerfile",
		Tag:        "hostix/python-app:latest",
		Output:     &output,
	})
	if err != nil {
		t.Fatalf("BuildImage() error = %v", err)
	}
	if capturedContext != buildContext {
		t.Fatal("BuildImage() did not pass the supplied build context")
	}
	if got, want := captured.Dockerfile, ".hostix.Dockerfile"; got != want {
		t.Fatalf("Dockerfile = %q, want %q", got, want)
	}
	if got, want := captured.Tags, []string{"hostix/python-app:latest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	if got, want := output.String(), "Step 1/1\nbuilt image sha256:abc\n"; got != want {
		t.Fatalf("build output = %q, want %q", got, want)
	}
	if got := body.closed.Load(); got != 1 {
		t.Fatalf("build response close count = %d, want 1", got)
	}
}

func TestBuildImageSurfacesEmbeddedBuildError(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("{\"errorDetail\":{\"message\":\"Dockerfile syntax error\"},\"error\":\"Dockerfile syntax error\"}\n")}
	api := &fakeAPI{imageBuild: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
		return client.ImageBuildResult{Body: body}, nil
	}}
	runtime := newTestRuntime(t, api)

	err := runtime.BuildImage(context.Background(), BuildRequest{Context: strings.NewReader("tar"), Tag: "test:latest"})
	if err == nil || !strings.Contains(err.Error(), "Dockerfile syntax error") {
		t.Fatalf("BuildImage() error = %v", err)
	}
	if got := body.closed.Load(); got != 1 {
		t.Fatalf("build response close count = %d, want 1", got)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	var closes atomic.Int32
	runtime, err := NewWithClient(&fakeAPI{close: func() error {
		closes.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatalf("NewWithClient() error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("close calls = %d, want 1", got)
	}
}

type trackingReadCloser struct {
	io.Reader
	closed atomic.Int32
}

func (r *trackingReadCloser) Close() error {
	r.closed.Add(1)
	return nil
}

func dockerFrame(stream byte, payload string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, payload...)
}
