package sync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/omp-sync/internal/snapshot"
)

func TestWalk_BasicFiles(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "agents"))
	mustWrite(t, filepath.Join(dir, "agents", "a.md"), "alpha")
	mustWrite(t, filepath.Join(dir, "agents", "b.md"), "beta")
	mustWrite(t, filepath.Join(dir, "README.md"), "root readme")

	s, err := Walk(dir, nil, nil)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(s.Files) != 3 {
		t.Errorf("files: got %d, want 3", len(s.Files))
	}
	for _, f := range s.Files {
		if f.SHA256 == "" {
			t.Errorf("missing hash: %s", f.Path)
		}
	}
}

func TestWalk_RespectsIncludeExclude(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "agents", "a.md"), "x")
	mustWrite(t, filepath.Join(dir, "secret.env"), "y")
	mustWrite(t, filepath.Join(dir, "b.md"), "z")

	s, err := Walk(dir, []string{"**"}, []string{"secret.env"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, f := range s.Files {
		if filepath.Base(f.Path) == "secret.env" {
			t.Error("secret.env should have been excluded")
		}
	}
	for _, p := range s.Ignored {
		if filepath.Base(p) != "secret.env" {
			t.Errorf("unexpected ignored: %s", p)
		}
	}
}

func TestWalk_RejectsSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "outside.txt"), "secret")

	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "agents"))
	if err := os.Symlink(outside, filepath.Join(dir, "agents", "escape")); err != nil {
		t.Skip("symlink not supported on this platform")
	}
	mustWrite(t, filepath.Join(dir, "agents", "ok.md"), "ok")

	_, err := Walk(dir, nil, nil)
	if err == nil {
		t.Fatal("expected error for symlink escaping root")
	}
	if !strings.Contains(err.Error(), "escapes root") {
		t.Errorf("expected 'escapes root' in error, got: %v", err)
	}
}

func TestWalk_AllowsSymlinkInside(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "agents"))
	mustWrite(t, filepath.Join(dir, "agents", "real.txt"), "real")

	target := filepath.Join(dir, "agents", "real.txt")
	if err := os.Symlink(target, filepath.Join(dir, "agents", "link.txt")); err != nil {
		t.Skip("symlink not supported on this platform")
	}

	// Walk should not error on in-root symlinks.
	if _, err := Walk(dir, nil, nil); err != nil {
		t.Errorf("Walk rejected in-root symlink: %v", err)
	}
}

func TestWalk_Empty(t *testing.T) {
	dir := t.TempDir()
	s, err := Walk(dir, nil, nil)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(s.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(s.Files))
	}
}
func TestLoadState_NotFound(t *testing.T) {
	st, err := LoadState("/nonexistent/path/state.json")
	if err != nil {
		t.Fatalf("LoadState on missing file: %v", err)
	}
	if st.SchemaVersion != 1 {
		t.Errorf("schema: got %d", st.SchemaVersion)
	}
	if len(st.Backends) != 0 {
		t.Errorf("backends: got %d, want 0", len(st.Backends))
	}
}

func TestState_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st.Set("local", BackendState{
		LastSnapshotID: "abc123",
		LastSyncedAt:   now,
		LastOp:         "push",
	})
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	st2, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	bs := st2.Get("local")
	if bs.LastSnapshotID != "abc123" {
		t.Errorf("snapshot id: got %q", bs.LastSnapshotID)
	}
	if bs.LastOp != "push" {
		t.Errorf("op: got %q", bs.LastOp)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func timeDate() (t timeAlias) {
	return timeNowFn()
}

type timeAlias = struct {
	_ int64
}

// timeNowFn is a small indirection so tests can call into the standard
// time package via a single import path.
var (
	timeNowFn   = func() timeAlias { return defaultTimeAlias() }
	defaultTimeAlias = func() timeAlias { return timeAlias{} }
	_ = errors.New
	_ = snapshot.ManifestName
)
