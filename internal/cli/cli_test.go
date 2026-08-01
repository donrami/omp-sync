package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/omp-sync/internal/audit"
	"github.com/example/omp-sync/internal/backend"
	"github.com/example/omp-sync/internal/snapshot"
)

// fakeBackend is an in-memory backend.Backend used to exercise the CLI commands.
type fakeBackend struct {
	id        backend.SnapshotID
	files     map[string][]byte
	conflicts int
}

func newFakeBackend() *fakeBackend { return &fakeBackend{files: map[string][]byte{}} }

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Verify(ctx context.Context) error { return nil }
func (f *fakeBackend) CurrentSnapshot(ctx context.Context) (backend.SnapshotID, error) {
	if f.id == "" {
		return "", backend.ErrNoSnapshot
	}
	return f.id, nil
}
func (f *fakeBackend) ListSnapshots(ctx context.Context, limit int) ([]backend.SnapshotInfo, error) {
	if f.id == "" {
		return nil, nil
	}
	return []backend.SnapshotInfo{{ID: f.id, CreatedAt: time.Now()}}, nil
}

func (f *fakeBackend) UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious backend.SnapshotID) (backend.SnapshotID, error) {
	if expectedPrevious != f.id {
		f.conflicts++
		return "", backend.ErrConflict
	}
	manifestPath := filepath.Join(rootDir, snapshot.ManifestName)
	data, err := os.ReadFile(manifestPath) //nolint:gosec
	if err != nil {
		return "", err
	}
	var m snapshot.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	newID := backend.SnapshotID(hex.EncodeToString(sha256.New().Sum(data)))
	f.id = newID
	f.files = map[string][]byte{}
	for _, e := range m.Files {
		body, err := os.ReadFile(filepath.Join(rootDir, snapshot.FilesDir, filepath.FromSlash(e.Path))) //nolint:gosec
		if err != nil {
			return "", err
		}
		f.files[e.Path] = body
	}
	return newID, nil
}

func (f *fakeBackend) DownloadSnapshot(ctx context.Context, id backend.SnapshotID, destDir string) error {
	if err := os.MkdirAll(filepath.Join(destDir, snapshot.FilesDir), 0o755); err != nil {
		return err
	}
	m := snapshot.Manifest{
		Version:     snapshot.ManifestVersion,
		ToolVersion: "test",
		CreatedAt:   time.Now().UTC(),
		Files:       []snapshot.FileEntry{},
	}
	for p, body := range f.files {
		sum := sha256.Sum256(body)
		m.Files = append(m.Files, snapshot.FileEntry{
			Path: p, Mode: 0o644, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		})
		target := filepath.Join(destDir, snapshot.FilesDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return err
		}
	}
	data, _ := m.CanonicalJSON()
	return os.WriteFile(filepath.Join(destDir, snapshot.ManifestName), data, 0o644)
}

func TestFakeBackend_RoundTrip(t *testing.T) {
	b := newFakeBackend()

	if _, err := b.CurrentSnapshot(context.Background()); err != backend.ErrNoSnapshot {
		t.Fatalf("expected ErrNoSnapshot, got %v", err)
	}

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, snapshot.FilesDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, snapshot.FilesDir, "agents", "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBody := `{"version":1,"created_at":"2026-07-31T00:00:00Z","tool_version":"t","files":[{"path":"agents/a.md","mode":420,"size":2,"sha256":"` + sha256HexStr([]byte("hi")) + `","executable":false}]}`
	if err := os.WriteFile(filepath.Join(src, snapshot.ManifestName), []byte(manifestBody), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := b.UploadSnapshot(context.Background(), src, "")
	if err != nil {
		t.Fatalf("UploadSnapshot: %v", err)
	}
	if id == "" {
		t.Fatal("empty id")
	}

	cur, err := b.CurrentSnapshot(context.Background())
	if err != nil || cur != id {
		t.Errorf("current: %v %v", cur, err)
	}

	if _, err := b.UploadSnapshot(context.Background(), src, ""); err == nil {
		t.Error("expected conflict")
	}
	if b.conflicts != 1 {
		t.Errorf("conflicts: got %d", b.conflicts)
	}

	dst := t.TempDir()
	if err := b.DownloadSnapshot(context.Background(), id, dst); err != nil {
		t.Fatalf("DownloadSnapshot: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, snapshot.FilesDir, "agents", "a.md")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Errorf("got %q", got)
	}
}

func TestApplySnapshotToOmpDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	filesDir := filepath.Join(src, snapshot.FilesDir)
	if err := os.MkdirAll(filepath.Join(filesDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("hello")
	if err := os.WriteFile(filepath.Join(filesDir, "agents", "a.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	hash := sha256HexStr(body)
	manifest := &snapshot.Manifest{
		Version:     snapshot.ManifestVersion,
		CreatedAt:   time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		ToolVersion: "test",
		Files: []snapshot.FileEntry{
			{Path: "agents/a.md", Mode: 0o644, Size: int64(len(body)), SHA256: hash},
		},
	}
	data, _ := manifest.CanonicalJSON()
	if err := os.WriteFile(filepath.Join(src, snapshot.ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applySnapshotToOmpDir(src, dst); err != nil {
		t.Fatalf("applySnapshotToOmpDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "agents", "a.md")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestEffectivePatterns_FlagsWin(t *testing.T) {
	cfg := configStub{include: []string{"a"}, exclude: []string{"b"}}
	inc, exc := effectivePatterns(cfg, []string{"x"}, []string{"y"})
	if !slicesEq(inc, []string{"x"}) || !slicesEq(exc, []string{"y"}) {
		t.Errorf("expected flag override, got inc=%v exc=%v", inc, exc)
	}
}

func TestEffectivePatterns_DefaultsWhenEmpty(t *testing.T) {
	cfg := configStub{include: []string{"a"}}
	inc, exc := effectivePatterns(cfg, nil, nil)
	if !slicesEq(inc, []string{"a"}) {
		t.Errorf("expected config include, got %v", inc)
	}
	if len(exc) != 0 {
		t.Errorf("expected empty exclude, got %v", exc)
	}
}

func TestIsBackendConflict(t *testing.T) {
	if !isBackendConflict(backend.ErrConflict) {
		t.Error("expected true on ErrConflict")
	}
	if isBackendConflict(nil) {
		t.Error("expected false for nil")
	}
}

func TestIsYes(t *testing.T) {
	if !isYes("y") || !isYes("yes") || !isYes("Y") || !isYes("YES") {
		t.Error("yes tokens should match")
	}
	if isYes("n") || isYes("no") {
		t.Error("non-yes tokens should not match")
	}
}

func TestExitCode(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Error("nil -> 0")
	}
	if ExitCode(exitErr(2, nil)) != 2 {
		t.Error("exitErr(2) -> 2")
	}
}

func TestAuditPath(t *testing.T) {
	got := audit.Path()
	if got == "" {
		t.Error("audit.Path() empty")
	}
	if !strings.Contains(got, "audit.log") {
		t.Errorf("audit path: %q", got)
	}
}

// helpers

type configStub struct {
	include, exclude []string
}

func (c configStub) EffectiveInclude() []string { return c.include }
func (c configStub) EffectiveExclude() []string { return c.exclude }

func slicesEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sha256HexStr(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
