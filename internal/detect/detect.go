// Package detect identifies the application stack and safe launch defaults for
// a project directory. Detection is deliberately based on project files only;
// it never executes code from the project.
package detect

import "errors"

// Stack is a project ecosystem understood by Hostix.
type Stack string

const (
	// StackPython identifies a Python project.
	StackPython Stack = "python"
)

// Framework is a supported application framework found in a project.
type Framework string

const (
	FrameworkDjango  Framework = "django"
	FrameworkFastAPI Framework = "fastapi"
	FrameworkFlask   Framework = "flask"
)

var (
	// ErrNotPython means the directory has no supported Python project marker.
	ErrNotPython = errors.New("not a Python project")
	// ErrAmbiguous means detection found conflicting frameworks or entrypoints.
	ErrAmbiguous = errors.New("ambiguous Python project")
	// ErrInvalidProject means the supplied path or a required project file could
	// not be inspected.
	ErrInvalidProject = errors.New("invalid project directory")
)

// Result contains deterministic facts inferred from a project directory.
// DefaultCommand is nil when the Python stack is known but a launch command
// cannot be inferred safely.
type Result struct {
	Stack          Stack
	ProjectRoot    string
	Manifests      []string
	Framework      Framework
	DefaultCommand []string
}

// Detect inspects projectPath for a supported project. The current MVP detects
// Python; future stack detectors can be composed here without changing callers.
func Detect(projectPath string) (Result, error) {
	return detectPython(projectPath)
}
