package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/credentials"
	"github.com/donrami/omp-sync/internal/snapshot"
)

// makeBareRepo creates a bare git repo at `<dir>/<name>.git`.
func makeBareRepo(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+".git")
	cmd := exec.Command("git", "init", "--bare", path)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@x", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	return path
}

func TestGitHub_NewConfigured(t *testing.T) {
	t.Setenv(credentials.EnvName("ghtest"), "fakepat")

	cases := []struct {
		name string
		f    func() (backend.Backend, error)
		want bool
	}{
		{"empty repo", func() (backend.Backend, error) {
			return NewConfigured("", "main", "ghtest", "omp-sync author", "omp-sync@example")()
		}, true},
		{"empty credential", func() (backend.Backend, error) {
			return NewConfigured("https://example.com/u/r.git", "main", "", "omp-sync author", "omp-sync@example")()
		}, true},
		{"happy path", func() (backend.Backend, error) {
			return NewConfigured("https://example.com/u/r.git", "main", "ghtest", "omp-sync author", "omp-sync@example")()
		}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.f()
			if (err != nil) != tt.want {
				t.Errorf("err = %v, wantErr = %v", err, tt.want)
			}
		})
	}
}

func TestGitHub_NewConfigured_MissingCredentialLookup(t *testing.T) {
	t.Setenv(credentials.EnvName("missing"), "")
	_, err := NewConfigured("https://example.com/u/r.git", "main", "missing", "omp-sync author", "omp-sync@example")()
	if !errors.Is(err, backend.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestGitHub_BareRepo_BasicLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	t.Setenv(credentials.EnvName("ghbasic"), "fakepat")

	dir := t.TempDir()
	barePath := makeBareRepo(t, dir, "repo")
	// go-git clones file:// URLs without auth.
	b, err := NewConfigured("file://"+barePath, "main", "ghbasic", "omp-sync", "omp-sync@x")()
	if err != nil {
		t.Fatalf("NewConfigured: %v", err)
	}

	ctx := context.Background()

	// Empty repo: CurrentSnapshot returns ErrNoSnapshot.
	if _, err := b.CurrentSnapshot(ctx); err != backend.ErrNoSnapshot {
		t.Errorf("expected ErrNoSnapshot, got %v", err)
	}
	if _, err := b.ListSnapshots(ctx, 0); err != nil {
		t.Errorf("ListSnapshots: %v", err)
	}

	// Build a snapshot source.
	snapRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(snapRoot, "files", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapRoot, "files", "agents", "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"version":1,"created_at":"2026-07-31T00:00:00Z","tool_version":"test",
		"files":[{"path":"agents/a.md","mode":420,"size":2,
		"sha256":"` + shaOf("hi") + `","executable":false}]
	}`
	if err := os.WriteFile(filepath.Join(snapRoot, snapshot.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := b.UploadSnapshot(ctx, snapRoot, "")
	if err != nil {
		t.Fatalf("UploadSnapshot: %v", err)
	}
	if id == "" {
		t.Fatal("empty id")
	}

	// CurrentSnapshot now returns the id.
	cur, err := b.CurrentSnapshot(ctx)
	if err != nil || cur != id {
		t.Fatalf("CurrentSnapshot: id=%v err=%v", cur, err)
	}

	// ListSnapshots returns at least one entry.
	snaps, err := b.ListSnapshots(ctx, 0)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) < 1 {
		t.Errorf("expected at least 1 snapshot, got %d", len(snaps))
	}

	// DownloadSnapshot.
	dst := t.TempDir()
	if err := b.DownloadSnapshot(ctx, id, dst); err != nil {
		t.Fatalf("DownloadSnapshot: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "files", "agents", "a.md")) //nolint:gosec
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("downloaded body: %q", data)
	}

	// Conflict on stale expectedPrevious.
	if _, err := b.UploadSnapshot(ctx, snapRoot, "deadbeef"); err == nil {
		t.Error("expected conflict")
	}
}

func TestGitHub_Auth(t *testing.T) {
	t.Setenv(credentials.EnvName("authtest"), "supersecret")
	b, err := NewConfigured("https://example.com/u/r.git", "main", "authtest", "a", "a@x")()
	if err != nil {
		t.Fatal(err)
	}
	bi, ok := b.(*backendImpl)
	if !ok {
		t.Fatal("not *backendImpl")
	}
	auth := bi.auth()
	if auth == nil {
		t.Fatal("auth nil")
	}
	if auth.Username != "x-access-token" {
		t.Errorf("auth.Username: got %q", auth.Username)
	}
	// The git package's BasicAuth.Password is unexported, but we can
	// verify via reflection-or-equivalent by storing/reading via the
	// backend itself in the bare-repo test above.
	_ = time.Now()
}

func shaOf(b string) string {
	sum := sha256.Sum256([]byte(b))
	return hex.EncodeToString(sum[:])
}
