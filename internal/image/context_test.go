package image

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var _ io.ReadCloser = (*BuildContext)(nil)

type archivedEntry struct {
	header tar.Header
	body   []byte
}

func TestPythonBuildContextContainsGeneratedAndProjectFiles(t *testing.T) {
	projectDir := t.TempDir()
	tempDir := t.TempDir()
	writeTestFile(t, projectDir, "requirements.txt", "fastapi==0.116.1\n", 0o644)
	writeTestFile(t, projectDir, "main.py", "print('ready')\n", 0o644)
	writeTestFile(t, projectDir, "Dockerfile", "FROM user-owned\n", 0o644)
	writeTestFile(t, projectDir, "scripts/start.sh", "#!/bin/sh\nexec python main.py\n", 0o755)
	writeTestFile(t, projectDir, "app/build/source.py", "SOURCE = True\n", 0o644)

	writeTestFile(t, projectDir, ".git/config", "secret repository metadata\n", 0o644)
	writeTestFile(t, projectDir, ".venv/bin/python", "local environment\n", 0o755)
	writeTestFile(t, projectDir, "__pycache__/main.cpython-312.pyc", "bytecode\n", 0o644)
	writeTestFile(t, projectDir, "dist/app.whl", "artifact\n", 0o644)
	writeTestFile(t, projectDir, ".pytest_cache/state", "cache\n", 0o644)

	context, err := NewPythonBuildContext(PythonBuildOptions{
		ProjectDir: projectDir,
		Command:    []string{"python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0"},
		TempDir:    tempDir,
	})
	if err != nil {
		t.Fatalf("NewPythonBuildContext() error = %v", err)
	}
	archivePath := context.archivePath

	if got, want := context.DockerfileName(), GeneratedDockerfileName; got != want {
		t.Fatalf("DockerfileName() = %q, want %q", got, want)
	}
	entries := readArchivedEntries(t, context)

	wantNames := []string{
		".hostix.Dockerfile",
		"Dockerfile",
		"app/",
		"app/build/",
		"app/build/source.py",
		"main.py",
		"requirements.txt",
		"scripts/",
		"scripts/start.sh",
	}
	gotNames := make([]string, 0, len(entries))
	for name := range entries {
		gotNames = append(gotNames, name)
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("archive entries = %q, want %q", gotNames, wantNames)
	}

	if got, want := string(entries["Dockerfile"].body), "FROM user-owned\n"; got != want {
		t.Fatalf("user Dockerfile in archive = %q, want %q", got, want)
	}
	generated := string(entries[GeneratedDockerfileName].body)
	for _, expected := range []string{
		"FROM python:3.12-slim",
		"RUN python -m pip install -r /tmp/requirements.txt",
		`CMD ["python","-m","uvicorn","main:app","--host","0.0.0.0"]`,
	} {
		if !strings.Contains(generated, expected) {
			t.Errorf("generated Dockerfile does not contain %q:\n%s", expected, generated)
		}
	}
	if got, want := entries["scripts/start.sh"].header.Mode, int64(0o755); got != want {
		t.Errorf("executable mode = %#o, want %#o", got, want)
	}
	if got, want := entries["main.py"].header.Mode, int64(0o644); got != want {
		t.Errorf("regular-file mode = %#o, want %#o", got, want)
	}

	if _, err := os.Stat(filepath.Join(projectDir, GeneratedDockerfileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated Dockerfile was written into user project: %v", err)
	}
	userDockerfile, err := os.ReadFile(filepath.Join(projectDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read user Dockerfile: %v", err)
	}
	if got, want := string(userDockerfile), "FROM user-owned\n"; got != want {
		t.Fatalf("user Dockerfile after generation = %q, want %q", got, want)
	}

	if err := context.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary archive remains after Close(): %v", err)
	}
	if _, err := context.Read(make([]byte, 1)); !errors.Is(err, ErrBuildContextClosed) {
		t.Fatalf("Read() after Close() error = %v, want %v", err, ErrBuildContextClosed)
	}
	if err := context.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestPythonBuildContextIsByteDeterministic(t *testing.T) {
	projectDir := t.TempDir()
	tempDir := t.TempDir()
	writeTestFile(t, projectDir, "requirements.txt", "flask==3.1.2\n", 0o600)
	writeTestFile(t, projectDir, "src/app.py", "app = object()\n", 0o664)

	options := PythonBuildOptions{
		ProjectDir: projectDir,
		Command:    []string{"python", "src/app.py"},
		TempDir:    tempDir,
	}
	first, err := NewPythonBuildContext(options)
	if err != nil {
		t.Fatalf("first NewPythonBuildContext() error = %v", err)
	}
	firstBytes, err := io.ReadAll(first)
	if err != nil {
		t.Fatalf("read first context: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first context: %v", err)
	}

	if err := os.Chtimes(filepath.Join(projectDir, "src/app.py"), normalizedTarTime.AddDate(20, 0, 0), normalizedTarTime.AddDate(20, 0, 0)); err != nil {
		t.Fatalf("change source timestamps: %v", err)
	}
	second, err := NewPythonBuildContext(options)
	if err != nil {
		t.Fatalf("second NewPythonBuildContext() error = %v", err)
	}
	secondBytes, err := io.ReadAll(second)
	if err != nil {
		t.Fatalf("read second context: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second context: %v", err)
	}

	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("equivalent project snapshots produced different TAR bytes")
	}
}

func TestPythonBuildContextRejectsReservedDockerfileCollisionWithoutTempLeak(t *testing.T) {
	projectDir := t.TempDir()
	tempDir := t.TempDir()
	writeTestFile(t, projectDir, "requirements.txt", "fastapi\n", 0o644)
	writeTestFile(t, projectDir, GeneratedDockerfileName, "user content\n", 0o644)

	_, err := NewPythonBuildContext(PythonBuildOptions{
		ProjectDir: projectDir,
		Command:    []string{"python", "main.py"},
		TempDir:    tempDir,
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("NewPythonBuildContext() error = %v, want reserved-path conflict", err)
	}
	files, readErr := os.ReadDir(tempDir)
	if readErr != nil {
		t.Fatalf("read temporary directory: %v", readErr)
	}
	if len(files) != 0 {
		t.Fatalf("temporary files leaked after failure: %v", files)
	}
}

func TestPythonBuildContextHonorsDockerignoreAndProtectsBuildInputs(t *testing.T) {
	projectDir := t.TempDir()
	tempDir := t.TempDir()
	writeTestFile(t, projectDir, ".dockerignore", `# Ordered Docker-like patterns
secret*.txt
private/**
!private/public.txt
*.log
!important.log
requirements.txt
Dockerfile
`, 0o644)
	writeTestFile(t, projectDir, "requirements.txt", "fastapi\n", 0o644)
	writeTestFile(t, projectDir, "Dockerfile", "FROM user-owned\n", 0o644)
	writeTestFile(t, projectDir, "main.py", "print('ready')\n", 0o644)
	writeTestFile(t, projectDir, "secret-token.txt", "do not send\n", 0o644)
	writeTestFile(t, projectDir, "private/hidden.txt", "do not send\n", 0o644)
	writeTestFile(t, projectDir, "private/public.txt", "safe to send\n", 0o644)
	writeTestFile(t, projectDir, "debug.log", "do not send\n", 0o644)
	writeTestFile(t, projectDir, "important.log", "safe to send\n", 0o644)
	writeTestFile(t, projectDir, ".env", "TOKEN=secret\n", 0o600)
	writeTestFile(t, projectDir, ".env.production", "TOKEN=secret\n", 0o600)
	writeTestFile(t, projectDir, "nested/.env.local", "TOKEN=secret\n", 0o600)
	writeTestFile(t, projectDir, ".env.example", "TOKEN=replace-me\n", 0o644)

	context, err := NewPythonBuildContext(PythonBuildOptions{
		ProjectDir: projectDir,
		Command:    []string{"python", "main.py"},
		TempDir:    tempDir,
	})
	if err != nil {
		t.Fatalf("NewPythonBuildContext() error = %v", err)
	}
	defer func() {
		if err := context.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	entries := readArchivedEntries(t, context)
	for _, included := range []string{
		GeneratedDockerfileName,
		".dockerignore",
		"requirements.txt",
		"Dockerfile",
		"main.py",
		"private/public.txt",
		"important.log",
		".env.example",
	} {
		if _, ok := entries[included]; !ok {
			t.Errorf("expected %q to be included", included)
		}
	}
	for _, excluded := range []string{
		"secret-token.txt",
		"private/hidden.txt",
		"debug.log",
		".env",
		".env.production",
		"nested/.env.local",
	} {
		if _, ok := entries[excluded]; ok {
			t.Errorf("expected %q to be excluded", excluded)
		}
	}
}

func TestInvalidDockerignoreDoesNotLeakTemporaryArchive(t *testing.T) {
	projectDir := t.TempDir()
	tempDir := t.TempDir()
	writeTestFile(t, projectDir, "requirements.txt", "fastapi\n", 0o644)
	writeTestFile(t, projectDir, ".dockerignore", "[invalid\n", 0o644)

	_, err := NewPythonBuildContext(PythonBuildOptions{
		ProjectDir: projectDir,
		Command:    []string{"python", "main.py"},
		TempDir:    tempDir,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid pattern") {
		t.Fatalf("NewPythonBuildContext() error = %v, want invalid-pattern error", err)
	}
	files, readErr := os.ReadDir(tempDir)
	if readErr != nil {
		t.Fatalf("read temporary directory: %v", readErr)
	}
	if len(files) != 0 {
		t.Fatalf("temporary files leaked after failure: %v", files)
	}
}

func readArchivedEntries(t *testing.T, reader io.Reader) map[string]archivedEntry {
	t.Helper()
	entries := make(map[string]archivedEntry)
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read TAR header: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read TAR entry %q: %v", header.Name, err)
		}
		copyHeader := *header
		entries[header.Name] = archivedEntry{header: copyHeader, body: body}
	}
	return entries
}

func writeTestFile(t *testing.T, root, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %q: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}
