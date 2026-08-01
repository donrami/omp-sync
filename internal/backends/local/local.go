// Package local implements the local-filesystem backend.
//
// Storage layout:
//
//	<root>/
//	  .lock                  // flock target
//	  current.id             // snapshot id of the current snapshot
//	  snapshots/<id>/        // one directory per snapshot
//	    manifest.json
//	    files/<relpath>
//
// Uploads are serialized with flock on .lock so concurrent uploads cannot
// both succeed (SC-005).
package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/donrami/omp-sync/internal/atomic"
	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/snapshot"
)

// backendImpl is the local-filesystem backend.
type backendImpl struct {
	root string
}

// NewConfigured returns a backend.Factory that builds a local backend
// rooted at the given path.
func NewConfigured(path string) backend.Factory {
	return func() (backend.Backend, error) {
		if path == "" {
			return nil, errors.New("local: path is required")
		}
		if !filepath.IsAbs(path) {
			abs, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			path = abs
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir local root %s: %w", path, err)
		}
		return &backendImpl{root: path}, nil
	}
}

// Name returns "local".
func (b *backendImpl) Name() string { return "local" }

// Verify checks that the local root is reachable.
func (b *backendImpl) Verify(ctx context.Context) error {
	if b.root == "" {
		return backend.ErrUnreachable
	}
	if _, err := os.Stat(b.root); err != nil {
		return fmt.Errorf("%w: %v", backend.ErrUnreachable, err)
	}
	return nil
}

func (b *backendImpl) currentFile() string  { return filepath.Join(b.root, "current.id") }
func (b *backendImpl) snapshotsDir() string { return filepath.Join(b.root, "snapshots") }
func (b *backendImpl) lockPath() string     { return filepath.Join(b.root, ".lock") }

// CurrentSnapshot returns the id of the current snapshot, or ErrNoSnapshot.
func (b *backendImpl) CurrentSnapshot(ctx context.Context) (backend.SnapshotID, error) {
	data, err := os.ReadFile(b.currentFile()) //nolint:gosec // controlled path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", backend.ErrNoSnapshot
		}
		return "", fmt.Errorf("%w: read current: %v", backend.ErrUnreachable, err)
	}
	return backend.SnapshotID(strings.TrimSpace(string(data))), nil
}

// promoteCurrent atomically writes newID to current.id.
func (b *backendImpl) promoteCurrent(newID backend.SnapshotID) error {
	tmp := b.currentFile() + ".tmp." + string(newID)
	if err := atomic.WriteFile(tmp, []byte(string(newID)+"\n"), 0o644); err != nil {
		return err
	}
	_ = os.Remove(b.currentFile())
	return os.Rename(tmp, b.currentFile())
}

// UploadSnapshot stages the snapshot directory and atomically promotes it
// under a flock so concurrent uploads are serialized.
func (b *backendImpl) UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious backend.SnapshotID) (backend.SnapshotID, error) {
	if err := os.MkdirAll(b.root, 0o755); err != nil {
		return "", err
	}
	lockFile, err := os.OpenFile(b.lockPath(), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // controlled path
	if err != nil {
		return "", fmt.Errorf("open lock: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	id, err := b.computeID(rootDir)
	if err != nil {
		return "", err
	}

	cur, err := b.CurrentSnapshot(ctx)
	if err != nil && !errors.Is(err, backend.ErrNoSnapshot) {
		return "", err
	}
	if cur != expectedPrevious {
		return "", fmt.Errorf("%w: have %q, expected %q", backend.ErrConflict, cur, expectedPrevious)
	}

	stagingDir := filepath.Join(b.snapshotsDir(), "staging-"+string(id))
	if err := os.RemoveAll(stagingDir); err != nil {
		return "", fmt.Errorf("clean staging: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return "", err
	}
	if err := copyDir(rootDir, stagingDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", fmt.Errorf("stage: %w", err)
	}

	finalDir := filepath.Join(b.snapshotsDir(), string(id))
	if err := os.RemoveAll(finalDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", fmt.Errorf("clean final: %w", err)
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", fmt.Errorf("promote: %w", err)
	}

	if err := b.promoteCurrent(id); err != nil {
		_ = os.RemoveAll(finalDir)
		return "", fmt.Errorf("%w: %v", backend.ErrConflict, err)
	}
	return id, nil
}

// DownloadSnapshot writes the snapshot's files into destDir.
func (b *backendImpl) DownloadSnapshot(ctx context.Context, id backend.SnapshotID, destDir string) error {
	src := filepath.Join(b.snapshotsDir(), string(id))
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return backend.ErrNoSnapshot
		}
		return fmt.Errorf("%w: %v", backend.ErrUnreachable, err)
	}
	if err := copyDir(src, destDir); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	return nil
}

// ListSnapshots returns snapshots in chronological order.
func (b *backendImpl) ListSnapshots(ctx context.Context, limit int) ([]backend.SnapshotInfo, error) {
	entries, err := os.ReadDir(b.snapshotsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]backend.SnapshotInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "staging-") {
			continue
		}
		manifestPath := filepath.Join(b.snapshotsDir(), e.Name(), snapshot.ManifestName)
		data, err := os.ReadFile(manifestPath) //nolint:gosec // controlled path
		if err != nil {
			continue
		}
		var m snapshot.Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		out = append(out, backend.SnapshotInfo{
			ID:        backend.SnapshotID(e.Name()),
			CreatedAt: m.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (b *backendImpl) computeID(rootDir string) (backend.SnapshotID, error) {
	manifestPath := filepath.Join(rootDir, snapshot.ManifestName)
	data, err := os.ReadFile(manifestPath) //nolint:gosec // controlled path
	if err != nil {
		return "", fmt.Errorf("read manifest: %w", err)
	}
	var m snapshot.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("decode manifest: %w", err)
	}
	canon, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return backend.SnapshotID(hex.EncodeToString(sum[:])), nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		mode := info.Mode().Perm()
		if info.Mode()&0o111 != 0 {
			mode |= 0o111
		}
		tmp := target + ".tmp"
		if err := atomic.CopyFile(path, tmp, mode); err != nil {
			return err
		}
		return os.Rename(tmp, target)
	})
}

var _ backend.Backend = (*backendImpl)(nil)
