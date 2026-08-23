// Package docker implements the Hostix runtime contract with the Docker Engine
// API. It deliberately uses the Go SDK instead of invoking the docker CLI.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	// LabelManaged marks resources that Hostix owns and may manage.
	LabelManaged = "io.hostix.managed"
	// LabelProjectDir records the absolute source project directory.
	LabelProjectDir = "io.hostix.project-dir"
	// LabelRuntime records the adapter that created a resource.
	LabelRuntime = "io.hostix.runtime"
	// LabelInstanceName records the stable Hostix instance name.
	LabelInstanceName = "io.hostix.instance-name"

	managedLabelValue = "true"
	workspaceTarget   = "/workspace"
)

// APIClient is the narrow part of the official Moby client used by Hostix.
// Keeping it as an interface makes adapter tests independent of a Docker daemon.
// NewWithClient takes ownership of the supplied client and closes it from Close.
type APIClient interface {
	ContainerCreate(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ExecCreate(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
	ImageBuild(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error)
	Close() error
}

// Runtime manages Docker containers through the Engine API.
type Runtime struct {
	api       APIClient
	closeOnce sync.Once
	closeErr  error
}

// DockerRuntime is an explicit alias for callers that prefer the adapter's full
// name over docker.Runtime.
type DockerRuntime = Runtime

// BuildRequest describes a Docker build. Context must be a tar archive accepted
// by the Engine API; Dockerfile is a path inside that archive.
type BuildRequest struct {
	Context    io.Reader
	Dockerfile string
	Tag        string
	Output     io.Writer
}

var _ hostruntime.Runtime = (*Runtime)(nil)

// New creates a Docker runtime from the standard DOCKER_HOST and TLS environment
// variables. API-version negotiation is enabled by the current Moby client.
func New() (*Runtime, error) {
	api, err := client.New(client.FromEnv, client.WithUserAgent("hostix"))
	if err != nil {
		return nil, fmt.Errorf("create Docker API client: %w", err)
	}

	return NewWithClient(api)
}

// NewWithClient creates a runtime around an injected client. The runtime owns
// the client and callers should call Close when the runtime is no longer needed.
func NewWithClient(api APIClient) (*Runtime, error) {
	if api == nil {
		return nil, errors.New("create Docker runtime: API client is nil")
	}
	return &Runtime{api: api}, nil
}

// Close releases the SDK transport. It is safe to call more than once.
func (r *Runtime) Close() error {
	if r == nil || r.api == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if err := r.api.Close(); err != nil {
			r.closeErr = fmt.Errorf("close Docker API client: %w", err)
		}
	})
	return r.closeErr
}

// Name returns the runtime selector name.
func (*Runtime) Name() string {
	return "docker"
}

// Create creates a stopped Hostix-owned container. Higher-level orchestration
// decides whether an existing container should be reused or replaced.
func (r *Runtime) Create(ctx context.Context, request hostruntime.CreateRequest) (hostruntime.Instance, error) {
	options, err := createOptions(request)
	if err != nil {
		return hostruntime.Instance{}, err
	}

	response, err := r.api.ContainerCreate(ctx, options)
	if err != nil {
		return hostruntime.Instance{}, wrapCreateError(request.Name, err)
	}
	if response.ID == "" {
		return hostruntime.Instance{}, fmt.Errorf("create Docker container %q: daemon returned an empty ID", request.Name)
	}

	return hostruntime.Instance{ID: response.ID, Name: options.Name}, nil
}

func createOptions(request hostruntime.CreateRequest) (client.ContainerCreateOptions, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return client.ContainerCreateOptions{}, errors.New("create Docker container: name is required")
	}
	imageName := strings.TrimSpace(request.Image)
	if imageName == "" {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: image is required", name)
	}

	nanoCPUs, err := normalizeCPUCount(request.CPUCount)
	if err != nil {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: %w", name, err)
	}
	if request.MemoryBytes < 0 {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: memory bytes must not be negative", name)
	}

	restartPolicy, err := normalizeRestartPolicy(request.RestartPolicy)
	if err != nil {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: %w", name, err)
	}

	exposedPorts, portBindings, err := normalizePorts(request.Ports)
	if err != nil {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: %w", name, err)
	}

	environment, err := normalizeEnvironment(request.Environment)
	if err != nil {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: %w", name, err)
	}

	projectDir, mounts, err := normalizeProjectDir(request.ProjectDir)
	if err != nil {
		return client.ContainerCreateOptions{}, fmt.Errorf("create Docker container %q: %w", name, err)
	}
	workingDir := ""
	if projectDir != "" {
		workingDir = workspaceTarget
	}

	labels := map[string]string{
		LabelManaged:      managedLabelValue,
		LabelProjectDir:   projectDir,
		LabelRuntime:      "docker",
		LabelInstanceName: name,
	}

	config := &container.Config{
		Cmd:          append([]string(nil), request.Command...),
		Env:          environment,
		ExposedPorts: exposedPorts,
		Labels:       labels,
		WorkingDir:   workingDir,
	}
	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: restartPolicy,
		Resources: container.Resources{
			Memory:   request.MemoryBytes,
			NanoCPUs: nanoCPUs,
		},
		Mounts: mounts,
	}

	return client.ContainerCreateOptions{
		Config:     config,
		HostConfig: hostConfig,
		Image:      imageName,
		Name:       name,
	}, nil
}

func normalizeCPUCount(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, errors.New("CPU count must be a finite, non-negative number")
	}
	if value > float64(math.MaxInt64)/1_000_000_000 {
		return 0, errors.New("CPU count is too large")
	}
	return int64(math.Round(value * 1_000_000_000)), nil
}

func normalizeRestartPolicy(value string) (container.RestartPolicy, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "none" {
		mode = string(container.RestartPolicyDisabled)
	}
	policy := container.RestartPolicy{Name: container.RestartPolicyMode(mode)}
	if err := container.ValidateRestartPolicy(policy); err != nil {
		return container.RestartPolicy{}, fmt.Errorf("restart policy %q is invalid: %w", value, err)
	}
	return policy, nil
}

func normalizePorts(bindings []hostruntime.PortBinding) (network.PortSet, network.PortMap, error) {
	if len(bindings) == 0 {
		return nil, nil, nil
	}

	exposed := make(network.PortSet, len(bindings))
	published := make(network.PortMap, len(bindings))
	for _, binding := range bindings {
		if binding.ContainerPort == 0 {
			return nil, nil, errors.New("container port must be greater than zero")
		}

		protocol := strings.ToLower(strings.TrimSpace(binding.Protocol))
		if protocol == "" {
			protocol = string(network.TCP)
		}
		switch network.IPProtocol(protocol) {
		case network.TCP, network.UDP, network.SCTP:
		default:
			return nil, nil, fmt.Errorf("unsupported port protocol %q", binding.Protocol)
		}

		port, err := network.ParsePort(fmt.Sprintf("%d/%s", binding.ContainerPort, protocol))
		if err != nil {
			return nil, nil, fmt.Errorf("parse container port %d/%s: %w", binding.ContainerPort, protocol, err)
		}

		hostPort := ""
		if binding.HostPort != 0 {
			hostPort = strconv.Itoa(int(binding.HostPort))
		}
		exposed[port] = struct{}{}
		published[port] = append(published[port], network.PortBinding{HostPort: hostPort})
	}
	return exposed, published, nil
}

func normalizeEnvironment(environment map[string]string) ([]string, error) {
	if len(environment) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(environment))
	for key := range environment {
		if key == "" || strings.ContainsRune(key, '=') {
			return nil, fmt.Errorf("environment variable name %q is invalid", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result, nil
}

func normalizeProjectDir(projectDir string) (string, []mount.Mount, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", nil, nil
	}

	absolute, err := filepath.Abs(projectDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", nil, fmt.Errorf("inspect project directory %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("project path %q is not a directory", absolute)
	}

	return absolute, []mount.Mount{{
		Type:   mount.TypeBind,
		Source: absolute,
		Target: workspaceTarget,
	}}, nil
}

// Start starts a created or stopped container. Starting an already-running
// container is treated as success to preserve idempotency.
func (r *Runtime) Start(ctx context.Context, id string) error {
	_, err := r.api.ContainerStart(ctx, id, client.ContainerStartOptions{})
	if cerrdefs.IsNotModified(err) {
		return nil
	}
	if err != nil {
		return wrapContainerError("start", id, err)
	}
	return nil
}

// Stop gracefully stops a running container. Stopping an already-stopped
// container is treated as success.
func (r *Runtime) Stop(ctx context.Context, id string) error {
	_, err := r.api.ContainerStop(ctx, id, client.ContainerStopOptions{})
	if cerrdefs.IsNotModified(err) {
		return nil
	}
	if err != nil {
		return wrapContainerError("stop", id, err)
	}
	return nil
}

// Remove deletes a container without implicitly removing attached volumes.
func (r *Runtime) Remove(ctx context.Context, id string, options hostruntime.RemoveOptions) error {
	_, err := r.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: options.Force})
	if err != nil {
		return wrapContainerError("remove", id, err)
	}
	return nil
}

// Exec runs a command and returns separated stdout and stderr. Interactive mode
// allocates a TTY; because the Runtime contract has no input stream, stdin is not
// attached.
func (r *Runtime) Exec(ctx context.Context, id string, request hostruntime.ExecRequest) (hostruntime.ExecResult, error) {
	if len(request.Command) == 0 {
		return hostruntime.ExecResult{}, errors.New("exec in Docker container: command is required")
	}
	environment, err := normalizeEnvironment(request.Environment)
	if err != nil {
		return hostruntime.ExecResult{}, fmt.Errorf("exec in Docker container %q: %w", id, err)
	}

	created, err := r.api.ExecCreate(ctx, id, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          append([]string(nil), request.Command...),
		Env:          environment,
		WorkingDir:   request.WorkingDir,
		TTY:          request.Interactive,
	})
	if err != nil {
		return hostruntime.ExecResult{}, wrapContainerError("create exec in", id, err)
	}
	if created.ID == "" {
		return hostruntime.ExecResult{}, fmt.Errorf("create exec in Docker container %q: daemon returned an empty exec ID", id)
	}

	attached, err := r.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: request.Interactive})
	if err != nil {
		return hostruntime.ExecResult{}, wrapContainerError("attach exec in", id, err)
	}
	defer attached.Close()
	if attached.Reader == nil {
		return hostruntime.ExecResult{}, fmt.Errorf("attach exec in Docker container %q: daemon returned no output stream", id)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if request.Interactive {
		_, err = io.Copy(&stdout, attached.Reader)
	} else {
		_, err = stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
	}
	if err != nil {
		return hostruntime.ExecResult{}, fmt.Errorf("read exec output from Docker container %q: %w", id, err)
	}

	inspected, err := r.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return hostruntime.ExecResult{}, wrapContainerError("inspect exec in", id, err)
	}
	if inspected.Running {
		return hostruntime.ExecResult{}, fmt.Errorf("inspect exec in Docker container %q: exec is still running after its output stream closed", id)
	}

	return hostruntime.ExecResult{
		ExitCode: inspected.ExitCode,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
	}, nil
}

// Logs returns a plain stream. Docker's multiplexing headers are removed for
// non-TTY containers; closing the returned reader also closes the SDK stream.
func (r *Runtime) Logs(ctx context.Context, id string, options hostruntime.LogsOptions) (io.ReadCloser, error) {
	if options.Tail < 0 {
		return nil, fmt.Errorf("read logs from Docker container %q: tail must not be negative", id)
	}

	inspected, err := r.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, wrapContainerError("inspect for logs", id, err)
	}

	tail := "all"
	if options.Tail > 0 {
		tail = strconv.Itoa(options.Tail)
	}
	since := ""
	if !options.Since.IsZero() {
		since = strconv.FormatInt(options.Since.Unix(), 10)
	}

	stream, err := r.api.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     options.Follow,
		Tail:       tail,
		Since:      since,
	})
	if err != nil {
		return nil, wrapContainerError("read logs from", id, err)
	}
	if stream == nil {
		return nil, fmt.Errorf("read logs from Docker container %q: daemon returned no log stream", id)
	}

	if inspected.Container.Config == nil || inspected.Container.Config.Tty {
		return stream, nil
	}
	return demultiplexLogs(stream), nil
}

func demultiplexLogs(source io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	closer := &onceCloser{closer: source}
	go func() {
		defer closer.Close()
		_, err := stdcopy.StdCopy(writer, writer, source)
		if err != nil {
			_ = writer.CloseWithError(fmt.Errorf("decode Docker log stream: %w", err))
			return
		}
		_ = writer.Close()
	}()
	return &demultiplexedReadCloser{PipeReader: reader, closeSource: closer.Close}
}

type onceCloser struct {
	closer io.Closer
	once   sync.Once
	err    error
}

func (c *onceCloser) Close() error {
	c.once.Do(func() { c.err = c.closer.Close() })
	return c.err
}

type demultiplexedReadCloser struct {
	*io.PipeReader
	closeSource func() error
}

func (r *demultiplexedReadCloser) Close() error {
	return errors.Join(r.closeSource(), r.PipeReader.Close())
}

// Status inspects any Docker container, including non-Hostix containers. The
// Managed field is true only for an exact Hostix ownership label match.
func (r *Runtime) Status(ctx context.Context, id string) (hostruntime.Status, error) {
	result, err := r.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return hostruntime.Status{}, wrapContainerError("inspect", id, err)
	}

	status, err := statusFromInspect(result.Container)
	if err != nil {
		return hostruntime.Status{}, fmt.Errorf("inspect Docker container %q: %w", id, err)
	}
	return status, nil
}

func statusFromInspect(inspected container.InspectResponse) (hostruntime.Status, error) {
	status := hostruntime.Status{
		ID:      inspected.ID,
		Name:    normalizeContainerName(inspected.Name),
		State:   hostruntime.StateUnknown,
		Managed: hasManagedLabel(inspected.Config),
	}
	if inspected.State == nil {
		return status, nil
	}

	status.State = normalizeState(inspected.State.Status)
	if inspected.State.StartedAt == "" || strings.HasPrefix(inspected.State.StartedAt, "0001-01-01T") {
		return status, nil
	}
	startedAt, err := time.Parse(time.RFC3339Nano, inspected.State.StartedAt)
	if err != nil {
		return hostruntime.Status{}, fmt.Errorf("parse started-at timestamp %q: %w", inspected.State.StartedAt, err)
	}
	status.StartedAt = startedAt
	return status, nil
}

func hasManagedLabel(config *container.Config) bool {
	return config != nil && config.Labels[LabelManaged] == managedLabelValue
}

// List returns only containers bearing Hostix's exact ownership label. The
// client-side label check is intentional defense in depth around daemon filters.
func (r *Runtime) List(ctx context.Context) ([]hostruntime.Status, error) {
	filters := make(client.Filters).Add("label", LabelManaged+"="+managedLabelValue)
	result, err := r.api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("list Docker containers managed by Hostix: %w", err)
	}

	statuses := make([]hostruntime.Status, 0, len(result.Items))
	for _, item := range result.Items {
		if item.Labels[LabelManaged] != managedLabelValue {
			continue
		}
		statuses = append(statuses, hostruntime.Status{
			ID:      item.ID,
			Name:    summaryName(item.Names, item.Labels),
			State:   normalizeState(item.State),
			Managed: true,
		})
	}
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Name == statuses[j].Name {
			return statuses[i].ID < statuses[j].ID
		}
		return statuses[i].Name < statuses[j].Name
	})
	return statuses, nil
}

func summaryName(names []string, labels map[string]string) string {
	if len(names) != 0 {
		return normalizeContainerName(names[0])
	}
	return labels[LabelInstanceName]
}

func normalizeContainerName(name string) string {
	return strings.TrimPrefix(name, "/")
}

func normalizeState(state container.ContainerState) hostruntime.State {
	switch state {
	case container.StateCreated:
		return hostruntime.StateCreated
	case container.StateRunning, container.StatePaused, container.StateRestarting:
		return hostruntime.StateRunning
	case container.StateExited, container.StateDead, container.StateRemoving:
		return hostruntime.StateStopped
	default:
		return hostruntime.StateUnknown
	}
}

// BuildImage builds and tags an image, consumes the complete asynchronous SDK
// response, and surfaces build failures embedded in successful HTTP responses.
func (r *Runtime) BuildImage(ctx context.Context, request BuildRequest) error {
	if request.Context == nil {
		return errors.New("build Docker image: build context is required")
	}
	tag := strings.TrimSpace(request.Tag)
	if tag == "" {
		return errors.New("build Docker image: tag is required")
	}
	dockerfile, err := normalizeDockerfilePath(request.Dockerfile)
	if err != nil {
		return fmt.Errorf("build Docker image %q: %w", tag, err)
	}

	response, err := r.api.ImageBuild(ctx, request.Context, client.ImageBuildOptions{
		Tags:       []string{tag},
		Dockerfile: dockerfile,
		Remove:     true,
		Labels: map[string]string{
			LabelManaged: managedLabelValue,
			LabelRuntime: "docker",
		},
	})
	if err != nil {
		if response.Body != nil {
			err = errors.Join(err, response.Body.Close())
		}
		return fmt.Errorf("build Docker image %q: %w", tag, err)
	}
	if response.Body == nil {
		return fmt.Errorf("build Docker image %q: daemon returned no build stream", tag)
	}

	consumeErr := consumeBuildResponse(response.Body, request.Output)
	closeErr := response.Body.Close()
	if err := errors.Join(consumeErr, closeErr); err != nil {
		return fmt.Errorf("build Docker image %q: %w", tag, err)
	}
	return nil
}

func normalizeDockerfilePath(dockerfile string) (string, error) {
	if strings.TrimSpace(dockerfile) == "" {
		return "Dockerfile", nil
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(dockerfile), "\\", "/")
	cleaned := path.Clean(normalized)
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "." {
		return "", fmt.Errorf("Dockerfile path %q must be relative to the build context", dockerfile)
	}
	return cleaned, nil
}

type buildMessage struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	Progress    string          `json:"progress"`
	Stream      string          `json:"stream"`
	Error       string          `json:"error"`
	ErrorDetail *buildError     `json:"errorDetail"`
	Aux         json.RawMessage `json:"aux"`
}

type buildError struct {
	Message string `json:"message"`
}

func consumeBuildResponse(source io.Reader, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}

	decoder := json.NewDecoder(source)
	var buildErr error
	var outputErr error
	for {
		var message buildMessage
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.Join(buildErr, outputErr)
			}
			return errors.Join(buildErr, outputErr, fmt.Errorf("decode build response: %w", err))
		}

		if buildErr == nil {
			errorMessage := message.Error
			if errorMessage == "" && message.ErrorDetail != nil {
				errorMessage = message.ErrorDetail.Message
			}
			if errorMessage != "" {
				buildErr = fmt.Errorf("Docker build failed: %s", errorMessage)
			}
		}

		if outputErr == nil {
			if text := formatBuildMessage(message); text != "" {
				if _, err := io.WriteString(output, text); err != nil {
					outputErr = fmt.Errorf("write build output: %w", err)
				}
			}
		}
	}
}

func formatBuildMessage(message buildMessage) string {
	if message.Stream != "" {
		return message.Stream
	}
	if message.Status != "" {
		prefix := ""
		if message.ID != "" {
			prefix = message.ID + ": "
		}
		line := prefix + message.Status
		if message.Progress != "" {
			line += " " + message.Progress
		}
		return line + "\n"
	}
	if len(message.Aux) != 0 {
		var aux struct {
			ID string `json:"ID"`
		}
		if json.Unmarshal(message.Aux, &aux) == nil && aux.ID != "" {
			return "built image " + aux.ID + "\n"
		}
	}
	return ""
}

func wrapCreateError(name string, err error) error {
	if cerrdefs.IsConflict(err) || cerrdefs.IsAlreadyExists(err) {
		err = errors.Join(hostruntime.ErrAlreadyExists, err)
	}
	return fmt.Errorf("create Docker container %q: %w", name, err)
}

func wrapContainerError(action, id string, err error) error {
	if cerrdefs.IsNotFound(err) {
		err = errors.Join(hostruntime.ErrNotFound, err)
	}
	return fmt.Errorf("%s Docker container %q: %w", action, id, err)
}
