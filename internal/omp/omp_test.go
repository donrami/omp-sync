package omp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMP_SYNC_OMP_DIR", dir)
	got, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Resolve symlinks for comparison on macOS where t.TempDir() may live under /private/var.
	gotEval, _ := filepath.EvalSymlinks(got)
	dirEval, _ := filepath.EvalSymlinks(dir)
	if gotEval != dirEval {
		t.Errorf("got %q, want %q", gotEval, dirEval)
	}
}

func TestDiscover_NotFound(t *testing.T) {
	t.Setenv("OMP_SYNC_OMP_DIR", "/nonexistent/path/__omp_test__")
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent/__x__")
	t.Setenv("HOME", "/nonexistent/__home__")
	if _, err := Discover(); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestValidateLayout_Happy(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLayout(dir); err != nil {
		t.Errorf("ValidateLayout: %v", err)
	}
}

func TestValidateLayout_Sad(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateLayout(dir); err == nil {
		t.Error("expected error for non-omp layout")
	}
}
