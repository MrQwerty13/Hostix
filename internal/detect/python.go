package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	pyprojectManifest    = "pyproject.toml"
	requirementsManifest = "requirements.txt"
)

var (
	packageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
	packageSeparator   = regexp.MustCompile(`[-_.]+`)
	fastAPIImport      = regexp.MustCompile(`(?m)^[\t ]*(?:from[\t ]+fastapi(?:\.[A-Za-z_][A-Za-z0-9_.]*)?[\t ]+import[\t ]+|import[\t ]+fastapi(?:[\t ,.]|$))`)
	flaskImport        = regexp.MustCompile(`(?m)^[\t ]*(?:from[\t ]+flask(?:\.[A-Za-z_][A-Za-z0-9_.]*)?[\t ]+import[\t ]+|import[\t ]+flask(?:[\t ,.]|$))`)
	fastAPIAssignment  = regexp.MustCompile(`(?m)^[\t ]*([A-Za-z_][A-Za-z0-9_]*)[\t ]*(?::[^=\n]+)?=[\t ]*(?:[A-Za-z_][A-Za-z0-9_]*\.)?FastAPI[\t ]*\(`)
	flaskAssignment    = regexp.MustCompile(`(?m)^[\t ]*([A-Za-z_][A-Za-z0-9_]*)[\t ]*(?::[^=\n]+)?=[\t ]*(?:[A-Za-z_][A-Za-z0-9_]*\.)?Flask[\t ]*\(`)
)

type entrypoint struct {
	framework Framework
	file      string
	module    string
	variable  string
}

func detectPython(projectPath string) (Result, error) {
	root, err := validateProjectRoot(projectPath)
	if err != nil {
		return Result{}, err
	}

	manifests, err := pythonManifests(root)
	if err != nil {
		return Result{}, err
	}
	if len(manifests) == 0 {
		return Result{}, fmt.Errorf(
			"%w: no %s or %s found in %q; point Hostix at the Python project root or add a supported dependency manifest",
			ErrNotPython, pyprojectManifest, requirementsManifest, root,
		)
	}

	result := Result{
		Stack:       StackPython,
		ProjectRoot: root,
		Manifests:   manifests,
	}

	dependencies, err := readDependencies(root, manifests)
	if err != nil {
		return result, err
	}
	entrypoints, err := inspectEntrypoints(root)
	if err != nil {
		return result, err
	}

	frameworks := detectedFrameworks(dependencies, entrypoints)
	if len(frameworks) > 1 {
		return result, fmt.Errorf(
			"%w: multiple supported frameworks detected (%s); remove stale framework declarations or configure the stack and start command explicitly",
			ErrAmbiguous, strings.Join(frameworkNames(frameworks), ", "),
		)
	}
	if len(frameworks) == 0 {
		return result, nil
	}

	result.Framework = frameworks[0]
	candidates := entrypointsFor(entrypoints, result.Framework)
	if len(candidates) > 1 {
		files := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			files = append(files, candidate.file)
		}
		sort.Strings(files)
		return result, fmt.Errorf(
			"%w: multiple %s entrypoints detected (%s); keep one conventional entrypoint or configure the start command explicitly",
			ErrAmbiguous, result.Framework, strings.Join(files, ", "),
		)
	}
	if len(candidates) == 0 {
		return result, nil
	}

	entry := candidates[0]
	switch result.Framework {
	case FrameworkDjango:
		result.DefaultCommand = []string{"python", "manage.py", "runserver", "0.0.0.0:8000"}
	case FrameworkFlask:
		result.DefaultCommand = []string{
			"python", "-m", "flask", "--app", entry.module + ":" + entry.variable,
			"run", "--host", "0.0.0.0", "--port", "8000",
		}
	case FrameworkFastAPI:
		// FastAPI does not install an ASGI server with every dependency form.
		// Only emit an executable command when uvicorn is explicitly installed.
		if _, ok := dependencies["uvicorn"]; ok {
			result.DefaultCommand = []string{
				"python", "-m", "uvicorn", entry.module + ":" + entry.variable,
				"--host", "0.0.0.0", "--port", "8000",
			}
		}
	}

	return result, nil
}

func validateProjectRoot(projectPath string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return "", fmt.Errorf("%w: project path is empty", ErrInvalidProject)
	}

	root, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %v", ErrInvalidProject, projectPath, err)
	}
	root = filepath.Clean(root)

	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("%w: inspect %q: %v", ErrInvalidProject, root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %q is not a directory", ErrInvalidProject, root)
	}
	return root, nil
}

func pythonManifests(root string) ([]string, error) {
	manifests := make([]string, 0, 2)
	for _, name := range []string{pyprojectManifest, requirementsManifest} {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		switch {
		case err == nil && !info.Mode().IsRegular():
			return nil, fmt.Errorf("%w: Python marker %q is not a regular file", ErrInvalidProject, path)
		case err == nil:
			manifests = append(manifests, name)
		case os.IsNotExist(err):
			continue
		default:
			return nil, fmt.Errorf("%w: inspect Python marker %q: %v", ErrInvalidProject, path, err)
		}
	}
	return manifests, nil
}

func readDependencies(root string, manifests []string) (map[string]struct{}, error) {
	dependencies := make(map[string]struct{})
	for _, manifest := range manifests {
		path := filepath.Join(root, manifest)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %q: %v", ErrInvalidProject, path, err)
		}

		switch manifest {
		case requirementsManifest:
			for dependency := range requirementsDependencies(string(data)) {
				dependencies[dependency] = struct{}{}
			}
		case pyprojectManifest:
			for dependency := range pyprojectDependencies(string(data)) {
				dependencies[dependency] = struct{}{}
			}
		}
	}
	return dependencies, nil
}

func requirementsDependencies(contents string) map[string]struct{} {
	dependencies := make(map[string]struct{})
	for _, rawLine := range strings.Split(contents, "\n") {
		line := strings.TrimSpace(stripRequirementComment(rawLine))
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		if name := normalizedPackageName(line); name != "" {
			dependencies[name] = struct{}{}
		}
	}
	return dependencies
}

func stripRequirementComment(line string) string {
	for index, char := range line {
		if char == '#' && (index == 0 || line[index-1] == ' ' || line[index-1] == '\t') {
			return line[:index]
		}
	}
	return line
}

func normalizedPackageName(specification string) string {
	match := packageNamePattern.FindString(strings.TrimSpace(specification))
	if match == "" {
		return ""
	}
	return packageSeparator.ReplaceAllString(strings.ToLower(match), "-")
}

func pyprojectDependencies(contents string) map[string]struct{} {
	dependencies := make(map[string]struct{})
	section := ""
	projectDependencyDepth := 0

	for _, rawLine := range strings.Split(contents, "\n") {
		line := strings.TrimSpace(stripTOMLComment(rawLine))
		if line == "" {
			continue
		}

		if projectDependencyDepth > 0 {
			addDependencySpecifications(dependencies, tomlStrings(line))
			projectDependencyDepth += tomlArrayDepth(line)
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			continue
		}

		key, value, ok := splitTOMLAssignment(line)
		if !ok {
			continue
		}
		switch section {
		case "project":
			if normalizeTOMLKey(key) != "dependencies" {
				continue
			}
			addDependencySpecifications(dependencies, tomlStrings(value))
			if depth := tomlArrayDepth(value); depth > 0 {
				projectDependencyDepth = depth
			}
		case "tool.poetry.dependencies":
			if name := normalizedPackageName(normalizeTOMLKey(key)); name != "" && name != "python" {
				dependencies[name] = struct{}{}
			}
		}
	}
	return dependencies
}

func addDependencySpecifications(dependencies map[string]struct{}, specifications []string) {
	for _, specification := range specifications {
		if name := normalizedPackageName(specification); name != "" {
			dependencies[name] = struct{}{}
		}
	}
}

func normalizeTOMLKey(key string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(key), `"'`))
}

func stripTOMLComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for index, char := range line {
		switch {
		case escaped:
			escaped = false
		case char == '\\' && inDouble:
			escaped = true
		case char == '\'' && !inDouble:
			inSingle = !inSingle
		case char == '"' && !inSingle:
			inDouble = !inDouble
		case char == '#' && !inSingle && !inDouble:
			return line[:index]
		}
	}
	return line
}

func splitTOMLAssignment(line string) (string, string, bool) {
	inSingle := false
	inDouble := false
	escaped := false
	for index, char := range line {
		switch {
		case escaped:
			escaped = false
		case char == '\\' && inDouble:
			escaped = true
		case char == '\'' && !inDouble:
			inSingle = !inSingle
		case char == '"' && !inSingle:
			inDouble = !inDouble
		case char == '=' && !inSingle && !inDouble:
			return strings.TrimSpace(line[:index]), strings.TrimSpace(line[index+1:]), true
		}
	}
	return "", "", false
}

func tomlStrings(line string) []string {
	var values []string
	var value strings.Builder
	quote := rune(0)
	escaped := false
	for _, char := range line {
		if quote == 0 {
			if char == '\'' || char == '"' {
				quote = char
				value.Reset()
			}
			continue
		}

		if escaped {
			value.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if char == quote {
			values = append(values, value.String())
			quote = 0
			continue
		}
		value.WriteRune(char)
	}
	return values
}

func tomlArrayDepth(line string) int {
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	for _, char := range line {
		switch {
		case escaped:
			escaped = false
		case char == '\\' && inDouble:
			escaped = true
		case char == '\'' && !inDouble:
			inSingle = !inSingle
		case char == '"' && !inSingle:
			inDouble = !inDouble
		case char == '[' && !inSingle && !inDouble:
			depth++
		case char == ']' && !inSingle && !inDouble:
			depth--
		}
	}
	return depth
}

func inspectEntrypoints(root string) ([]entrypoint, error) {
	var entrypoints []entrypoint

	managePath := filepath.Join(root, "manage.py")
	if info, err := os.Stat(managePath); err == nil && info.Mode().IsRegular() {
		data, readErr := os.ReadFile(managePath)
		if readErr != nil {
			return nil, fmt.Errorf("%w: read %q: %v", ErrInvalidProject, managePath, readErr)
		}
		contents := string(data)
		if strings.Contains(contents, "DJANGO_SETTINGS_MODULE") || strings.Contains(contents, "execute_from_command_line") {
			entrypoints = append(entrypoints, entrypoint{framework: FrameworkDjango, file: "manage.py"})
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: inspect %q: %v", ErrInvalidProject, managePath, err)
	}

	for _, relativePath := range []string{
		"main.py",
		"app.py",
		filepath.Join("app", "main.py"),
		filepath.Join("app", "app.py"),
		filepath.Join("src", "main.py"),
		filepath.Join("src", "app.py"),
	} {
		path := filepath.Join(root, relativePath)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: inspect %q: %v", ErrInvalidProject, path, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %q: %v", ErrInvalidProject, path, err)
		}
		contents := string(data)
		module := pythonModule(relativePath)
		if fastAPIImport.MatchString(contents) {
			for _, match := range fastAPIAssignment.FindAllStringSubmatch(contents, -1) {
				entrypoints = append(entrypoints, entrypoint{
					framework: FrameworkFastAPI,
					file:      filepath.ToSlash(relativePath),
					module:    module,
					variable:  match[1],
				})
			}
		}
		if flaskImport.MatchString(contents) {
			for _, match := range flaskAssignment.FindAllStringSubmatch(contents, -1) {
				entrypoints = append(entrypoints, entrypoint{
					framework: FrameworkFlask,
					file:      filepath.ToSlash(relativePath),
					module:    module,
					variable:  match[1],
				})
			}
		}
	}
	return deduplicateEntrypoints(entrypoints), nil
}

func pythonModule(relativePath string) string {
	withoutExtension := strings.TrimSuffix(filepath.ToSlash(relativePath), filepath.Ext(relativePath))
	return strings.ReplaceAll(withoutExtension, "/", ".")
}

func deduplicateEntrypoints(entrypoints []entrypoint) []entrypoint {
	seen := make(map[string]struct{}, len(entrypoints))
	result := make([]entrypoint, 0, len(entrypoints))
	for _, entry := range entrypoints {
		key := string(entry.framework) + "\x00" + entry.file + "\x00" + entry.variable
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	return result
}

func detectedFrameworks(dependencies map[string]struct{}, entrypoints []entrypoint) []Framework {
	found := make(map[Framework]struct{})
	for dependency := range dependencies {
		switch dependency {
		case string(FrameworkDjango):
			found[FrameworkDjango] = struct{}{}
		case string(FrameworkFastAPI):
			found[FrameworkFastAPI] = struct{}{}
		case string(FrameworkFlask):
			found[FrameworkFlask] = struct{}{}
		}
	}
	for _, entry := range entrypoints {
		found[entry.framework] = struct{}{}
	}

	frameworks := make([]Framework, 0, len(found))
	for framework := range found {
		frameworks = append(frameworks, framework)
	}
	sort.Slice(frameworks, func(i, j int) bool { return frameworks[i] < frameworks[j] })
	return frameworks
}

func frameworkNames(frameworks []Framework) []string {
	names := make([]string, len(frameworks))
	for index, framework := range frameworks {
		names[index] = string(framework)
	}
	return names
}

func entrypointsFor(entrypoints []entrypoint, framework Framework) []entrypoint {
	result := make([]entrypoint, 0, len(entrypoints))
	for _, entry := range entrypoints {
		if entry.framework == framework {
			result = append(result, entry)
		}
	}
	return result
}
