package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityForProjectIsStableAndSafe(t *testing.T) {
	root := filepath.Join(t.TempDir(), "My Python API!")

	first, err := IdentityForProject(root)
	if err != nil {
		t.Fatalf("IdentityForProject() error = %v", err)
	}
	second, err := IdentityForProject(root)
	if err != nil {
		t.Fatalf("IdentityForProject() second error = %v", err)
	}

	if first != second {
		t.Fatalf("identities differ: %#v and %#v", first, second)
	}
	if !strings.HasPrefix(first.Name, "hostix-my-python-api-") {
		t.Fatalf("container name = %q", first.Name)
	}
	if !strings.HasPrefix(first.ImageRef, "hostix/my-python-api:") {
		t.Fatalf("image ref = %q", first.ImageRef)
	}
}

func TestIdentityForProjectDistinguishesEqualBasenames(t *testing.T) {
	base := t.TempDir()
	first, err := IdentityForProject(filepath.Join(base, "one", "api"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := IdentityForProject(filepath.Join(base, "two", "api"))
	if err != nil {
		t.Fatal(err)
	}

	if first.Name == second.Name || first.ImageRef == second.ImageRef {
		t.Fatalf("different paths produced the same identity: %#v", first)
	}
}

func TestIdentityForProjectUsesFallbackForNonASCIIName(t *testing.T) {
	identity, err := IdentityForProject(filepath.Join(t.TempDir(), "проект"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(identity.Name, "hostix-project-") {
		t.Fatalf("container name = %q", identity.Name)
	}
}
