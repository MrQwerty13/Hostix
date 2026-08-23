package detect

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDetectRequirementsFastAPIProject(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "FastAPI>=0.115\nuvicorn[standard]==0.34\n")
	writeTestFile(t, root, "app/main.py", "from fastapi import FastAPI\n\napp = FastAPI()\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	assertResultBasics(t, result, root, []string{"requirements.txt"}, FrameworkFastAPI)
	wantCommand := []string{"python", "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"}
	if !reflect.DeepEqual(result.DefaultCommand, wantCommand) {
		t.Fatalf("DefaultCommand = %#v, want %#v", result.DefaultCommand, wantCommand)
	}
}

func TestDetectPEP621FlaskProject(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pyproject.toml", `
[project]
name = "sample"
dependencies = [
  "Flask>=3.0", # an inline comment
]

[project.optional-dependencies]
test = ["django"]
`)
	writeTestFile(t, root, "app.py", "from flask import Flask\napp: Flask = Flask(__name__)\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	assertResultBasics(t, result, root, []string{"pyproject.toml"}, FrameworkFlask)
	wantCommand := []string{
		"python", "-m", "flask", "--app", "app:app",
		"run", "--host", "0.0.0.0", "--port", "8000",
	}
	if !reflect.DeepEqual(result.DefaultCommand, wantCommand) {
		t.Fatalf("DefaultCommand = %#v, want %#v", result.DefaultCommand, wantCommand)
	}
}

func TestDetectPoetryDjangoProject(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "pyproject.toml", `
[tool.poetry]
name = "sample"

[tool.poetry.dependencies]
python = "^3.12"
Django = "^5.1"
`)
	writeTestFile(t, root, "manage.py", `
import os
from django.core.management import execute_from_command_line
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "sample.settings")
execute_from_command_line()
`)

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	assertResultBasics(t, result, root, []string{"pyproject.toml"}, FrameworkDjango)
	wantCommand := []string{"python", "manage.py", "runserver", "0.0.0.0:8000"}
	if !reflect.DeepEqual(result.DefaultCommand, wantCommand) {
		t.Fatalf("DefaultCommand = %#v, want %#v", result.DefaultCommand, wantCommand)
	}
}

func TestDetectReturnsBothPythonMarkersInStableOrder(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "requests==2.32.0\n")
	writeTestFile(t, root, "pyproject.toml", "[project]\nname = 'worker'\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	assertResultBasics(t, result, root, []string{"pyproject.toml", "requirements.txt"}, "")
	if result.DefaultCommand != nil {
		t.Fatalf("DefaultCommand = %#v, want nil for a generic Python project", result.DefaultCommand)
	}
}

func TestDetectCanInferFlaskFromConventionalSource(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "-r requirements/base.txt\n")
	writeTestFile(t, root, "main.py", "import flask\napplication = flask.Flask(__name__)\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.Framework != FrameworkFlask {
		t.Fatalf("Framework = %q, want %q", result.Framework, FrameworkFlask)
	}
	if got, want := result.DefaultCommand[4], "main:application"; got != want {
		t.Fatalf("application reference = %q, want %q", got, want)
	}
}

func TestDetectFastAPIWithoutUvicornLeavesCommandUnset(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "fastapi==0.115.0\n")
	writeTestFile(t, root, "main.py", "from fastapi import FastAPI\napp = FastAPI()\n")

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.Framework != FrameworkFastAPI {
		t.Fatalf("Framework = %q, want %q", result.Framework, FrameworkFastAPI)
	}
	if result.DefaultCommand != nil {
		t.Fatalf("DefaultCommand = %#v, want nil without an ASGI server dependency", result.DefaultCommand)
	}
}

func TestDetectDoesNotTreatCommentsOrOptionalDependenciesAsRuntimeFrameworks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "# flask is not installed\nrequests>=2 # django is not installed\n")
	writeTestFile(t, root, "pyproject.toml", `
[project]
name = "fastapi-is-in-the-description-only"
dependencies = ["requests"]

[project.optional-dependencies]
web = ["fastapi", "uvicorn"]
`)

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.Framework != "" {
		t.Fatalf("Framework = %q, want no framework", result.Framework)
	}
}

func TestDetectDoesNotTreatAnUnimportedExampleAsAnEntrypoint(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "requests>=2\n")
	writeTestFile(t, root, "main.py", `
# This is documentation, not an executable application.
example = "app = FastAPI()"
app = FastAPI()
`)

	result, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.Framework != "" {
		t.Fatalf("Framework = %q, want no framework without a framework import", result.Framework)
	}
}

func TestDetectRejectsDirectoryWithoutPythonMarkers(t *testing.T) {
	root := t.TempDir()

	_, err := Detect(root)
	if !errors.Is(err, ErrNotPython) {
		t.Fatalf("Detect() error = %v, want ErrNotPython", err)
	}
	for _, fragment := range []string{"pyproject.toml", "requirements.txt", "dependency manifest"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not contain actionable detail %q", err, fragment)
		}
	}
}

func TestDetectRejectsInvalidProjectPaths(t *testing.T) {
	root := t.TempDir()
	file := writeTestFile(t, root, "project.txt", "not a directory")

	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "missing", path: filepath.Join(root, "missing")},
		{name: "regular file", path: file},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Detect(test.path)
			if !errors.Is(err, ErrInvalidProject) {
				t.Fatalf("Detect() error = %v, want ErrInvalidProject", err)
			}
		})
	}
}

func TestDetectRejectsMarkerThatIsNotAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "requirements.txt"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := Detect(root)
	if !errors.Is(err, ErrInvalidProject) {
		t.Fatalf("Detect() error = %v, want ErrInvalidProject", err)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Detect() error = %q, want regular-file guidance", err)
	}
}

func TestDetectRejectsMultipleFrameworks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "flask==3.1\n")
	writeTestFile(t, root, "pyproject.toml", "[project]\ndependencies = ['Django>=5']\n")

	result, err := Detect(root)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Detect() error = %v, want ErrAmbiguous", err)
	}
	if result.Stack != StackPython {
		t.Fatalf("partial result Stack = %q, want %q", result.Stack, StackPython)
	}
	if got, want := err.Error(), "django, flask"; !strings.Contains(got, want) {
		t.Fatalf("error = %q, want deterministic framework list %q", got, want)
	}
	if !strings.Contains(err.Error(), "explicitly") {
		t.Fatalf("error = %q, want actionable override guidance", err)
	}
}

func TestDetectRejectsMultipleEntrypointsForOneFramework(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "requirements.txt", "fastapi\nuvicorn\n")
	writeTestFile(t, root, "main.py", "from fastapi import FastAPI\napp = FastAPI()\n")
	writeTestFile(t, root, "app/main.py", "from fastapi import FastAPI\napi = FastAPI()\n")

	result, err := Detect(root)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Detect() error = %v, want ErrAmbiguous", err)
	}
	if result.Framework != FrameworkFastAPI {
		t.Fatalf("partial result Framework = %q, want %q", result.Framework, FrameworkFastAPI)
	}
	for _, fragment := range []string{"app/main.py", "main.py", "start command"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not contain %q", err, fragment)
		}
	}
}

func assertResultBasics(t *testing.T, result Result, root string, manifests []string, framework Framework) {
	t.Helper()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	if result.Stack != StackPython {
		t.Errorf("Stack = %q, want %q", result.Stack, StackPython)
	}
	if result.ProjectRoot != filepath.Clean(absRoot) {
		t.Errorf("ProjectRoot = %q, want %q", result.ProjectRoot, filepath.Clean(absRoot))
	}
	if !reflect.DeepEqual(result.Manifests, manifests) {
		t.Errorf("Manifests = %#v, want %#v", result.Manifests, manifests)
	}
	if result.Framework != framework {
		t.Errorf("Framework = %q, want %q", result.Framework, framework)
	}
}

func writeTestFile(t *testing.T, root, relativePath, contents string) string {
	t.Helper()
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
