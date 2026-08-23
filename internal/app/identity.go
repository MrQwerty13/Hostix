// Package app coordinates Hostix use cases independently of the CLI layer.
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// ProjectIdentity contains stable Docker identifiers derived from a project
// path. Repeated runs of the same project therefore target the same container.
type ProjectIdentity struct {
	Name     string
	ImageRef string
}

// IdentityForProject returns deterministic, Docker-safe identifiers for a
// project directory.
func IdentityForProject(projectDir string) (ProjectIdentity, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return ProjectIdentity{}, fmt.Errorf("resolve project path: %w", err)
	}
	canonical := filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}

	slug := dockerSlug(filepath.Base(canonical))
	if slug == "" {
		slug = "project"
	}
	digest := sha256.Sum256([]byte(canonical))
	shortHash := hex.EncodeToString(digest[:])[:10]

	return ProjectIdentity{
		Name:     fmt.Sprintf("hostix-%s-%s", slug, shortHash),
		ImageRef: fmt.Sprintf("hostix/%s:%s", slug, shortHash),
	}, nil
}

func dockerSlug(value string) string {
	var b strings.Builder
	previousSeparator := false
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			previousSeparator = false
			continue
		}
		if !previousSeparator && b.Len() > 0 {
			b.WriteByte('-')
			previousSeparator = true
		}
	}
	return strings.Trim(b.String(), "-")
}
