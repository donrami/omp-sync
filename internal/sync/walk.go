// Package sync contains the core sync engine: walking, push, pull,
// status, and diff. The package is intentionally stateless.
package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/donrami/omp-sync/internal/filter"
	"github.com/donrami/omp-sync/internal/snapshot"
)

// LocalSnapshot is a snapshot of the local config tree, ready to be
// compared against the remote or uploaded via the backend.
type LocalSnapshot struct {
	// RootDir is the absolute path to the omp config root.
	RootDir string
	// Files is the set of included files, sorted by path.
	Files []snapshot.FileEntry
	// AllFiles includes ignored files for reporting.
	AllFiles []string
	// Ignored lists paths that were excluded by the filter.
	Ignored []string
}

// Walk walks rootDir and returns a LocalSnapshot containing only files
// that match the include set and do not match the exclude set.
//
// Symlinks are followed only when they resolve inside rootDir. Escaping
// symlinks are rejected with an error (FR-013).
func Walk(rootDir string, include, exclude []string) (*LocalSnapshot, error) {
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("abs root: %w", err)
	}

	var allPaths []string
	if err := walkDir(root, root, &allPaths); err != nil {
		return nil, err
	}

	// Convert OS paths to forward-slash form for glob matching.
	rel := make([]string, 0, len(allPaths))
	for _, p := range allPaths {
		rel = append(rel, filepath.ToSlash(p))
	}

	included, err := filter.Included(rel, include, exclude)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}

	ignored := make([]string, 0)
	for _, p := range rel {
		if !contains(included, p) {
			ignored = append(ignored, p)
		}
	}

	files := make([]snapshot.FileEntry, 0, len(included))
	for _, p := range included {
		abs := filepath.Join(root, filepath.FromSlash(p))
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		if !info.Mode().IsRegular() {
			// Skip non-regular files (e.g. devices, sockets, fifos).
			continue
		}
		hash, err := snapshot.FileSHA256(abs)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", p, err)
		}
		files = append(files, snapshot.FileEntry{
			Path:       p,
			Mode:       uint32(info.Mode().Perm()),
			Size:       info.Size(),
			SHA256:     hash,
			Executable: info.Mode()&0o111 != 0,
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(ignored)

	return &LocalSnapshot{
		RootDir:  root,
		Files:    files,
		AllFiles: rel,
		Ignored:  ignored,
	}, nil
}

// walkDir recurses into root, appending regular-file paths (relative to
// root, forward-slash) to out. Symlinks are followed only when their
// target resolves inside root.
func walkDir(root, current string, out *[]string) error {
	entries, err := os.ReadDir(current)
	if err != nil {
		return fmt.Errorf("read %s: %w", current, err)
	}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(current, name)
		info, err := os.Lstat(full)
		if err != nil {
			return fmt.Errorf("lstat %s: %w", full, err)
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return fmt.Errorf("rel %s: %w", full, err)
		}
		relSlash := filepath.ToSlash(rel)

		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(full)
			if err != nil {
				return fmt.Errorf("eval symlink %s: %w", full, err)
			}
			// Reject symlinks that escape root.
			absTarget, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("abs target: %w", err)
			}
			relTarget, err := filepath.Rel(root, absTarget)
			if err != nil || strings.HasPrefix(relTarget, "..") {
				return fmt.Errorf("symlink %q escapes root", relSlash)
			}
			// Stat the resolved path; if it's a directory, recurse; if it's
			// a regular file, append it.
			sinfo, err := os.Stat(full)
			if err != nil {
				return fmt.Errorf("stat symlink %s: %w", full, err)
			}
			if sinfo.IsDir() {
				if err := walkDir(root, full, out); err != nil {
					return err
				}
			} else if sinfo.Mode().IsRegular() {
				*out = append(*out, relSlash)
			}
			continue
		}

		if info.IsDir() {
			if err := walkDir(root, full, out); err != nil {
				return err
			}
			continue
		}
		if info.Mode().IsRegular() {
			*out = append(*out, relSlash)
		}
	}
	return nil
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// ComputeSnapshotDir writes a snapshot directory tree for the local
// snapshot into destDir. The resulting directory has manifest.json and
// files/<relpath> for each file. The directory is left in a stable
// state suitable for handing to backend.UploadSnapshot.
func ComputeSnapshotDir(snap *LocalSnapshot, destDir string, toolVersion string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir dest: %w", err)
	}
	filesDir := filepath.Join(destDir, snapshot.FilesDir)
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir files: %w", err)
	}

	for _, f := range snap.Files {
		src := filepath.Join(snap.RootDir, filepath.FromSlash(f.Path))
		dst := filepath.Join(filesDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
		}
		if err := copyFile(src, dst, os.FileMode(f.Mode)); err != nil {
			return "", fmt.Errorf("copy %s: %w", f.Path, err)
		}
	}

	man := snapshot.Manifest{
		Version:     snapshot.ManifestVersion,
		CreatedAt:   nowUTC(),
		ToolVersion: toolVersion,
		Files:       snap.Files,
	}
	data, err := man.CanonicalJSON()
	if err != nil {
		return "", fmt.Errorf("manifest json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(destDir, snapshot.ManifestName), data, 0o644); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return destDir, nil
}

// applyManifest validates the manifest in srcDir and copies its files
// into destDir. Symlink-safe.
func applyManifest(srcDir, destDir string) error {
	manifestPath := filepath.Join(srcDir, snapshot.ManifestName)
	data, err := os.ReadFile(manifestPath) //nolint:gosec // controlled path
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var m snapshot.Manifest
	if err := decodeJSON(data, &m); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(destDir); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir dest: %w", err)
	}

	for _, f := range m.Files {
		src := filepath.Join(srcDir, snapshot.FilesDir, filepath.FromSlash(f.Path))
		dst := filepath.Join(destDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
		}
		// Verify the hash against the file we're about to copy.
		actual, err := snapshot.FileSHA256(src)
		if err != nil {
			return fmt.Errorf("hash %s: %w", f.Path, err)
		}
		if actual != f.SHA256 {
			return fmt.Errorf("hash mismatch for %s", f.Path)
		}
		mode := os.FileMode(f.Mode)
		if f.Executable {
			mode |= 0o111
		}
		if err := copyFile(src, dst, mode); err != nil {
			return fmt.Errorf("copy %s: %w", f.Path, err)
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // path is user-provided
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// ErrConflict is a re-export of backend.ErrConflict for callers that
// only import the sync package.
var ErrConflict = errors.New("sync: conflict")

// hashString is a small helper that returns the lowercase hex SHA-256 of s.
func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
