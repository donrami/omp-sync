package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/example/omp-sync/internal/backend"
)

func TestLocalBackend_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "remote")
	factory := NewConfigured(root)
	b, err := factory()
	if err != nil {
		t.Fatalf("factory: %v", err)
	}

	if _, err := b.CurrentSnapshot(context.Background()); err != backend.ErrNoSnapshot {
		t.Fatalf("expected ErrNoSnapshot, got %v", err)
	}

	src := filepath.Join(dir, "snap")
	if err := os.MkdirAll(filepath.Join(src, "files", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "files", "agents", "hello.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), []byte(`{
		"version": 1,
		"created_at": "2026-01-01T00:00:00Z",
		"tool_version": "test",
		"files": [{
			"path": "agents/hello.md",
			"mode": 420,
			"size": 2,
			"sha256": "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4",
			"executable": false
		}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := b.UploadSnapshot(context.Background(), src, "")
	if err != nil {
		t.Fatalf("UploadSnapshot: %v", err)
	}
	if id == "" {
		t.Fatal("empty snapshot id")
	}

	cur, err := b.CurrentSnapshot(context.Background())
	if err != nil {
		t.Fatalf("CurrentSnapshot: %v", err)
	}
	if cur != id {
		t.Errorf("current id: got %q, want %q", cur, id)
	}

	dst := filepath.Join(dir, "dest")
	if err := b.DownloadSnapshot(context.Background(), id, dst); err != nil {
		t.Fatalf("DownloadSnapshot: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "files", "agents", "hello.md")) //nolint:gosec
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(got) != "hi" {
		t.Errorf("contents: got %q", got)
	}

	_, err = b.UploadSnapshot(context.Background(), src, "deadbeef")
	if err == nil {
		t.Error("expected conflict")
	}
}

// TestLocalBackend_ConcurrentPush simulates two machines pushing simultaneously
// to the same backend. SC-005 requires exactly one to succeed and the other to
// receive ErrConflict.
func TestLocalBackend_ConcurrentPush(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "remote")
	factory := NewConfigured(root)
	b, err := factory()
	if err != nil {
		t.Fatalf("factory: %v", err)
	}

	// First push establishes the baseline id.
	src := filepath.Join(dir, "snap")
	if err := os.MkdirAll(filepath.Join(src, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), []byte(`{
		"version":1,"created_at":"2026-01-01T00:00:00Z","tool_version":"t","files":[]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	firstID, err := b.UploadSnapshot(context.Background(), src, "")
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}

	src2 := filepath.Join(dir, "snap2")
	if err := os.MkdirAll(filepath.Join(src2, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src2, "manifest.json"), []byte(`{
		"version":1,"created_at":"2026-01-01T00:01:00Z","tool_version":"t","files":[]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	type result struct {
		id  backend.SnapshotID
		err error
	}
	resCh := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			id, err := b.UploadSnapshot(context.Background(), src2, firstID)
			resCh <- result{id: id, err: err}
		}()
	}
	close(start)

	wins, conflicts := 0, 0
	for range 2 {
		r := <-resCh
		switch {
		case r.err == nil && r.id != "":
			wins++
		case r.err != nil && errors.Is(r.err, backend.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected result: id=%q err=%v", r.id, r.err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Errorf("expected 1 win and 1 conflict; got wins=%d conflicts=%d", wins, conflicts)
	}
}
