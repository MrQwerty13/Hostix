package image

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// PythonBaseImage is intentionally fixed so generation is repeatable and
	// upgrades can be reviewed explicitly.
	PythonBaseImage = "python:3.12-slim"

	// GeneratedDockerfileName is reserved inside generated build contexts. It
	// deliberately differs from Dockerfile so a project-owned file is retained.
	GeneratedDockerfileName = ".hostix.Dockerfile"
)

var (
	// ErrNoPythonDependencyFile indicates that neither supported Python
	// dependency input exists at the project root.
	ErrNoPythonDependencyFile = errors.New("no supported Python dependency file found")

	// ErrInvalidCommand indicates that a safe exec-form container command could
	// not be generated from caller input.
	ErrInvalidCommand = errors.New("invalid container command")
)

// PythonDependencyFile identifies a supported Python dependency input.
type PythonDependencyFile string

const (
	RequirementsTXT PythonDependencyFile = "requirements.txt"
	PyProjectTOML   PythonDependencyFile = "pyproject.toml"
)

// PythonBuildOptions describes the project and command used to create a build
// context. TempDir is optional and primarily useful to control temporary-file
// placement; an empty value uses the operating-system default.
type PythonBuildOptions struct {
	ProjectDir string
	Command    []string
	TempDir    string
}

// NewPythonBuildContext generates a Dockerfile and packages it with an
// unchanged snapshot of the project in a temporary deterministic TAR archive.
// Common .dockerignore patterns are honored, while required build inputs and
// the generated Dockerfile remain present. If both supported dependency files
// exist, requirements.txt takes precedence.
func NewPythonBuildContext(options PythonBuildOptions) (*BuildContext, error) {
	projectDir, err := canonicalProjectDir(options.ProjectDir)
	if err != nil {
		return nil, err
	}
	dependency, err := DetectPythonDependencyFile(projectDir)
	if err != nil {
		return nil, err
	}
	dockerfile, err := GeneratePythonDockerfile(dependency, options.Command)
	if err != nil {
		return nil, err
	}
	return newBuildContext(projectDir, options.TempDir, GeneratedDockerfileName, dockerfile, string(dependency))
}

// DetectPythonDependencyFile selects the supported dependency input at the
// project root. requirements.txt is preferred when both files are present.
func DetectPythonDependencyFile(projectDir string) (PythonDependencyFile, error) {
	for _, candidate := range []PythonDependencyFile{RequirementsTXT, PyProjectTOML} {
		path := filepath.Join(projectDir, string(candidate))
		info, err := os.Lstat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			return candidate, nil
		case err == nil:
			return "", fmt.Errorf("Python dependency input %q must be a regular file", path)
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return "", fmt.Errorf("inspect Python dependency input %q: %w", path, err)
		}
	}
	return "", fmt.Errorf("%w: expected %s or %s in %q", ErrNoPythonDependencyFile, RequirementsTXT, PyProjectTOML, projectDir)
}

// GeneratePythonDockerfile returns a fixed Python 3.12-slim Dockerfile. The
// caller supplies the application command as an argument vector; it is encoded
// as exec-form JSON and is never interpreted by a shell.
func GeneratePythonDockerfile(dependency PythonDependencyFile, command []string) ([]byte, error) {
	encodedCommand, err := encodeCommand(command)
	if err != nil {
		return nil, err
	}

	var install string
	switch dependency {
	case RequirementsTXT:
		install = "COPY requirements.txt /tmp/requirements.txt\nRUN python -m pip install -r /tmp/requirements.txt\n\nCOPY --chown=hostix:hostix . /app"
	case PyProjectTOML:
		install = "COPY --chown=hostix:hostix . /app\nRUN python -m pip install ."
	default:
		return nil, fmt.Errorf("unsupported Python dependency file %q", dependency)
	}

	dockerfile := fmt.Sprintf(`FROM %s

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /app

RUN groupadd --system hostix \
    && useradd --system --gid hostix --create-home hostix

%s

USER hostix

CMD %s
`, PythonBaseImage, install, encodedCommand)
	return []byte(dockerfile), nil
}

func canonicalProjectDir(projectDir string) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", errors.New("project directory is required")
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("resolve project directory %q: %w", projectDir, err)
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project directory %q: %w", projectDir, err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("inspect project directory %q: %w", projectDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path %q is not a directory", projectDir)
	}
	return realPath, nil
}

func encodeCommand(command []string) (string, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return "", fmt.Errorf("%w: executable is required", ErrInvalidCommand)
	}
	for _, arg := range command {
		if strings.ContainsRune(arg, '\x00') {
			return "", fmt.Errorf("%w: arguments cannot contain NUL bytes", ErrInvalidCommand)
		}
	}
	encoded, err := json.Marshal(command)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	return string(encoded), nil
}
