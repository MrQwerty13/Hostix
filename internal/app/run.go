package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MrQwerty13/Hostix/internal/detect"
	"github.com/MrQwerty13/Hostix/internal/image"
	hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"
	dockerruntime "github.com/MrQwerty13/Hostix/internal/runtime/docker"
)

const defaultWebPort uint16 = 8000

// DockerBackend is the narrow Docker capability set required by the run use
// case. It is intentionally smaller than runtime.Runtime to keep orchestration
// tests independent from a Docker daemon.
type DockerBackend interface {
	BuildImage(context.Context, dockerruntime.BuildRequest) error
	Status(context.Context, string) (hostruntime.Status, error)
	Remove(context.Context, string, hostruntime.RemoveOptions) error
	Create(context.Context, hostruntime.CreateRequest) (hostruntime.Instance, error)
	Start(context.Context, string) error
	Close() error
}

type projectDetector func(string) (detect.Result, error)
type pythonContextBuilder func(image.PythonBuildOptions) (*image.BuildContext, error)

// DockerRunService implements the Python-on-Docker vertical slice.
type DockerRunService struct {
	backend      DockerBackend
	progress     io.Writer
	detect       projectDetector
	buildContext pythonContextBuilder
}

// NewDockerRunService constructs a run service backed by the local Docker
// Engine configuration.
func NewDockerRunService(progress io.Writer) (*DockerRunService, error) {
	backend, err := dockerruntime.New()
	if err != nil {
		return nil, err
	}
	return newDockerRunService(backend, progress), nil
}

func newDockerRunService(backend DockerBackend, progress io.Writer) *DockerRunService {
	if progress == nil {
		progress = io.Discard
	}
	return &DockerRunService{
		backend:      backend,
		progress:     progress,
		detect:       detect.Detect,
		buildContext: image.NewPythonBuildContext,
	}
}

// Close releases the underlying Docker client.
func (s *DockerRunService) Close() error {
	if s == nil || s.backend == nil {
		return nil
	}
	return s.backend.Close()
}

// Run detects, builds, creates, and starts a Python project. An existing
// Hostix-owned container with the stable project name is replaced only after a
// successful image build. A same-name foreign container is never removed.
func (s *DockerRunService) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if s == nil || s.backend == nil {
		return RunResult{}, errors.New("run project: Docker backend is not configured")
	}
	if err := validateRunSelectors(request.Stack, request.Runtime); err != nil {
		return RunResult{}, err
	}

	fmt.Fprintln(s.progress, "Detecting Python project...")
	project, err := s.detect(request.ProjectDir)
	if err != nil && !(len(request.Command) > 0 && errors.Is(err, detect.ErrAmbiguous)) {
		return RunResult{}, fmt.Errorf("detect project: %w", err)
	}
	if project.Stack != detect.StackPython {
		return RunResult{}, fmt.Errorf("detect project: unsupported stack %q", project.Stack)
	}

	command := append([]string(nil), request.Command...)
	if len(command) == 0 {
		command = append(command, project.DefaultCommand...)
	}
	if len(command) == 0 {
		return RunResult{}, errors.New("no safe Python start command was detected; pass one after --, for example: hostix run . -- python main.py")
	}

	identity, err := IdentityForProject(project.ProjectRoot)
	if err != nil {
		return RunResult{}, err
	}
	if name := strings.TrimSpace(request.Name); name != "" {
		identity.Name = name
	}

	buildContext, err := s.buildContext(image.PythonBuildOptions{
		ProjectDir: project.ProjectRoot,
		Command:    command,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("prepare Python image: %w", err)
	}

	fmt.Fprintf(s.progress, "Building image %s...\n", identity.ImageRef)
	buildErr := s.backend.BuildImage(ctx, dockerruntime.BuildRequest{
		Context:    buildContext,
		Dockerfile: buildContext.DockerfileName(),
		Tag:        identity.ImageRef,
		Output:     s.progress,
	})
	closeErr := buildContext.Close()
	if err := errors.Join(buildErr, closeErr); err != nil {
		return RunResult{}, fmt.Errorf("build project image: %w", err)
	}

	replaced, err := s.removeManagedPredecessor(ctx, identity.Name)
	if err != nil {
		return RunResult{}, err
	}

	ports := append([]hostruntime.PortBinding(nil), request.Ports...)
	if len(ports) == 0 && project.Framework != "" {
		ports = []hostruntime.PortBinding{{
			HostPort:      defaultWebPort,
			ContainerPort: defaultWebPort,
			Protocol:      "tcp",
		}}
	}

	fmt.Fprintf(s.progress, "Creating container %s...\n", identity.Name)
	instance, err := s.backend.Create(ctx, hostruntime.CreateRequest{
		Name:        identity.Name,
		Image:       identity.ImageRef,
		Command:     command,
		ProjectDir:  project.ProjectRoot,
		Environment: cloneEnvironment(request.Environment),
		Ports:       ports,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("create project container: %w", err)
	}

	fmt.Fprintf(s.progress, "Starting container %s...\n", identity.Name)
	if err := s.backend.Start(ctx, instance.ID); err != nil {
		cleanupErr := s.backend.Remove(ctx, instance.ID, hostruntime.RemoveOptions{Force: true})
		return RunResult{}, errors.Join(fmt.Errorf("start project container: %w", err), cleanupError(cleanupErr))
	}

	return RunResult{
		Instance:  instance,
		ImageRef:  identity.ImageRef,
		Framework: string(project.Framework),
		Replaced:  replaced,
	}, nil
}

func (s *DockerRunService) removeManagedPredecessor(ctx context.Context, name string) (bool, error) {
	status, err := s.backend.Status(ctx, name)
	if errors.Is(err, hostruntime.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect existing project container: %w", err)
	}
	if !status.Managed {
		return false, fmt.Errorf("container name %q is already used by a container not managed by Hostix: %w", name, hostruntime.ErrAlreadyExists)
	}

	fmt.Fprintf(s.progress, "Replacing existing Hostix container %s...\n", name)
	if err := s.backend.Remove(ctx, status.ID, hostruntime.RemoveOptions{Force: true}); err != nil {
		return false, fmt.Errorf("replace existing project container: %w", err)
	}
	return true, nil
}

func validateRunSelectors(stack, runtimeName string) error {
	stack = strings.ToLower(strings.TrimSpace(stack))
	if stack != "" && stack != "auto" && stack != string(detect.StackPython) {
		return fmt.Errorf("stack %q is not supported yet; this release supports python", stack)
	}
	runtimeName = strings.ToLower(strings.TrimSpace(runtimeName))
	if runtimeName != "" && runtimeName != "auto" && runtimeName != "docker" {
		return fmt.Errorf("runtime %q is not supported by run yet; use docker", runtimeName)
	}
	return nil
}

func cloneEnvironment(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cleanupError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("clean up failed container: %w", err)
}
