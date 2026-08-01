package atomic

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(target) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestWriteFileExec(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "script.sh")
	if err := WriteFileExec(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFileExec: %v", err)
	}
	got, err := os.ReadFile(target) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\n" {
		t.Errorf("got %q", got)
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := WriteFile(target, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, []byte("bb"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target) //nolint:gosec
	if string(got) != "bb" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceDir_Simple(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := ReplaceDir(src, dst); err != nil {
		t.Fatalf("ReplaceDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "sub", "f.txt")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceDir_Overwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "old.txt"), []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDir(src, dst); err != nil {
		t.Fatalf("ReplaceDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "new.txt")); err != nil {
		t.Errorf("new.txt missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old.txt should be gone: %v", err)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst, 0o644); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	got, _ := os.ReadFile(dst) //nolint:gosec
	if string(got) != "z" {
		t.Errorf("got %q", got)
	}
	// Reference strings to keep imports used.
	_ = strings.HasSuffix
}
