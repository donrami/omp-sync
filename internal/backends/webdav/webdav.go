// Package webdav implements the WebDAV backend.
//
// Auth uses HTTP Basic with a credential looked up from env or keyring.
// The snapshot id is the SHA-256 of the canonical manifest.
package webdav

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donrami/omp-sync/internal/backend"
	"github.com/donrami/omp-sync/internal/credentials"
	"github.com/donrami/omp-sync/internal/snapshot"
	"github.com/studio-b12/gowebdav"
)

// backendImpl is the WebDAV backend.
type backendImpl struct {
	client   *gowebdav.Client
	root     string
	credName string
	username string
}

// NewConfigured returns a backend.Factory that builds a WebDAV backend.
func NewConfigured(url, username, credName, path string) backend.Factory {
	return func() (backend.Backend, error) {
		if url == "" || username == "" || credName == "" {
			return nil, errors.New("webdav: url, username, and credential are required")
		}
		if path == "" {
			path = "/"
		}
		password, err := credentials.Lookup(credName)
		if err != nil {
			return nil, fmt.Errorf("%w: credential %q", backend.ErrAuth, credName)
		}
		root := strings.TrimSuffix(url, "/") + "/" + strings.Trim(path, "/")
		cli := gowebdav.NewClient(url, username, password)
		cli.SetTimeout(60 * time.Second)
		return &backendImpl{
			client:   cli,
			root:     root,
			credName: credName,
			username: username,
		}, nil
	}
}

// Name returns "webdav".
func (b *backendImpl) Name() string { return "webdav" }

// Verify checks that the WebDAV server is reachable.
func (b *backendImpl) Verify(ctx context.Context) error {
	if err := b.client.MkdirAll(b.root, 0o755); err != nil {
		return fmt.Errorf("%w: %v", backend.ErrUnreachable, err)
	}
	return nil
}

// CurrentSnapshot returns the id of the current snapshot, or ErrNoSnapshot.
func (b *backendImpl) CurrentSnapshot(ctx context.Context) (backend.SnapshotID, error) {
	manifestPath := b.root + "/current/manifest.json"
	data, err := b.client.Read(manifestPath)
	if err != nil {
		if isNotFound(err) {
			return "", backend.ErrNoSnapshot
		}
		return "", fmt.Errorf("%w: %v", backend.ErrUnreachable, err)
	}
	var m snapshot.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	canon, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return backend.SnapshotID(hex.EncodeToString(sum[:])), nil
}

// UploadSnapshot uploads rootDir to the WebDAV backend and atomically promotes it.
func (b *backendImpl) UploadSnapshot(ctx context.Context, rootDir string, expectedPrevious backend.SnapshotID) (backend.SnapshotID, error) {
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
	id := backend.SnapshotID(hex.EncodeToString(sum[:]))

	cur, err := b.CurrentSnapshot(ctx)
	if err != nil && !errors.Is(err, backend.ErrNoSnapshot) {
		return "", err
	}
	if cur != expectedPrevious {
		return "", fmt.Errorf("%w: have %q, expected %q", backend.ErrConflict, cur, expectedPrevious)
	}

	stageDir := fmt.Sprintf("%s/staging-%s", b.root, id)
	if err := b.uploadDir(rootDir, stageDir); err != nil {
		_ = b.client.RemoveAll(stageDir)
		return "", fmt.Errorf("upload: %w", err)
	}

	if err := b.client.RemoveAll(b.root + "/current"); err != nil && !isNotFound(err) {
		_ = b.client.RemoveAll(stageDir)
		return "", fmt.Errorf("clean current: %w", err)
	}
	if err := b.client.Rename(stageDir, b.root+"/current", false); err != nil {
		_ = b.client.RemoveAll(stageDir)
		return "", fmt.Errorf("promote: %w", err)
	}
	return id, nil
}

// DownloadSnapshot populates destDir with the snapshot's files.
func (b *backendImpl) DownloadSnapshot(ctx context.Context, id backend.SnapshotID, destDir string) error {
	src := b.root + "/current"
	return b.downloadDir(src, destDir)
}

// ListSnapshots returns historical snapshots.
func (b *backendImpl) ListSnapshots(ctx context.Context, limit int) ([]backend.SnapshotInfo, error) {
	snapshotsRoot := b.root + "/snapshots"
	files, err := b.client.ReadDir(snapshotsRoot)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]backend.SnapshotInfo, 0, len(files))
	for _, fi := range files {
		if !fi.IsDir() {
			continue
		}
		manifestPath := fmt.Sprintf("%s/%s/manifest.json", snapshotsRoot, fi.Name())
		data, err := b.client.Read(manifestPath)
		if err != nil {
			continue
		}
		var m snapshot.Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		out = append(out, backend.SnapshotInfo{
			ID:        backend.SnapshotID(fi.Name()),
			CreatedAt: m.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (b *backendImpl) uploadDir(srcDir, dstDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		target := strings.TrimRight(dstDir, "/") + "/" + filepath.ToSlash(rel)
		if info.IsDir() {
			if rel == "." {
				return nil
			}
		return b.client.MkdirAll(target, 0o755)
		}
		f, err := os.Open(path) //nolint:gosec // controlled path
		if err != nil {
			return err
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		return b.client.Write(target, data, 0o644)
	})
}

func (b *backendImpl) downloadDir(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	files, err := b.client.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcDir, err)
	}
	for _, fi := range files {
		src := strings.TrimRight(srcDir, "/") + "/" + fi.Name()
		dst := filepath.Join(dstDir, fi.Name())
		if fi.IsDir() {
			if err := b.downloadDir(src, dst); err != nil {
				return err
			}
			continue
		}
		data, err := b.client.Read(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "404") || strings.Contains(s, "not found")
}

var _ backend.Backend = (*backendImpl)(nil)
