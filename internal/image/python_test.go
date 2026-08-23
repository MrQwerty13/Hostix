package image

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDetectPythonDependencyFile(t *testing.T) {
	t.Run("requirements takes precedence", func(t *testing.T) {
		projectDir := t.TempDir()
		writeTestFile(t, projectDir, "requirements.txt", "flask\n", 0o644)
		writeTestFile(t, projectDir, "pyproject.toml", "[project]\n", 0o644)

		got, err := DetectPythonDependencyFile(projectDir)
		if err != nil {
			t.Fatalf("DetectPythonDependencyFile() error = %v", err)
		}
		if got != RequirementsTXT {
			t.Fatalf("DetectPythonDependencyFile() = %q, want %q", got, RequirementsTXT)
		}
	})

	t.Run("pyproject is supported", func(t *testing.T) {
		projectDir := t.TempDir()
		writeTestFile(t, projectDir, "pyproject.toml", "[project]\nname = \"demo\"\n", 0o644)

		got, err := DetectPythonDependencyFile(projectDir)
		if err != nil {
			t.Fatalf("DetectPythonDependencyFile() error = %v", err)
		}
		if got != PyProjectTOML {
			t.Fatalf("DetectPythonDependencyFile() = %q, want %q", got, PyProjectTOML)
		}
	})

	t.Run("missing input has a sentinel error", func(t *testing.T) {
		_, err := DetectPythonDependencyFile(t.TempDir())
		if !errors.Is(err, ErrNoPythonDependencyFile) {
			t.Fatalf("DetectPythonDependencyFile() error = %v, want %v", err, ErrNoPythonDependencyFile)
		}
	})

	t.Run("dependency symlink is rejected", func(t *testing.T) {
		projectDir := t.TempDir()
		writeTestFile(t, projectDir, "real-requirements.txt", "flask\n", 0o644)
		if err := os.Symlink("real-requirements.txt", filepath.Join(projectDir, "requirements.txt")); err != nil {
			t.Fatalf("create dependency symlink: %v", err)
		}

		_, err := DetectPythonDependencyFile(projectDir)
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("DetectPythonDependencyFile() error = %v, want regular-file validation", err)
		}
	})
}

func TestGeneratePythonDockerfileForPyProject(t *testing.T) {
	dockerfile, err := GeneratePythonDockerfile(PyProjectTOML, []string{"python", "-m", "demo"})
	if err != nil {
		t.Fatalf("GeneratePythonDockerfile() error = %v", err)
	}
	got := string(dockerfile)
	for _, expected := range []string{
		"FROM python:3.12-slim",
		"COPY --chown=hostix:hostix . /app",
		"RUN python -m pip install .",
		"USER hostix",
		`CMD ["python","-m","demo"]`,
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("Dockerfile does not contain %q:\n%s", expected, got)
		}
	}
	if strings.Contains(got, "requirements.txt") {
		t.Errorf("pyproject Dockerfile unexpectedly refers to requirements.txt:\n%s", got)
	}
}

func TestGeneratePythonDockerfileEncodesCommandAsJSON(t *testing.T) {
	command := []string{"python", "app.py", `value with \"quotes\"`, "line one\nRUN touch /owned", "$HOME; rm -rf /"}
	dockerfile, err := GeneratePythonDockerfile(RequirementsTXT, command)
	if err != nil {
		t.Fatalf("GeneratePythonDockerfile() error = %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(dockerfile), "\n"), "\n")
	cmdLine := lines[len(lines)-1]
	if !strings.HasPrefix(cmdLine, "CMD ") {
		t.Fatalf("last Dockerfile line = %q, want CMD", cmdLine)
	}
	var decoded []string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(cmdLine, "CMD ")), &decoded); err != nil {
		t.Fatalf("CMD is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, command) {
		t.Fatalf("decoded CMD = %q, want %q", decoded, command)
	}
	if strings.Contains(string(dockerfile), "\nRUN touch /owned") {
		t.Fatal("command argument escaped the JSON-form CMD instruction")
	}
}

func TestGeneratePythonDockerfileRejectsInvalidCommands(t *testing.T) {
	tests := []struct {
		name    string
		command []string
	}{
		{name: "missing", command: nil},
		{name: "empty executable", command: []string{"", "app.py"}},
		{name: "blank executable", command: []string{"  ", "app.py"}},
		{name: "NUL byte", command: []string{"python", "bad\x00argument"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := GeneratePythonDockerfile(RequirementsTXT, test.command)
			if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("GeneratePythonDockerfile() error = %v, want %v", err, ErrInvalidCommand)
			}
		})
	}
}

func TestGeneratePythonDockerfileRejectsUnsupportedDependency(t *testing.T) {
	_, err := GeneratePythonDockerfile(PythonDependencyFile("poetry.lock"), []string{"python", "app.py"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("GeneratePythonDockerfile() error = %v, want unsupported dependency error", err)
	}
}

func TestNewPythonBuildContextValidatesProjectPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "project.py")
	if err := os.WriteFile(file, []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	_, err := NewPythonBuildContext(PythonBuildOptions{ProjectDir: file, Command: []string{"python", "project.py"}})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("NewPythonBuildContext() error = %v, want directory validation", err)
	}
}
