// Package image creates build inputs for Hostix runtime adapters.
package image

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrBuildContextClosed is returned when a closed build context is read.
var ErrBuildContextClosed = errors.New("build context is closed")

// BuildContext is a deterministic TAR archive suitable for passing directly
// to Docker's ImageBuild method. Callers must close it after the build; Close
// closes and removes the temporary archive and is safe to call more than once.
type BuildContext struct {
	mu          sync.Mutex
	archive     *os.File
	archivePath string
	removed     bool
	dockerfile  string
}

// DockerfileName returns the path to select in Docker image-build options.
func (c *BuildContext) DockerfileName() string {
	return c.dockerfile
}

// Read implements io.Reader so the context can be passed directly to Docker's
// image-build API.
func (c *BuildContext) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.archive == nil {
		return 0, ErrBuildContextClosed
	}
	return c.archive.Read(p)
}

// Close releases the archive and removes its temporary backing file.
func (c *BuildContext) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var closeErr error
	if c.archive != nil {
		closeErr = c.archive.Close()
		c.archive = nil
	}

	var removeErr error
	if !c.removed {
		removeErr = os.Remove(c.archivePath)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			c.removed = true
			removeErr = nil
		}
	}

	return errors.Join(closeErr, removeErr)
}

type contextEntry struct {
	absPath  string
	tarPath  string
	info     os.FileInfo
	linkName string
}

var normalizedTarTime = time.Unix(0, 0).UTC()

func newBuildContext(projectDir, tempDir, dockerfileName string, dockerfile []byte, protectedPaths ...string) (_ *BuildContext, err error) {
	entries, err := collectContextEntries(projectDir, dockerfileName, protectedPaths...)
	if err != nil {
		return nil, err
	}

	archive, err := os.CreateTemp(tempDir, "hostix-build-context-*.tar")
	if err != nil {
		return nil, fmt.Errorf("create temporary build context: %w", err)
	}
	archivePath := archive.Name()
	defer func() {
		if err != nil {
			_ = archive.Close()
			_ = os.Remove(archivePath)
		}
	}()

	tw := tar.NewWriter(archive)
	if err = writeGeneratedDockerfile(tw, dockerfileName, dockerfile); err != nil {
		_ = tw.Close()
		return nil, err
	}
	for _, entry := range entries {
		if err = writeContextEntry(tw, entry); err != nil {
			_ = tw.Close()
			return nil, err
		}
	}
	if err = tw.Close(); err != nil {
		return nil, fmt.Errorf("finalize build-context archive: %w", err)
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind build-context archive: %w", err)
	}

	return &BuildContext{
		archive:     archive,
		archivePath: archivePath,
		dockerfile:  dockerfileName,
	}, nil
}

func collectContextEntries(projectDir, dockerfileName string, protectedPaths ...string) ([]contextEntry, error) {
	matcher, err := loadDockerignore(projectDir)
	if err != nil {
		return nil, err
	}
	protected := make(map[string]struct{}, len(protectedPaths)+2)
	protected[".dockerignore"] = struct{}{}
	protected["Dockerfile"] = struct{}{}
	for _, protectedPath := range protectedPaths {
		protected[filepath.ToSlash(protectedPath)] = struct{}{}
	}

	var entries []contextEntry
	err = filepath.Walk(projectDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect project entry %q: %w", path, walkErr)
		}
		if path == projectDir {
			return nil
		}

		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			return fmt.Errorf("resolve project entry %q: %w", path, err)
		}
		tarPath := filepath.ToSlash(rel)
		if tarPath == dockerfileName {
			return fmt.Errorf("project entry %q conflicts with Hostix generated Dockerfile path %q", path, dockerfileName)
		}
		if tarPath == "." || strings.HasPrefix(tarPath, "../") || filepath.IsAbs(rel) {
			return fmt.Errorf("project entry %q resolves outside the build context", path)
		}
		if excludeDefaultContextEntry(tarPath, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		_, isProtected := protected[tarPath]
		if !isProtected && matcher.excludes(tarPath) {
			if info.IsDir() && !matcher.hasNegations {
				return filepath.SkipDir
			}
			return nil
		}

		entry := contextEntry{absPath: path, tarPath: tarPath, info: info}
		switch {
		case info.Mode().IsRegular(), info.IsDir():
		case info.Mode()&os.ModeSymlink != 0:
			entry.linkName, err = os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read project symlink %q: %w", path, err)
			}
		default:
			return fmt.Errorf("unsupported project entry %q with mode %s", path, info.Mode())
		}

		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].tarPath < entries[j].tarPath
	})
	return entries, nil
}

func excludeDefaultContextEntry(tarPath string, info os.FileInfo) bool {
	// These safety and generated-artifact exclusions are applied before the
	// project matcher and intentionally cannot be restored with ! patterns.
	// .env.example is the explicit shareable exception to the .env rule.
	base := path.Base(tarPath)
	if base != ".env.example" && (base == ".env" || strings.HasPrefix(base, ".env.")) {
		return true
	}
	if base == ".git" || base == ".hg" || base == ".svn" {
		return true
	}
	if info.IsDir() {
		switch base {
		case "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".nox", ".eggs", "node_modules":
			return true
		}
		if strings.HasSuffix(base, ".egg-info") {
			return true
		}
		if !strings.Contains(tarPath, "/") {
			switch base {
			case ".venv", "venv", "env", "build", "dist":
				return true
			}
		}
	}
	return base == ".DS_Store" || strings.HasSuffix(base, ".pyc") || strings.HasSuffix(base, ".pyo")
}

func writeGeneratedDockerfile(tw *tar.Writer, name string, content []byte) error {
	header := &tar.Header{
		Name:       name,
		Mode:       0o644,
		Size:       int64(len(content)),
		ModTime:    normalizedTarTime,
		AccessTime: normalizedTarTime,
		ChangeTime: normalizedTarTime,
		Typeflag:   tar.TypeReg,
		Format:     tar.FormatPAX,
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write generated Dockerfile header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("write generated Dockerfile: %w", err)
	}
	return nil
}

func writeContextEntry(tw *tar.Writer, entry contextEntry) error {
	header := &tar.Header{
		Name:       entry.tarPath,
		Mode:       normalizedMode(entry.info),
		ModTime:    normalizedTarTime,
		AccessTime: normalizedTarTime,
		ChangeTime: normalizedTarTime,
		Format:     tar.FormatPAX,
	}

	switch {
	case entry.info.IsDir():
		header.Typeflag = tar.TypeDir
		header.Name += "/"
	case entry.info.Mode()&os.ModeSymlink != 0:
		header.Typeflag = tar.TypeSymlink
		header.Linkname = entry.linkName
	case entry.info.Mode().IsRegular():
		header.Typeflag = tar.TypeReg
		header.Size = entry.info.Size()
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write build-context header for %q: %w", entry.tarPath, err)
	}
	if !entry.info.Mode().IsRegular() {
		return nil
	}

	file, err := os.Open(entry.absPath)
	if err != nil {
		return fmt.Errorf("open project file %q: %w", entry.absPath, err)
	}
	_, copyErr := io.Copy(tw, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("archive project file %q: %w", entry.absPath, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close project file %q: %w", entry.absPath, closeErr)
	}
	return nil
}

func normalizedMode(info os.FileInfo) int64 {
	if info.IsDir() {
		return 0o755
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0o777
	}
	if info.Mode().Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}
